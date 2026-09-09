package log

import (
	"bytes"
	"encoding/json"
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

func decodeLine(t *testing.T, buf *bytes.Buffer) map[string]interface{} {
	t.Helper()
	var entry map[string]interface{}
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

func TestJSONLoggerHistoryIncludesSuppressedDebug(t *testing.T) {
	l, _, _ := newTestJSONLogger()
	l.Debug("debug")
	l.Info("info")
	l.Error("error")
	assert.Equal(t, []string{"debug", "info", "error"}, l.History())
}

func TestSetFormat(t *testing.T) {
	defer func() {
		logger = NewFmtMachineLogger()
		debug = false
	}()

	SetDebug(true)

	require.NoError(t, SetFormat(FormatJSON))
	jl, ok := logger.(*JSONMachineLogger)
	require.True(t, ok)
	assert.True(t, jl.debug, "debug setting survives the logger swap")

	require.NoError(t, SetFormat(""))
	_, ok = logger.(*FmtMachineLogger)
	assert.True(t, ok)

	assert.Error(t, SetFormat("yaml"))
}
