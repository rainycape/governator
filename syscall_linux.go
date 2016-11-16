package main

import (
	"syscall"
)

func prepareSysProcAttr(attr *syscall.SysProcAttr) {
	attr.Pdeathsig = syscall.SIGQUIT // Send SIGQUIT to children if parent exits
}
