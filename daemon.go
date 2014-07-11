package main

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
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

var services struct {
	sync.Mutex
	list []*Service
}

type servicesByPriority []*Service

func (s servicesByPriority) Len() int           { return len(s) }
func (s servicesByPriority) Less(i, j int) bool { return s[i].Config.Priority < s[j].Config.Priority }
func (s servicesByPriority) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s servicesByPriority) Sort()              { sort.Stable(s) }

type quit struct {
	stop    chan bool
	stopped chan bool
}

func newQuit() *quit {
	return &quit{
		stop:    make(chan bool, 1),
		stopped: make(chan bool, 1),
	}
}

func (q *quit) sendStop() {
	q.stop <- true
}

func (q *quit) sendStopped() {
	q.stopped <- true
}

func ensureUniqueName(cfg *Config) {
	ii := 1
	orig := cfg.ServiceName()
	for {
		name := cfg.ServiceName()
		unique := name != "all"
		for _, v := range services.list {
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

func serviceByFilename(name string) (int, *Service) {
	for ii, v := range services.list {
		if v.Config.File == name {
			return ii, v
		}
	}
	return -1, nil
}

func startWatching(q *quit) error {
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
				services.Lock()
				switch {
				case ev.IsCreate():
					cfg := ParseConfig(name)
					// If a file is moved or copied over an already existing
					// service configuration, we only receive a CREATE. Check
					// if we already have a configuration with that name and, in
					// that case, stop it, update its config and restart.
					if _, s := serviceByFilename(name); s != nil {
						s.updateConfig(cfg)
						servicesByPriority(services.list).Sort()
					} else {
						ensureUniqueName(cfg)
						log.Debugf("added service %s", cfg.ServiceName())
						s := newService(cfg)
						services.list = append(services.list, s)
						servicesByPriority(services.list).Sort()
						s.Start()
					}
				case ev.IsDelete() || ev.IsRename():
					if ii, s := serviceByFilename(name); s != nil {
						log.Debugf("removed service %s", s.Name())
						if s.State == StateStarted {
							s.Stop()
						}
						services.list = append(services.list[:ii], services.list[ii+1:]...)
					}
				case ev.IsModify():
					if _, s := serviceByFilename(name); s != nil {
						cfg := ParseConfig(name)
						s.updateConfig(cfg)
						servicesByPriority(services.list).Sort()
					}
				default:
					log.Errorf("unhandled event: %s\n", ev)
				}
				services.Unlock()
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

func startService(conn net.Conn, s *Service) error {
	name := s.Name()
	encodeResponse(conn, respOk, fmt.Sprintf("starting %s\n", name))
	if serr := s.Start(); serr != nil {
		return encodeResponse(conn, respErr, fmt.Sprintf("error starting %s: %s\n", name, serr))
	}
	return encodeResponse(conn, respOk, fmt.Sprintf("started %s\n", name))
}

func stopService(conn net.Conn, s *Service) (bool, error) {
	name := s.Name()
	encodeResponse(conn, respOk, fmt.Sprintf("stopping %s\n", name))
	if serr := s.Stop(); serr != nil {
		return false, encodeResponse(conn, respErr, fmt.Sprintf("error stopping %s: %s\n", name, serr))
	}
	return true, encodeResponse(conn, respOk, fmt.Sprintf("stopped %s\n", name))
}

func serveConn(conn net.Conn) error {
	defer conn.Close()
	args, err := decodeArgs(conn)
	if err != nil {
		return fmt.Errorf("error decoding arguments: %s", err)
	}
	if len(args) > 0 {
		var err error
		var st *Service
		var name string
		cmd := strings.ToLower(args[0])
		if cmd == "start" || cmd == "stop" || cmd == "restart" || cmd == "log" {
			if len(args) != 2 {
				err = encodeResponse(conn, respErr, fmt.Sprintf("command %s requires exactly one argument\n", cmd))
				cmd = ""
			}
			if cmd != "" && (cmd == "log" || args[1] != "all") {
				services.Lock()
				for _, v := range services.list {
					if sn := v.Name(); sn == args[1] {
						st = v
						name = sn
						break
					}
				}
				services.Unlock()
				if st == nil {
					err = encodeResponse(conn, respErr, fmt.Sprintf("no service named %s\n", args[1]))
					cmd = ""
				}
			}
		}
		switch cmd {
		case "":
			// cmd already handled
		case "start":
			if st == nil {
				// all
				startServices(conn)
				break
			}
			if st.State == StateStarted {
				err = encodeResponse(conn, respOk, fmt.Sprintf("%s is already running\n", name))
			} else {
				err = startService(conn, st)
			}
		case "stop":
			if st == nil {
				// all
				stopServices(conn)
				break
			}
			if !st.State.canStop() {
				err = encodeResponse(conn, respOk, fmt.Sprintf("%s is not running\n", name))
			} else {
				_, err = stopService(conn, st)
			}
		case "restart":
			if st == nil {
				// all
				stopServices(conn)
				startServices(conn)
				break
			}
			stopped := true
			if st.State.isRunState() {
				stopped, err = stopService(conn, st)
			}
			if stopped {
				err = startService(conn, st)
			}
		case "list":
			var buf bytes.Buffer
			w := tabwriter.NewWriter(&buf, 4, 4, 4, ' ', 0)
			fmt.Fprint(w, "SERVICE\tSTATUS\t\n")
			services.Lock()
			for _, v := range services.list {
				fmt.Fprintf(w, "%s\t", v.Name())
				switch v.State {
				case StateStopped:
					fmt.Fprint(w, "STOPPED")
				case StateStopping:
					fmt.Fprint(w, "STOPPING")
				case StateStarting:
					fmt.Fprint(w, "STARTING")
				case StateStarted:
					if v.Restarts > 0 {
						fmt.Fprintf(w, "RUNNING since %s - %d restarts", formatTime(v.Started), v.Restarts)
					} else {
						fmt.Fprintf(w, "RUNNING since %s", formatTime(v.Started))
					}
				case StateBackoff:
					fmt.Fprintf(w, "BACKOFF - %s - next retry in %s", v.Err, v.untilNextRestart())
				case StateFailed:
					fmt.Fprintf(w, "FAILED - %s", v.Err)
				default:
					panic("invalid state")
				}
				fmt.Fprint(w, "\t\n")
			}
			services.Unlock()
			w.Flush()
			buf.WriteString("\n")
			err = encodeResponse(conn, respOk, buf.String())
		case "log":
			if st.State != StateStarted {
				err = encodeResponse(conn, respErr, fmt.Sprintf("%s is not running\n", name))
				break
			}
			if st.Config.Log.Monitor != nil {
				err = encodeResponse(conn, respErr, fmt.Sprintf("%s is already being monitored\n", name))
				break
			}
			ch := make(chan bool, 1)
			st.Config.Log.Monitor = func(prefix string, b []byte) {
				var buf bytes.Buffer
				buf.WriteByte('[')
				buf.WriteString(prefix)
				buf.WriteString("] ")
				buf.Write(b)
				if b[len(b)-1] != '\n' {
					buf.Write(newLine)
				}
				encodeResponse(conn, respOk, buf.String())
			}
			go func() {
				// log stops when the client sends something over the connection
				// or the connection is closed
				b := make([]byte, 1)
				conn.Read(b)
				conn.Close()
				ch <- true
			}()
			<-ch
			st.Config.Log.Monitor = nil
			return nil
		case "conf":
			if len(args) != 2 {
				err = encodeResponse(conn, respErr, fmt.Sprintf("conf requires one argument, %d given", len(args)-1))
				break
			}
			var value string
			switch strings.ToLower(args[1]) {
			case "config-dir":
				value = *configDir
			case "services-dir":
				value = servicesDir()
			}
			if !filepath.IsAbs(value) {
				p, err := filepath.Abs(value)
				if err != nil {
					return err
				}
				value = p
			}
			r := respOk
			if value == "" {
				r = respErr
				value = fmt.Sprintf("unknown configuration parameter %q", args[1])
			} else {
				value += "\n"
			}
			err = encodeResponse(conn, r, value)
		case "wait-for":
			// wait until a service is registered
			if len(args) != 2 {
				err = encodeResponse(conn, respErr, fmt.Sprintf("wait-for requires one argument, %d given", len(args)-1))
				break
			}
			start := time.Now()
			// wait 10 seconds at max
			found := false
			for ii := 0; ii < 10; ii++ {
				if _, s := serviceByFilename(args[1]); s != nil {
					found = true
					break
				}
				time.Sleep(time.Second)
			}
			if found {
				err = encodeResponse(conn, respOk, "")
			} else {
				err = encodeResponse(conn, respErr, fmt.Sprintf("service %s not found after waiting %s", args[1], time.Since(start)))
			}
		default:
			err = encodeResponse(conn, respErr, fmt.Sprintf("unknown command %s - %s\n", cmd, help))
			if err != nil {
				return err
			}
		}
	}
	return encodeResponse(conn, respEnd, "")
}

func startServer(q *quit) error {
	os.Remove(SocketPath)
	server, err := net.Listen("unix", SocketPath)
	if err != nil {
		return err
	}
	if gid := getGroupId(AppName); gid >= 0 {
		os.Chown(SocketPath, 0, gid)
		os.Chmod(SocketPath, 0775)
	}
	conns := make(chan net.Conn, 10)
	go func() {
		for {
			conn, err := server.Accept()
			if err != nil {
				log.Errorf("error accepting connection: %s", err)
			}
			log.Debugf("new connection %s", conn.RemoteAddr())
			conns <- conn
		}
	}()
	go func() {
		for {
			select {
			case <-q.stop:
				os.Remove(SocketPath)
				q.sendStopped()
				return
			case conn := <-conns:
				go func() {
					if err := serveConn(conn); err != nil {
						log.Errorf("error serving connection: %s", err)
					}
				}()
			}
		}
	}()
	return nil
}

func startServices(conn net.Conn) {
	services.Lock()
	defer services.Unlock()
	for _, s := range services.list {
		if s.Config.Start {
			startService(conn, s)
		}
	}
}

func stopServices(conn net.Conn) {
	services.Lock()
	defer services.Unlock()
	// Stop in reverse order, to respect priorities
	for ii := len(services.list) - 1; ii >= 0; ii-- {
		s := services.list[ii]
		stopService(conn, s)
	}
}

func daemonMain() error {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Signal(syscall.SIGTERM), os.Kill)
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
	services.list = make([]*Service, len(configs))
	for ii, v := range configs {
		ensureUniqueName(v)
		s := newService(v)
		services.list[ii] = s
	}
	servicesByPriority(services.list).Sort()
	services.Unlock()
	quitWatcher := newQuit()
	if err := startWatching(quitWatcher); err != nil {
		log.Errorf("error watching %s, configuration won't be automatically updated: %s", servicesDir(), err)
	}
	quitServer := newQuit()
	if err := startServer(quitServer); err != nil {
		log.Errorf("error starting server, can't receive remote commands: %s", err)
	}
	startServices(nil)
	// Wait for signal
	<-c
	quitWatcher.sendStop()
	quitServer.sendStop()
	// Wait for goroutines to exit cleanly
	<-quitWatcher.stopped
	<-quitServer.stopped
	stopServices(nil)
	log.Debugf("daemon exiting")
	return nil
}
