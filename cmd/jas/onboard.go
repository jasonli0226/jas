package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/jasonli0226/jas/settings"
)

// onboardIfNeeded runs first-run setup before a bare `jas` opens the chat:
// with no config.json it goes through the same flow as `jas config init`.
func onboardIfNeeded() error {
	home, err := settingsHome()
	if err != nil {
		return err
	}
	path := filepath.Join(home, configFile)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	fmt.Fprintln(os.Stderr, "Welcome to jas. Let's set it up (rerun any time with `jas config init`).")
	return configInit(path, home, settings.File{})
}
