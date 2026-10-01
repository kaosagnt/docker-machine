//go:build !windows

package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// writeFakeSSH writes an executable standing in for the ssh binary. It
// records its PID in pidFile, then runs body.
func writeFakeSSH(t *testing.T, body string) (binary, pidFile string) {
	t.Helper()

	dir := t.TempDir()
	binary = filepath.Join(dir, "ssh")
	pidFile = filepath.Join(dir, "pid")
	script := "#!/bin/sh\necho $$ > " + pidFile + "\n" + body + "\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return binary, pidFile
}

func readPID(t *testing.T, pidFile string) int {
	t.Helper()

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("fake ssh never started: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func processExists(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func TestExternalClientOutputContextKillsBlockedSSH(t *testing.T) {
	t.Parallel()

	// exec replaces the shell, so the PID on file is the blocked process.
	binary, pidFile := writeFakeSSH(t, "exec sleep 60")
	client := &ExternalClient{BinaryPath: binary, BaseArgs: []string{"user@host"}}

	// Cancel once ssh is running, so the test exercises a blocked process
	// rather than one killed before it started.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var cancelledAt time.Time
	go func() {
		for {
			if data, err := os.ReadFile(pidFile); err == nil && strings.HasSuffix(string(data), "\n") {
				cancelledAt = time.Now()
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	_, err := client.OutputContext(ctx, "exit 0")
	returnedAt := time.Now()

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if lag := returnedAt.Sub(cancelledAt); lag > 3*time.Second {
		t.Fatalf("OutputContext returned %s after cancellation", lag)
	}
	if pid := readPID(t, pidFile); processExists(pid) {
		t.Fatalf("ssh process %d is still running after cancellation", pid)
	}
}

func TestExternalClientOutputContextBoundsInheritedPipes(t *testing.T) {
	t.Parallel()

	// A background child keeps ssh's output pipe open after ssh is killed;
	// WaitDelay must stop CombinedOutput from waiting for it. OpenSSH starts
	// no such child with baseSSHArgs; this only exercises the bound. The
	// child is not ours to kill via ssh, so the test cleans it up itself.
	dir := t.TempDir()
	childPID := filepath.Join(dir, "child")
	binary, _ := writeFakeSSH(t, "sleep 60 &\necho $! > "+childPID+"\nexec sleep 60")
	t.Cleanup(func() {
		if pid, err := os.ReadFile(childPID); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(pid))); err == nil {
				_ = syscall.Kill(n, syscall.SIGKILL)
			}
		}
	})
	client := &ExternalClient{BinaryPath: binary, BaseArgs: []string{"user@host"}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			// Wait for the child's PID so cleanup can always find it.
			if data, err := os.ReadFile(childPID); err == nil && strings.HasSuffix(string(data), "\n") {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	started := time.Now()
	_, err := client.OutputContext(ctx, "exit 0")
	elapsed := time.Since(started)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed > externalCancelWaitDelay+5*time.Second {
		t.Fatalf("OutputContext returned after %s; WaitDelay is %s", elapsed, externalCancelWaitDelay)
	}
}

func TestExternalClientOutputContextSuccess(t *testing.T) {
	t.Parallel()

	binary, _ := writeFakeSSH(t, `echo "ran: $*"`)
	client := &ExternalClient{BinaryPath: binary, BaseArgs: []string{"user@host"}}

	out, err := client.OutputContext(context.Background(), "exit 0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "ran: user@host exit 0" {
		t.Fatalf("output = %q", out)
	}
}

func TestExternalClientOutputContextPassesThroughRemoteExit(t *testing.T) {
	t.Parallel()

	binary, _ := writeFakeSSH(t, "exit 255")
	client := &ExternalClient{BinaryPath: binary, BaseArgs: []string{"user@host"}}

	_, err := client.OutputContext(context.Background(), "exit 0")
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("a failure with a live context was reported as cancellation: %v", err)
	}
	var exitErr interface{ ExitCode() int }
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 255 {
		t.Fatalf("err = %v, want the ssh exit status 255", err)
	}
}

func TestExternalClientOutputContextDoesNotMutateBaseArgs(t *testing.T) {
	t.Parallel()

	binary, _ := writeFakeSSH(t, "exit 0")
	base := make([]string, 1, 4)
	base[0] = "user@host"
	client := &ExternalClient{BinaryPath: binary, BaseArgs: base}

	if _, err := client.OutputContext(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	if got := base[:cap(base)][1]; got != "" {
		t.Fatalf("OutputContext wrote %q into BaseArgs' spare capacity", got)
	}
}

// tarpit accepts TCP connections and never speaks SSH, like a host whose
// sshd never answers. It reports when the client side closes.
func tarpit(t *testing.T) (addr *net.TCPAddr, closed <-chan struct{}) {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })

	ch := make(chan struct{})
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 1024)
		for {
			if _, err := conn.Read(buf); err != nil {
				close(ch)
				return
			}
		}
	}()

	return l.Addr().(*net.TCPAddr), ch
}

