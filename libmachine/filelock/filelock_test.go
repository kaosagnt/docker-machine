package filelock

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileLockHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_FILELOCK_HELPER") != "1" {
		return
	}
	lock, err := Acquire(os.Getenv("FILELOCK_PATH"), time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Println("locked")
	time.Sleep(300 * time.Millisecond)
	_ = lock.Unlock()
	os.Exit(0)
}

func TestAcquireAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	cmd := exec.Command(os.Args[0], "-test.run=TestFileLockHelperProcess")
	cmd.Env = append(os.Environ(), "GO_WANT_FILELOCK_HELPER=1", "FILELOCK_PATH="+path)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	buf := make([]byte, len("locked\n"))
	_, err = io.ReadFull(stdout, buf)
	require.NoError(t, err)
	assert.Equal(t, "locked\n", string(buf))

	_, err = Acquire(path, 50*time.Millisecond)
	assert.ErrorIs(t, err, ErrAcquireTimeout)
	require.NoError(t, cmd.Wait())
	lock, err := Acquire(path, time.Second)
	require.NoError(t, err)
	require.NoError(t, lock.Unlock())
}

func TestAcquireContentionAndReacquisition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	first, err := Acquire(path, time.Second)
	require.NoError(t, err)
	_, err = Acquire(path, 20*time.Millisecond)
	assert.ErrorIs(t, err, ErrAcquireTimeout)
	require.NoError(t, first.Unlock())
	second, err := Acquire(path, time.Second)
	require.NoError(t, err)
	require.NoError(t, second.Unlock())
}

func TestAcquireDoesNotSucceedAfterTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	first, err := Acquire(path, time.Second)
	require.NoError(t, err)
	go func() {
		time.Sleep(60 * time.Millisecond)
		_ = first.Unlock()
	}()
	started := time.Now()
	_, err = Acquire(path, 50*time.Millisecond)
	assert.ErrorIs(t, err, ErrAcquireTimeout)
	assert.Less(t, time.Since(started), 60*time.Millisecond)
}

func TestAcquireCreatesOwnerOnlyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	lock, err := Acquire(path, time.Second)
	require.NoError(t, err)
	require.NoError(t, lock.Unlock())
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestAcquireZeroTimeoutMakesOneImmediateAttempt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	lock, err := Acquire(path, 0)
	require.NoError(t, err)
	require.NoError(t, lock.Unlock())

	held, err := Acquire(path, time.Second)
	require.NoError(t, err)
	_, err = Acquire(path, 0)
	assert.ErrorIs(t, err, ErrAcquireTimeout)
	require.NoError(t, held.Unlock())
}

func TestAcquireRejectsNegativeTimeout(t *testing.T) {
	_, err := Acquire(filepath.Join(t.TempDir(), "lock"), -time.Second)
	assert.ErrorIs(t, err, ErrInvalidTimeout)
}

func TestAcquireMissingParent(t *testing.T) {
	_, err := Acquire(filepath.Join(t.TempDir(), "missing", "lock"), time.Second)
	assert.Error(t, err)
	assert.False(t, errors.Is(err, ErrAcquireTimeout))
}
