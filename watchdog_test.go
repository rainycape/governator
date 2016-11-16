package main

import (
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

type watchdogTest struct {
	config string
	perr   string
	cerr   string
}

func errorMatches(err error, expected string) bool {
	const (
		containsPrefix = "contains:"
	)
	if strings.HasPrefix(expected, containsPrefix) {
		return strings.Contains(err.Error(), expected[len(containsPrefix):])
	}
	return err.Error() == expected
}

func checkExpectedErr(t *testing.T, err error, exp string) bool {
	if err == nil && exp != "" {
		t.Errorf("expecting error %s, got nil", exp)
		return false
	}
	if err != nil && !errorMatches(err, exp) {
		if exp == "" {
			t.Error(err)
		} else {
			t.Errorf("expecting error %s, got %s instead", exp, err)
		}
		return false
	}
	return true
}

func netListen(port int) {
	s, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		panic(err)
	}
	for {
		conn, err := s.Accept()
		if err != nil {
			break
		}
		conn.Close()
	}
}

func httpListen(port int, reply bool) {
	f := func(w http.ResponseWriter, r *http.Request) {
		if !reply {
			time.Sleep(1000 * time.Hour)
		}
		w.Write([]byte("Hello World"))
	}
	http.ListenAndServe("127.0.0.1:"+strconv.Itoa(port), http.HandlerFunc(f))
}

func randomPort() int {
	return 1024 + (rand.Int() % 60000)
}

func TestWatchdog(t *testing.T) {
	np := randomPort()
	go netListen(np)
	hp := randomPort()
	go httpListen(hp, true)
	hpnr := randomPort()
	go httpListen(hpnr, false)
	tests := []watchdogTest{
		{"run echo foo", "", ""},
		{"run false", "", "exit status 1"},
		{"run does-not-exist", "", "exec: \"does-not-exist\": executable file not found in $PATH"},
		{"connect tcp://127.0.0.1:1", "", "contains:connection refused"},
		{"invalid", "invalid watchdog \"invalid\" - available watchdogs are run, connect and get", ""},
		{"connect tcp://127.0.0.1:" + strconv.Itoa(np), "", ""},
		{"get http://127.0.0.1:" + strconv.Itoa(hp), "", ""},
		{"connect tcp://127.0.0.1:" + strconv.Itoa(np) + " 30", "", ""},
		{"get http://127.0.0.1:" + strconv.Itoa(hp) + " 30", "", ""},
		{"get http://127.0.0.1:" + strconv.Itoa(hpnr) + " 1", "", "contains:i/o timeout"},
	}
	for _, v := range tests {
		t.Logf("testing wd config %q", v.config)
		w := new(Watchdog)
		err := w.Parse(v.config)
		if !checkExpectedErr(t, err, v.perr) || err != nil {
			continue
		}
		err = w.Check()
		if !checkExpectedErr(t, err, v.cerr) {
			continue
		}
	}
}
