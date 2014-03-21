package main

import (
	"compress/gzip"
	"errors"
	"fmt"
	"gnd.la/log"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type fileWriter struct {
	name         string
	dir          string
	maxSize      uint64
	size         uint64
	count        int
	f            *os.File
	waitCompress bool // only used for tests
}

func (f *fileWriter) logPath(ii int) string {
	if ii == 0 {
		return filepath.Join(f.dir, f.name+".log")
	}
	return filepath.Join(f.dir, fmt.Sprintf("%s.%d.log", f.name, ii))
}

func (f *fileWriter) compressedLogPath(ii int) string {
	return f.logPath(ii) + ".gz"
}

func (f *fileWriter) Open(name string) error {
	dir, err := os.Stat(f.dir)
	if err != nil || !dir.IsDir() {
		os.Remove(f.dir)
		// Make logs directory
		if err := os.MkdirAll(f.dir, 0755); err != nil {
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
	w, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("error opening log file %s: %s", logPath, err)
	}
	log.Debugf("opened log file %s", name)
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
	if err := f.Close(); err != nil {
		return err
	}
	last := []string{f.logPath(f.count - 1), f.compressedLogPath(f.count - 1)}
	for _, v := range last {
		if fileExists(v) {
			log.Debugf("removing %s", v)
			if err := os.Remove(v); err != nil {
				return err
			}
		}
	}
	var compress []string
	for ii := f.count - 2; ii >= 0; ii-- {
		ccur := f.compressedLogPath(ii)
		if fileExists(ccur) {
			to := f.compressedLogPath(ii + 1)
			log.Debugf("moving %s to %s", ccur, to)
			if err := os.Rename(ccur, to); err != nil {
				return err
			}
			continue
		}
		cur := f.logPath(ii)
		if fileExists(cur) {
			to := f.logPath(ii + 1)
			log.Debugf("moving %s to %s", cur, to)
			if err := os.Rename(cur, to); err != nil {
				return err
			}
			compress = append(compress, to)
		}
	}
	for _, v := range compress {
		f.compressFile(v)
	}
	return f.Open(f.name)
}

func (f *fileWriter) compressFile(name string) {
	log.Debugf("will compress %s", name)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		if err := compressFile(name); err != nil {
			log.Errorf("error compressing %s: %s", name, err)
		}
		wg.Done()
	}()
	if f.waitCompress {
		wg.Wait()
	}
}

func compressFile(name string) error {
	r, err := os.Open(name)
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := os.OpenFile(name+".gz", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer w.Close()
	gw := gzip.NewWriter(w)
	defer gw.Close()
	if _, err := io.Copy(gw, r); err != nil {
		return err
	}
	os.Remove(name)
	log.Debugf("compressed %s to %s", r.Name(), w.Name())
	return nil
}

func fileExists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}
