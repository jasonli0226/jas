//go:build windows

package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// createNoWindow (CREATE_NO_WINDOW) keeps the shell from opening a console
// window.
const createNoWindow = 0x08000000

// extraBaseEnv are platform-specific names the shell child inherits on
// windows: SystemRoot is required by most Win32 programs, PATHEXT by command
// lookup, and the profile and program directories by common tooling (go needs
// LOCALAPPDATA for its build cache).
var extraBaseEnv = []string{
	"SystemRoot", "SystemDrive", "windir", "ComSpec", "PATHEXT", "TEMP", "TMP",
	"USERPROFILE", "USERNAME", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA",
	"ProgramData", "ProgramFiles", "ProgramFiles(x86)", "ProgramW6432",
	"CommonProgramFiles", "CommonProgramFiles(x86)", "NUMBER_OF_PROCESSORS",
	"PROCESSOR_ARCHITECTURE", "OS",
}

// envCaseFold: windows environment names are case-insensitive (Path vs PATH).
const envCaseFold = true

// shellInvocation describes the shell used for the tool description.
func shellInvocation() string { return "cmd /d /s /c" }

// procTree is a job object that owns the shell and everything it starts, so
// grandchildren die with the call. A child spawned in the instant between
// process start and assignment escapes the job; cmd.exe has not run the
// command line by then in practice.
type procTree struct {
	job  windows.Handle
	once sync.Once
}

func newProcTree() (*procTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("configure job object: %w", err)
	}
	return &procTree{job: job}, nil
}

// attach puts the started shell into the job.
func (t *procTree) attach(p *os.Process) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid))
	if err != nil {
		return fmt.Errorf("open shell process: %w", err)
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(t.job, h); err != nil {
		return fmt.Errorf("assign job object: %w", err)
	}
	return nil
}

// kill terminates every process in the job.
func (t *procTree) kill() { _ = windows.TerminateJobObject(t.job, 1) }

// close releases the job; KILL_ON_JOB_CLOSE ends anything still in it.
func (t *procTree) close() { t.once.Do(func() { _ = windows.CloseHandle(t.job) }) }

// shellSpawn builds the shell command and the job that owns its processes.
// The command line is passed verbatim through CmdLine: exec's default
// argument quoting escapes " as \", which cmd.exe does not understand.
// /s makes cmd strip exactly the outer quotes and run the rest as typed.
func shellSpawn(tctx context.Context, command string) (*exec.Cmd, *procTree, error) {
	tree, err := newProcTree()
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.CommandContext(tctx, "cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       `cmd /d /s /c "` + command + `"`,
		CreationFlags: createNoWindow,
	}
	cmd.Cancel = func() error {
		tree.kill()
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		return nil
	}
	return cmd, tree, nil
}
