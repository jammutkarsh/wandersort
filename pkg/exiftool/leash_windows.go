//go:build windows

package exiftool

import (
	"fmt"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// leash ends one exiftool when this process dies without closing it: in
// -stay_open mode exiftool polls its input forever, end of input included.
type leash struct{ job windows.Handle }

// leashTo leashes cmd, already started: Windows ends every process in a job
// once its last handle closes, and only this process holds one.
func leashTo(cmd *exec.Cmd) (*leash, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create exiftool job: %w", err)
	}
	if err := assign(job, uint32(cmd.Process.Pid)); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	return &leash{job: job}, nil
}

func assign(job windows.Handle, pid uint32) error {
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return fmt.Errorf("limit exiftool job: %w", err)
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return fmt.Errorf("open exiftool process: %w", err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		return fmt.Errorf("assign exiftool to its job: %w", err)
	}
	return nil
}

// wait waits for cmd to exit, then closes the job: closing it kills what is
// still in it.
func (l *leash) wait(cmd *exec.Cmd) error {
	err := cmd.Wait()
	windows.CloseHandle(l.job)
	return err
}
