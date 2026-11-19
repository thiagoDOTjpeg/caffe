//go:build unix

package service

import "syscall"

func detachedAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
