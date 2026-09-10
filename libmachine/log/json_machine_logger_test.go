package log

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestJSONLogger() (*JSONMachineLogger, *bytes.Buffer, *bytes.Buffer) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	l := NewJSONMachineLogger().(*JSONMachineLogger)
	l.SetOutWriter(out)
	l.SetErrWriter(errOut)
	l.now = func() time.Time { return time.Date(2026, 9, 8, 14, 38, 52, 864000000, time.UTC) }
	return l, out, errOut
}

func decodeLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var entry map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry), "raw: %q", buf.String())
	return entry
}

func TestJSONLoggerLevelsAndStreams(t *testing.T) {
	tests := []struct {
		name   string
		log    func(l MachineLogger)
		level  string
		msg    string
		stderr bool
	}{
		{"info", func(l MachineLogger) { l.Info("Waiting for", "SSH") }, "info", "Waiting for SSH", false},
		{"infof", func(l MachineLogger) { l.Infof("placed in %s", "us-east1-d") }, "info", "placed in us-east1-d", false},
		{"warn", func(l MachineLogger) { l.Warn("careful") }, "warn", "careful", false},
		{"warnf", func(l MachineLogger) { l.Warnf("attempt %d", 2) }, "warn", "attempt 2", false},
		{"error", func(l MachineLogger) { l.Error("boom") }, "error", "boom", true},
		{"errorf", func(l MachineLogger) { l.Errorf("boom %d", 1) }, "error", "boom 1", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, out, errOut := newTestJSONLogger()
			tc.log(l)

			buf := out
			if tc.stderr {
				assert.Empty(t, out.String())
				buf = errOut
			} else {
				assert.Empty(t, errOut.String())
			}

			entry := decodeLine(t, buf)
			assert.Equal(t, tc.level, entry["level"])
			assert.Equal(t, tc.msg, entry["msg"])
			assert.Equal(t, "2026-09-08T14:38:52.864Z", entry["time"])
			assert.True(t, bytes.HasSuffix(buf.Bytes(), []byte("\n")))
		})
	}
}

func TestJSONLoggerDebugOnlyWhenEnabled(t *testing.T) {
	l, out, errOut := newTestJSONLogger()

	l.Debug("hidden")
	assert.Empty(t, out.String())
	assert.Empty(t, errOut.String())

	l.SetDebug(true)
	l.Debugf("shown %d", 1)
	entry := decodeLine(t, errOut)
	assert.Equal(t, "debug", entry["level"])
	assert.Equal(t, "shown 1", entry["msg"])
}

func TestJSONLoggerFields(t *testing.T) {
	l, out, _ := newTestJSONLogger()

	l.Info("before")
	_, hasPhase := decodeLine(t, out)["phase"]
	assert.False(t, hasPhase)

	out.Reset()
	l.WithFields(Fields{"phase": "wait_ssh", "attempt": 2}).Infof("during %d", 1)
	entry := decodeLine(t, out)
	assert.Equal(t, "wait_ssh", entry["phase"])
	assert.Equal(t, float64(2), entry["attempt"])
	assert.Equal(t, "during 1", entry["msg"])

	out.Reset()
	l.Warn("after")
	_, hasPhase = decodeLine(t, out)["phase"]
	assert.False(t, hasPhase, "fields do not leak back into the parent logger")

	out.Reset()
	l.WithFields(Fields{"msg": "override"}).Info("real")
	assert.Equal(t, "real", decodeLine(t, out)["msg"], "reserved keys win over fields")
}

func TestJSONLoggerHistoryIncludesSuppressedDebug(t *testing.T) {
	l, _, _ := newTestJSONLogger()
	l.Debug("debug")
	l.Info("info")
	l.Error("error")
	assert.Equal(t, []string{"debug", "info", "error"}, l.History())
}

func TestSetFormat(t *testing.T) {
	defer func() { logger = NewFmtMachineLogger() }()

	require.NoError(t, SetFormat(FormatJSON))
	_, ok := logger.(*JSONMachineLogger)
	require.True(t, ok)

	require.NoError(t, SetFormat(""))
	_, ok = logger.(*FmtMachineLogger)
	assert.True(t, ok)

	assert.Error(t, SetFormat("yaml"))
}

func TestHistoryIncludesFields(t *testing.T) {
	for name, l := range map[string]MachineLogger{"json": NewJSONMachineLogger(), "text": NewFmtMachineLogger()} {
		t.Run(name, func(t *testing.T) {
			l.SetOutWriter(io.Discard)
			l.SetErrWriter(io.Discard)
			l.WithFields(Fields{"zone": "us-east1-c", "attempt": 2}).Info("placed")
			l.Debug("hidden but recorded")
			assert.Equal(t, []string{"placed attempt=2 zone=us-east1-c", "hidden but recorded"}, l.History())
		})
	}
}

func TestParseEntry(t *testing.T) {
	level, msg, fields, ok := ParseEntry(`{"time":"2026-09-08T14:38:50Z","level":"warn","msg":"placed","zone":"us-east1-c"}`)
	require.True(t, ok)
	assert.Equal(t, "warn", level)
	assert.Equal(t, "placed", msg)
	assert.Equal(t, Fields{"zone": "us-east1-c"}, fields)

	for _, line := range []string{"", "plain text", `{"level":"info"}`, `{"msg":`} {
		_, _, _, ok := ParseEntry(line)
		assert.False(t, ok, line)
	}
}

func TestFormat(t *testing.T) {
	defer func() { require.NoError(t, SetFormat(FormatText)) }()

	assert.Equal(t, FormatText, Format())
	require.NoError(t, SetFormat(FormatJSON))
	assert.Equal(t, FormatJSON, Format())
	assert.Error(t, SetFormat("yaml"))
	assert.Equal(t, FormatJSON, Format(), "unchanged after a rejected format")
}
