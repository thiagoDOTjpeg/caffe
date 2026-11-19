//go:build unix

package service

import (
	"fmt"
	"os"
	"syscall"
	"testing"
)

func detachEvidence() string { return fmt.Sprintf("%d %d", os.Getpid(), syscall.Getpgrp()) }

func assertDetached(t *testing.T, evidence string) {
	t.Helper()
	var pid, pgrp int
	fmt.Sscanf(evidence, "%d %d", &pid, &pgrp)
	if pid == 0 || pgrp != pid || pgrp == syscall.Getpgrp() {
		t.Errorf("child not detached: pid=%d pgrp=%d ours=%d", pid, pgrp, syscall.Getpgrp())
	}
}
