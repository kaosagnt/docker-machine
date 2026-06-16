package drivers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/docker/machine/libmachine/ssh"
	gossh "golang.org/x/crypto/ssh"
)

// exitErrorWithCode runs a trivial command that exits with the given code so we
// get a real *exec.ExitError whose ExitCode() matches, mirroring what the ssh
// external client returns from CombinedOutput.
func exitErrorWithCode(t *testing.T, code int) *exec.ExitError {
	t.Helper()

	cmd := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code))
	err := cmd.Run()

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *exec.ExitError for exit code %d, got %T: %v", code, err, err)
	}
	if exitErr.ExitCode() != code {
		t.Fatalf("expected exit code %d, got %d", code, exitErr.ExitCode())
	}
	return exitErr
}

func TestIsSSHTransportError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil is not a transport error",
			err:  nil,
			want: false,
		},
		{
			name: "external ssh transport failure (exit 255) is retryable",
			err:  exitErrorWithCode(t, sshTransportExitStatus),
			want: true,
		},
		{
			name: "external remote command failure (exit 1) is not retryable",
			err:  exitErrorWithCode(t, 1),
			want: false,
		},
		{
			name: "external remote command failure (exit 2) is not retryable",
			err:  exitErrorWithCode(t, 2),
			want: false,
		},
		{
			name: "external remote command failure (exit 254) is not retryable",
			err:  exitErrorWithCode(t, 254),
			want: false,
		},
		{
			name: "native ExitError (real remote exit) is NOT retryable",
			err:  &gossh.ExitError{},
			want: false,
		},
		{
			name: "wrapped native ExitError is NOT retryable",
			err:  fmt.Errorf("process exited: %w", &gossh.ExitError{}),
			want: false,
		},
		{
			name: "native ExitMissingError (session torn down, no status) is retryable",
			err:  &gossh.ExitMissingError{},
			want: true,
		},
		{
			name: "non-ExitError (binary failed to start) is retryable",
			err:  errors.New("exec: \"ssh\": executable file not found in $PATH"),
			want: true,
		},
		{
			name: "context.Canceled is NOT retryable",
			err:  context.Canceled,
			want: false,
		},
		{
			name: "wrapped context.DeadlineExceeded is NOT retryable",
			err:  fmt.Errorf("ssh: %w", context.DeadlineExceeded),
			want: false,
		},
		{
			name: "wrapped external transport ExitError is detected through errors.As",
			err:  fmt.Errorf("ssh failed: %w", exitErrorWithCode(t, sshTransportExitStatus)),
			want: true,
		},
		{
			name: "wrapped external real-command ExitError is not retryable",
			err:  fmt.Errorf("ssh failed: %w", exitErrorWithCode(t, 1)),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSSHTransportError(tt.err); got != tt.want {
				t.Errorf("isSSHTransportError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

type cmdResult struct {
	out string
	err error
}

// fakeSeqClient is a minimal ssh.Client that returns a queued sequence of
// results, one per Output call, so we can model "transport-fail twice then
// succeed" and assert how many times the loop actually invoked the client.
type fakeSeqClient struct {
	queue []cmdResult
	calls int
}

func (c *fakeSeqClient) Output(command string) (string, error) {
	if c.calls >= len(c.queue) {
		return "", fmt.Errorf("unexpected extra Output call #%d for %q", c.calls+1, command)
	}
	r := c.queue[c.calls]
	c.calls++
	return r.out, r.err
}
func (c *fakeSeqClient) Shell(args ...string) error { return nil }
func (c *fakeSeqClient) Start(command string) (io.ReadCloser, io.ReadCloser, error) {
	return nil, nil, nil
}
func (c *fakeSeqClient) Wait() error { return nil }

// installFakeSSHClient swaps the package factory to hand back the given client,
// and zeroes the retry sleep so the test runs fast. Restores both on cleanup.
func installFakeSSHClient(t *testing.T, client ssh.Client) {
	t.Helper()
	origFactory := sshClientFactory
	origInterval := sshRetryInterval
	sshClientFactory = func(Driver) (ssh.Client, error) { return client, nil }
	sshRetryInterval = 0
	t.Cleanup(func() {
		sshClientFactory = origFactory
		sshRetryInterval = origInterval
	})
}

func TestRunSSHCommandFromDriverWithRetry(t *testing.T) {
	transport := exitErrorWithCode(t, sshTransportExitStatus)
	realFail := exitErrorWithCode(t, 1)

	t.Run("retries transport failure then succeeds", func(t *testing.T) {
		fake := &fakeSeqClient{queue: []cmdResult{
			{"", transport},
			{"", transport},
			{"ok", nil},
		}}
		installFakeSSHClient(t, fake)

		out, err := RunSSHCommandFromDriverWithRetry(nil, "apt-get install -y curl")
		if err != nil {
			t.Fatalf("expected success after retries, got error: %v", err)
		}
		if out != "ok" {
			t.Errorf("expected output %q, got %q", "ok", out)
		}
		if fake.calls != 3 {
			t.Errorf("expected 3 attempts (2 transport fails + success), got %d", fake.calls)
		}
	})

	t.Run("does NOT retry a genuine command failure", func(t *testing.T) {
		fake := &fakeSeqClient{queue: []cmdResult{
			{"boom", realFail},
			{"should-not-run", nil},
		}}
		installFakeSSHClient(t, fake)

		_, err := RunSSHCommandFromDriverWithRetry(nil, "false")
		if err == nil {
			t.Fatal("expected error for genuine command failure")
		}
		if fake.calls != 1 {
			t.Errorf("expected exactly 1 attempt for a real command failure, got %d", fake.calls)
		}
		if !strings.Contains(err.Error(), "ssh command error") {
			t.Errorf("expected wrapped 'ssh command error', got %v", err)
		}
	})

	t.Run("exhausts attempts on persistent transport failure", func(t *testing.T) {
		fake := &fakeSeqClient{queue: []cmdResult{
			{"", transport},
			{"", transport},
			{"", transport},
		}}
		installFakeSSHClient(t, fake)

		_, err := RunSSHCommandFromDriverWithRetry(nil, "apt-get install -y curl")
		if err == nil {
			t.Fatal("expected error after exhausting attempts")
		}
		if fake.calls != sshCommandMaxAttempts {
			t.Errorf("expected %d attempts, got %d", sshCommandMaxAttempts, fake.calls)
		}
	})
}

func TestRunSSHCommandFromDriverIsSingleShot(t *testing.T) {
	transport := exitErrorWithCode(t, sshTransportExitStatus)

	fake := &fakeSeqClient{queue: []cmdResult{
		{"", transport},
		{"", transport},
	}}
	installFakeSSHClient(t, fake)

	// The non-retrying entry point must run exactly once even on a transport
	// failure, so a session-severing command (e.g. `sudo shutdown -r now`,
	// which exits 255 on success) is never re-issued.
	_, err := RunSSHCommandFromDriver(nil, "sudo shutdown -r now")
	if err == nil {
		t.Fatal("expected error from single-shot transport failure")
	}
	if fake.calls != 1 {
		t.Errorf("single-shot must run exactly once (reboot safety), got %d calls", fake.calls)
	}
}