func TestNativeClientOutputContextCancelsBlockedHandshake(t *testing.T) {
	addr, closed := tarpit(t)
	client := &NativeClient{
		Config:   ssh.ClientConfig{User: "user", HostKeyCallback: ssh.InsecureIgnoreHostKey()},
		Hostname: addr.IP.String(),
		Port:     addr.Port,
	}

	goroutinesBefore := runtime.NumGoroutine()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := client.OutputContext(ctx, "exit 0")
	elapsed := time.Since(started)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("OutputContext returned after %s, want shortly after the 300ms deadline", elapsed)
	}

	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("connection to the unresponsive server was not closed")
	}

	// Allow the tarpit goroutine and any ssh internals to wind down.
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > goroutinesBefore && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > goroutinesBefore {
		t.Fatalf("goroutines: %d before, %d after cancellation", goroutinesBefore, after)
	}
}

func TestNativeClientOutputContextDialsOnce(t *testing.T) {
	t.Parallel()

	// Reserve a port, then close it so connections are refused.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().(*net.TCPAddr)
	l.Close()

	client := &NativeClient{
		Config:   ssh.ClientConfig{User: "user", HostKeyCallback: ssh.InsecureIgnoreHostKey()},
		Hostname: addr.IP.String(),
		Port:     addr.Port,
	}

	// Output would spend minutes in mcnutils.WaitFor; OutputContext must
	// return the first dial error for the caller's loop to handle.
	started := time.Now()
	_, err = client.OutputContext(context.Background(), "exit 0")
	if err == nil {
		t.Fatal("expected a dial error")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("OutputContext took %s on a refused port; it should not retry", elapsed)
	}
}

// serveExecExit0 runs a minimal SSH server that accepts any client and
// answers every exec request with exit status 0.
func serveExecExit0(t *testing.T) *net.TCPAddr {
	t.Helper()

	_, hostKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(signer)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, chans, reqs, err := ssh.NewServerConn(conn, config)
		if err != nil {
			return
		}
		go ssh.DiscardRequests(reqs)
		for newCh := range chans {
			ch, chReqs, err := newCh.Accept()
			if err != nil {
				return
			}
			for req := range chReqs {
				if req.Type != "exec" {
					_ = req.Reply(false, nil)
					continue
				}
				_ = req.Reply(true, nil)
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
				ch.Close()
				break
			}
		}
	}()

	return l.Addr().(*net.TCPAddr)
}

func TestNativeClientOutputContextSuccess(t *testing.T) {
	t.Parallel()

	addr := serveExecExit0(t)
	client := &NativeClient{
		Config:   ssh.ClientConfig{User: "user", HostKeyCallback: ssh.InsecureIgnoreHostKey()},
		Hostname: addr.IP.String(),
		Port:     addr.Port,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := client.OutputContext(ctx, "exit 0"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
