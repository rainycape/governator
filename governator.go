package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"code.google.com/p/go.exp/fsnotify"
	"gnd.la/log"
)

func formatTime(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}

var (
	newLine = []byte{'\n'}
)

type Governator struct {
	mu       sync.Mutex
	services []*Service
}

func NewGovernator() (*Governator, error) {
	return &Governator{}, nil
}

func (g *Governator) ensureUniqueName(cfg *Config) {
	ii := 1
	orig := cfg.ServiceName()
	for {
		name := cfg.ServiceName()
		unique := name != "all"
		for _, v := range g.services {
			if v != nil && name == v.Name() {
				unique = false
				cfg.Name = fmt.Sprintf("%s-%d", orig, ii)
				ii++
				break
			}
		}
		if unique {
			break
		}
	}
}

func (g *Governator) serviceByFilename(name string) (int, *Service) {
	for ii, v := range g.services {
		if v.Config.File == name {
			return ii, v
		}
	}
	return -1, nil
}

func (g *Governator) startWatching(q *quit) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	go func() {
	End:
		for {
			select {
			case ev := <-watcher.Event:
				log.Debugf("file watcher event %s", ev)
				name := filepath.Base(ev.Name)
				if shouldIgnoreFile(name, ev.IsDelete() || ev.IsRename()) {
					break
				}
				g.mu.Lock()
				switch {
				case ev.IsCreate():
					cfg := ParseConfig(name)
					// If a file is moved or copied over an already existing
					// service configuration, we only receive a CREATE. Check
					// if we already have a configuration with that name and, in
					// that case, stop it, update its config and restart.
					if _, s := g.serviceByFilename(name); s != nil {
						s.updateConfig(cfg)
						servicesByPriority(g.services).Sort()
					} else {
						g.ensureUniqueName(cfg)
						log.Debugf("added service %s", cfg.ServiceName())
						s := newService(cfg)
						g.services = append(g.services, s)
						servicesByPriority(g.services).Sort()
						s.Start()
					}
				case ev.IsDelete() || ev.IsRename():
					if ii, s := g.serviceByFilename(name); s != nil {
						log.Debugf("removed service %s", s.Name())
						if s.State == StateStarted {
							s.Stop()
						}
						g.services = append(g.services[:ii], g.services[ii+1:]...)
					}
				case ev.IsModify():
					if _, s := g.serviceByFilename(name); s != nil {
						cfg := ParseConfig(name)
						s.updateConfig(cfg)
						servicesByPriority(g.services).Sort()
					}
				default:
					log.Errorf("unhandled event: %s\n", ev)
				}
				g.mu.Unlock()
			case err := <-watcher.Error:
				log.Errorf("error watching: %s", err)
			case <-q.stop:
				watcher.Close()
				q.sendStopped()
				break End
			}
		}
	}()
	if err := watcher.Watch(servicesDir()); err != nil {
		return err
	}
	return nil
}

func (g *Governator) startService(conn net.Conn, s *Service) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.startServiceLocked(conn, s)
}

func (g *Governator) startServiceLocked(conn net.Conn, s *Service) error {
	name := s.Name()
	encodeResponse(conn, respOk, fmt.Sprintf("starting %s\n", name))
	if serr := s.Start(); serr != nil {
		return encodeResponse(conn, respErr, fmt.Sprintf("error starting %s: %s\n", name, serr))
	}
	return encodeResponse(conn, respOk, fmt.Sprintf("started %s\n", name))
}

func (g *Governator) startServices(conn net.Conn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, s := range g.services {
		if s.Config.Start {
			g.startServiceLocked(conn, s)
		}
	}
}

func (g *Governator) stopService(conn net.Conn, s *Service) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stopServiceLocked(conn, s)
}

func (g *Governator) stopServiceLocked(conn net.Conn, s *Service) (bool, error) {
	name := s.Name()
	encodeResponse(conn, respOk, fmt.Sprintf("stopping %s\n", name))
	if serr := s.Stop(); serr != nil {
		return false, encodeResponse(conn, respErr, fmt.Sprintf("error stopping %s: %s\n", name, serr))
	}
	return true, encodeResponse(conn, respOk, fmt.Sprintf("stopped %s\n", name))
}

func (g *Governator) stopServices(conn net.Conn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	// Stop in reverse order, to respect priorities
	for ii := len(g.services) - 1; ii >= 0; ii-- {
		s := g.services[ii]
		g.stopServiceLocked(conn, s)
	}
}

func (g *Governator) LoadServices() error {
	configs, err := ParseConfigs()
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, v := range configs {
		g.ensureUniqueName(v)
		s := newService(v)
		g.services = append(g.services, s)
	}
	return nil
}

func (g *Governator) Main() error {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Signal(syscall.SIGTERM), os.Kill)
	u, err := user.Current()
	if err != nil {
		return err
	}
	if u.Uid != "0" {
		return errors.New("govenator daemon must be run as root")
	}
	g.mu.Lock()
	servicesByPriority(g.services).Sort()
	g.mu.Unlock()
	quitWatcher := newQuit()
	if err := g.startWatching(quitWatcher); err != nil {
		log.Errorf("error watching %s, configuration won't be automatically updated: %s", servicesDir(), err)
	}
	quitServer := newQuit()
	if err := g.startServer(quitServer); err != nil {
		log.Errorf("error starting server, can't receive remote commands: %s", err)
	}
	g.startServices(nil)
	// Wait for signal
	<-c
	quitWatcher.sendStop()
	quitServer.sendStop()
	// Wait for goroutines to exit cleanly
	<-quitWatcher.stopped
	<-quitServer.stopped
	g.stopServices(nil)
	log.Debugf("daemon exiting")
	return nil
}
