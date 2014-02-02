package main

import (
	"fmt"
	"gnd.la/log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type State uint8

const (
	StateStopped State = iota
	StateStopping
	StateStarted
	StateStarting
	StateFailed
)

const (
	minTime = time.Second
)

type Status struct {
	sync.Mutex
	Config   *Config
	Cmd      *exec.Cmd
	State    State
	Started  time.Time
	Restarts int
	Err      error
	Ch       chan error
	logger   *logger
}

func newStatus(cfg *Config) *Status {
	return &Status{Config: cfg, Ch: make(chan error)}
}

func (s *Status) initLogger() {
	var m monitor
	if s.logger != nil {
		s.logger.Close()
		m = s.logger.monitor
		s.logger = nil
	}
	logPath := filepath.Join(LogDir, s.Config.ServiceName()+".log.gz")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Errorf("error opening log file %s: %s", logPath, err)
		return
	}
	s.logger = newLogger(f)
	s.logger.monitor = m
}

func (s *Status) sendErr(err error) bool {
	select {
	case s.Ch <- err:
		return true
	default:
	}
	return false
}

func (s *Status) Run() {
	s.Lock()
	s.State = StateStarted
	s.initLogger()
	s.Unlock()
	for {
		s.Lock()
		name := s.Config.ServiceName()
		if s.State != StateStarted {
			s.Unlock()
			break
		}
		cmd, err := s.Config.Cmd()
		if err != nil {
			s.State = StateFailed
			s.Err = err
			log.Errorf("could not initialize %s: %s", name, err)
			s.Unlock()
			break
		}
		if s.logger != nil {
			cmd.Stdout = s.logger.stdout
			cmd.Stderr = s.logger.stderr
		}
		s.Cmd = cmd
		s.Started = time.Now()
		log.Infof("Starting %s", name)
		if err := s.Cmd.Start(); err != nil {
			s.State = StateFailed
			s.Err = err
			log.Errorf("failed to start %s: %s", name, err)
			s.Unlock()
			break
		}
		s.Unlock()
		time.AfterFunc(time.Duration(float64(minTime)*1.1), func() {
			s.Lock()
			defer s.Unlock()
			if s.Err == nil {
				s.sendErr(nil)
				log.Infof("Started %s", name)
			}
		})
		err = s.Cmd.Wait()
		s.Lock()
		if s.State != StateStarted {
			s.Cmd = nil
			s.sendErr(nil)
			s.Unlock()
			break
		}
		if since := time.Since(s.Started); since < minTime {
			// Consider failure
			s.Cmd = nil
			s.State = StateFailed
			s.Err = fmt.Errorf("exited too fast (%s)", since)
			s.sendErr(s.Err)
			s.Unlock()
			log.Errorf("%s %s", name, s.Err)
			break
		}
		s.Restarts++
		s.Unlock()
		log.Infof("%s exited with error %s - restarting", name, err)
	}
}

func (s *Status) Stop() error {
	s.Lock()
	s.State = StateStopping
	name := s.Config.ServiceName()
	cmd := s.Cmd
	s.Unlock()
	if cmd == nil {
		s.Lock()
		s.State = StateStopped
		s.Unlock()
		return nil
	}
	log.Infof("Stopping %s", name)
	if cmd.Process != nil {
		cmd.Process.Signal(os.Signal(syscall.SIGTERM))
	}
	stopped := false
	select {
	case <-s.Ch:
		stopped = true
	case <-time.After(10 * time.Second):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}
	if !stopped {
		select {
		case <-s.Ch:
		case <-time.After(2 * time.Second):
			s.Lock()
			s.State = StateStarted
			s.Unlock()
			err := fmt.Errorf("could not stop %s, probably stuck", name)
			log.Error(err)
			return err
		}
	}
	s.Lock()
	s.State = StateStopped
	s.Restarts = 0
	s.Unlock()
	log.Infof("Stopped %s", name)
	if s.logger != nil {
		s.logger.Close()
		s.logger = nil
	}
	return nil
}
