package localbinary

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/docker/machine/libmachine/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type FakeExecutor struct {
	stdout, stderr io.ReadCloser
	closed         bool
}

func (fe *FakeExecutor) Start() (*bufio.Scanner, *bufio.Scanner, error) {
	return bufio.NewScanner(fe.stdout), bufio.NewScanner(fe.stderr), nil
}

func (fe *FakeExecutor) Close() error {
	fe.closed = true
	return nil
}

func TestLocalBinaryPluginAddress(t *testing.T) {
	lbp := &Plugin{}
	expectedAddr := "127.0.0.1:12345"

	lbp.addrCh = make(chan string, 1)
	lbp.addrCh <- expectedAddr

	// Call the first time to read from the channel
	addr, err := lbp.Address()
	if err != nil {
		t.Fatalf("Expected no error, instead got %s", err)
	}
	if addr != expectedAddr {
		t.Fatal("Expected did not match actual address")
	}

	// Call the second time to read the "cached" address value
	addr, err = lbp.Address()
	if err != nil {
		t.Fatalf("Expected no error, instead got %s", err)
	}
	if addr != expectedAddr {
		t.Fatal("Expected did not match actual address")
	}
}

func TestLocalBinaryPluginAddressTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping timeout test")
	}

	lbp := &Plugin{
		addrCh:  make(chan string, 1),
		timeout: 1 * time.Second,
	}

	addr, err := lbp.Address()

	assert.Empty(t, addr)
	assert.EqualError(t, err, "Failed to dial the plugin server in 1s")
}

func TestLocalBinaryPluginClose(t *testing.T) {
	lbp := &Plugin{}
	lbp.stopCh = make(chan struct{})
	go lbp.Close()
	_, isOpen := <-lbp.stopCh
	if isOpen {
		t.Fatal("Close did not send a stop message on the proper channel")
	}
}

func TestExecServer(t *testing.T) {
	logOutReader, logOutWriter := io.Pipe()
	logErrReader, logErrWriter := io.Pipe()

	log.SetDebug(true)
	log.SetOutWriter(logOutWriter)
	log.SetErrWriter(logErrWriter)

	defer func() {
		log.SetDebug(false)
		log.SetOutWriter(os.Stdout)
		log.SetErrWriter(os.Stderr)
	}()

	stdoutReader, stdoutWriter := io.Pipe()
	stderrReader, stderrWriter := io.Pipe()

	fe := &FakeExecutor{
		stdout: stdoutReader,
		stderr: stderrReader,
	}

	machineName := "test"
	lbp := &Plugin{
		MachineName: machineName,
		Executor:    fe,
		addrCh:      make(chan string, 1),
		stopCh:      make(chan struct{}),
	}

	finalErr := make(chan error)

	// Start the docker-machine-foo plugin server
	go func() {
		finalErr <- lbp.execServer()
	}()

	logOutScanner := bufio.NewScanner(logOutReader)
	logErrScanner := bufio.NewScanner(logErrReader)

	// Write the ip address
	expectedAddr := "127.0.0.1:12345"
	if _, err := io.WriteString(stdoutWriter, expectedAddr+"\n"); err != nil {
		t.Fatalf("Error attempting to write plugin address: %s", err)
	}

	if addr := <-lbp.addrCh; addr != expectedAddr {
		t.Fatalf("Expected to read the expected address properly in server but did not")
	}

	// Write a log in stdout
	expectedPluginOut := "Doing some fun plugin stuff..."
	if _, err := io.WriteString(stdoutWriter, expectedPluginOut+"\n"); err != nil {
		t.Fatalf("Error attempting to write to out in plugin: %s", err)
	}

	expectedOut := fmt.Sprintf(pluginOut, machineName, expectedPluginOut)
	if logOutScanner.Scan(); logOutScanner.Text() != expectedOut {
		t.Fatalf("Output written to log was not what we expected\nexpected: %s\nactual:   %s", expectedOut, logOutScanner.Text())
	}

	// Write a log in stderr
	expectedPluginErr := "Uh oh, something in plugin went wrong..."
	if _, err := io.WriteString(stderrWriter, expectedPluginErr+"\n"); err != nil {
		t.Fatalf("Error attempting to write to err in plugin: %s", err)
	}

	expectedErr := fmt.Sprintf(pluginErr, machineName, expectedPluginErr)
	if logErrScanner.Scan(); logErrScanner.Text() != expectedErr {
		t.Fatalf("Error written to log was not what we expected\nexpected: %s\nactual:   %s", expectedErr, logErrScanner.Text())
	}

	lbp.Close()

	if err := <-finalErr; err != nil {
		t.Fatalf("Error serving: %s", err)
	}
}

func TestRelay(t *testing.T) {
	lbp := &Plugin{MachineName: "m1"}

	capture := func(format string, debug bool, fn func()) (string, string) {
		require.NoError(t, log.SetFormat(format))
		defer func() { require.NoError(t, log.SetFormat(log.FormatText)) }()
		var out, errOut bytes.Buffer
		log.SetDebug(debug)
		log.SetOutWriter(&out)
		log.SetErrWriter(&errOut)
		fn()
		return out.String(), errOut.String()
	}

	t.Run("text mode keeps the prefix", func(t *testing.T) {
		out, _ := capture(log.FormatText, false, func() { lbp.relay("bulkInsert placed zone=us-east1-c", false) })
		assert.Equal(t, "(m1) bulkInsert placed zone=us-east1-c\n", out)
	})

	t.Run("json mode re-attaches fields", func(t *testing.T) {
		out, _ := capture(log.FormatJSON, false, func() {
			lbp.relay(`{"time":"2026-09-08T14:38:50Z","level":"warn","msg":"bulkInsert placed","zone":"us-east1-c"}`, false)
		})
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &entry))
		assert.Equal(t, "bulkInsert placed", entry["msg"])
		assert.Equal(t, "warn", entry["level"])
		assert.Equal(t, "us-east1-c", entry["zone"])
		assert.Equal(t, "m1", entry["machine"])
		assert.NotEqual(t, "2026-09-08T14:38:50Z", entry["time"], "parent re-stamps")
	})

	t.Run("json mode falls back for plain lines", func(t *testing.T) {
		out, _ := capture(log.FormatJSON, false, func() { lbp.relay("panic: boom", false) })
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &entry))
		assert.Equal(t, "(m1) panic: boom", entry["msg"])
	})

	t.Run("stderr stays at debug", func(t *testing.T) {
		_, errOut := capture(log.FormatJSON, true, func() {
			lbp.relay(`{"level":"error","msg":"bad"}`, true)
		})
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(errOut), &entry))
		assert.Equal(t, "debug", entry["level"])
		assert.Equal(t, "bad", entry["msg"])
	})
}
