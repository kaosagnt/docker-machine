package log

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

type FmtMachineLogger struct {
	outWriter io.Writer
	errWriter io.Writer
	debug     bool
	history   *HistoryRecorder
	fields    Fields
}

// NewFmtMachineLogger creates a MachineLogger implementation used by the drivers
func NewFmtMachineLogger() MachineLogger {
	return &FmtMachineLogger{
		outWriter: os.Stdout,
		errWriter: os.Stderr,
		debug:     false,
		history:   NewHistoryRecorder(),
	}
}

func (ml *FmtMachineLogger) SetDebug(debug bool) {
	ml.debug = debug
}

func (ml *FmtMachineLogger) SetOutWriter(out io.Writer) {
	ml.outWriter = out
}

func (ml *FmtMachineLogger) SetErrWriter(err io.Writer) {
	ml.errWriter = err
}

func (ml *FmtMachineLogger) WithFields(fields Fields) MachineLogger {
	clone := *ml
	clone.fields = mergeFields(ml.fields, fields)
	return &clone
}

func (ml *FmtMachineLogger) format(msg string) string {
	if len(ml.fields) == 0 {
		return msg
	}
	keys := make([]string, 0, len(ml.fields))
	for k := range ml.fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString(msg)
	for _, k := range keys {
		fmt.Fprintf(&b, " %s=%v", k, ml.fields[k])
	}
	return b.String()
}

func (ml *FmtMachineLogger) print(w io.Writer, msg string) {
	fmt.Fprintln(w, ml.format(msg))
}

func (ml *FmtMachineLogger) Debug(args ...interface{}) {
	ml.history.Record(args...)
	if ml.debug {
		ml.print(ml.errWriter, sprint(args...))
	}
}

func (ml *FmtMachineLogger) Debugf(fmtString string, args ...interface{}) {
	ml.history.Recordf(fmtString, args...)
	if ml.debug {
		ml.print(ml.errWriter, fmt.Sprintf(fmtString, args...))
	}
}

func (ml *FmtMachineLogger) Error(args ...interface{}) {
	ml.history.Record(args...)
	ml.print(ml.errWriter, sprint(args...))
}

func (ml *FmtMachineLogger) Errorf(fmtString string, args ...interface{}) {
	ml.history.Recordf(fmtString, args...)
	ml.print(ml.errWriter, fmt.Sprintf(fmtString, args...))
}

func (ml *FmtMachineLogger) Info(args ...interface{}) {
	ml.history.Record(args...)
	ml.print(ml.outWriter, sprint(args...))
}

func (ml *FmtMachineLogger) Infof(fmtString string, args ...interface{}) {
	ml.history.Recordf(fmtString, args...)
	ml.print(ml.outWriter, fmt.Sprintf(fmtString, args...))
}

func (ml *FmtMachineLogger) Warn(args ...interface{}) {
	ml.history.Record(args...)
	ml.print(ml.outWriter, sprint(args...))
}

func (ml *FmtMachineLogger) Warnf(fmtString string, args ...interface{}) {
	ml.history.Recordf(fmtString, args...)
	ml.print(ml.outWriter, fmt.Sprintf(fmtString, args...))
}

func (ml *FmtMachineLogger) History() []string {
	return ml.history.records
}
