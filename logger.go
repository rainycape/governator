package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"time"
)

var (
	newLine = []byte{'\n'}
)

func formatTime(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}

type sink struct {
	logger *logger
	prefix string
	buf    []byte
}

func (s *sink) Write(b []byte) (int, error) {
	if len(s.buf) == 0 && bytes.IndexByte(b, '\n') == (len(b)-1) {
		s.logger.Write(s.prefix, b)
	} else {
		s.buf = append(s.buf, b...)
		p := bytes.IndexByte(s.buf, '\n')
		for p >= 0 {
			rem := len(s.buf) - p - 1
			s.logger.Write(s.prefix, s.buf[:p+1])
			if rem > 0 {
				copy(s.buf, s.buf[p+1:])
			}
			s.buf = s.buf[:rem]
			p = bytes.IndexByte(s.buf, '\n')
		}
	}
	return len(b), nil
}

type monitor func(string, []byte)

type logger struct {
	w       io.WriteCloser
	zw      *gzip.Writer
	stdout  *sink
	stderr  *sink
	monitor monitor
}

func (l *logger) Write(prefix string, b []byte) {
	fmt.Fprintf(l.zw, "[%s] %s - ", prefix, formatTime(time.Now()))
	l.zw.Write(b)
	if b[len(b)-1] != '\n' {
		l.zw.Write(newLine)
	}
	l.Flush()
	if l.monitor != nil {
		l.monitor(prefix, b)
	}
}

func (l *logger) WriteString(prefix string, s string) {
	l.Write(prefix, []byte(s))
}

type syncer interface {
	Sync() error
}

type flusher interface {
	Flush() error
}

func (l *logger) Flush() {
	l.zw.Flush()
	if s, ok := l.w.(syncer); ok {
		s.Sync()
	}
	if f, ok := l.w.(flusher); ok {
		f.Flush()
	}
}

func (l *logger) Close() error {
	l.zw.Close()
	return l.w.Close()
}

func newLogger(w io.WriteCloser) *logger {
	log := &logger{w: w}
	log.zw = gzip.NewWriter(w)
	log.stdout = &sink{logger: log, prefix: "stdout"}
	log.stderr = &sink{logger: log, prefix: "stderr"}
	return log
}
