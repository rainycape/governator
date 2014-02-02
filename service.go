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

type Service struct {
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

func newService(cfg *Config) *Service {
	return &Service{Config: cfg, Ch: make(chan error)}
}

func (s *Service) Name() string {
	return s.Config.ServiceName()
}

func (s *Service) initLogger() {
	var m monitor
	if s.logger != nil {
		s.logger.Close()
		m = s.logger.monitor
		s.logger = nil
	}
	logPath := filepath.Join(LogDir, s.Name()+".log.gz")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		s.errorf("error opening log file %s: %s", logPath, err)
		return
	}
	s.logger = newLogger(f)
	s.logger.monitor = m
}

func (s *Service) sendErr(err error) bool {
	select {
	case s.Ch <- err:
		return true
	default:
	}
	return false
}

func (s *Service) Start() error {
	if err := s.startService(); err != nil {
		return err
	}
	if err := s.startWatchdog(); err != nil {
		s.errorf("error starting watchdog: %s", s.Name(), err)
	}
	return nil
}

func (s *Service) Run() {
	s.Lock()
	s.State = StateStarted
	s.initLogger()
	s.Unlock()
	for {
		s.Lock()
		if s.State != StateStarted {
			s.Unlock()
			break
		}
		cmd, err := s.Config.Cmd()
		if err != nil {
			s.State = StateFailed
			s.Err = err
			s.errorf("could not initialize: %s", err)
			s.Unlock()
			break
		}
		if s.logger != nil {
			cmd.Stdout = s.logger.stdout
			cmd.Stderr = s.logger.stderr
		}
		s.Cmd = cmd
		s.Started = time.Now()
		s.infof("starting")
		if err := s.Cmd.Start(); err != nil {
			s.State = StateFailed
			s.Err = err
			s.errorf("failed to start: %s", err)
			s.Unlock()
			break
		}
		s.Unlock()
		time.AfterFunc(time.Duration(float64(minTime)*1.1), func() {
			s.Lock()
			defer s.Unlock()
			if s.Err == nil {
				s.sendErr(nil)
				s.infof("started")
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
			s.errorf(s.Err.Error())
			break
		}
		s.Restarts++
		s.Unlock()
		s.infof("exited with error %s - restarting", err)
	}
}

func (s *Service) Stop() error {
	s.stopWatchdog()
	if err := s.stopService(); err != nil {
		s.startWatchdog()
		return err
	}
	return nil
}

func (s *Service) startWatchdog() error {
	if s.Config.Watchdog != nil {
		interval := s.Config.WatchdogInterval
		if interval < 0 {
			interval = defaultWatchdogInterval
		}
		return s.Config.Watchdog.Start(s, interval)
	}
	return nil
}

func (s *Service) stopWatchdog() {
	if s.Config.Watchdog != nil {
		s.Config.Watchdog.Stop()
	}
}

func (s *Service) startService() error {
	go s.Run()
	return <-s.Ch
}

func (s *Service) stopService() error {
	s.Lock()
	s.State = StateStopping
	cmd := s.Cmd
	s.Unlock()
	if cmd == nil {
		s.Lock()
		s.State = StateStopped
		s.Unlock()
		return nil
	}
	s.infof("stopping")
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
			err := fmt.Errorf("could not stop, probably stuck")
			s.errorf(err.Error())
			return err
		}
	}
	s.Lock()
	s.State = StateStopped
	s.Restarts = 0
	s.Unlock()
	s.infof("stopped")
	if s.logger != nil {
		s.logger.Close()
		s.logger = nil
	}
	return nil
}

func (s *Service) log(level log.LLevel, prefix string, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	log.Logf(level, "[%s] %s", s.Name(), msg)
	if s.logger != nil {
		s.logger.Write(prefix, []byte(msg))
	}
}

func (s *Service) errorf(format string, args ...interface{}) {
	s.log(log.LError, "error", format, args...)
}

func (s *Service) infof(format string, args ...interface{}) {
	s.log(log.LInfo, "info", format, args...)
}

func (s *Service) debugf(format string, args ...interface{}) {
	s.log(log.LDebug, "debug", format, args...)
}
