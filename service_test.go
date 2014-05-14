package main

import (
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestService(t *testing.T) {
	cfg := &Config{
		File:    "/non-existant",
		Command: "sleep 50000",
		Name:    "sleep",
		Log:     new(Logger),
	}
	if err := cfg.Log.Parse("file"); err != nil {
		t.Fatal(err)
	}
	s := newService(cfg)
	s.errCh = make(chan error)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		go func() {
			// This goroutine starts after s.Start() is called.
			if err := s.Stop(); err != nil {
				t.Fatalf("error stopping: %s", err)
			}
			wg.Done()
		}()
		if err := s.Start(); err != nil {
			t.Fatalf("error starting: %s", err)
		}
		wg.Done()
	}()
	wg.Wait()
	if s.State != StateStopped {
		t.Fatal("service is not stopped")
	}
	if err := s.Start(); err != nil {
		t.Fatal("error starting again: %s", err)
	}
	// Kill it and check if it's restarted
	if err := exec.Command("killall", "-9", "sleep").Run(); err != nil {
		t.Fatal("error killing: %s", err)
	}
	if s.State != StateStarted {
		t.Fatal("service is not started")
	}
	<-s.errCh
	if s.Restarts != 1 {
		t.Fatalf("expecting 1 restarts, got %d", s.Restarts)
	}
	// Kill it and stop while it's restarting
	if err := exec.Command("killall", "-9", "sleep").Run(); err != nil {
		t.Fatal("error killing: %s", err)
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("error stopping: %s", err)
	}
}

func TestExitingService(t *testing.T) {
	cfg := &Config{
		File:    "/non-existant",
		Command: "true",
		Name:    "true",
		Log:     new(Logger),
	}
	if err := cfg.Log.Parse("file"); err != nil {
		t.Fatal(err)
	}
	s := newService(cfg)
	err := s.Start()
	if err == nil || !strings.Contains(err.Error(), "too fast") {
		t.Fatalf("expecting error due to fast exit, got %s instead", err)
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("error stopping backoff service: %s", err)
	}
	// Wait until the next restart and make sure it doesn't happen
	time.Sleep(s.untilNextRestart() + time.Second)
	if s.Restarts > 0 {
		t.Fatalf("expecting no restarts, got %d instead", s.Restarts)
	}
}

func init() {
	logDir = "/tmp/governator"
}
