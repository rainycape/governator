package main

import (
	"log/syslog"
)

type syslogWriter struct {
	scheme string
	addr   string
	w      *syslog.Writer
	buf    []byte
}

func (s *syslogWriter) Open(name string) error {
	w, err := syslog.Dial(s.scheme, s.addr, syslog.LOG_LOCAL0|syslog.LOG_NOTICE, name)
	if err != nil {
		return err
	}
	s.w = w
	return nil
}

func (s *syslogWriter) Close() error {
	if s.w != nil {
		err := s.w.Close()
		s.w = nil
		return err
	}
	return nil
}

func (s *syslogWriter) Write(prefix string, b []byte) error {
	var err error
	switch prefix {
	case "error":
		err = s.w.Err(string(b))
	case "info":
		err = s.w.Info(string(b))
	case "debug":
		err = s.w.Debug(string(b))
	default:
		s.buf = append(s.buf[:0], '[')
		s.buf = append(s.buf, prefix...)
		s.buf = append(s.buf, ']', ' ')
		s.buf = append(s.buf, b...)
		_, err = s.w.Write(s.buf)
	}
	return err
}

func (s *syslogWriter) Flush() error {
	return nil
}
