package main

import (
	"errors"
	"gnd.la/log"
	"os"
	"os/signal"
	"os/user"
	"sync"
)

var services struct {
	sync.Mutex
	status []*Status
}

func daemonMain() error {
	u, err := user.Current()
	if err != nil {
		return err
	}
	if u.Uid != "0" {
		return errors.New("govenator daemon must be run as root")
	}
	configs, err := ParseConfigs()
	if err != nil {
		return err
	}
	services.Lock()
	services.status = make([]*Status, len(configs))
	for ii, v := range configs {
		services.status[ii] = &Status{
			Config: v,
		}
		go services.status[ii].Run()
	}
	services.Unlock()
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	// Wait for signal
	<-c
	services.Lock()
	for _, v := range services.status {
		v.Stop()
	}
	services.Unlock()
	log.Debugf("daemon exiting")
	return nil
}
