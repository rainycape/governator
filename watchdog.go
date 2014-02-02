package main

import (
	"fmt"
	"gnd.la/log"
	"gnd.la/util/textutil"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"time"
)

const (
	defaultWatchdogInterval = 300
)

type dog interface {
	check() error
}

type runDog struct {
	argv []string
}

func (d *runDog) check() error {
	cmd := exec.Command(d.argv[0], d.argv[1:]...)
	return cmd.Run()
}

type connectDog struct {
	proto string
	addr  string
}

func (d *connectDog) check() error {
	proto := d.proto
	if proto == "" {
		proto = "tcp"
	}
	conn, err := net.Dial(proto, d.addr)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

type getDog struct {
	url string
}

func (d *getDog) check() error {
	log.Debugf("watchdog checking URL %s", d.url)
	req, err := http.NewRequest("GET", d.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", fmt.Sprintf("%s watchdog", AppName))
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("non-200 error code %d", resp.StatusCode)
	}
	return nil
}

type Watchdog struct {
	service *Service
	dog     dog
	ch      chan bool
}

func (w *Watchdog) Start(s *Service, interval int) error {
	w.service = s
	w.ch = make(chan bool, 1)
	ticker := time.NewTicker(time.Second * time.Duration(interval))
	for {
		select {
		case <-w.ch:
			ticker.Stop()
			w.ch <- true
			return nil
		case <-ticker.C:
			if err := w.Check(); err != nil {
				log.Errorf("%s's watchdog returned an error: %s", s.Name(), err)
				if err := s.stopService(); err == nil {
					s.startService()
				}
			}
		}
	}
	return nil
}

func (w *Watchdog) Check() error {
	return w.dog.check()
}

func (w *Watchdog) Stop() {
	if w.ch != nil {
		w.ch <- true
		<-w.ch
		w.ch = nil
	}
}

func (w *Watchdog) Parse(input string) error {
	if input == "" {
		return nil
	}
	args, err := textutil.SplitFields(input, " ")
	if err != nil {
		return err
	}
	if len(args) > 0 {
		switch args[0] {
		case "run":
			if len(args) == 1 {
				return fmt.Errorf("run watchdog requires at least one argument")
			}
			w.dog = &runDog{args[1:]}
		case "connect":
			var proto string
			var addr string
			switch len(args) {
			case 2:
				proto = "tcp"
				addr = args[1]
			case 3:
				proto = args[1]
				addr = args[2]
			default:
				return fmt.Errorf("run watchdog requires one or two arguments")
			}
			if _, _, err := net.SplitHostPort(addr); err != nil {
				return fmt.Errorf("address %q must specifiy a host and a port", addr)
			}
			w.dog = &connectDog{proto, addr}
		case "get":
			if len(args) != 2 {
				return fmt.Errorf("exactly watchdog requires exactly one argument")
			}
			u, err := url.Parse(args[1])
			if err != nil {
				return fmt.Errorf("invalid GET URL %q: %s", args[1], err)
			}
			if u.Scheme != "http" && u.Scheme != "https" {
				return fmt.Errorf("invalid GET URL scheme %q - must be http or https", u.Scheme)
			}
			w.dog = &getDog{args[1]}
		}
	}
	if w.dog == nil {
		return fmt.Errorf("invalid watchdog %q - available watchdogs are run, connect and get")
	}
	return nil
}
