//go:build unix

package service

import "syscall"

// detachedAttr starts the child in a new session: it has no controlling
// terminal and is not in the caller's process group, so closing the caller's
// terminal (SIGHUP) or Ctrl-C in it does not reach the run.
func detachedAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
