package log

import (
	"fmt"
	"io"
	"os"
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

func (ml *FmtMachineLogger) log(w io.Writer, msg string) {
	line := renderFields(msg, ml.fields)
	ml.history.Record(line)
	if w != nil {
		fmt.Fprintln(w, line)
	}
}

func (ml *FmtMachineLogger) debugWriter() io.Writer {
	if ml.debug {
		return ml.errWriter
	}
	return nil
}

func (ml *FmtMachineLogger) Debug(args ...any) {
	ml.log(ml.debugWriter(), sprint(args...))
}

func (ml *FmtMachineLogger) Debugf(fmtString string, args ...any) {
	ml.log(ml.debugWriter(), fmt.Sprintf(fmtString, args...))
}

func (ml *FmtMachineLogger) Error(args ...any) {
	ml.log(ml.errWriter, sprint(args...))
}

func (ml *FmtMachineLogger) Errorf(fmtString string, args ...any) {
	ml.log(ml.errWriter, fmt.Sprintf(fmtString, args...))
}

func (ml *FmtMachineLogger) Info(args ...any) {
	ml.log(ml.outWriter, sprint(args...))
}

func (ml *FmtMachineLogger) Infof(fmtString string, args ...any) {
	ml.log(ml.outWriter, fmt.Sprintf(fmtString, args...))
}

func (ml *FmtMachineLogger) Warn(args ...any) {
	ml.log(ml.outWriter, sprint(args...))
}

func (ml *FmtMachineLogger) Warnf(fmtString string, args ...any) {
	ml.log(ml.outWriter, fmt.Sprintf(fmtString, args...))
}

func (ml *FmtMachineLogger) History() []string {
	return ml.history.records
}
