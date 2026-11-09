package main

import (
	"fmt"
	"os"

	"github.com/Plasmoid77/moneroid/internal/tui"
)

func cmdTUI(cfgPath string, args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "tui takes no arguments")
		return exitUsage
	}
	for _, f := range []*os.File{os.Stdin, os.Stdout} {
		if st, err := f.Stat(); err != nil || st.Mode()&os.ModeCharDevice == 0 {
			fmt.Fprintln(os.Stderr, "tui needs an interactive terminal; use `moneroid status` instead")
			return exitUsage
		}
	}
	cfg, code := loadConfig(cfgPath)
	if code != exitOK {
		return code
	}
	c := newCollector(cfg)
	if err := tui.Run(tui.New(cfg, c, version)); err != nil {
		fmt.Fprintln(os.Stderr, "tui:", err)
		return exitCheck
	}
	return exitOK
}
