// +build !linux

package main

import (
	"syscall"
)

func prepareSysProcAttr(attr *syscall.SysProcAttr) {}
