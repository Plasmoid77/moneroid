package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Plasmoid77/moneroid/internal/doctor"
	"github.com/Plasmoid77/moneroid/internal/status"
	"github.com/Plasmoid77/moneroid/internal/systemd"
	"github.com/Plasmoid77/moneroid/internal/xmrig"
)

func cmdDoctor(cfgPath string, args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "print the report as JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return exitUsage
	}
	cfg, code := loadConfig(cfgPath)
	if code != exitOK {
		return code
	}
	token, _ := cfg.ReadToken()
	c := &status.Collector{
		Cfg:       cfg,
		Run:       systemd.ExecRunner,
		XMRig:     &xmrig.Client{HTTP: xmrig.NewHTTPClient(status.ReadTimeout), BaseURL: cfg.XMRig.APIURL, Token: token},
		Now:       time.Now,
		Monotonic: status.Monotonic,
		HeldFile:  status.GuardHeldFile,
	}
	snap := c.Collect(context.Background())
	rep := doctor.Run(context.Background(), cfg, snap, systemd.ExecRunner)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return exitCheck
		}
	} else {
		for _, ch := range rep.Checks {
			fmt.Printf("%-4s %-22s %-9s %s\n", ch.Result, ch.Code, ch.Component, ch.Message)
			if ch.Remedy != nil && ch.Result != doctor.Pass {
				fmt.Printf("     -> %s\n", *ch.Remedy)
			}
		}
		fmt.Printf("\n%d pass, %d warn, %d fail, %d skip\n", rep.Summary[doctor.Pass], rep.Summary[doctor.Warn], rep.Summary[doctor.Fail], rep.Summary[doctor.Skip])
	}
	if rep.HasFail() {
		return exitCheck
	}
	return exitOK
}
