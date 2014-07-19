package main

import (
	"bytes"
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

	"gnd.la/log"
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

func prepareGovernatorTest(t testing.TB) *Governator {
	if testing.Verbose() {
		log.SetLevel(log.LDebug)
	}
	g, err := NewGovernator("")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		if err := g.Run(); err != nil {
			t.Fatal("error running goernator: %s", err)
		}
	}()
	return g
}

func afterGovernatorTest(t testing.TB, g *Governator) {
	g.StopRunning()
}

func TestService(t *testing.T) {
	g := prepareGovernatorTest(t)
	defer afterGovernatorTest(t, g)
	cfg := &Config{
		File:    "/non-existant",
		Command: "sleep 50000",
		Name:    "sleep",
	}
	name, err := g.AddService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		go func() {
			// This goroutine starts after s.Start() is called.
			if err := g.Stop(name); err != nil {
				t.Fatalf("error stopping: %s", err)
			}
			wg.Done()
		}()
		if err := g.Start(name); err != nil {
			t.Fatalf("error starting: %s", err)
		}
		wg.Done()
	}()
	wg.Wait()
	s, err := g.serviceByName(name)
	if err != nil {
		t.Fatal(err)
	}
	if s.State != StateStopped {
		t.Fatal("service is not stopped")
	}
	if err := g.Start(name); err != nil {
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
	time.Sleep(1 * time.Second)
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
	if err := g.Stop(name); err != nil {
		t.Fatalf("error stopping: %s", err)
	}
}

func TestExitingService(t *testing.T) {
	g := prepareGovernatorTest(t)
	defer afterGovernatorTest(t, g)
	cfg := &Config{
		File:    "/non-existant",
		Command: "true",
		Name:    "true",
	}
	name, err := g.AddService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	err = g.Start(name)
	if err == nil || !strings.Contains(err.Error(), "too fast") {
		t.Fatalf("expecting error due to fast exit, got %v instead", err)
	}
	if err := g.Stop(name); err != nil {
		t.Fatalf("error stopping backoff service: %s", err)
	}
	s, err := g.serviceByName(name)
	if err != nil {
		t.Fatal(err)
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
	g := prepareGovernatorTest(t)
	defer afterGovernatorTest(t, g)
	maxOpen1 := getMaxOpenFiles(t)
	cfg := &Config{
		File:    "wait",
		Command: waitPy,
		Name:    "wait",
	}
	setLogger(t, cfg, "none")
	buf := new(bytes.Buffer)
	cfg.Log.w = (*bufWriter)(buf)
	name, err := g.AddService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err := g.serviceByName(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Start(name); err != nil {
		t.Fatal(err)
	}
	checkMaxOpenFiles(t, s, maxOpen1)
	if err := g.Stop(name); err != nil {
		t.Fatal(err)
	}
	sMaxOpen := maxOpen1 / 2
	buf.Reset()
	s.Config.MaxOpenFiles = sMaxOpen
	if err := g.Start(name); err != nil {
		t.Fatal(err)
	}
	checkMaxOpenFiles(t, s, sMaxOpen)
	if err := g.Stop(name); err != nil {
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

func TestNumGoroutines(t *testing.T) {
	const (
		numServices = 20
	)
	cfg := &Config{
		Command: "yes",
		Name:    "test",
		Start:   true,
	}
	g := prepareGovernatorTest(t)
	defer afterGovernatorTest(t, g)
	for ii := 0; ii < numServices; ii++ {
		cpy := *cfg
		setLogger(t, &cpy, "file")
		if _, err := g.AddService(&cpy); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.Start("all"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Second)
	if n := runtime.NumGoroutine(); n > numServices {
		t.Errorf("using %d goroutines for %d services", n, numServices)
	} else {
		t.Logf("using %d goroutines for %d services", n, numServices)
	}
}

func init() {
	logDir = "/tmp/governator"
}
