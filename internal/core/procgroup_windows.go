//go:build windows

package core

import (
	"os/exec"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// procTree is a command and every process it starts: on Windows, a job
// object with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE. Children of a process in a
// job are in the job too, so terminating (or closing) the job ends the tree.
type procTree struct {
	once sync.Once
	// job is set right after Start, while Cancel may already run on another
	// goroutine: hence atomic. 0 if the job could not be set up.
	job atomic.Uintptr
	cmd *exec.Cmd
}

func startTree(cmd *exec.Cmd) (*procTree, error) {
	t := &procTree{cmd: cmd}
	cmd.Cancel = func() error {
		if job := windows.Handle(t.job.Load()); job != 0 {
			return windows.TerminateJobObject(job, 1)
		}
		return cmd.Process.Kill()
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// ponytail: the process is assigned to the job right after it started, so
	// a child it spawns in that instant escapes the job. Creating it suspended
	// (CREATE_SUSPENDED) and resuming after the assignment closes that gap, but
	// os/exec does not expose the thread handle needed to resume it.
	t.job.Store(uintptr(newKillOnCloseJob(cmd.Process.Pid)))
	return t, nil
}

// newKillOnCloseJob puts pid in a new kill-on-close job and returns the job,
// or 0 if any step fails (the command still runs, but its tree is not
// contained: the same as before this existed).
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

// kill ends every process still in the job (background children) and
// releases it. Safe to call once the command exited.
func (t *procTree) kill() {
	t.once.Do(func() {
		if job := windows.Handle(t.job.Swap(0)); job != 0 {
			_ = windows.TerminateJobObject(job, 1)
			_ = windows.CloseHandle(job)
		}
	})
}
