package log

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// JSONMachineLogger writes one JSON object per line. Info and Warn go to
// the out writer, Error and Debug to the err writer, matching
// FmtMachineLogger so callers that redirect one stream keep working.
type JSONMachineLogger struct {
	outWriter io.Writer
	errWriter io.Writer
	debug     bool
	history   *HistoryRecorder
	now       func() time.Time

	// phase is set from the create flow and read from the plugin output
	// relay goroutine.
	phaseMu sync.RWMutex
	phase   string
}

type jsonLogEntry struct {
	Time  string `json:"time"`
	Level string `json:"level"`
	Msg   string `json:"msg"`
	Phase string `json:"phase,omitempty"`
}

// NewJSONMachineLogger creates a MachineLogger that emits JSON lines.
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

func (ml *JSONMachineLogger) SetPhase(phase string) {
	ml.phaseMu.Lock()
	ml.phase = phase
	ml.phaseMu.Unlock()
}

func (ml *JSONMachineLogger) emit(w io.Writer, level, msg string) {
	ml.phaseMu.RLock()
	phase := ml.phase
	ml.phaseMu.RUnlock()

	entry := jsonLogEntry{
		Time:  ml.now().UTC().Format(time.RFC3339Nano),
		Level: level,
		Msg:   msg,
		Phase: phase,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(ml.errWriter, "marshalling log entry: %s\n", err)
		return
	}

	// One Write per line so concurrent loggers (plugin output relay) do not
	// interleave within a line.
	w.Write(append(data, '\n'))
}

// sprint mirrors fmt.Fprintln, which FmtMachineLogger uses: operands are
// space separated.
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
