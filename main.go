package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"gnd.la/log"
)

var (
	defaultConfigDir  = fmt.Sprintf("/etc/%s", AppName)
	governatorVersion = "1.0"
	gitVersion        = ""
)

func testConfigurations(g *Governator) {
	configs, err := g.parseConfigs()
	if err != nil {
		die(err)
	}
	ok := true
	for _, v := range configs {
		fmt.Println("checking", v.Name)
		if v.Err != nil {
			fmt.Fprintf(os.Stderr, "error in %s: %s\n", v.Name, v.Err)
			ok = false
		}
	}
	if ok {
		fmt.Println("configurations OK")
	}
}

func die(err error) {
	fmt.Fprintf(os.Stderr, "%s\n", err)
	os.Exit(1)
}

func main() {
	var (
		daemon       = flag.Bool("D", false, "Run in daemon mode")
		debug        = flag.Bool("d", false, "Enable debug logging")
		testConfig   = flag.Bool("t", false, "Test configuration files")
		configDir    = flag.String("c", defaultConfigDir, "Configuration directory")
		serverAddr   = flag.String("daemon", "unix://"+socketPath, "Daemon URL to listen on in daemon mode or to connect to in client mode")
		printVersion = flag.Bool("V", false, "Print version and exit")
	)
	flag.Parse()
	if *debug {
		log.SetLevel(log.LDebug)
	}
	switch {
	case *printVersion:
		fmt.Println(governatorVersion, gitVersion)
	case *testConfig:
		g, err := NewGovernator(*configDir)
		if err != nil {
			die(fmt.Errorf("error initializing daemon: %s", err))
		}
		testConfigurations(g)
	case *daemon:
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, os.Signal(syscall.SIGTERM), os.Kill)
		if os.Geteuid() != 0 {
			die(errors.New("govenator daemon must be run as root"))
		}
		g, err := NewGovernator(*configDir)
		if err != nil {
			die(fmt.Errorf("error initializing daemon: %s", err))
		}
		g.ServerAddr = *serverAddr
		go func() {
			// Wait for signal
			<-c
			g.StopRunning()
		}()
		if err := g.Run(); err != nil {
			die(fmt.Errorf("error starting daemon: %s", err))
		}
	default:
		ok, err := clientMain(*serverAddr, flag.Args())
		if err != nil {
			if oe, ok := err.(*net.OpError); ok {
				switch {
				case oe.Err == syscall.EACCES:
					fmt.Fprint(os.Stderr, "can't connect to governator, permission denied\n")
					os.Exit(1)
				case oe.Err == syscall.ENOENT:
					fmt.Fprint(os.Stderr, "governator daemon is not running\n")
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
