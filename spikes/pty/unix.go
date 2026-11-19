//go:build !windows

package main

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

var (
	shellCmd     = []string{"bash", "--norc", "--noprofile", "-i"}
	floodCmd     = []string{"sh", "-c", "head -c 10000000 /dev/zero | tr '\\0' x | fold -w 99; echo DONE-FLOOD"}
	greet        = "echo hi-$((40+2))\r"
	sizeCmd      = "stty size\r"
	clearSizeCmd = "clear; stty size\r"
)

func background(pidFile string) string { return "sleep 300 & echo $! > " + pidFile + "\r" }

func tool(name string) string {
	p, _ := exec.LookPath(name)
	return p
}

func sessionMembers(sid int) []int {
	entries, _ := os.ReadDir("/proc")
	var out []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		f := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
		if len(f) > 3 {
			if s, _ := strconv.Atoi(f[3]); s == sid {
				out = append(out, pid)
			}
		}
	}
	return out
}

func track(sid int) (func(), error) {
	return func() {
		for _, pid := range sessionMembers(sid) {
			syscall.Kill(pid, syscall.SIGKILL)
		}
		syscall.Kill(-sid, syscall.SIGKILL)
	}, nil
}

func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	st, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	return !strings.Contains(string(st), ") Z")
}

func killPid(pid int) { syscall.Kill(pid, syscall.SIGKILL) }
