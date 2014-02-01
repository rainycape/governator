package main

import (
	"fmt"
	"gnd.la/log"
	"os/exec"
	"sync"
	"time"
)

const (
	SocketPath = "/tmp/gobernator.sock"
)

const (
	CmdRegister = iota
	CmdStart
	CmdStop
	CmdList
)

type Request struct {
	//Cmd  Command
	Data interface{}
}

type State uint8

const (
	StateStopped State = iota
	StateStarted
	StateFailed
)

type Status struct {
	sync.Mutex
	Config   *Config
	Cmd      *exec.Cmd
	State    State
	Started  time.Time
	Restarts int
	Err      error
}

func newStatus(cfg *Config) *Status {
	return &Status{Config: cfg}
}

func (s *Status) Run() {
	s.Lock()
	s.State = StateStarted
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
		err = s.Cmd.Wait()
		if since := time.Since(s.Started); since < time.Second {
			// Consider failure
			s.Lock()
			s.State = StateFailed
			s.Err = fmt.Errorf("exited too fast (%s)", since)
			s.Unlock()
			log.Errorf("%s %s", name, s.Err)
			break
		}
		s.Lock()
		if s.State != StateStarted {
			s.Restarts = 0
			s.Unlock()
			break
		}
		s.Restarts++
		s.Unlock()
		log.Infof("%s exited with error %s - restarting", name, err)
	}
}

func (s *Status) Stop() {
	s.Lock()
	s.State = StateStopped
	s.Unlock()
	if s.Cmd != nil && s.Cmd.Process != nil {
		log.Infof("Stopping %s", s.Config.ServiceName())
		s.Cmd.Process.Kill()
	}
}
