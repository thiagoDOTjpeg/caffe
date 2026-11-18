package core

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as the validation commands of the orchestrator
// tests, so they need no shell and run on Windows too. Children of the tests
// inherit CAFFE_CORE_HELPER=1; a child started that way runs one helper
// command (see helperMain) and exits.
func TestMain(m *testing.M) {
	if os.Getenv("CAFFE_CORE_HELPER") == "1" {
		os.Exit(helperMain(os.Args[1:]))
	}
	os.Setenv("CAFFE_CORE_HELPER", "1")
	// Under -race a child would sleep 1 s at exit (atexit_sleep_ms).
	os.Setenv("GORACE", strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	os.Exit(m.Run())
}

// helperMain implements the commands used in validation.commands:
//
//	check-file NAME   exit 0 if NAME exists in the working directory, else 1
//	fail              print "boom", exit 3
//	sleep             sleep for a minute
//	spawn FILE MODE   start a "sleep" child, write its pid to FILE, then
//	                  wait for it (MODE wait) or exit at once (MODE exit)
func helperMain(args []string) int {
	switch {
	case len(args) == 1 && args[0] == "sleep":
		time.Sleep(time.Minute)
		return 0
	case len(args) == 3 && args[0] == "spawn":
		child := exec.Command(os.Args[0], "sleep")
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := os.WriteFile(args[1], []byte(strconv.Itoa(child.Process.Pid)), 0o644); err != nil {
			return 1
		}
		if args[2] == "wait" {
			child.Wait()
		}
		return 0
	case len(args) == 2 && args[0] == "check-file":
		if _, err := os.Stat(args[1]); err != nil {
			fmt.Println(err)
			return 1
		}
		return 0
	case len(args) == 1 && args[0] == "fail":
		fmt.Println("boom")
		return 3
	}
	fmt.Fprintln(os.Stderr, "unknown helper command:", args)
	return 2
}

// helperCommand is a validation command line that runs the helper. Commands
// are split on whitespace, so the test binary's path must not contain any.
func helperCommand(t *testing.T, args ...string) string {
	t.Helper()
	if strings.ContainsAny(os.Args[0], " \t") {
		t.Skipf("test binary path %q contains whitespace; validation commands cannot express it", os.Args[0])
	}
	return strings.Join(append([]string{os.Args[0]}, args...), " ")
}
