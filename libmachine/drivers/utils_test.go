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
	t.Parallel()

	tests := map[string]struct {
		err  error
		want bool
	}{
		"nil is not a transport error": {
			err:  nil,
			want: false,
		},
		"external ssh transport failure (exit 255) is retryable": {
			err:  exitErrorWithCode(t, sshTransportExitStatus),
			want: true,
		},
		"external remote command failure (exit 1) is not retryable": {
			err:  exitErrorWithCode(t, 1),
			want: false,
		},
		"external remote command failure (exit 2) is not retryable": {
			err:  exitErrorWithCode(t, 2),
			want: false,
		},
		"external remote command failure (exit 254) is not retryable": {
			err:  exitErrorWithCode(t, 254),
			want: false,
		},
		"native ExitError (real remote exit) is NOT retryable": {
			err:  &gossh.ExitError{},
			want: false,
		},
		"wrapped native ExitError is NOT retryable": {
			err:  fmt.Errorf("process exited: %w", &gossh.ExitError{}),
			want: false,
		},
		"native ExitMissingError (session torn down, no status) is retryable": {
			err:  &gossh.ExitMissingError{},
			want: true,
		},
		"non-ExitError (binary failed to start) is retryable": {
			err:  errors.New("exec: \"ssh\": executable file not found in $PATH"),
			want: true,
		},
		"context.Canceled is NOT retryable": {
			err:  context.Canceled,
			want: false,
		},
		"wrapped context.DeadlineExceeded is NOT retryable": {
			err:  fmt.Errorf("ssh: %w", context.DeadlineExceeded),
			want: false,
		},
		"wrapped external transport ExitError is detected through errors.As": {
			err:  fmt.Errorf("ssh failed: %w", exitErrorWithCode(t, sshTransportExitStatus)),
			want: true,
		},
		"wrapped external real-command ExitError is not retryable": {
			err:  fmt.Errorf("ssh failed: %w", exitErrorWithCode(t, 1)),
			want: false,
		},
	}

	for name, tt := range tests {
		tt := tt
		t.Run(name, func(t *testing.T) {
			t.Parallel()
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

// fakeParams builds an sshRunParams that uses the given factory, skips the
// retry sleep entirely, and runs at most maxAttempts attempts. Each test owns
// its own params on the stack, so tests can safely run in parallel.
func fakeParams(factory func(Driver) (ssh.Client, error), maxAttempts int) sshRunParams {
	return sshRunParams{
		clientFactory: factory,
		retryInterval: 0,
		maxAttempts:   maxAttempts,
	}
}

// fakeParamsForClient is the common case: the factory always returns the same
// pre-built client.
func fakeParamsForClient(c ssh.Client, maxAttempts int) sshRunParams {
	return fakeParams(func(Driver) (ssh.Client, error) { return c, nil }, maxAttempts)
}

func TestRunSSHCommandFromDriverWithRetry(t *testing.T) {
	t.Parallel()

	transport := exitErrorWithCode(t, sshTransportExitStatus)
	realFail := exitErrorWithCode(t, 1)

	t.Run("retries transport failure then succeeds", func(t *testing.T) {
		t.Parallel()
		fake := &fakeSeqClient{queue: []cmdResult{
			{"", transport},
			{"", transport},
			{"ok", nil},
		}}

		out, err := runSSHCommandFromDriver(nil, "apt-get install -y curl", fakeParamsForClient(fake, sshCommandMaxAttempts))
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
		t.Parallel()
		fake := &fakeSeqClient{queue: []cmdResult{
			{"boom", realFail},
			{"should-not-run", nil},
		}}

		_, err := runSSHCommandFromDriver(nil, "false", fakeParamsForClient(fake, sshCommandMaxAttempts))
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
		t.Parallel()
		fake := &fakeSeqClient{queue: []cmdResult{
			{"", transport},
			{"", transport},
			{"", transport},
		}}

		_, err := runSSHCommandFromDriver(nil, "apt-get install -y curl", fakeParamsForClient(fake, sshCommandMaxAttempts))
		if err == nil {
			t.Fatal("expected error after exhausting attempts")
		}
		if fake.calls != sshCommandMaxAttempts {
			t.Errorf("expected %d attempts, got %d", sshCommandMaxAttempts, fake.calls)
		}
	})
}

// TestRunSSHCommandFromDriverHonorsMaxAttempts pins how runSSHCommandFromDriver
// reacts to every maxAttempts value the public API actually requests, plus the
// defensive clamp for 0/negative inputs. The maxAttempts=1 row stands in for
// the reboot-safety contract of the public RunSSHCommandFromDriver wrapper:
// a session-severing command (e.g. `sudo shutdown -r now`, which exits 255 on
// success) must NEVER be re-issued after a transport-class error.
func TestRunSSHCommandFromDriverHonorsMaxAttempts(t *testing.T) {
	t.Parallel()

	transport := exitErrorWithCode(t, sshTransportExitStatus)

	cases := []struct {
		name             string
		maxAttempts      int
		wantFactoryCalls int
	}{
		{"single-shot pins reboot safety", 1, 1},
		{"retry uses full attempt budget", sshCommandMaxAttempts, sshCommandMaxAttempts},
		{"zero is clamped to one", 0, 1},
		{"negative is clamped to one", -1, 1},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var factoryCalls int
			params := fakeParams(func(Driver) (ssh.Client, error) {
				factoryCalls++
				// Every attempt gets a fresh client whose queue holds a
				// single transport failure. If the loop ever ran an extra
				// attempt past the budget, the next call would hit a fresh
				// client's transport failure too, so factoryCalls would
				// exceed wantFactoryCalls and the assertion would fail.
				return &fakeSeqClient{queue: []cmdResult{{"", transport}}}, nil
			}, tc.maxAttempts)

			_, err := runSSHCommandFromDriver(nil, "apt-get install -y curl", params)
			if err == nil {
				t.Fatal("expected error on persistent transport failure")
			}
			if factoryCalls != tc.wantFactoryCalls {
				t.Errorf("got %d factory calls, want %d", factoryCalls, tc.wantFactoryCalls)
			}
		})
	}
}

// TestRunSSHCommandFromDriverWithRetryBuildsFreshClientPerAttempt pins the
// "build a fresh client (and therefore a fresh ssh process and connection) on
// every attempt" behavior. Each attempt gets a brand-new fakeSeqClient whose
// queue holds exactly ONE transport failure. If the loop ever reused a single
// client across attempts (a regression that hoists params.clientFactory out of
// the loop), the second Output call would hit the "unexpected extra Output
// call" guard and fail this test.
func TestRunSSHCommandFromDriverWithRetryBuildsFreshClientPerAttempt(t *testing.T) {
	t.Parallel()

	transport := exitErrorWithCode(t, sshTransportExitStatus)

	var factoryCalls int
	clients := []*fakeSeqClient{}
	params := fakeParams(func(Driver) (ssh.Client, error) {
		factoryCalls++
		c := &fakeSeqClient{queue: []cmdResult{{"", transport}}}
		clients = append(clients, c)
		return c, nil
	}, sshCommandMaxAttempts)

	_, err := runSSHCommandFromDriver(nil, "apt-get install -y curl", params)
	if err == nil {
		t.Fatal("expected error after exhausting attempts")
	}
	if factoryCalls != sshCommandMaxAttempts {
		t.Errorf("expected a fresh client per attempt (%d factory calls), got %d", sshCommandMaxAttempts, factoryCalls)
	}
	for i, c := range clients {
		if c.calls != 1 {
			t.Errorf("client %d should have received exactly 1 Output call, got %d", i, c.calls)
		}
	}
}

// TestRunSSHCommandFromDriverWithRetryFactoryErrorFailsFast pins that a client
// CONSTRUCTION failure (deterministic local/driver state, not a transient
// transport drop) is returned immediately, without retry and without being
// wrapped in the "ssh command error" envelope.
func TestRunSSHCommandFromDriverWithRetryFactoryErrorFailsFast(t *testing.T) {
	t.Parallel()

	buildErr := errors.New("get ssh port: boom")

	var factoryCalls int
	params := fakeParams(func(Driver) (ssh.Client, error) {
		factoryCalls++
		return nil, buildErr
	}, sshCommandMaxAttempts)

	_, err := runSSHCommandFromDriver(nil, "apt-get install -y curl", params)
	if !errors.Is(err, buildErr) {
		t.Fatalf("expected the raw construction error, got %v", err)
	}
	if strings.Contains(err.Error(), "ssh command error") {
		t.Errorf("construction error must not be wrapped in the command-error envelope, got %v", err)
	}
	if factoryCalls != 1 {
		t.Errorf("construction failure must fail fast (1 factory call, no retry), got %d", factoryCalls)
	}
}

// TestDefaultSSHRunParams pins the wrapper→params wiring that the public
// entry points depend on. Removing the package-global test seam made the public
// RunSSHCommandFromDriver / ...WithRetry wrappers un-mockable, so this asserts
// the params they build instead: single-shot must be exactly 1 attempt (the
// reboot-safety contract — a regression to defaultSSHRunParams(3) here would
// otherwise be invisible), retry uses the full budget, and the production
// factory + retry interval are wired.
func TestDefaultSSHRunParams(t *testing.T) {
	t.Parallel()

	single := defaultSSHRunParams(1)
	if single.maxAttempts != 1 {
		t.Errorf("defaultSSHRunParams(1).maxAttempts = %d, want 1 (reboot safety)", single.maxAttempts)
	}
	if single.clientFactory == nil {
		t.Error("defaultSSHRunParams must set a non-nil clientFactory")
	}

	retry := defaultSSHRunParams(sshCommandMaxAttempts)
	if retry.maxAttempts != sshCommandMaxAttempts {
		t.Errorf("defaultSSHRunParams(%d).maxAttempts = %d, want %d",
			sshCommandMaxAttempts, retry.maxAttempts, sshCommandMaxAttempts)
	}
	if retry.retryInterval != sshCommandRetryInterval {
		t.Errorf("defaultSSHRunParams retryInterval = %v, want %v",
			retry.retryInterval, sshCommandRetryInterval)
	}
}
