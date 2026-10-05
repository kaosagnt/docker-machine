package drivers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/docker/machine/libmachine/log"
	"github.com/docker/machine/libmachine/ssh"
)

type fakeProbeClient struct {
	probe func(ctx context.Context) error
}

func (c *fakeProbeClient) OutputContext(ctx context.Context, command string) (string, error) {
	return "", c.probe(ctx)
}
func (c *fakeProbeClient) Output(command string) (string, error) {
	return "", errors.New("Output called on a ContextClient")
}
func (c *fakeProbeClient) Shell(args ...string) error { return nil }
func (c *fakeProbeClient) Start(command string) (io.ReadCloser, io.ReadCloser, error) {
	return nil, nil, nil
}
func (c *fakeProbeClient) Wait() error { return nil }

func readinessParams(probe func(ctx context.Context) error, b func() backoff.BackOff) (sshReadinessParams, *atomic.Int32) {
	var calls atomic.Int32
	client := &fakeProbeClient{probe: func(ctx context.Context) error {
		calls.Add(1)
		return probe(ctx)
	}}
	return sshReadinessParams{
		clientFactory: func(Driver) (ssh.Client, error) { return client, nil },
		backOff:       b,
	}, &calls
}

func noWait() backoff.BackOff { return &backoff.ZeroBackOff{} }

func withDeadline(t *testing.T, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func TestWaitForSSHCancelsBlockedProbe(t *testing.T) {
	t.Parallel()

	var sawCancel atomic.Bool
	params, calls := readinessParams(func(ctx context.Context) error {
		<-ctx.Done()
		sawCancel.Store(true)
		return ctx.Err()
	}, noWait)

	started := time.Now()
	err := waitForSSH(withDeadline(t, 200*time.Millisecond), nil, 200*time.Millisecond, params)
	elapsed := time.Since(started)

	if !errors.Is(err, ErrSSHReadinessTimeout) {
		t.Fatalf("err = %v", err)
	}
	if !sawCancel.Load() {
		t.Fatal("probe was not cancelled")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("probes = %d, want 1", got)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("returned after %s", elapsed)
	}
	for _, want := range []string{"within 200ms", "1 probes", "context deadline exceeded"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestWaitForSSHExpiredDeadlineStartsNoProbe(t *testing.T) {
	t.Parallel()

	var factoryCalls atomic.Int32
	params := sshReadinessParams{
		clientFactory: func(Driver) (ssh.Client, error) {
			factoryCalls.Add(1)
			return nil, errors.New("unexpected")
		},
		backOff: noWait,
	}

	err := waitForSSH(withDeadline(t, 0), nil, 0, params)

	if !errors.Is(err, ErrSSHReadinessTimeout) {
		t.Fatalf("err = %v", err)
	}
	if got := factoryCalls.Load(); got != 0 {
		t.Fatalf("factory calls = %d, want 0", got)
	}
	if !strings.Contains(err.Error(), "0 probes") || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("error = %q", err)
	}
}

func TestWaitForSSHDeadlineDuringBackoff(t *testing.T) {
	t.Parallel()

	params, calls := readinessParams(func(ctx context.Context) error {
		return errors.New("connection refused")
	}, func() backoff.BackOff { return backoff.NewConstantBackOff(time.Hour) })

	started := time.Now()
	err := waitForSSH(withDeadline(t, 200*time.Millisecond), nil, 200*time.Millisecond, params)

	if !errors.Is(err, ErrSSHReadinessTimeout) {
		t.Fatalf("err = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("returned after %s", elapsed)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("probes = %d, want 1", got)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error = %q", err)
	}
}

func TestWaitForSSHSlowHealthyHostSucceeds(t *testing.T) {
	t.Parallel()

	var failuresLeft atomic.Int32
	failuresLeft.Store(4)
	params, calls := readinessParams(func(ctx context.Context) error {
		if failuresLeft.Add(-1) >= 0 {
			return errors.New("connection refused")
		}
		return nil
	}, noWait)

	if err := waitForSSH(withDeadline(t, 5*time.Second), nil, 5*time.Second, params); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 5 {
		t.Fatalf("probes = %d, want 5", got)
	}
}

func TestWaitForSSHKeepsAttemptCap(t *testing.T) {
	t.Parallel()

	params, calls := readinessParams(func(ctx context.Context) error {
		return errors.New("connection refused")
	}, func() backoff.BackOff { return backoff.WithMaxRetries(&backoff.ZeroBackOff{}, 2) })

	err := waitForSSH(context.Background(), nil, 0, params)

	if err == nil || errors.Is(err, ErrSSHReadinessTimeout) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "Too many retries waiting for SSH to be available") {
		t.Errorf("error = %q", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("probes = %d, want 3", got)
	}
}

func TestWaitForSSHRetriesClientFactoryErrors(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	params := sshReadinessParams{
		clientFactory: func(Driver) (ssh.Client, error) {
			calls.Add(1)
			return nil, errors.New("no address yet")
		},
		backOff: func() backoff.BackOff { return backoff.WithMaxRetries(&backoff.ZeroBackOff{}, 2) },
	}

	err := waitForSSH(context.Background(), nil, 0, params)
	if err == nil || !strings.Contains(err.Error(), "no address yet") {
		t.Fatalf("err = %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("factory calls = %d, want 3", got)
	}
}

func TestWaitForSSHFallsBackToOutput(t *testing.T) {
	t.Parallel()

	client := &fakeSeqClient{queue: []cmdResult{
		{err: errors.New("refused")},
		{out: ""},
	}}
	params := sshReadinessParams{
		clientFactory: func(Driver) (ssh.Client, error) { return client, nil },
		backOff:       noWait,
	}

	if err := waitForSSH(withDeadline(t, 5*time.Second), nil, 5*time.Second, params); err != nil {
		t.Fatal(err)
	}
	if client.calls != 2 {
		t.Fatalf("Output calls = %d, want 2", client.calls)
	}
}

// Alerts key on these fields. Not parallel: replaces the package logger.
func TestWaitForSSHTimeoutLogsReason(t *testing.T) {
	prev := log.Format()
	if err := log.SetFormat(log.FormatJSON); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.SetFormat(prev) })
	var buf bytes.Buffer
	log.SetOutWriter(&buf)
	log.SetErrWriter(&buf)

	params, _ := readinessParams(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}, noWait)
	_ = waitForSSH(withDeadline(t, 100*time.Millisecond), nil, 100*time.Millisecond, params)

	scanner := bufio.NewScanner(&buf)
	for scanner.Scan() {
		var entry map[string]any
		if json.Unmarshal(scanner.Bytes(), &entry) != nil || entry["reason"] != "ssh_readiness_timeout" {
			continue
		}
		if entry["phase"] != "wait_ssh" || entry["level"] != "warn" || entry["ssh_probes"] != float64(1) || entry["timeout"] != "100ms" {
			t.Fatalf("entry = %v", entry)
		}
		return
	}
	t.Fatalf("no ssh_readiness_timeout entry in %q", buf.String())
}

