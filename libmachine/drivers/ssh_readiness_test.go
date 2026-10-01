package drivers

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/machine/libmachine/ssh"
)

// fakeProbeClient is a ContextClient whose OutputContext delegates to probe.
type fakeProbeClient struct {
	probe func(ctx context.Context) error
}

func (c *fakeProbeClient) OutputContext(ctx context.Context, command string) (string, error) {
	return "", c.probe(ctx)
}
func (c *fakeProbeClient) Output(command string) (string, error) {
	return "", errors.New("Output must not be used when OutputContext is available")
}
func (c *fakeProbeClient) Shell(args ...string) error { return nil }
func (c *fakeProbeClient) Start(command string) (io.ReadCloser, io.ReadCloser, error) {
	return nil, nil, nil
}
func (c *fakeProbeClient) Wait() error { return nil }

func readinessParams(probe func(ctx context.Context) error, maxAttempts int, interval time.Duration) (sshReadinessParams, *atomic.Int32) {
	var calls atomic.Int32
	client := &fakeProbeClient{probe: func(ctx context.Context) error {
		calls.Add(1)
		return probe(ctx)
	}}
	return sshReadinessParams{
		clientFactory: func(Driver) (ssh.Client, error) { return client, nil },
		maxAttempts:   maxAttempts,
		interval:      interval,
	}, &calls
}

func TestWaitForSSHWithinCancelsBlockedProbe(t *testing.T) {
	t.Parallel()

	var probeSawCancel atomic.Bool
	params, calls := readinessParams(func(ctx context.Context) error {
		<-ctx.Done() // an unreachable host: the probe blocks until cancelled
		probeSawCancel.Store(true)
		return ctx.Err()
	}, 60, 0)

	started := time.Now()
	err := waitForSSHWithin(nil, 200*time.Millisecond, params)
	elapsed := time.Since(started)

	if !errors.Is(err, ErrSSHReadinessTimeout) {
		t.Fatalf("err = %v, want ErrSSHReadinessTimeout", err)
	}
	if !probeSawCancel.Load() {
		t.Fatal("the in-flight probe was not cancelled")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("probes = %d, want 1: no probe may start after the deadline", got)
	}
	if elapsed < 200*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("returned after %s, want shortly after the 200ms deadline", elapsed)
	}
	for _, want := range []string{"within 200ms", "1 probes", "context deadline exceeded"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestWaitForSSHWithinExpiredDeadlineStartsNoProbe(t *testing.T) {
	t.Parallel()

	// A non-positive timeout yields an already-expired context.
	var factoryCalls atomic.Int32
	params := sshReadinessParams{
		clientFactory: func(Driver) (ssh.Client, error) {
			factoryCalls.Add(1)
			return nil, errors.New("must not be called")
		},
		maxAttempts: 60,
	}

	err := waitForSSHWithin(nil, 0, params)

	if !errors.Is(err, ErrSSHReadinessTimeout) {
		t.Fatalf("err = %v, want ErrSSHReadinessTimeout", err)
	}
	if got := factoryCalls.Load(); got != 0 {
		t.Fatalf("client factory called %d times after the deadline", got)
	}
	if !strings.Contains(err.Error(), "0 probes") || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("error %q should report 0 probes and the deadline", err)
	}
}

