package main

import (
	"errors"
	"fmt"
	"net"

	"path/filepath"
	"sync"
	"time"

	"gnd.la/log"
	"gopkg.in/fsnotify.v0"
)

func formatTime(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}

var (
	newLine = []byte{'\n'}
)

type Governator struct {
	ServerAddr string
	mu         sync.Mutex
	services   []*Service
	configDir  string
	quit       *quit
	quits      []*quit
	monitor    *Monitor
}

func NewGovernator(configDir string) (*Governator, error) {
	mon, err := newMonitor()
	if err != nil {
		return nil, err
	}
	return &Governator{
		configDir: configDir,
		monitor:   mon,
	}, nil
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
			if cfg.Log != nil {
				cfg.Log.Name = cfg.Name
			}
			break
		}
	}
}

func (g *Governator) serviceByFilenameLocked(name string) (int, *Service) {
	for ii, v := range g.services {
		if v.Config.File == name {
			return ii, v
		}
	}
	return -1, nil
}

func (g *Governator) serviceByFilename(name string) (int, *Service) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.serviceByFilenameLocked(name)
}

func (g *Governator) startWatching() error {
	q := newQuit()
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
				if g.shouldIgnoreFile(name, ev.IsDelete() || ev.IsRename()) {
					break
				}
				g.mu.Lock()
				switch {
				case ev.IsCreate():
					cfg := g.parseConfig(name)
					// If a file is moved or copied over an already existing
					// service configuration, we only receive a CREATE. Check
					// if we already have a configuration with that name and, in
					// that case, stop it, update its config and restart.
					if _, s := g.serviceByFilenameLocked(name); s != nil {
						s.updateConfig(cfg)
						g.sortServices()
						s.Stop()
						s.Start()
					} else {
						name, err := g.addServiceLocked(cfg)
						if err != nil {
							log.Errorf("error adding service %s: %s", cfg.ServiceName(), err)
						} else if cfg.Start {
							log.Debugf("starting service %s", name)
							s, _ := g.serviceByNameLocked(name)
							s.Start()
						}
					}
				case ev.IsDelete() || ev.IsRename():
					if ii, s := g.serviceByFilenameLocked(name); s != nil {
						log.Debugf("removed service %s", s.Name())
						if s.State == StateStarted {
							s.Stop()
						}
						g.services = append(g.services[:ii], g.services[ii+1:]...)
					}
				case ev.IsModify():
					if _, s := g.serviceByFilenameLocked(name); s != nil {
						cfg := g.parseConfig(name)
						s.updateConfig(cfg)
						g.sortServices()
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
	if err := watcher.Watch(g.servicesDir()); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.quits = append(g.quits, q)
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

func (g *Governator) startServices(conn net.Conn) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, s := range g.services {
		if s.Config.Start {
			g.startServiceLocked(conn, s)
		}
	}
	return nil
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

func (g *Governator) stopServices(conn net.Conn) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	// Stop in reverse order, to respect priorities
	for ii := len(g.services) - 1; ii >= 0; ii-- {
		s := g.services[ii]
		if _, err := g.stopServiceLocked(conn, s); err != nil {
			return err
		}
	}
	return nil
}

func (g *Governator) sortServices() {
	servicesByPriority(g.services).Sort()
}

func (g *Governator) serviceByNameLocked(name string) (*Service, error) {
	for _, v := range g.services {
		if v.Name() == name {
			return v, nil
		}
	}
	return nil, fmt.Errorf("no service named %s", name)
}

func (g *Governator) serviceByName(name string) (*Service, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.serviceByNameLocked(name)
}

func (g *Governator) addServiceLocked(cfg *Config) (string, error) {
	g.ensureUniqueName(cfg)
	s := newService(cfg)
	s.monitor = g.monitor
	g.services = append(g.services, s)
	g.sortServices()
	return cfg.Name, nil
}

func (g *Governator) AddService(cfg *Config) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.addServiceLocked(cfg)
}

func (g *Governator) Start(name string) error {
	if name == "all" {
		return g.startServices(nil)
	}
	s, err := g.serviceByName(name)
	if err != nil {
		return err
	}
	return s.Start()
}

func (g *Governator) Stop(name string) error {
	if name == "all" {
		return g.stopServices(nil)
	}
	s, err := g.serviceByName(name)
	if err != nil {
		return err
	}
	return s.Stop()
}

func (g *Governator) State(name string) (State, error) {
	s, err := g.serviceByName(name)
	if err != nil {
		return 0, err
	}
	return s.State, nil
}

func (g *Governator) LoadServices() error {
	configs, err := g.parseConfigs()
	if err != nil {
		return err
	}
	for _, v := range configs {
		g.AddService(v)
	}
	return nil
}

func (g *Governator) Run() error {
	g.mu.Lock()
	if g.quit != nil {
		g.mu.Unlock()
		return errors.New("governator already running")
	}
	g.quit = newQuit()
	g.mu.Unlock()
	go g.monitor.Run()
	if g.configDir != "" {
		if err := g.startWatching(); err != nil {
			log.Errorf("error watching %s, configuration won't be automatically updated: %s", g.servicesDir(), err)
		}
	}
	if g.ServerAddr != "" {
		if err := g.startServer(); err != nil {
			log.Errorf("error starting server, can't receive remote commands: %s", err)
		}
	}
	g.startServices(nil)
	g.quit.waitForStop()
	g.mu.Lock()
	for _, q := range g.quits {
		q.sendStop()
	}
	// Wait for goroutines to exit cleanly
	for _, q := range g.quits {
		q.waitForStopped()
	}
	g.quits = nil
	g.mu.Unlock()
	// Release the lock for stopServices
	g.stopServices(nil)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.monitor.quit.sendStop()
	g.monitor.quit.waitForStopped()
	log.Debugf("daemon exiting")
	g.quit.sendStopped()
	g.quit = nil
	return nil
}

func (g *Governator) StopRunning() {
	g.mu.Lock()
	quit := g.quit
	g.mu.Unlock()
	if quit != nil {
		quit.sendStop()
		quit.waitForStopped()
	}
}
