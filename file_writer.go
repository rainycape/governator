package main

import (
	"compress/gzip"
	"errors"
	"fmt"
	"gnd.la/log"
	"io"
	"os"
	"path/filepath"
)

const (
	LogDir = "/var/log/governator"
)

type fileWriter struct {
	name    string
	maxSize uint64
	size    uint64
	count   int
	f       *os.File
}

func (f *fileWriter) logPath(ii int) string {
	if ii > 0 {
		return filepath.Join(LogDir, fmt.Sprintf("%s.%d.log.gz", f.name, ii))
	}
	return filepath.Join(LogDir, f.name+".log")
}

func (f *fileWriter) Open(name string) error {
	dir, err := os.Stat(LogDir)
	if err != nil || !dir.IsDir() {
		os.Remove(LogDir)
		// Make logs directory
		if err := os.Mkdir(LogDir, 0755); err != nil {
			return err
		}
	}
	if f.f != nil {
		if err := f.Close(); err != nil {
			return err
		}
	}
	f.name = name
	logPath := f.logPath(0)
	// Allow reading for the compression during rotation
	w, err := os.OpenFile(logPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("error opening log file %s: %s", logPath, err)
	}
	info, err := w.Stat()
	if err != nil {
		w.Close()
		return err
	}
	f.size = uint64(info.Size())
	f.f = w
	return nil
}

func (f *fileWriter) Close() error {
	if f.f != nil {
		err := f.f.Close()
		f.f = nil
		return err
	}
	return nil
}

func (f *fileWriter) Write(prefix string, b []byte) error {
	if f.f == nil {
		return errors.New("file not opened")
	}
	c1, err := fmt.Fprintf(f.f, "[%s] ", prefix)
	if err != nil {
		return err
	}
	c2, err := f.f.Write(b)
	if err != nil {
		return err
	}
	f.size += uint64(c1 + c2)
	if f.maxSize > 0 && f.count > 0 && f.size > f.maxSize {
		log.Debugf("rotating log file %s", f.name)
		if err := f.rotate(); err != nil {
			log.Errorf("error rotating file %s: %s", f.name, err)
			return err
		}
	}
	return nil
}

func (f *fileWriter) Flush() error {
	return f.f.Sync()
}

func (f *fileWriter) rotate() error {
	last := f.logPath(f.count - 1)
	if fileExists(last) {
		if err := os.Remove(last); err != nil {
			return err
		}
	}
	for ii := f.count - 2; ii > 0; ii-- {
		cur := f.logPath(ii)
		if fileExists(cur) {
			if err := os.Rename(cur, f.logPath(ii+1)); err != nil {
				return err
			}
		}
	}
	if _, err := f.f.Seek(0, os.SEEK_SET); err != nil {
		return err
	}
	w, err := os.OpenFile(f.logPath(1), os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	gw := gzip.NewWriter(w)
	if _, err := io.Copy(gw, f.f); err != nil {
		return err
	}
	if err := gw.Close(); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := f.Open(f.name); err != nil {
		return err
	}
	return nil
}

func fileExists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}
