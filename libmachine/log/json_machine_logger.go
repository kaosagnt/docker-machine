package log

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
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

func (ml *JSONMachineLogger) emit(w io.Writer, level, msg string) {
	entry := make(map[string]interface{}, len(ml.fields)+3)
	for k, v := range ml.fields {
		entry[k] = v
	}
	entry["time"] = ml.now().UTC().Format(time.RFC3339Nano)
	entry["level"] = level
	entry["msg"] = msg

	data, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(ml.errWriter, "marshalling log entry: %s\n", err)
		return
	}

	// Single Write so lines from concurrent goroutines don't interleave.
	w.Write(append(data, '\n'))
}

func sprint(args ...interface{}) string {
	return strings.TrimSuffix(fmt.Sprintln(args...), "\n")
}

func (ml *JSONMachineLogger) Debug(args ...interface{}) {
	ml.history.Record(args...)
	if ml.debug {
		ml.emit(ml.errWriter, "debug", sprint(args...))
	}
}

func (ml *JSONMachineLogger) Debugf(fmtString string, args ...interface{}) {
	ml.history.Recordf(fmtString, args...)
	if ml.debug {
		ml.emit(ml.errWriter, "debug", fmt.Sprintf(fmtString, args...))
	}
}

func (ml *JSONMachineLogger) Error(args ...interface{}) {
	ml.history.Record(args...)
	ml.emit(ml.errWriter, "error", sprint(args...))
}

func (ml *JSONMachineLogger) Errorf(fmtString string, args ...interface{}) {
	ml.history.Recordf(fmtString, args...)
	ml.emit(ml.errWriter, "error", fmt.Sprintf(fmtString, args...))
}

func (ml *JSONMachineLogger) Info(args ...interface{}) {
	ml.history.Record(args...)
	ml.emit(ml.outWriter, "info", sprint(args...))
}

func (ml *JSONMachineLogger) Infof(fmtString string, args ...interface{}) {
	ml.history.Recordf(fmtString, args...)
	ml.emit(ml.outWriter, "info", fmt.Sprintf(fmtString, args...))
}

func (ml *JSONMachineLogger) Warn(args ...interface{}) {
	ml.history.Record(args...)
	ml.emit(ml.outWriter, "warn", sprint(args...))
}

func (ml *JSONMachineLogger) Warnf(fmtString string, args ...interface{}) {
	ml.history.Recordf(fmtString, args...)
	ml.emit(ml.outWriter, "warn", fmt.Sprintf(fmtString, args...))
}

func (ml *JSONMachineLogger) History() []string {
	return ml.history.records
}
