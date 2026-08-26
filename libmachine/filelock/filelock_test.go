package filelock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestAcquireCreatesOwnerOnlyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	lock, err := Acquire(path, time.Second)
	require.NoError(t, err)
	require.NoError(t, lock.Unlock())
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestAcquireMissingParent(t *testing.T) {
	_, err := Acquire(filepath.Join(t.TempDir(), "missing", "lock"), time.Second)
	assert.Error(t, err)
	assert.False(t, errors.Is(err, ErrAcquireTimeout))
}
