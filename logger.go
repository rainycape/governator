package main

import (
	"bytes"
	"fmt"
	"gnd.la/util/parseutil"
	"gnd.la/util/stringutil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	LogDir = "/var/log/governator"
)

var (
	// Altered during tests
	logDir = LogDir
)

type Out struct {
	Logger *Logger
	prefix string
	buf    []byte
}

func (o *Out) Write(b []byte) (int, error) {
	if len(o.buf) == 0 && bytes.IndexByte(b, '\n') == (len(b)-1) {
		o.Logger.Write(o.prefix, b)
	} else {
		o.buf = append(o.buf, b...)
		p := bytes.IndexByte(o.buf, '\n')
		for p >= 0 {
			rem := len(o.buf) - p - 1
			o.Logger.Write(o.prefix, o.buf[:p+1])
			if rem > 0 {
				copy(o.buf, o.buf[p+1:])
			}
			o.buf = o.buf[:rem]
			p = bytes.IndexByte(o.buf, '\n')
		}
	}
	return len(b), nil
}

type Writer interface {
	Open(string) error
	Close() error
	Write(string, []byte) error
	Flush() error
}

type Monitor func(string, []byte)

type Logger struct {
	Name    string
	w       Writer
	Stdout  *Out
	Stderr  *Out
	Monitor Monitor
	buf     []byte
	mu      sync.Mutex
}

func (l *Logger) Open() error {
	return l.w.Open(l.Name)
}

func (l *Logger) Close() error {
	return l.w.Close()
}

func (l *Logger) Write(prefix string, b []byte) error {
	now := time.Now().Unix()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = strconv.AppendInt(l.buf[:0], now, 10)
	l.buf = append(l.buf, ' ', '-', ' ')
	l.buf = append(l.buf, b...)
	if b[len(b)-1] != '\n' {
		l.buf = append(l.buf, '\n')
	}
	if err := l.w.Write(prefix, l.buf); err != nil {
		return err
	}
	if err := l.w.Flush(); err != nil {
		return err
	}
	if l.Monitor != nil {
		l.Monitor(prefix, l.buf)
	}
	return nil
}

func (l *Logger) WriteString(prefix string, s string) {
	l.Write(prefix, []byte(s))
}

func (l *Logger) Flush() {
	l.w.Flush()
}

func (l *Logger) Parse(input string) error {
	if input == "" {
		input = "file"
	}
	args, err := stringutil.SplitFields(input, " ")
	if err != nil {
		return err
	}
	switch strings.ToLower(args[0]) {
	case "file":
		maxSize := uint64(500 * 1024 * 1024) // 500MB
		count := 10                          // 10 rotated files
		switch len(args) {
		case 1:
			break
		case 3:
			c, err := strconv.Atoi(args[2])
			if err != nil {
				return fmt.Errorf("invalid file count %q, must be an integer", args[2])
			}
			count = c
			fallthrough
		case 2:
			size, err := parseutil.Size(args[1])
			if err != nil {
				return err
			}
			maxSize = size
		default:
			return fmt.Errorf("invalid number of arguments for file logger - must be one or two, %d given", len(args)-1)
		}
		l.w = &fileWriter{dir: logDir, maxSize: maxSize, count: count}
	case "syslog":
		var scheme string
		var addr string
		switch len(args) {
		case 1:
			break
		case 2:
			u, err := url.Parse(args[1])
			if err != nil {
				return fmt.Errorf("invalid syslog URL %q: %s", args[1], err)
			}
			if u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
				return fmt.Errorf("invalid syslog URL %q: must not have user, nor path, nor query, nor fragment", args[1])
			}
			if u.Scheme == "" {
				return fmt.Errorf("invalid syslog URL %q: scheme can't be empty", args[1])
			}
			if u.Host == "" {
				return fmt.Errorf("invalid syslog URL %q: host can't be empty", args[1])
			}
			scheme = u.Scheme
			addr = u.Host
		default:
			return fmt.Errorf("invalid number of arguments for syslog logger - must be zero or one, %d given", len(args)-1)
		}
		l.w = &syslogWriter{scheme: scheme, addr: addr}
	case "none":
		l.w = &noneWriter{}
	default:
		return fmt.Errorf("invalid logger %s", args[0])
	}
	l.Stdout = &Out{Logger: l, prefix: "stdout"}
	l.Stderr = &Out{Logger: l, prefix: "stderr"}
	return nil
}
