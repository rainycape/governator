package main

import (
	"code.google.com/p/go.exp/fsnotify"
	"errors"
	"gnd.la/log"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"reflect"
	"sync"
)

var services struct {
	sync.Mutex
	status []*Status
}

func startWatching(ch chan bool) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	go func() {
	End:
		for {
			select {
			case ev := <-watcher.Event:
				name := filepath.Base(ev.Name)
				if shouldIgnoreFile(name) {
					break
				}
				services.Lock()
				switch {
				case ev.IsCreate():
					cfg := ParseConfig(name)
					log.Debugf("added service %s", cfg.ServiceName())
					s := newStatus(cfg)
					services.status = append(services.status, s)
					go s.Run()
				case ev.IsDelete() || ev.IsRename():
					for ii := range services.status {
						s := services.status[ii]
						if s.Config.File == name {
							log.Debugf("removed service %s", s.Config.ServiceName())
							if s.State == StateStarted {
								s.Stop()
							}
							services.status = append(services.status[:ii], services.status[ii+1:]...)
							break
						}
					}
				case ev.IsModify():
					for _, v := range services.status {
						if v.Config.File == name {
							cfg := ParseConfig(name)
							if reflect.DeepEqual(v.Config, cfg) {
								// there were changes to the file which don't affect the conf
								break
							}
							log.Debugf("changed service %s's configuration", v.Config.ServiceName())
							if v.State == StateStarted {
								v.Stop()
							}
							v.Config = cfg
							go v.Run()
							break
						}
					}
				default:
					log.Errorf("unhandled event: %s\n", ev)
				}
				services.Unlock()
			case err := <-watcher.Error:
				log.Errorf("error watching: %s", err)
			case _ = <-ch:
				watcher.Close()
				ch <- true
				break End
			}
		}
	}()
	if err := watcher.Watch(*configDir); err != nil {
		return err
	}
	return nil
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
		s := newStatus(v)
		services.status[ii] = s
		go s.Run()
	}
	services.Unlock()
	quit := make(chan bool, 1)
	if err := startWatching(quit); err != nil {
		log.Errorf("error watching %s, configuration won't be automatically updated")
	}
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	// Wait for signal
	<-c
	quit <- true
	// Wait for goroutine to exit cleanly
	<-quit
	services.Lock()
	for _, v := range services.status {
		v.Stop()
	}
	services.Unlock()
	log.Debugf("daemon exiting")
	return nil
}
