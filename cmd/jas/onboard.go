package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/jasonli0226/jas/settings"
)

// onboardOrUsage handles a bare `jas`: first run (no config.json) goes
// through the same flow as `jas config init`; otherwise it prints usage.
func onboardOrUsage() error {
	home, err := settingsHome()
	if err != nil {
		return err
	}
	path := filepath.Join(home, configFile)
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintln(os.Stderr, usage)
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	fmt.Fprintln(os.Stderr, "Welcome to jas. Let's set it up (rerun any time with `jas config init`).")
	return configInit(path, home, settings.File{})
}
