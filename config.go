package main

import (
	"fmt"
	"gnd.la/config"
	"gnd.la/log"
	"gnd.la/util/textutil"
	"io/ioutil"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type Config struct {
	File        string
	Command     string
	Name        string
	Dir         string
	Environment map[string]string
	User        string
	Group       string
	Priority    int
	Err         error
}

func (c *Config) Cmd() (*exec.Cmd, error) {
	if c.Err != nil {
		return nil, c.Err
	}
	if c.Command == "" {
		return nil, fmt.Errorf("no command")
	}
	fields, err := textutil.SplitFields(c.Command, " ")
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(fields[0]) {
		p, err := exec.LookPath(fields[0])
		if err != nil {
			return nil, err
		}
		fields[0] = p
	}
	cmd := &exec.Cmd{Path: fields[0], Args: fields, Dir: c.Dir}
	for k, v := range c.Environment {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	for _, v := range os.Environ() {
		if p := strings.IndexByte(v, '='); p >= 0 {
			k := v[:p]
			if _, ok := c.Environment[k]; !ok {
				cmd.Env = append(cmd.Env, v)
			}
		}
	}
	var uid uint32
	var gid uint32
	if c.Group != "" {
		if g := getGroupId(c.Group); g > 0 {
			gid = uint32(g)
		} else {
			return nil, fmt.Errorf("invalid group %q", c.Group)
		}
	}
	if c.User != "" {
		u, err := user.Lookup(c.User)
		if err != nil {
			return nil, err
		}
		ui, _ := strconv.Atoi(u.Uid)
		uid = uint32(ui)
		if gid == 0 {
			gi, _ := strconv.Atoi(u.Gid)
			gid = uint32(gi)
		}
	}
	if uid != 0 || gid != 0 {
		attr := &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: gid}}
		cmd.SysProcAttr = attr
	}
	return cmd, nil
}

func (c *Config) ServiceName() string {
	if c.Name != "" {
		return c.Name
	}
	if fields, err := textutil.SplitFields(c.Command, " "); err == nil && len(fields) > 0 {
		return filepath.Base(fields[0])
	}
	return c.File
}

func ParseConfig(filename string) *Config {
	cfg := &Config{File: filename}
	err := config.ParseFile(filepath.Join(*configDir, filename), cfg)
	cfg.Err = err
	return cfg
}

func ParseConfigs() ([]*Config, error) {
	files, err := ioutil.ReadDir(*configDir)
	if err != nil {
		return nil, fmt.Errorf("error reading config directory %s: %s", *configDir, err)
	}
	var configs []*Config
	for _, v := range files {
		name := v.Name()
		if name[0] == '.' {
			continue
		}
		cfg := ParseConfig(name)
		log.Debugf("Parsed config %s: %+v", name, cfg)
		configs = append(configs, cfg)
	}
	return configs, nil
}
