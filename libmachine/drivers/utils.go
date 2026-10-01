package drivers

import (
	"context"
	"errors"
	"fmt"
	math_rand "math/rand"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/docker/machine/libmachine/log"
	"github.com/docker/machine/libmachine/mcnutils"
	"github.com/docker/machine/libmachine/ssh"
	gossh "golang.org/x/crypto/ssh"
)

const (
	// sshTransportExitStatus is the exit code the external ssh binary uses to
	// signal that it failed before the remote command produced its own exit
	// code, i.e. a connection/transport-level failure (refused, reset, dropped
	// mid-session, auth). Remote commands report their own exit codes in the
	// 1-254 range, so this value unambiguously identifies an ssh transport
	// failure rather than a genuine command failure.
	sshTransportExitStatus = 255

	// sshCommandMaxAttempts is the total number of times a provisioning SSH
	// command is attempted (first attempt plus retries) when it fails at the
	// transport layer. Only call sites that pass idempotent commands opt into
	// retry via RunSSHCommandFromDriverWithRetry; see that function.
	sshCommandMaxAttempts = 3

	// sshCommandRetryInterval is the default delay between transport-failure
	// retries.
	sshCommandRetryInterval = 3 * time.Second
)

// sshRunParams bundles the substitutable parameters of runSSHCommandFromDriver
// so tests can inject a fake client factory, a zero retry interval, and the
// desired attempt count without mutating package state (which would force
// tests to serialize and rule out t.Parallel()). Production callers build
// these via defaultSSHRunParams.
type sshRunParams struct {
	clientFactory func(Driver) (ssh.Client, error)
	retryInterval time.Duration
	maxAttempts   int
}

func defaultSSHRunParams(maxAttempts int) sshRunParams {
	return sshRunParams{
		clientFactory: GetSSHClientFromDriver,
		retryInterval: sshCommandRetryInterval,
		maxAttempts:   maxAttempts,
	}
}

func GetSSHClientFromDriver(d Driver) (ssh.Client, error) {
	address, err := d.GetSSHHostname()
	if err != nil {
		return nil, err
	}

	port, err := d.GetSSHPort()
	if err != nil {
		return nil, err
	}

	var auth *ssh.Auth
	if d.GetSSHKeyPath() == "" {
		auth = &ssh.Auth{}
	} else {
		auth = &ssh.Auth{
			Keys: []string{d.GetSSHKeyPath()},
		}
	}

	client, err := ssh.NewClient(d.GetSSHUsername(), address, port, auth)
	return client, err

}

// isSSHTransportError reports whether err from an ssh client Output call is a
// connection/transport-level failure (the session dropped, was refused, reset,
// or never established) as opposed to a genuine non-zero exit from the remote
// command. Only transport failures are safe to retry; a real command failure
// must be surfaced unchanged so we never mask a legitimate provisioning error
// by re-running it.
//
// The two ssh client implementations report a remote command's own exit code
// differently, so both are handled explicitly:
//
//   - External client (libmachine/ssh ExternalClient): runs the `ssh` binary,
//     whose CombinedOutput returns an *exec.ExitError. ssh exits 255 for any
//     transport failure and passes the remote command's own code (1-254)
//     through otherwise. So 255 => transport, 1-254 => real command failure.
//
//   - Native client (golang.org/x/crypto/ssh): a real remote command failure
//     is a *gossh.ExitError carrying the remote exit status => NOT a transport
//     error. A *gossh.ExitMissingError (session torn down without an exit
//     status) is a transport-level event => retryable.
//
// Any other error (failed to start the ssh binary, dial/handshake failure,
// client construction) is a connection/process problem, not a remote command
// exit code, and is therefore retryable. A nil error is not a transport error.
func isSSHTransportError(err error) bool {
	if err == nil {
		return false
	}

	// External client: ssh binary exit status.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode() == sshTransportExitStatus
	}

	// Native client: a delivered remote exit status is a real command failure,
	// not a transport drop.
	var nativeExit *gossh.ExitError
	if errors.As(err, &nativeExit) {
		return false
	}

	// Native client: session torn down without an exit status is transport.
	var nativeMissing *gossh.ExitMissingError
	if errors.As(err, &nativeMissing) {
		return true
	}

	// An explicit cancellation or deadline is a caller decision to stop, not a
	// transient transport blip — never retry past it. (The current ssh.Client
	// surface does not carry a context, but guard it so a future
	// context-aware client cannot be silently retried against the caller's
	// intent.)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Everything else (dial/handshake failure, ssh binary not startable, etc.)
	// is connection/process level, so it is safe to retry.
	return true
}

