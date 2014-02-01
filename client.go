package main

import (
	"bufio"
	"fmt"
	"gnd.la/util/textutil"
	"io"
	"net"
	"os"
	"strings"
)

const help = `available commands are:
    start <service>   : starts a service
    stop <service>    : stops a service
    restart <service> : restart
    list              : list registered services
    exit              : close the shell
    help:             : show help`

type oneArgError string

func (o oneArgError) Error() string {
	return fmt.Sprintf("%s requires exactly one argument", string(o))
}

func sendCommand(args []string) error {
	conn, err := net.Dial("unix", SocketPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := encodeArgs(conn, args); err != nil {
		return err
	}
	for {
		r, s, err := decodeResponse(conn)
		if err != nil {
			return err
		}
		switch r {
		case respEnd:
			return nil
		case respOk:
			fmt.Println(s)
		case respErr:
			fmt.Fprintln(os.Stderr, s)
		default:
			return fmt.Errorf("invalid response type %d", r)
		}
	}
	return nil
}

func evalCommand(args []string) error {
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "quit", "exit":
			os.Exit(0)
		case "help":
			fmt.Fprintf(os.Stderr, "%s\n", help)
			return nil
		}
	}
	return sendCommand(args)
}

func clientMain(args []string) error {
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
			if err := evalCommand(fields); err != nil {
				fmt.Fprintf(os.Stderr, "error executing command: %s\n", err)
			}
		}
	}
	return nil
}
