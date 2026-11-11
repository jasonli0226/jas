package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/memory"
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
// would resolve memory from: project and local config apply only when the
// project's stored trust decision still holds.
func memoryLayers(home, root, name string) (settings.Layers, error) {
	files, err := settings.LoadLayers(home, root)
	if err != nil {
		return settings.Layers{}, err
	}
	prof, _, err := loadProfile(home, name, settings.SourceDefault)
	if err != nil {
		return settings.Layers{}, err
	}
	layers, _ := files.WithProfile(prof.name, prof.layer).ForProject(memoryProjectTrusted(root, home))
	return layers, nil
}

// memoryProjectTrusted is the project's stored trust decision, read the way
// a session reads it but never asking and never warning. No stored decision
// means untrusted.
func memoryProjectTrusted(root, home string) bool {
	deny := core.ExecPolicy{Root: root, Deny: denyPatterns(home)}.Denied
	ok, err := trustStore(home).Check(root, currentTrustBundle(root, deny))
	return err == nil && ok
}

// memoryReport reads the current memory files and settings for profile in
// root and returns the report with the dirs it describes. The dirs come
// back even when memory is off, for `jas memory --path`.
func memoryReport(home, profile, root string, layers settings.Layers) (string, memory.Dirs, error) {
	dirs, err := memoryDirs(home, profile, root)
	if err != nil {
		return "", memory.Dirs{}, err
	}
	mode, hide := memory.ResolveMode(memorySetting(layers, os.Getenv))
	if mode == memory.ModeOff {
		return memory.Report(dirs, memory.Index{}, memory.Index{}, mode), dirs, nil
	}
	// A folder the lift refuses is never read: what a link reaches is not
	// memory, so there are no indexes or notes to count.
	policy := profilePolicy(core.ExecPolicy{Root: root, Deny: denyPatterns(home)}, home, profile)
	policy.ProfileMemory = dirs.Profile
	if why := policy.ProfileMemoryIgnored(); why != "" {
		return fmt.Sprintf("memory: %s\nprofile memory ignored: %s", mode, why), dirs, nil
	}
	if !policy.ProfileMemoryLifted() {
		return fmt.Sprintf("memory: %s\n%s", mode, unliftedMemory(dirs.Profile)), dirs, nil
	}
	prof, proj, err := memory.LoadIndexes(dirs, hide)
	if err != nil {
		return "", dirs, err
	}
	if hide {
		proj = memory.Index{}
	}
	text := memory.Report(dirs, prof, proj, mode)
	for _, why := range []string{prof.Ignored, proj.Ignored} {
		if why != "" {
			text += "\n" + why
		}
	}
	return text, dirs, nil
}
