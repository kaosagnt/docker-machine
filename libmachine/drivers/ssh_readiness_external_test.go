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

type sshTarget struct {
	*fakedriver.Driver
}

func (d *sshTarget) GetSSHHostname() (string, error) { return "192.0.2.1", nil }
func (d *sshTarget) GetSSHPort() (int, error)        { return 22, nil }
func (d *sshTarget) GetSSHUsername() string          { return "docker" }

// Not parallel: uses t.Setenv.
func TestWaitForSSHDeadlineKillsSSHProcess(t *testing.T) {
	dir := t.TempDir()
	pids := filepath.Join(dir, "pids")
	script := "#!/bin/sh\necho $$ >> " + pids + "\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	drivers.SetMinSSHReadinessTimeout(t, time.Second)
	t.Setenv("DOCKER_MACHINE_SSH_READINESS_TIMEOUT", "5s")

	d := &sshTarget{Driver: &fakedriver.Driver{BaseDriver: &drivers.BaseDriver{}}}

	started := time.Now()
	err := drivers.WaitForSSH(d)
	elapsed := time.Since(started)

	if !errors.Is(err, drivers.ErrSSHReadinessTimeout) {
		t.Fatalf("err = %v", err)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("returned after %s", elapsed)
	}

	data, err := os.ReadFile(pids)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(data))
	if len(lines) != 1 {
		t.Fatalf("ssh processes started = %d, want 1", len(lines))
	}
	pid, err := strconv.Atoi(lines[0])
	if err != nil {
		t.Fatal(err)
	}
	if syscall.Kill(pid, 0) == nil {
		t.Fatalf("ssh process %d is still running", pid)
	}
}

// Not parallel: uses t.Setenv.
func TestWaitForSSHWithoutDeadlineSucceeds(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	for _, value := range []string{"", "0", "soon", "5ms", "2m"} {
		t.Run("value="+value, func(t *testing.T) {
			t.Setenv("DOCKER_MACHINE_SSH_READINESS_TIMEOUT", value)
			if value == "" {
				if err := os.Unsetenv("DOCKER_MACHINE_SSH_READINESS_TIMEOUT"); err != nil {
					t.Fatal(err)
				}
			}

			d := &sshTarget{Driver: &fakedriver.Driver{BaseDriver: &drivers.BaseDriver{}}}
			if err := drivers.WaitForSSH(d); err != nil {
				t.Fatal(err)
			}
		})
	}
}
