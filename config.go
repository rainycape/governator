package main

import (
	"fmt"
	"gnd.la/config"
	"gnd.la/log"
	"gnd.la/util/stringutil"
	"io/ioutil"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

type Config struct {
	File             string
	Command          string
	Name             string
	Dir              string
	Env              map[string]string
	Start            bool `default:"true"`
	User             string
	Group            string
	Priority         int `default:"1000"`
	Watchdog         *Watchdog
	WatchdogInterval int `default:"300"`
	Log              *Logger
	Err              error
}

func (c *Config) Cmd() (*exec.Cmd, error) {
	if c.Err != nil {
		return nil, c.Err
	}
	if c.Command == "" {
		return nil, fmt.Errorf("no command")
	}
	fields, err := stringutil.SplitFields(c.Command, " ")
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
	dir := c.Dir
	if dir == "" {
		dir = filepath.Dir(fields[0])
	}
	cmd := &exec.Cmd{Path: fields[0], Args: fields, Dir: dir}
	for k, v := range c.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	if _, ok := c.Env["GOMAXPROCS"]; !ok {
		cmd.Env = append(cmd.Env, fmt.Sprintf("GOMAXPROCS=%d", runtime.NumCPU()))
	}
	for _, v := range os.Environ() {
		if p := strings.IndexByte(v, '='); p >= 0 {
			k := v[:p]
			if _, ok := c.Env[k]; !ok {
				cmd.Env = append(cmd.Env, v)
			}
		}
	}
	info, err := os.Stat(fields[0])
	if err != nil {
		return nil, err
	}
	stat := info.Sys().(*syscall.Stat_t)
	uid := stat.Uid
	gid := stat.Gid
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
	var cred *syscall.Credential
	if uid != 0 || gid != 0 {
		cred = &syscall.Credential{Uid: uid, Gid: gid}
		attr := &syscall.SysProcAttr{Credential: cred}
		cmd.SysProcAttr = attr
	}
	log.Debugf("%s wd: %s, env: %s, cred: %+v", c.ServiceName(), dir, cmd.Env, cred)
	return cmd, nil
}

func (c *Config) ServiceName() string {
	if c.Name != "" {
		return c.Name
	}
	return c.File
}

func ParseConfig(filename string) *Config {
	cfg := &Config{File: filename}
	err := config.ParseFile(filepath.Join(servicesDir(), filename), cfg)
	cfg.Err = err
	if cfg.Log == nil {
		cfg.Log = new(Logger)
		cfg.Log.Parse("")
	}
	cfg.Log.Name = cfg.ServiceName()
	return cfg
}

func ParseConfigs() ([]*Config, error) {
	dir := servicesDir()
	if configDirIsDefault() {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("error creating services directory %s: %s", dir, err)
		}
	}
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("error reading config directory %s: %s", dir, err)
	}
	var configs []*Config
	for _, v := range files {
		name := v.Name()
		if shouldIgnoreFile(name, false) {
			continue
		}
		cfg := ParseConfig(name)
		log.Debugf("Parsed config %s: %+v", name, cfg)
		configs = append(configs, cfg)
	}
	return configs, nil
}

func shouldIgnoreFile(name string, deleted bool) bool {
	if name == "" || name[0] == '.' || strings.HasSuffix(name, "~") {
		return true
	}
	if !deleted {
		info, err := os.Stat(filepath.Join(servicesDir(), name))
		if err != nil || info.Size() == 0 || info.IsDir() {
			return true
		}
	}
	return false
}
