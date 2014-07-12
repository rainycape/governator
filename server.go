package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"gnd.la/log"
)

func (g *Governator) serveConn(conn net.Conn) error {
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
				st, err = g.serviceByName(args[1])
				if err != nil {
					err = encodeResponse(conn, respErr, fmt.Sprintf("%s\n", err))
					cmd = ""
				}
				name = args[1]
			}
		}
		switch cmd {
		case "":
			// cmd already handled
		case "start":
			if st == nil {
				// all
				g.startServices(conn)
				break
			}
			if st.State == StateStarted {
				err = encodeResponse(conn, respOk, fmt.Sprintf("%s is already running\n", name))
			} else {
				err = g.startService(conn, st)
			}
		case "stop":
			if st == nil {
				// all
				g.stopServices(conn)
				break
			}
			if !st.State.canStop() {
				err = encodeResponse(conn, respOk, fmt.Sprintf("%s is not running\n", name))
			} else {
				_, err = g.stopService(conn, st)
			}
		case "restart":
			if st == nil {
				// all
				g.stopServices(conn)
				g.startServices(conn)
				break
			}
			stopped := true
			if st.State.isRunState() {
				stopped, err = g.stopService(conn, st)
			}
			if stopped {
				err = g.startService(conn, st)
			}
		case "list":
			var buf bytes.Buffer
			w := tabwriter.NewWriter(&buf, 4, 4, 4, ' ', 0)
			fmt.Fprint(w, "SERVICE\tSTATUS\t\n")
			g.mu.Lock()
			for _, v := range g.services {
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
			g.mu.Unlock()
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
				value = g.configDir
			case "services-dir":
				value = g.servicesDir()
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
				if _, s := g.serviceByFilename(args[1]); s != nil {
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

func (g *Governator) startServer() error {
	q := newQuit()
	g.quits = append(g.quits, q)
	scheme, addr, err := parseServerAddr(g.ServerAddr)
	if err != nil {
		return err
	}
	if scheme == "unix" {
		os.Remove(addr)
	}
	server, err := net.Listen(scheme, addr)
	if err != nil {
		return err
	}
	if scheme == "unix" {
		if gid := getGroupId(AppName); gid >= 0 {
			os.Chown(addr, 0, gid)
			os.Chmod(addr, 0775)
		}
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
				if scheme == "unix" {
					os.Remove(addr)
				}
				q.sendStopped()
				return
			case conn := <-conns:
				go func() {
					if err := g.serveConn(conn); err != nil {
						log.Errorf("error serving connection: %s", err)
					}
				}()
			}
		}
	}()
	return nil
}
