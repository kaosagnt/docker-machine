package log

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

type Fields map[string]any

type MachineLogger interface {
	SetDebug(debug bool)

	SetOutWriter(io.Writer)
	SetErrWriter(io.Writer)

	WithFields(fields Fields) MachineLogger

	Debug(args ...any)
	Debugf(fmtString string, args ...any)

	Error(args ...any)
	Errorf(fmtString string, args ...any)

	Info(args ...any)
	Infof(fmtString string, args ...any)

	Warn(args ...any)
	Warnf(fmtString string, args ...any)

	History() []string
}

func mergeFields(base, extra Fields) Fields {
	out := make(Fields, len(base)+len(extra))
	maps.Copy(out, base)
	maps.Copy(out, extra)
	return out
}

// renderFields appends "key=value" pairs in key order.
func renderFields(msg string, fields Fields) string {
	if len(fields) == 0 {
		return msg
	}
	var b strings.Builder
	b.WriteString(msg)
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		fmt.Fprintf(&b, " %s=%v", k, fields[k])
	}
	return b.String()
}

// sprint matches fmt.Fprintln's spacing.
func sprint(args ...any) string {
	return strings.TrimSuffix(fmt.Sprintln(args...), "\n")
}
