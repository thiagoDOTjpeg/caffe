//go:build windows

package service

import "syscall"

// detachedAttr puts the child in its own process group, without a console,
// so Ctrl-C in the caller's console does not reach the run.
//
// ponytail: compiled but never run on Windows; whether the child survives the
// caller's console closing is unverified (backlog F-10, "Investigate").
func detachedAttr() *syscall.SysProcAttr {
	const detachedProcess = 0x00000008 // DETACHED_PROCESS
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess}
}
