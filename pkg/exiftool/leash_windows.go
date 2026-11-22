//go:build windows

package exiftool

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// leash ends one exiftool when this process dies without closing it: in
// -stay_open mode exiftool polls its input forever, end of input included.
type leash struct{ job windows.Handle }

// startLeashed starts cmd in a job: Windows ends every process in one once its
// last handle closes, and only this process holds one. exiftool starts
// suspended, so any process of its own is started in the job too.
func startLeashed(cmd *exec.Cmd) (*leash, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create exiftool job: %w", err)
	}
	l := &leash{job: job}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("limit exiftool job: %w", err)
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	if err := cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	pid := uint32(cmd.Process.Pid)
	if err := errors.Join(assign(job, pid), resume(pid)); err != nil {
		cmd.Process.Kill()
		l.wait(cmd)
		return nil, err
	}
	return l, nil
}

func assign(job windows.Handle, pid uint32) error {
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

// resume lets a process started suspended run: its one thread is the only one
// the system lists for it.
func resume(pid uint32) error {
	threads, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("list threads: %w", err)
	}
	defer windows.CloseHandle(threads)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(threads, &entry); err == nil; err = windows.Thread32Next(threads, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return fmt.Errorf("open exiftool thread: %w", err)
		}
		defer windows.CloseHandle(thread)
		if _, err := windows.ResumeThread(thread); err != nil {
			return fmt.Errorf("resume exiftool: %w", err)
		}
		return nil
	}
	return fmt.Errorf("find exiftool's thread: %w", err)
}

// wait waits for cmd to exit, then closes the job: closing it kills what is
// still in it.
func (l *leash) wait(cmd *exec.Cmd) error {
	err := cmd.Wait()
	windows.CloseHandle(l.job)
	return err
}
