package main

import (
	"fmt"
	"gnd.la/log"
	"math"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	// avoids starting multiple services
	// at the same time, to enforce resource limits
	startLock sync.Mutex
)

type State uint8

const (
	StateStopped State = iota
	StateStopping
	StateStarted
	StateStarting
	StateBackoff
	StateFailed
)

func (s State) isRunState() bool {
	return s == StateStarted || s == StateStarting
}

func (s State) canStop() bool {
	return s.isRunState() || s == StateBackoff
}

const (
	minTime    = time.Second
	maxRetries = 10
)

type Service struct {
	mu         sync.Mutex // protects access to the fields
	st         sync.Mutex // prevents start/stop from running concurrently
	Config     *Config
	Cmd        *exec.Cmd
	State      State
	Started    time.Time
	Restarts   int
	Err        error
	stopCh     chan error
	errCh      chan error
	retries    int
	startTimer *time.Timer
	nextStart  time.Time
}

func newService(cfg *Config) *Service {
	return &Service{Config: cfg, stopCh: make(chan error)}
}

func (s *Service) Name() string {
	return s.Config.ServiceName()
}

func (s *Service) sendErr(ch *chan<- error, err error) {
	if err != nil && s.State != StateStopping {
		s.errorf("%v", err)
	}
	s.Err = err
	if ch != nil && *ch != nil {
		select {
		case *ch <- err:
			*ch = nil
		default:
		}
	}
	if s.errCh != nil {
		select {
		case s.errCh <- err:
		default:
		}
	}
}

func (s *Service) Start() error {
	s.st.Lock()
	defer s.st.Unlock()
	if s.State.isRunState() {
		return nil
	}
	if err := s.Config.Log.Open(); err != nil {
		return err
	}
	if err := s.startService(); err != nil {
		return err
	}
	if err := s.startWatchdog(); err != nil {
		s.errorf("error starting watchdog %s: %s", s.Name(), err)
	}
	return nil
}

func (s *Service) startIn(d time.Duration) {
	s.stopTimer()
	s.startTimer = time.AfterFunc(d, func() { s.Run(nil) })
	s.nextStart = time.Now().Add(d)
}

func (s *Service) started(ch *chan<- error) {
	s.State = StateStarted
	s.stopTimer()
	s.sendErr(ch, nil)
	s.retries = 0
}

func (s *Service) startFailed(ch *chan<- error, err error) {
	s.sendErr(ch, err)
	if s.retries < maxRetries-1 {
		s.State = StateBackoff
		duration := time.Second * time.Duration(math.Pow(2, float64(s.retries)))
		s.startIn(duration)
		s.retries++
		s.infof("will retry in %s", duration)
	} else {
		s.State = StateFailed
		s.Cmd = nil
		s.errorf("maximum retries reached")
	}
}

func (s *Service) untilNextRestart() time.Duration {
	return s.nextStart.Sub(time.Now())
}

