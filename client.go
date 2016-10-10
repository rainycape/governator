package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"

	"gnd.la/log"

	"github.com/fiam/stringutil"
)

const help = `available commands are:
    start <service|all>   : starts a service or all services, in priority order
    stop <service|all>    : stops a service or all services, in priority order
    restart <service|all> : restart a service or all services, in priority order
    list                  : list registered services
    exit                  : close the shell
    help                  : show help`

func sendCommand(serverAddr string, args []string) (bool, error) {
	scheme, addr, err := parseServerAddr(serverAddr)
	if err != nil {
		return false, err
	}
	conn, err := net.Dial(scheme, addr)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	if err := encodeArgs(conn, args); err != nil {
		return false, err
	}
	log.Debugf("sent command %s", args)
	closed := false
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	defer signal.Stop(ch)
	done := make(chan struct{}, 1)
	defer func() {
		done <- struct{}{}
	}()
	go func() {
		select {
		case <-ch:
			closed = true
			conn.Close()
		case <-done:
		}
	}()
	ok := true
	for {
		r, s, err := decodeResponse(conn)
		if err != nil {
			if closed {
				return ok, nil
			}
			return ok, err
		}
		log.Debugf("received response %d", r)
		switch r {
		case respEnd:
			return ok, nil
		case respOk:
			fmt.Print(s)
		case respErr:
			ok = false
			fmt.Fprint(os.Stderr, s)
		default:
			return false, fmt.Errorf("invalid response type %d", r)
		}
	}
	return ok, nil
}

func evalCommand(addr string, args []string) (bool, error) {
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "quit", "exit":
			os.Exit(0)
		case "help":
			fmt.Fprintf(os.Stderr, "%s\n", help)
			return true, nil
		}
	}
	return sendCommand(addr, args)
}

func clientMain(addr string, args []string) (bool, error) {
	createGovernatorUserDir()
	if len(args) > 0 {
		return evalCommand(addr, args)
	}
	r := newLineReader()
	fmt.Printf("%s interactive shell\nType exit or press control+d to end\nType help to show available commands\n\n", AppName)
	sendCommand(addr, []string{"list"})
	for {
		s, err := r.ReadLine()
		if err == io.EOF {
			fmt.Print("exit\n")
			break
		}
		s = strings.TrimSpace(s)
		if s != "" {
			r.AddHistory(s)
			fields, err := stringutil.SplitFields(s, " ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "error reading input: %s\n", err)
				continue
			}
			if _, err := evalCommand(addr, fields); err != nil {
				fmt.Fprintf(os.Stderr, "error executing command: %s\n", err)
			}
		}
	}
	return true, nil
}

func governatorUserDir() (string, error) {
	usr, err := user.Current()
	if err != nil {
		return "", err
	}
	return filepath.Join(usr.HomeDir, "."+AppName), nil
}

func createGovernatorUserDir() error {
	dir, err := governatorUserDir()
	if err != nil {
		return err
	}
	return os.Mkdir(dir, 0755)
}
