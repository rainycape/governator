package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var (
	maxOpenRe = regexp.MustCompile("Max open files\\s+(\\d+)")
)

func setLogger(t *testing.T, cfg *Config, value string) {
	cfg.Log = new(Logger)
	if err := cfg.Log.Parse(value); err != nil {
		t.Fatal(err)
	}
}

func TestService(t *testing.T) {
	cfg := &Config{
		File:    "/non-existant",
		Command: "sleep 50000",
		Name:    "sleep",
	}
	setLogger(t, cfg, "file")
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
	}
	setLogger(t, cfg, "none")
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

func checkMaxOpenFiles(t *testing.T, s *Service, expect int) {
	limitsFile := fmt.Sprintf("/proc/%d/limits", s.Cmd.Process.Pid)
	data, err := ioutil.ReadFile(limitsFile)
	if err != nil {
		t.Fatal(err)
	}
	m := maxOpenRe.FindStringSubmatch(string(data))
	val, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatal(err)
	}
	if val != expect {
		t.Errorf("expecting max open files %d, got %d", expect, val)
	}
}

func getMaxOpenFiles(t *testing.T) int {
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatal(err)
	}
	return int(limit.Cur)
}

func TestServiceMaxOpenFiles(t *testing.T) {
	maxOpen1 := getMaxOpenFiles(t)
	cfg := &Config{
		File:    "/non-existant",
		Command: "sleep 5000",
		Name:    "sleep",
	}
	setLogger(t, cfg, "none")
	s := newService(cfg)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	checkMaxOpenFiles(t, s, maxOpen1)
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	sMaxOpen := maxOpen1 / 2
	cfg.MaxOpenFiles = sMaxOpen
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	checkMaxOpenFiles(t, s, sMaxOpen)
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		maxOpen2 := getMaxOpenFiles(t)
		if maxOpen1 != maxOpen2 {
			t.Errorf("max open files for this process changed from %d to %d", maxOpen1, maxOpen2)
		}
	} else {
		t.Log("skipping max open files value restoration test, must run as root")
	}
}

func init() {
	logDir = "/tmp/governator"
}
