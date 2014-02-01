package main

import (
	"bytes"
	"code.google.com/p/go.exp/fsnotify"
	"errors"
	"fmt"
	"gnd.la/log"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"text/tabwriter"
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

func serveConn(conn net.Conn) error {
	defer conn.Close()
	args, err := decodeArgs(conn)
	if err != nil {
		return fmt.Errorf("error decoding arguments: %s", err)
	}
	if len(args) > 0 {
		var err error
		var st *Status
		var name string
		cmd := strings.ToLower(args[0])
		if cmd == "start" || cmd == "stop" || cmd == "restart" {
			if len(args) != 2 {
				err = encodeResponse(conn, respErr, fmt.Sprintf("command %s requires exactly one argument", cmd))
				cmd = ""
			}
			if cmd != "" {
				services.Lock()
				for _, v := range services.status {
					if sn := v.Config.ServiceName(); sn == args[1] {
						st = v
						name = sn
						break
					}
				}
				services.Unlock()
				if st == nil {
					err = encodeResponse(conn, respErr, fmt.Sprintf("no service named %s", args[1]))
					cmd = ""
				}
			}
		}
		switch cmd {
		case "":
			// cmd already handled
		case "start":
			if st.State == StateStarted {
				err = encodeResponse(conn, respErr, fmt.Sprintf("%s is already running", name))
			} else {
				go st.Run()
				err = encodeResponse(conn, respOk, fmt.Sprintf("started %s", name))
			}
		case "stop":
			if st.State != StateStarted {
				err = encodeResponse(conn, respErr, fmt.Sprintf("%s is not running", name))
			} else {
				st.Stop()
				err = encodeResponse(conn, respOk, fmt.Sprintf("stopped %s", name))
			}
		case "restart":
			if st.State == StateStarted {
				st.Stop()
				err = encodeResponse(conn, respOk, fmt.Sprintf("stopped %s", name))
				if err != nil {
					break
				}
			}
			go st.Run()
			err = encodeResponse(conn, respOk, fmt.Sprintf("started %s", name))
		case "list":
			var buf bytes.Buffer
			w := tabwriter.NewWriter(&buf, 4, 4, 4, ' ', 0)
			fmt.Fprint(w, "SERVICE\tSTATUS\t\n")
			services.Lock()
			for _, v := range services.status {
				fmt.Fprintf(w, "%s\t", v.Config.ServiceName())
				switch v.State {
				case StateStopped:
					fmt.Fprint(w, "STOPPED")
				case StateStarted:
					if v.Restarts > 0 {
						fmt.Fprintf(w, "RUNNING since %s - %d restarts", v.Started, v.Restarts)
					} else {
						fmt.Fprintf(w, "RUNNING since %s", v.Started)
					}
				case StateFailed:
					fmt.Fprintf(w, "FAILED - %s", v.Err)
				default:
					panic("invalid state")
				}
				fmt.Fprint(w, "\t\n")
			}
			services.Unlock()
			w.Flush()
			err = encodeResponse(conn, respOk, buf.String())
		default:
			err = encodeResponse(conn, respErr, fmt.Sprintf("unknown command %s - %s", cmd, help))
			if err != nil {
				return err
			}
		}
	}
	return encodeResponse(conn, respEnd, "")
}

func startServer(ch chan bool) error {
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
			conns <- conn
		}
	}()
	for {
		select {
		case <-ch:
			os.Remove(SocketPath)
			ch <- true
			return nil
		case conn := <-conns:
			go func() {
				if err := serveConn(conn); err != nil {
					log.Errorf("error serving connection: %s", err)
				}
			}()
		}
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
	quitWatcher := make(chan bool, 1)
	if err := startWatching(quitWatcher); err != nil {
		log.Errorf("error watching %s, configuration won't be automatically updated: %s", *configDir, err)
	}
	quitServer := make(chan bool, 1)
	if err := startServer(quitServer); err != nil {
		log.Errorf("error starting server, can't receive remote commands: %s", err)
	}
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	// Wait for signal
	<-c
	quitWatcher <- true
	quitServer <- true
	// Wait for goroutines to exit cleanly
	<-quitWatcher
	<-quitServer
	services.Lock()
	for _, v := range services.status {
		v.Stop()
	}
	services.Unlock()
	log.Debugf("daemon exiting")
	return nil
}
