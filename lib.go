package main

import (
	"encoding/binary"
	"fmt"
	"gnd.la/log"
	"io"
	"os/exec"
	"sync"
	"time"
)

const (
	SocketPath = "/tmp/governator.sock"
	AppName    = "governator"
)

type resp uint8

const (
	respEnd resp = iota
	respOk
	respErr
)

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
		s.Lock()
		if s.State != StateStarted {
			s.Unlock()
			break
		}
		if since := time.Since(s.Started); since < time.Second {
			// Consider failure
			s.State = StateFailed
			s.Err = fmt.Errorf("exited too fast (%s)", since)
			s.Unlock()
			log.Errorf("%s %s", name, s.Err)
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
	s.Restarts = 0
	s.Unlock()
	if s.Cmd != nil && s.Cmd.Process != nil {
		log.Infof("Stopping %s", s.Config.ServiceName())
		s.Cmd.Process.Kill()
	}
}

func encodeString(w io.Writer, s string) error {
	length := uint32(len(s))
	if err := codecWrite(w, length); err != nil {
		return err
	}
	if _, err := io.WriteString(w, s); err != nil {
		return err
	}
	return nil
}

func decodeString(r io.Reader) (string, error) {
	var length uint32
	if err := codecRead(r, &length); err != nil {
		return "", err
	}
	s := make([]byte, length)
	if _, err := io.ReadFull(r, s); err != nil {
		return "", err
	}
	return string(s), nil
}

func encodeArgs(w io.Writer, args []string) error {
	count := int32(len(args))
	codecWrite(w, count)
	for _, v := range args {
		if err := encodeString(w, v); err != nil {
			return err
		}
	}
	return nil
}

func decodeArgs(r io.Reader) ([]string, error) {
	var count uint32
	if err := codecRead(r, &count); err != nil {
		return nil, err
	}
	args := make([]string, int(count))
	for ii := 0; ii < int(count); ii++ {
		s, err := decodeString(r)
		if err != nil {
			return nil, err
		}
		args[ii] = s
	}
	return args, nil
}

func encodeResponse(w io.Writer, r resp, s string) error {
	if err := codecWrite(w, r); err != nil {
		return err
	}
	if err := encodeString(w, s); err != nil {
		return err
	}
	return nil
}

func decodeResponse(r io.Reader) (resp, string, error) {
	var re resp
	if err := codecRead(r, &re); err != nil {
		return 0, "", err
	}
	s, err := decodeString(r)
	return re, s, err
}

func codecRead(r io.Reader, out interface{}) error {
	return binary.Read(r, binary.BigEndian, out)
}

func codecWrite(w io.Writer, in interface{}) error {
	return binary.Write(w, binary.BigEndian, in)
}
