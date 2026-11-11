package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/memory"
	"github.com/jasonli0226/jas/sessions"
	"github.com/jasonli0226/jas/settings"
)

const memoryUsage = "usage: jas memory [--path]"

// memoryCmd runs jas memory [--path]: the memory report for the project a
// session started here would use, or just its project memory dir.
func memoryCmd(args []string) error {
	fs := flag.NewFlagSet("memory", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.Bool("path", false, "print the project memory dir")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return usageErr("%s", memoryUsage)
	}
	home, err := settingsHome()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	root := core.FindRoot(cwd)
	name, err := activeProfileName(home)
	if err != nil {
		return err
	}
	layers, err := memoryLayers(home, root, name)
	if err != nil {
		return err
	}
	text, dirs, err := memoryReport(home, name, root, layers)
	if err != nil {
		return err
	}
	if *path {
		fmt.Println(dirs.Project)
		return nil
	}
	fmt.Println(text)
	return nil
}

// memoryLayers is the settings a session started in root under profile name
// would resolve memory from (the project counts as trusted, as for config
// get/list: its value can only narrow the owner's).
func memoryLayers(home, root, name string) (settings.Layers, error) {
	files, err := settings.LoadLayers(home, root)
	if err != nil {
		return settings.Layers{}, err
	}
	prof, _, err := loadProfile(home, name, settings.SourceDefault)
	if err != nil {
		return settings.Layers{}, err
	}
	layers, _ := files.WithProfile(prof.name, prof.layer).ForProject(true)
	return layers, nil
}

// memoryReport reads the current memory files and settings for profile in
// root and returns the report with the dirs it describes. The dirs come
// back even when memory is off, for `jas memory --path`.
func memoryReport(home, profile, root string, layers settings.Layers) (string, memory.Dirs, error) {
	dirs, err := memory.DirsFor(settings.ProfileDir(home, profile), sessions.MainRoot(root))
	if err != nil {
		return "", memory.Dirs{}, err
	}
	mode, hide := memory.ResolveMode(memorySetting(layers, os.Getenv))
	if mode == memory.ModeOff {
		return memory.Report(dirs, memory.Index{}, memory.Index{}, mode), dirs, nil
	}
	prof, proj, err := memory.LoadIndexes(dirs, hide)
	if err != nil {
		return "", dirs, err
	}
	if hide {
		proj = memory.Index{}
	}
	return memory.Report(dirs, prof, proj, mode), dirs, nil
}
