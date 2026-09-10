package log

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"time"
)

type JSONMachineLogger struct {
	outWriter io.Writer
	errWriter io.Writer
	debug     bool
	history   *HistoryRecorder
	now       func() time.Time
	fields    Fields
}

func NewJSONMachineLogger() MachineLogger {
	return &JSONMachineLogger{
		outWriter: os.Stdout,
		errWriter: os.Stderr,
		history:   NewHistoryRecorder(),
		now:       time.Now,
	}
}

func (ml *JSONMachineLogger) SetDebug(debug bool) {
	ml.debug = debug
}

func (ml *JSONMachineLogger) SetOutWriter(out io.Writer) {
	ml.outWriter = out
}

func (ml *JSONMachineLogger) SetErrWriter(err io.Writer) {
	ml.errWriter = err
}

func (ml *JSONMachineLogger) WithFields(fields Fields) MachineLogger {
	clone := *ml
	clone.fields = mergeFields(ml.fields, fields)
	return &clone
}

func (ml *JSONMachineLogger) log(w io.Writer, level, msg string) {
	entry := make(map[string]any, len(ml.fields)+3)
	maps.Copy(entry, ml.fields)
	entry["time"] = ml.now().UTC().Format(time.RFC3339Nano)
	entry["level"] = level
	entry["msg"] = msg

	data, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(ml.errWriter, "marshalling log entry: %s\n", err)
		return
	}

	ml.history.Record(string(data))
	if w != nil {
		// Single Write so lines from concurrent goroutines don't interleave.
		w.Write(append(data, '\n'))
	}
}

func (ml *JSONMachineLogger) debugWriter() io.Writer {
	if ml.debug {
		return ml.errWriter
	}
	return nil
}

func (ml *JSONMachineLogger) Debug(args ...any) {
	ml.log(ml.debugWriter(), "debug", sprint(args...))
}

func (ml *JSONMachineLogger) Debugf(fmtString string, args ...any) {
	ml.log(ml.debugWriter(), "debug", fmt.Sprintf(fmtString, args...))
}

func (ml *JSONMachineLogger) Error(args ...any) {
	ml.log(ml.errWriter, "error", sprint(args...))
}

func (ml *JSONMachineLogger) Errorf(fmtString string, args ...any) {
	ml.log(ml.errWriter, "error", fmt.Sprintf(fmtString, args...))
}

func (ml *JSONMachineLogger) Info(args ...any) {
	ml.log(ml.outWriter, "info", sprint(args...))
}

func (ml *JSONMachineLogger) Infof(fmtString string, args ...any) {
	ml.log(ml.outWriter, "info", fmt.Sprintf(fmtString, args...))
}

func (ml *JSONMachineLogger) Warn(args ...any) {
	ml.log(ml.outWriter, "warn", sprint(args...))
}

func (ml *JSONMachineLogger) Warnf(fmtString string, args ...any) {
	ml.log(ml.outWriter, "warn", fmt.Sprintf(fmtString, args...))
}

func (ml *JSONMachineLogger) History() []string {
	return ml.history.records
}

// ParseEntry decodes a line written by JSONMachineLogger. ok is false for
// anything else. time and level are stripped from the returned fields.
func ParseEntry(line string) (level, msg string, fields Fields, ok bool) {
	if len(line) == 0 || line[0] != '{' {
		return "", "", nil, false
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return "", "", nil, false
	}
	msg, ok = raw["msg"].(string)
	if !ok {
		return "", "", nil, false
	}
	level, _ = raw["level"].(string)
	delete(raw, "msg")
	delete(raw, "level")
	delete(raw, "time")
	return level, msg, Fields(raw), true
}