// RunSSHCommandFromDriver runs command on the host exactly once. Use this for
// commands that are NOT safe to re-run on a transient transport failure — most
// importantly commands whose success severs the SSH session (e.g.
// `sudo shutdown -r now`, which exits 255 because the box reboots), and the
// WaitForSSH reachability probe (the caller's own loop provides retry there).
func RunSSHCommandFromDriver(d Driver, command string) (string, error) {
	return runSSHCommandFromDriver(d, command, defaultSSHRunParams(1))
}

// RunSSHCommandFromDriverWithRetry runs command, attempting up to
// sshCommandMaxAttempts times in total when it fails at the SSH transport layer (a
// dropped/refused session) while leaving genuine non-zero command exits
// unretried. Use this only for idempotent commands — the provisioning commands
// docker-machine issues (apt-get install, writing certs, systemctl restart)
// are safe to re-run. It is the mitigation for transient mid-session SSH drops,
// which are amplified on high-latency / cross-region links. The total attempt
// count defaults to sshCommandMaxAttempts and can be overridden at runtime via
// the DOCKER_MACHINE_SSH_COMMAND_MAX_ATTEMPTS environment variable.
func RunSSHCommandFromDriverWithRetry(d Driver, command string) (string, error) {
	return runSSHCommandFromDriver(d, command, defaultSSHRunParams(defaultSSHCommandMaxAttempts()))
}

// defaultSSHCommandMaxAttempts is the retry budget for
// RunSSHCommandFromDriverWithRetry: sshCommandMaxAttempts by default, overridable
// via the DOCKER_MACHINE_SSH_COMMAND_MAX_ATTEMPTS env var (a positive integer).
// An unset, unparseable, or <= 0 value falls back to the default.
func defaultSSHCommandMaxAttempts() int {
	attempts, err := strconv.Atoi(os.Getenv("DOCKER_MACHINE_SSH_COMMAND_MAX_ATTEMPTS"))
	if err != nil || attempts <= 0 {
		return sshCommandMaxAttempts
	}

	return attempts
}

func runSSHCommandFromDriver(d Driver, command string, params sshRunParams) (string, error) {
	log.Debugf("About to run SSH command:\n%s", command)

	// Defensive: callers pass a literal here, but guard the internal contract
	// so a future 0/negative never silently runs the command zero times and
	// returns a nil-error-formatted failure.
	maxAttempts := params.maxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	// Same defensive contract for the client factory: a zero-value sshRunParams
	// (a future same-package caller that forgets defaultSSHRunParams) would
	// otherwise panic on a nil function call. Fall back to the production factory.
	if params.clientFactory == nil {
		params.clientFactory = GetSSHClientFromDriver
	}

	var (
		output string
		err    error
	)

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Build a fresh client (and therefore a fresh ssh process and
		// connection) on every attempt so a dropped session is fully
		// re-established rather than reused.
		var client ssh.Client
		client, err = params.clientFactory(d)
		if err != nil {
			// Client construction does NO network I/O — it reads only
			// local/driver state (hostname, port, key file). The TCP dial,
			// DNS resolution and SSH handshake all happen later inside
			// client.Output(), where a transient failure surfaces as an
			// exit-255 / ExitMissingError that isSSHTransportError DOES retry.
			// So a failure here is a deterministic local error (bad port,
			// unreadable key), not a transient transport drop: fail fast
			// rather than retry. This also matches the pre-existing behavior.
			return "", err
		}

		output, err = client.Output(command)
		log.Debugf("SSH cmd err, output: %v: %s", err, output)
		if err == nil {
			return output, nil
		}

		if !isSSHTransportError(err) || attempt == maxAttempts {
			break
		}

		log.Warnf("SSH command hit a transport-level error (attempt %d/%d), retrying in %s: %v",
			attempt, maxAttempts, params.retryInterval, err)
		time.Sleep(params.retryInterval)
	}

	return "", fmt.Errorf(`ssh command error:
command : %s
err     : %v
output  : %s`, command, err, output)
}

func sshAvailableFunc(d Driver) func() bool {
	return func() bool {
		log.Debug("Getting to WaitForSSH function...")
		// Single-shot: mcnutils.WaitFor already retries this probe many times,
		// so an inner retry here would nest two retry loops.
		if _, err := RunSSHCommandFromDriver(d, "exit 0"); err != nil {
			log.Debugf("Error getting ssh command 'exit 0' : %s", err)
			return false
		}
		return true
	}
}

