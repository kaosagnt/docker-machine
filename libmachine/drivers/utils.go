package drivers

import (
	"context"
	"errors"
	"fmt"
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

func WaitForSSH(d Driver) error {
	// mcnutils.WaitFor retries the reachability probe up to 60 times with a
	// 3s (+0-9s jitter) interval, so this can wait several minutes for SSH to
	// come up before timing out.
	if err := mcnutils.WaitFor(sshAvailableFunc(d)); err != nil {
		return fmt.Errorf("Too many retries waiting for SSH to be available.  Last error: %s", err)
	}
	return nil
}
