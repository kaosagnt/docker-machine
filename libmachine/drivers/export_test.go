package drivers

import (
	"testing"
	"time"
)

// SetMinSSHReadinessTimeout lowers the readiness-deadline floor for one test.
// Callers must not be parallel.
func SetMinSSHReadinessTimeout(t testing.TB, d time.Duration) {
	t.Helper()
	old := minSSHReadinessTimeout
	minSSHReadinessTimeout = d
	t.Cleanup(func() { minSSHReadinessTimeout = old })
}