func TestWaitForSSHWithinDeadlineDuringBackoff(t *testing.T) {
	t.Parallel()

	params, calls := readinessParams(func(ctx context.Context) error {
		return errors.New("connection refused")
	}, 60, time.Hour)

	started := time.Now()
	err := waitForSSHWithin(nil, 200*time.Millisecond, params)

	if !errors.Is(err, ErrSSHReadinessTimeout) {
		t.Fatalf("err = %v, want ErrSSHReadinessTimeout", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("returned after %s; the deadline must interrupt the wait between probes", elapsed)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("probes = %d, want 1", got)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error %q does not carry the last probe error", err)
	}
}

func TestWaitForSSHWithinSlowHealthyHostSucceeds(t *testing.T) {
	t.Parallel()

	// sshd comes up after four refused probes, well inside the budget.
	var failuresLeft atomic.Int32
	failuresLeft.Store(4)
	params, calls := readinessParams(func(ctx context.Context) error {
		if failuresLeft.Add(-1) >= 0 {
			return errors.New("connection refused")
		}
		return nil
	}, 60, 10*time.Millisecond)

	if err := waitForSSHWithin(nil, 5*time.Second, params); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := calls.Load(); got != 5 {
		t.Fatalf("probes = %d, want 5", got)
	}
}

func TestWaitForSSHWithinKeepsAttemptCap(t *testing.T) {
	t.Parallel()

	params, calls := readinessParams(func(ctx context.Context) error {
		return errors.New("connection refused")
	}, 3, 0)

	err := waitForSSHWithin(nil, time.Hour, params)

	if err == nil || errors.Is(err, ErrSSHReadinessTimeout) {
		t.Fatalf("err = %v, want the legacy too-many-retries error", err)
	}
	if !strings.Contains(err.Error(), "Too many retries waiting for SSH to be available") {
		t.Errorf("error %q does not use the legacy message", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("probes = %d, want 3", got)
	}
}

func TestWaitForSSHWithinRetriesClientFactoryErrors(t *testing.T) {
	t.Parallel()

	// The legacy loop treats a client construction error as a failed probe,
	// so the deadline path does too.
	var calls atomic.Int32
	params := sshReadinessParams{
		clientFactory: func(Driver) (ssh.Client, error) {
			calls.Add(1)
			return nil, errors.New("no address yet")
		},
		maxAttempts: 3,
	}

	err := waitForSSHWithin(nil, time.Hour, params)
	if err == nil || !strings.Contains(err.Error(), "no address yet") {
		t.Fatalf("err = %v, want the factory error", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("factory calls = %d, want 3", got)
	}
}

func TestWaitForSSHWithinFallsBackToOutput(t *testing.T) {
	t.Parallel()

	// A client without OutputContext still works, uncancellably.
	client := &fakeSeqClient{queue: []cmdResult{
		{err: errors.New("refused")},
		{out: ""},
	}}
	params := sshReadinessParams{
		clientFactory: func(Driver) (ssh.Client, error) { return client, nil },
		maxAttempts:   60,
	}

	if err := waitForSSHWithin(nil, 5*time.Second, params); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.calls != 2 {
		t.Fatalf("Output calls = %d, want 2", client.calls)
	}
}

func TestDefaultSSHReadinessParamsMatchLegacyLoop(t *testing.T) {
	t.Parallel()

	p := defaultSSHReadinessParams()
	if p.maxAttempts != 60 || p.interval != 3*time.Second || p.jitter != 9*time.Second {
		t.Fatalf("params = %+v, want mcnutils.WaitFor's 60 attempts, 3s interval, 9s jitter", p)
	}
	if p.clientFactory == nil {
		t.Fatal("clientFactory is nil")
	}
}

// Not parallel: uses t.Setenv.
func TestSSHReadinessTimeout(t *testing.T) {
	tests := map[string]struct {
		value   string
		unset   bool
		want    time.Duration
		enabled bool
	}{
		"unset keeps the legacy loop":        {unset: true},
		"empty keeps the legacy loop":        {value: ""},
		"seconds":                            {value: "90s", want: 90 * time.Second, enabled: true},
		"minutes":                            {value: "2m", want: 2 * time.Minute, enabled: true},
		"zero is ignored":                    {value: "0"},
		"negative is ignored":                {value: "-30s"},
		"a bare number is ignored (no unit)": {value: "90"},
		"garbage is ignored":                 {value: "soon"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if tt.unset {
				// t.Setenv registers the restore; then remove the variable.
				t.Setenv(sshReadinessTimeoutEnv, "")
				if err := os.Unsetenv(sshReadinessTimeoutEnv); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Setenv(sshReadinessTimeoutEnv, tt.value)
			}

			got, enabled := sshReadinessTimeout()
			if got != tt.want || enabled != tt.enabled {
				t.Fatalf("sshReadinessTimeout() = (%s, %v), want (%s, %v)", got, enabled, tt.want, tt.enabled)
			}
		})
	}
}
