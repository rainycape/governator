package main

import (
	"github.com/rainycape/dl"
)

func init() {
	var lib *dl.DL
	for _, v := range []string{"", dl.LibExt + ".5", dl.LibExt + ".6"} {
		lib, _ = dl.Open("libreadline"+v, 0)
		if lib != nil {
			break
		}
	}
	if lib != nil {
		lib.Sym("readline", &readline)
		lib.Sym("add_history", &add_history)
		lib.Sym("read_history", &read_history)
		lib.Sym("write_history", &write_history)
	}
}
