package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var (
	readline      func(string) *string
	add_history   func(string)
	read_history  func(string)
	write_history func(string)
)

type lineReader interface {
	ReadLine() (string, error)
	AddHistory(s string)
}

type bufLineReader struct {
	r *bufio.Reader
}

func (r *bufLineReader) ReadLine() (string, error) {
	fmt.Printf("%s> ", AppName)
	return r.r.ReadString('\n')
}

func (r *bufLineReader) AddHistory(_ string) {
}

func newLineReader() lineReader {
	if readline != nil {
		r := &readlineLineReader{}
		r.readHistory()
		return r
	}
	return &bufLineReader{
		r: bufio.NewReader(os.Stdin),
	}
}

type readlineLineReader struct {
}

func (r *readlineLineReader) historyFile() (string, error) {
	dir, err := governatorUserDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "history"), nil
}

func (r *readlineLineReader) readHistory() error {
	if read_history != nil {
		file, err := r.historyFile()
		if err != nil {
			return err
		}
		read_history(file)
	}
	return nil
}

func (r *readlineLineReader) ReadLine() (string, error) {
	s := readline(fmt.Sprintf("%s> ", AppName))
	if s == nil {
		return "", io.EOF
	}
	return *s, nil
}

func (r *readlineLineReader) AddHistory(s string) {
	if add_history != nil {
		add_history(s)
		if write_history != nil {
			file, err := r.historyFile()
			if err != nil {
				return
			}
			write_history(file)
		}
	}
}
