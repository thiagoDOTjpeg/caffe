//go:build windows

package main

import (
	"encoding/base64"
	"encoding/binary"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shellCmd     = []string{"powershell.exe", "-NoLogo", "-NoProfile"}
	floodCmd     = []string{"powershell.exe", "-NoLogo", "-NoProfile", "-EncodedCommand", encode("$l = ('x' * 99) + \"`n\"; [Console]::Out.Write($l * 101011); [Console]::Out.Flush(); 'DONE-FLOOD'")}
	greet        = "\"hi-$(40+2)\"\r"
	sizeCmd      = "\"$([Console]::WindowHeight) $([Console]::WindowWidth)\"\r"
	clearSizeCmd = "cls; \"$([Console]::WindowHeight) $([Console]::WindowWidth)\"\r"
)

func encode(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func background(pidFile string) string {
	return "$p = Start-Process ping -ArgumentList '-n','300','127.0.0.1' -NoNewWindow -PassThru; $p.Id | Set-Content -Encoding ascii '" + pidFile + "'\r"
}

func tool(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	out, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		return ""
	}
	root := filepath.Join(filepath.FromSlash(strings.TrimSpace(string(out))), "..", "..", "..")
	p := filepath.Join(root, "usr", "bin", name+".exe")
	if _, err := exec.LookPath(p); err != nil {
		return ""
	}
	return p
}

func track(pid int) (func(), error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	defer windows.CloseHandle(proc)
	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	return func() {
		windows.TerminateJobObject(job, 1)
		windows.CloseHandle(job)
	}, nil
}

func alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == 259
}

func killPid(pid int) {
	if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid)); err == nil {
		windows.TerminateProcess(h, 1)
		windows.CloseHandle(h)
	}
}
