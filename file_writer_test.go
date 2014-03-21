package main

import (
	"gnd.la/log"
	"io/ioutil"
	"os"
	"strings"
	"testing"
)

func writeText(t *testing.T, w *fileWriter) {
	if err := w.Write("test", []byte(strings.Repeat("A", int(w.maxSize)))); err != nil {
		t.Fatal(err)
	}
}

func checkFiles(t *testing.T, w *fileWriter) {
	for ii := 0; ii < w.count; ii++ {
		var exp string
		if ii == 0 {
			exp = w.logPath(ii)
		} else {
			exp = w.compressedLogPath(ii)
		}
		if _, err := os.Stat(exp); err != nil {
			t.Errorf("error checking file at %s: %s", exp, err)
		}
	}
	files, err := ioutil.ReadDir(w.dir)
	if err != nil {
		t.Error(err)
	}
	if len(files) != w.count {
		names := make([]string, len(files))
		for ii, v := range files {
			names[ii] = v.Name()
		}
		t.Fatalf("expecting %d files, got %d instead: %v", w.count, len(files), names)
	}
}

func openTestFileWriter(t *testing.T) (*fileWriter, string) {
	if testing.Verbose() {
		log.SetLevel(log.LDebug)
	}
	dir, err := ioutil.TempDir("", "filewriter")
	if err != nil {
		t.Fatal(err)
	}
	w := &fileWriter{dir: dir, maxSize: 128, count: 3, waitCompress: true}
	if err := w.Open("test"); err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	return w, dir
}

func TestFileWriter(t *testing.T) {
	w, dir := openTestFileWriter(t)
	defer os.RemoveAll(dir)
	defer w.Close()
	for ii := 0; ii < 3; ii++ {
		writeText(t, w)
	}
	if err := w.Write("", []byte("A")); err != nil {
		t.Fatal(err)
	}
	checkFiles(t, w)
}

func TestFileWriterFiles(t *testing.T) {
	w, dir := openTestFileWriter(t)
	defer os.RemoveAll(dir)
	defer w.Close()
	for _, v := range []int{1, 2} {
		if err := ioutil.WriteFile(w.logPath(v), []byte{}, 0644); err != nil {
			t.Fatal(err)
		}
	}
	writeText(t, w)
	if err := w.Write("", []byte("A")); err != nil {
		t.Fatal(err)
	}
	checkFiles(t, w)
}
