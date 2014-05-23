package main

import (
	"syscall"
)

type Limit struct {
	Resource int
	Rlimit   *syscall.Rlimit
}

func SetLimits(cfg *Config) ([]*Limit, error) {
	var limits []*Limit
	if cfg.MaxOpenFiles > 0 {
		var limit syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
			return nil, err
		}
		limits = append(limits, &Limit{Resource: syscall.RLIMIT_NOFILE, Rlimit: &limit})
		max := uint64(cfg.MaxOpenFiles)
		if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &syscall.Rlimit{Cur: max, Max: max}); err != nil {
			return nil, err
		}
	}
	return limits, nil
}

func RestoreLimits(limits []*Limit) error {
	for _, v := range limits {
		if err := syscall.Setrlimit(v.Resource, v.Rlimit); err != nil {
			return err
		}
	}
	return nil
}
