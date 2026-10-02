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
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// writeFakeSSH writes a stand-in ssh that records its PID, then runs body.
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

	// Cancel only once ssh is running.
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
		t.Fatalf("returned %s after cancellation", lag)
	}
	if pid := readPID(t, pidFile); processExists(pid) {
		t.Fatalf("ssh process %d is still running after cancellation", pid)
	}
}

func TestExternalClientOutputContextBoundsInheritedPipes(t *testing.T) {
	t.Parallel()

	// The background child keeps the output pipe open after ssh is killed.
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
		t.Fatalf("returned after %s", elapsed)
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
		t.Fatalf("err = %v", err)
	}
	var exitErr interface{ ExitCode() int }
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 255 {
		t.Fatalf("err = %v", err)
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
		t.Fatalf("BaseArgs spare capacity = %q", got)
	}
}

// tarpit accepts connections and never speaks SSH. closed fires when the
// client side closes.
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

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := client.OutputContext(ctx, "exit 0")
	elapsed := time.Since(started)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("returned after %s", elapsed)
	}

	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("connection not closed")
	}
}

func TestNativeClientOutputContextDialsOnce(t *testing.T) {
	t.Parallel()

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

	started := time.Now()
	_, err = client.OutputContext(context.Background(), "exit 0")
	if err == nil {
		t.Fatal("expected a dial error")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("took %s", elapsed)
	}
}

// serveExecExit0 answers every exec request with exit status 0.
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
