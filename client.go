package main

import (
	"bufio"
	"fmt"
	"gnd.la/log"
	"gnd.la/util/textutil"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
)

const help = `available commands are:
    start <service>   : starts a service
    stop <service>    : stops a service
    restart <service> : restart
    list              : list registered services
    exit              : close the shell
    help              : show help`

func sendCommand(args []string) (bool, error) {
	conn, err := net.Dial("unix", SocketPath)
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

func evalCommand(args []string) (bool, error) {
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "quit", "exit":
			os.Exit(0)
		case "help":
			fmt.Fprintf(os.Stderr, "%s\n", help)
			return true, nil
		}
	}
	return sendCommand(args)
}

func clientMain(args []string) (bool, error) {
	if len(args) > 0 {
		return evalCommand(args)
	}
	r := bufio.NewReader(os.Stdin)
	fmt.Printf("%s interactive shell\nType exit or press control+d to end\nType help to show available commands\n\n", AppName)
	sendCommand([]string{"list"})
	for {
		fmt.Printf("%s> ", AppName)
		s, err := r.ReadString('\n')
		if err == io.EOF {
			fmt.Print("exit\n")
			break
		}
		s = strings.TrimSpace(s)
		if s != "" {
			fields, err := textutil.SplitFields(s, " ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "error reading input: %s\n", err)
				continue
			}
			if _, err := evalCommand(fields); err != nil {
				fmt.Fprintf(os.Stderr, "error executing command: %s\n", err)
			}
		}
	}
	return true, nil
}
