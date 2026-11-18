//go:build unix

package service

import (
	"fmt"
	"os"
	"syscall"
	"testing"
)

// detachEvidence runs in the launched child: its pid and process group.
func detachEvidence() string { return fmt.Sprintf("%d %d", os.Getpid(), syscall.Getpgrp()) }

// assertDetached: Setsid made the child the leader of its own session and
// process group, which is not ours.
func assertDetached(t *testing.T, evidence string) {
	t.Helper()
	var pid, pgrp int
	fmt.Sscanf(evidence, "%d %d", &pid, &pgrp)
	if pid == 0 || pgrp != pid || pgrp == syscall.Getpgrp() {
		t.Errorf("child not detached: pid=%d pgrp=%d ours=%d", pid, pgrp, syscall.Getpgrp())
	}
}
