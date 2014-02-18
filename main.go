package main

import (
	"flag"
	"fmt"
	"gnd.la/log"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

var (
	daemon            = flag.Bool("D", false, "Run in daemon mode")
	debug             = flag.Bool("d", false, "Enable debug logging")
	testConfig        = flag.Bool("t", false, "Test configuration files")
	defaultConfigDir  = fmt.Sprintf("/etc/%s", AppName)
	configDir         = flag.String("c", defaultConfigDir, "Configuration directory")
	printVersion      = flag.Bool("V", false, "Print version and exit")
	governatorVersion = "<unknown>"
)

func testConfigurations() {
	configs, err := ParseConfigs()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	ok := true
	for _, v := range configs {
		if v.Err != nil {
			fmt.Fprintf(os.Stderr, "error in %s: %s\n", v.Name, v.Err)
			ok = false
		}
	}
	if ok {
		fmt.Println("configurations OK")
	}
}

func servicesDir() string {
	return filepath.Join(*configDir, "services")
}

func configDirIsDefault() bool {
	return *configDir == defaultConfigDir
}

func main() {
	flag.Parse()
	if *debug {
		log.SetLevel(log.LDebug)
	}
	switch {
	case *printVersion:
		fmt.Println(governatorVersion)
	case *testConfig:
		testConfigurations()
	case *daemon:
		if err := daemonMain(); err != nil {
			fmt.Fprintf(os.Stderr, "error starting daemon: %s\n", err)
		}
	default:
		ok, err := clientMain(flag.Args())
		if err != nil {
			if oe, ok := err.(*net.OpError); ok {
				switch {
				case oe.Err == syscall.EACCES:
					fmt.Fprintf(os.Stderr, "can't connect to governator, permission denied")
					os.Exit(1)
				case oe.Err == syscall.ENOENT:
					fmt.Fprintf(os.Stderr, "governator daemon is not running")
					os.Exit(1)
				}
			}
			fmt.Fprintf(os.Stderr, "error running client: %s\n", err)
			os.Exit(1)
		}
		if !ok {
			os.Exit(1)
		}
	}
}
