package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
)

type waiter struct {
	cmd     *exec.Cmd
	fn      func(error)
	readers []io.ReadCloser
	writers []io.WriteCloser
}

type Monitor struct {
	sync.Mutex
	waiters []*waiter
	quit    *quit
}

func (m *Monitor) removeCmd(cmd *exec.Cmd) {
	for ii, v := range m.waiters {
		if v.cmd == cmd {
			for ii, r := range v.readers {
				r.Close()
				io.Copy(v.writers[ii], r)
				v.writers[ii].Close()
			}
			m.waiters = append(m.waiters[:ii], m.waiters[ii+1:]...)
			break
		}
	}
}

func (m *Monitor) waiter(cmd *exec.Cmd) *waiter {
	m.Lock()
	defer m.Unlock()
	for _, v := range m.waiters {
		if v.cmd == cmd {
			return v
		}
	}
	panic(fmt.Sprintf("no waiter for cmd %v", cmd))
}

func (m *Monitor) waitForExited() {
	m.Lock()
	defer m.Unlock()
	var ws syscall.WaitStatus
	waiters := make([]*waiter, len(m.waiters))
	copy(waiters, m.waiters)
	for _, v := range waiters {
		if v.cmd.Process == nil {
			continue
		}
		if pid, err := syscall.Wait4(v.cmd.Process.Pid, &ws, syscall.WNOHANG, nil); err == nil && pid == v.cmd.Process.Pid {
			exitStatus := ws.ExitStatus()
			var err error
			if exitStatus != 0 {
				err = fmt.Errorf("exit status %d", exitStatus)
			}
			v.fn(err)
			m.removeCmd(v.cmd)
		}
	}
}

func (m *Monitor) Run() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Signal(syscall.SIGCHLD))
loop:
	for {
		select {
		case <-ch:
			m.waitForExited()
		case <-m.quit.stop:
			m.quit.sendStopped()
			break loop
		}
	}
	signal.Stop(ch)
	close(ch)
}

func (m *Monitor) Start(cmd *exec.Cmd, logger *Logger, fn func(error)) error {
	m.Lock()
	defer m.Unlock()
	wa := &waiter{cmd: cmd, fn: fn}
	m.waiters = append(m.waiters, wa)
	if logger != nil {
		m.setOutputFd(wa, &cmd.Stdout, logger.Stdout)
		m.setOutputFd(wa, &cmd.Stderr, logger.Stderr)
	}
	err := cmd.Start()
	if err != nil {
		m.removeCmd(cmd)
	}
	return err
}

func (m *Monitor) setOutputFd(wa *waiter, out *io.Writer, input io.Writer) {
	*out = input
}