func (s *Service) Run(ch chan<- error) {
	s.mu.Lock()
	s.State = StateStarted
	s.mu.Unlock()
	for {
		s.mu.Lock()
		if s.State != StateStarted {
			s.mu.Unlock()
			break
		}
		cmd, err := s.Config.Cmd()
		if err != nil {
			s.State = StateFailed
			s.errorf("could not initialize: %s", err)
			s.sendErr(&ch, err)
			s.mu.Unlock()
			break
		}
		if s.Config.Log != nil {
			cmd.Stdout = s.Config.Log.Stdout
			cmd.Stderr = s.Config.Log.Stderr
		}
		s.Cmd = cmd
		s.Started = time.Now()
		s.infof("starting")
		startLock.Lock()
		limits, err := SetLimits(s.Config)
		if err != nil {
			s.errorf("error setting service limits: %s", err)
		}
		serr := s.Cmd.Start()
		if err := RestoreLimits(limits); err != nil {
			s.errorf("error restoring limits: %s", err)
		}
		startLock.Unlock()
		if serr != nil {
			s.errorf("failed to start: %s", serr)
			s.startFailed(&ch, serr)
			s.mu.Unlock()
			break
		}
		s.mu.Unlock()
		timer := time.AfterFunc(time.Duration(float64(minTime)*1.1), func() {
			s.mu.Lock()
			// Clear any potentially stored errors
			s.started(&ch)
			s.mu.Unlock()
			s.infof("started")
		})
		err = s.Cmd.Wait()
		timer.Stop()
		s.mu.Lock()
		if s.State != StateStarted {
			s.Cmd = nil
			s.sendErr(&ch, err)
			s.stopCh <- nil
			s.mu.Unlock()
			break
		}
		if since := time.Since(s.Started); since < minTime {
			// Consider failure
			s.startFailed(&ch, fmt.Errorf("exited too fast (%s)", since))
			s.mu.Unlock()
			break
		}
		s.Restarts++
		s.mu.Unlock()
		if err != nil {
			s.infof("exited with error %s - restarting", err)
		} else {
			s.infof("exited without error - restarting")
		}
	}
}

func (s *Service) Stop() error {
	s.st.Lock()
	defer s.st.Unlock()
	s.stopWatchdog()
	if err := s.stopService(); err != nil {
		s.startWatchdog()
		return err
	}
	return nil
}

func (s *Service) startWatchdog() error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Config.Watchdog != nil {
		s.Config.Watchdog.Stop()
	}
}

func (s *Service) stopTimerLocking() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopTimer()
}

func (s *Service) stopTimer() {
	if s.startTimer != nil {
		s.startTimer.Stop()
		s.startTimer = nil
		s.nextStart = time.Time{}
	}
}

func (s *Service) startService() error {
	ch := make(chan error)
	s.stopTimerLocking()
	go s.Run(ch)
	err := <-ch
	close(ch)
	return err
}

func (s *Service) stopService() error {
	s.stopTimer()
	s.mu.Lock()
	if !s.State.isRunState() {
		if s.State.canStop() {
			s.infof("stopped")
		}
		s.State = StateStopped
		s.mu.Unlock()
		return nil
	}
	prevState := s.State
	s.State = StateStopping
	s.infof("stopping")
	p := s.Cmd.Process
	s.mu.Unlock()
	if s != nil {
		stopped := isStoppedErr(p.Signal(os.Signal(syscall.SIGTERM)))
		if !stopped {
			select {
			case <-s.stopCh:
				stopped = true
			case <-time.After(10 * time.Second):
				stopped = isStoppedErr(p.Kill())
			}
			if !stopped {
				select {
				case <-s.stopCh:
				case <-time.After(2 * time.Second):
					// sending signal 0 checks that the process is
					// alive and we're allowed to send the signal
					// without actually sending anything
					if isStoppedErr(p.Signal(syscall.Signal(0))) {
						break
					}
					s.mu.Lock()
					s.State = prevState
					s.mu.Unlock()
					err := fmt.Errorf("could not stop, probably stuck")
					s.errorf("%v", err)
					return err
				}
			}
		}
	}
	s.mu.Lock()
	s.State = StateStopped
	s.Restarts = 0
	s.mu.Unlock()
	if s.Config.Log != nil {
		s.Config.Log.Close()
	}
	s.infof("stopped")
	return nil
}

func (s *Service) log(level log.LLevel, prefix string, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	log.Logf(level, "[%s] %s", s.Name(), msg)
	if s.Config.Log != nil {
		s.Config.Log.WriteString(prefix, msg)
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

func (s *Service) updateConfig(cfg *Config) {
	if reflect.DeepEqual(s.Config, cfg) {
		// there were changes to the file which don't affect the conf
		return
	}
	log.Debugf("changed service %s's configuration", s.Name())
	start := false
	if s.State == StateStarted {
		start = s.Stop() == nil
	}
	s.Config = cfg
	if start {
		s.Start()
	}
}

func isStoppedErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "process already finished")
}
