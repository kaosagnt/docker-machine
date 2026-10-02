//go:build !windows

package drivers_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/docker/machine/drivers/fakedriver"
	"github.com/docker/machine/libmachine/drivers"
)

// sshTarget is a fake driver with an SSH address.
type sshTarget struct {
	*fakedriver.Driver
}

func (d *sshTarget) GetSSHHostname() (string, error) { return "192.0.2.1", nil }
func (d *sshTarget) GetSSHPort() (int, error)        { return 22, nil }
func (d *sshTarget) GetSSHUsername() string          { return "docker" }

// TestWaitForSSHDeadlineKillsSSHProcess runs WaitForSSH through the
// environment variable, the real client factory and the external client,
// with an ssh on PATH that never returns, and checks that every ssh process
// it started is gone afterwards.
//
// Not parallel: uses t.Setenv.
func TestWaitForSSHDeadlineKillsSSHProcess(t *testing.T) {
	dir := t.TempDir()
	pids := filepath.Join(dir, "pids")
	script := "#!/bin/sh\necho $$ >> " + pids + "\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Long enough for the fake ssh to start even on a loaded machine; it
	// then blocks for 60s, so only cancellation ends it within the test.
	drivers.SetMinSSHReadinessTimeout(t, time.Second)
	t.Setenv("DOCKER_MACHINE_SSH_READINESS_TIMEOUT", "5s")

	d := &sshTarget{Driver: &fakedriver.Driver{BaseDriver: &drivers.BaseDriver{}}}

	started := time.Now()
	err := drivers.WaitForSSH(d)
	elapsed := time.Since(started)

	if !errors.Is(err, drivers.ErrSSHReadinessTimeout) {
		t.Fatalf("err = %v, want ErrSSHReadinessTimeout", err)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("WaitForSSH returned after %s with a 5s deadline", elapsed)
	}

	data, err := os.ReadFile(pids)
	if err != nil {
		t.Fatalf("the fake ssh never ran: %v", err)
	}
	lines := strings.Fields(string(data))
	if len(lines) != 1 {
		t.Fatalf("started %d ssh processes, want 1 (the blocked probe)", len(lines))
	}
	pid, err := strconv.Atoi(lines[0])
	if err != nil {
		t.Fatal(err)
	}
	if syscall.Kill(pid, 0) == nil {
		t.Fatalf("ssh process %d is still running", pid)
	}
}

// TestWaitForSSHWithoutDeadlineUsesLegacyLoop pins the default: with the
// variable unset, empty, invalid or below the floor, WaitForSSH takes the
// legacy loop and a reachable host succeeds. A dispatch that always used the deadline path
// would get a zero timeout and fail every create.
//
// Not parallel: uses t.Setenv.
func TestWaitForSSHWithoutDeadlineUsesLegacyLoop(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	for _, value := range []string{"", "0", "soon", "5ms", "59s"} {
		t.Run("value="+value, func(t *testing.T) {
			t.Setenv("DOCKER_MACHINE_SSH_READINESS_TIMEOUT", value)
			if value == "" {
				if err := os.Unsetenv("DOCKER_MACHINE_SSH_READINESS_TIMEOUT"); err != nil {
					t.Fatal(err)
				}
			}

			d := &sshTarget{Driver: &fakedriver.Driver{BaseDriver: &drivers.BaseDriver{}}}
			if err := drivers.WaitForSSH(d); err != nil {
				t.Fatalf("WaitForSSH() = %v, want nil via the legacy loop", err)
			}
		})
	}
}
