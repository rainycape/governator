package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net/url"
	"strings"
)

const (
	socketPath = "/tmp/governator.sock"
	AppName    = "governator"
)

type resp uint8

const (
	respEnd resp = iota
	respOk
	respErr
)

func encodeString(w io.Writer, s string) error {
	length := uint32(len(s))
	if err := codecWrite(w, length); err != nil {
		return err
	}
	if _, err := io.WriteString(w, s); err != nil {
		return err
	}
	return nil
}

func decodeString(r io.Reader) (string, error) {
	var length uint32
	if err := codecRead(r, &length); err != nil {
		return "", err
	}
	s := make([]byte, length)
	if _, err := io.ReadFull(r, s); err != nil {
		return "", err
	}
	return string(s), nil
}

func encodeArgs(w io.Writer, args []string) error {
	count := int32(len(args))
	codecWrite(w, count)
	for _, v := range args {
		if err := encodeString(w, v); err != nil {
			return err
		}
	}
	return nil
}

func decodeArgs(r io.Reader) ([]string, error) {
	var count uint32
	if err := codecRead(r, &count); err != nil {
		return nil, err
	}
	args := make([]string, int(count))
	for ii := 0; ii < int(count); ii++ {
		s, err := decodeString(r)
		if err != nil {
			return nil, err
		}
		args[ii] = s
	}
	return args, nil
}

func encodeResponse(w io.Writer, r resp, s string) error {
	if w != nil {
		if err := codecWrite(w, r); err != nil {
			return err
		}
		if err := encodeString(w, s); err != nil {
			return err
		}
	}
	return nil
}

func decodeResponse(r io.Reader) (resp, string, error) {
	var re resp
	if err := codecRead(r, &re); err != nil {
		return 0, "", err
	}
	s, err := decodeString(r)
	return re, s, err
}

func codecRead(r io.Reader, out interface{}) error {
	return binary.Read(r, binary.BigEndian, out)
}

func codecWrite(w io.Writer, in interface{}) error {
	return binary.Write(w, binary.BigEndian, in)
}

func parseServerAddr(addr string) (string, string, error) {
	u, err := url.Parse(addr)
	if err != nil {
		return "", "", fmt.Errorf("invalid server URL %q: %s", addr, err)
	}
	scheme := u.Scheme
	u.Scheme = ""
	return scheme, strings.TrimPrefix(u.String(), "//"), nil
}
