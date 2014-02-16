package main

import (
	"flag"
	"fmt"
	"gnd.la/log"
	"os"
)

var (
	daemon     = flag.Bool("D", false, "Run in daemon mode")
	debug      = flag.Bool("d", false, "Enable debug logging")
	testConfig = flag.Bool("t", false, "Test configuration files")
	configDir  = flag.String("-c", fmt.Sprintf("/etc/%s", AppName), "Configuration directory")
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

func main() {
	flag.Parse()
	if *debug {
		log.SetLevel(log.LDebug)
	}
	switch {
	case *testConfig:
		testConfigurations()
	case *daemon:
		if err := daemonMain(); err != nil {
			fmt.Fprintf(os.Stderr, "error starting daemon: %s\n", err)
		}
	default:
		ok, err := clientMain(flag.Args())
		if err != nil {
			fmt.Fprintf(os.Stderr, "error running client: %s\n", err)
			os.Exit(1)
		}
		if !ok {
			os.Exit(1)
		}
	}
}
