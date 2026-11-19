//go:build windows

package proctree

import (
	"os/exec"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

type Tree struct {
	once sync.Once

	job atomic.Uintptr
	cmd *exec.Cmd
}

func Start(cmd *exec.Cmd) (*Tree, error) {
	t := &Tree{cmd: cmd}
	cmd.Cancel = func() error {
		if job := windows.Handle(t.job.Load()); job != 0 {
			return windows.TerminateJobObject(job, 1)
		}
		return cmd.Process.Kill()
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	t.job.Store(uintptr(newKillOnCloseJob(cmd.Process.Pid)))
	return t, nil
}

func newKillOnCloseJob(pid int) windows.Handle {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return 0
	}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		windows.CloseHandle(job)
		return 0
	}
	defer windows.CloseHandle(proc)
	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		windows.CloseHandle(job)
		return 0
	}
	return job
}

func (t *Tree) Terminate() { t.Kill() }

func (t *Tree) Kill() {
	t.once.Do(func() {
		if job := windows.Handle(t.job.Swap(0)); job != 0 {
			_ = windows.TerminateJobObject(job, 1)
			_ = windows.CloseHandle(job)
		}
	})
}