func TestDefaultSSHReadinessParamsKeepOldSchedule(t *testing.T) {
	t.Parallel()

	p := defaultSSHReadinessParams()
	if p.clientFactory == nil {
		t.Fatal("clientFactory is nil")
	}
	b := p.backOff()
	waits := 0
	for next := b.NextBackOff(); next != backoff.Stop; next = b.NextBackOff() {
		waits++
		if waits > 59 {
			t.Fatal("more than 59 waits")
		}
		if next < 3*time.Second || next > 12*time.Second {
			t.Fatalf("wait %d = %s, want 3s to 12s", waits, next)
		}
	}
	if waits != 59 {
		t.Fatalf("waits = %d, want 59 (60 probes)", waits)
	}
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func TestSSHProbeIntervalHasNoElapsedLimit(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Unix(0, 0)}
	b := sshProbeInterval()
	b.Clock = clock
	b.Reset()
	clock.now = clock.now.Add(time.Hour)

	if next := b.NextBackOff(); next == backoff.Stop {
		t.Fatal("backoff stopped after an hour")
	}
}

func TestSSHReadinessTimeout(t *testing.T) {
	tests := map[string]struct {
		value   string
		unset   bool
		want    time.Duration
		enabled bool
	}{
		"unset":                               {unset: true},
		"empty":                               {value: ""},
		"seconds":                             {value: "90s", want: 90 * time.Second, enabled: true},
		"minutes":                             {value: "2m", want: 2 * time.Minute, enabled: true},
		"exactly the floor":                   {value: "1m", want: time.Minute, enabled: true},
		"just below the floor is ignored":     {value: "59s", enabled: false},
		"milliseconds for minutes is ignored": {value: "5ms", enabled: false},
		"zero is ignored":                     {value: "0"},
		"negative is ignored":                 {value: "-30s"},
		"a bare number is ignored (no unit)":  {value: "90"},
		"garbage is ignored":                  {value: "soon"},
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