// sshReadinessTimeoutEnv opts WaitForSSH into an overall deadline. Its value
// is a Go duration such as "90s" or "2m". Unset keeps the legacy behavior; a
// value that is not a positive duration is ignored with a warning.
const sshReadinessTimeoutEnv = "DOCKER_MACHINE_SSH_READINESS_TIMEOUT"

// ErrSSHReadinessTimeout is returned by WaitForSSH when the readiness deadline
// passes before an SSH probe succeeds. Drivers that call WaitForSSH from their
// plugin process return it over RPC as text, so errors.Is only works
// in-process.
var ErrSSHReadinessTimeout = errors.New("SSH readiness deadline exceeded")

// sshReadinessParams are the knobs of the deadline-bounded readiness loop.
// The defaults match mcnutils.WaitFor so that only the deadline differs from
// the legacy loop.
type sshReadinessParams struct {
	clientFactory func(Driver) (ssh.Client, error)
	maxAttempts   int
	interval      time.Duration
	jitter        time.Duration
}

func defaultSSHReadinessParams() sshReadinessParams {
	return sshReadinessParams{
		clientFactory: GetSSHClientFromDriver,
		maxAttempts:   60,
		interval:      3 * time.Second,
		jitter:        9 * time.Second,
	}
}

// sshReadinessTimeout returns the configured readiness deadline, or false
// when none is in effect.
func sshReadinessTimeout() (time.Duration, bool) {
	value, set := os.LookupEnv(sshReadinessTimeoutEnv)
	if !set || value == "" {
		return 0, false
	}

	timeout, err := time.ParseDuration(value)
	if err != nil || timeout <= 0 {
		log.Warnf("Ignoring %s=%q: want a positive duration such as 90s; SSH readiness has no overall deadline",
			sshReadinessTimeoutEnv, value)
		return 0, false
	}

	return timeout, true
}

func WaitForSSH(d Driver) error {
	if timeout, ok := sshReadinessTimeout(); ok {
		return waitForSSHWithin(d, timeout, defaultSSHReadinessParams())
	}

	// mcnutils.WaitFor retries the reachability probe up to 60 times with a
	// 3s (+0-9s jitter) interval. Each external ssh probe can itself take up
	// to 30s (ConnectionAttempts=3, ConnectTimeout=10), so an unreachable
	// host holds this for roughly 40 minutes.
	if err := mcnutils.WaitFor(sshAvailableFunc(d)); err != nil {
		return fmt.Errorf("Too many retries waiting for SSH to be available.  Last error: %s", err)
	}
	return nil
}

// waitForSSHWithin probes like the legacy loop, but stops at timeout: the
// in-flight probe is cancelled (its ssh process killed, or its connection
// closed) and no further probe starts. A probe that succeeds as the deadline
// passes still counts: the host answered, so there is nothing to gain by
// discarding it.
func waitForSSHWithin(d Driver, timeout time.Duration, params sshReadinessParams) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	started := time.Now()
	var (
		probes  int
		lastErr error
	)

	for probes < params.maxAttempts {
		probes++
		lastErr = probeSSH(ctx, d, params.clientFactory)
		if lastErr == nil {
			return nil
		}
		log.Debugf("SSH readiness probe %d failed: %s", probes, lastErr)

		if ctx.Err() != nil || probes == params.maxAttempts {
			break
		}

		wait := params.interval
		if params.jitter > 0 {
			wait += time.Duration(math_rand.Int63n(int64(params.jitter)))
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
		timer.Stop()
		if ctx.Err() != nil {
			break
		}
	}

	elapsed := time.Since(started)
	if ctx.Err() == nil {
		return fmt.Errorf("Too many retries waiting for SSH to be available.  Last error: %s", lastErr)
	}

	log.WithFields(log.Fields{
		"phase":      "wait_ssh",
		"reason":     "ssh_readiness_timeout",
		"timeout":    timeout.String(),
		"elapsed":    elapsed.Round(time.Millisecond).String(),
		"ssh_probes": probes,
	}).Warnf("No successful SSH probe within %s; giving up", timeout)

	return fmt.Errorf("%w: no successful SSH probe within %s (%d probes in %s). Last error: %v",
		ErrSSHReadinessTimeout, timeout, probes, elapsed.Round(time.Millisecond), lastErr)
}

// probeSSH runs one "exit 0" against the host, abandoning it when ctx ends if
// the client supports that. Both in-tree clients do.
func probeSSH(ctx context.Context, d Driver, factory func(Driver) (ssh.Client, error)) error {
	client, err := factory(d)
	if err != nil {
		return err
	}

	if cc, ok := client.(ssh.ContextClient); ok {
		_, err = cc.OutputContext(ctx, "exit 0")
		return err
	}

	_, err = client.Output("exit 0")
	return err
}
