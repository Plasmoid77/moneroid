package main

import (
	"fmt"
	"os"
	"time"

	"github.com/Plasmoid77/moneroid/internal/status"
	"github.com/Plasmoid77/moneroid/internal/systemd"
	"github.com/Plasmoid77/moneroid/internal/tui"
	"github.com/Plasmoid77/moneroid/internal/xmrig"
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
	token, err := cfg.ReadToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: token_file: %v\n", err)
	}
	c := &status.Collector{
		Cfg:       cfg,
		Run:       systemd.ExecRunner,
		XMRig:     &xmrig.Client{HTTP: xmrig.NewHTTPClient(status.ReadTimeout), BaseURL: cfg.XMRig.APIURL, Token: token},
		Now:       time.Now,
		Monotonic: status.Monotonic,
		HeldFile:  status.GuardHeldFile,
	}
	if err := tui.Run(tui.New(cfg, c, version)); err != nil {
		fmt.Fprintln(os.Stderr, "tui:", err)
		return exitCheck
	}
	return exitOK
}
