package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var (
	maxOpenRe = regexp.MustCompile("Max open files\\s+(\\d+)")
	waitPy    = "python " + abs(filepath.Join("_testdata", "wait.py"))
)

func abs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		panic(err)
	}
	return a
}

func setLogger(t *testing.T, cfg *Config, value string) {
	cfg.Log = new(Logger)
	cfg.Log.Name = cfg.Name
	if err := cfg.Log.Parse(value); err != nil {
		t.Fatal(err)
	}
}

type bufWriter bytes.Buffer

func (w *bufWriter) Open(_ string) error { return nil }

func (w *bufWriter) Close() error { return nil }

func (w *bufWriter) Write(_ string, b []byte) error {
	(*bytes.Buffer)(w).Write(b)
	return nil
}

func (w *bufWriter) Flush() error { return nil }

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
		t.Fatalf("error starting again: %s", err)
	}
	// Kill it and check if it's restarted
	if err := exec.Command("killall", "-9", "sleep").Run(); err != nil {
		t.Fatalf("error killing: %s", err)
	}
	s.mu.Lock()
	state := s.State
	s.mu.Unlock()
	if state != StateStarted {
		t.Fatal("service is not started")
	}
	<-s.errCh
	s.mu.Lock()
	restarts := s.Restarts
	s.mu.Unlock()
	if restarts != 1 {
		t.Fatalf("expecting 1 restarts, got %d", restarts)
	}
	// Kill it and stop while it's restarting
	if err := exec.Command("killall", "-9", "sleep").Run(); err != nil {
		t.Fatalf("error killing: %s", err)
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
		t.Fatalf("expecting error due to fast exit, got %v instead", err)
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
	buf := (*bytes.Buffer)(s.Config.Log.w.(*bufWriter))
	lines := strings.Split(buf.String(), "\n")
	var line string
	for _, v := range lines {
		if !strings.Contains(v, "start") && !strings.Contains(v, "error") {
			line = v
			break
		}
	}
	parts := strings.Split(line, "-")
	val, err := strconv.Atoi(strings.Trim(parts[1], "\n- "))
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
		File:    "wait",
		Command: waitPy,
		Name:    "wait",
	}
	setLogger(t, cfg, "none")
	buf := new(bytes.Buffer)
	cfg.Log.w = (*bufWriter)(buf)
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
	buf.Reset()
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

func countThreads() (int, error) {
	cmd := exec.Command("sh", "-c", fmt.Sprintf("ps -eLf|awk '{print $2}' |grep %d |wc -l", os.Getpid()))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(stdout.String()))
}

func TestThreads(t *testing.T) {
	const (
		numServices = 10
	)
	cfg := &Config{
		File:    "sleep",
		Command: "sleep 5000",
	}
	srvs := make([]*Service, numServices)
	for ii := range srvs {
		c := *cfg
		c.Name = fmt.Sprintf("%s-%d", c.File, ii)
		setLogger(t, &c, "file")
		srvs[ii] = newService(&c)
	}
	for _, v := range srvs {
		if err := v.Start(); err != nil {
			time.Sleep(5 * time.Second)
			t.Fatal(err)
		}
	}
	threads, err := countThreads()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d threads, %d goroutines", threads, runtime.NumGoroutine())
	stack := make([]byte, 1024*100)
	stack = stack[:runtime.Stack(stack, true)]
	t.Logf("STACK:\n %s", string(stack))
}

func init() {
	logDir = "/tmp/governator"
}
