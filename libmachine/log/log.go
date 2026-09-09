package log

import (
	"fmt"
	"io"
	"regexp"
)

const redactedText = "<REDACTED>"

const (
	FormatText = "text"
	FormatJSON = "json"
)

var (
	logger = NewFmtMachineLogger()
	debug  bool

	// (?s) enables '.' to match '\n' -- see https://golang.org/pkg/regexp/syntax/
	certRegex = regexp.MustCompile("(?s)-----BEGIN CERTIFICATE-----.*-----END CERTIFICATE-----")
	keyRegex  = regexp.MustCompile("(?s)-----BEGIN RSA PRIVATE KEY-----.*-----END RSA PRIVATE KEY-----")
)

func stripSecrets(original []string) []string {
	stripped := []string{}
	for _, line := range original {
		line = certRegex.ReplaceAllString(line, redactedText)
		line = keyRegex.ReplaceAllString(line, redactedText)
		stripped = append(stripped, line)
	}
	return stripped
}

func Debug(args ...interface{}) {
	logger.Debug(args...)
}

func Debugf(fmtString string, args ...interface{}) {
	logger.Debugf(fmtString, args...)
}

func Error(args ...interface{}) {
	logger.Error(args...)
}

func Errorf(fmtString string, args ...interface{}) {
	logger.Errorf(fmtString, args...)
}

func Info(args ...interface{}) {
	logger.Info(args...)
}

func Infof(fmtString string, args ...interface{}) {
	logger.Infof(fmtString, args...)
}

func Warn(args ...interface{}) {
	logger.Warn(args...)
}

func Warnf(fmtString string, args ...interface{}) {
	logger.Warnf(fmtString, args...)
}

func SetDebug(enabled bool) {
	debug = enabled
	logger.SetDebug(enabled)
}

// SetFormat replaces the package logger. Call it before anything is logged;
// history recorded by the previous logger is not carried over.
func SetFormat(format string) error {
	switch format {
	case "", FormatText:
		logger = NewFmtMachineLogger()
	case FormatJSON:
		logger = NewJSONMachineLogger()
	default:
		return fmt.Errorf("unknown log format %q (expected %q or %q)", format, FormatText, FormatJSON)
	}
	logger.SetDebug(debug)
	return nil
}

// SetPhase tags subsequent log entries with the create phase they belong
// to. Consumers derive phase durations from the timestamps of the first
// entry of consecutive phases. Rendered by the JSON format only.
func SetPhase(phase string) {
	logger.SetPhase(phase)
}

func SetOutWriter(out io.Writer) {
	logger.SetOutWriter(out)
}

func SetErrWriter(err io.Writer) {
	logger.SetErrWriter(err)
}

func History() []string {
	return stripSecrets(logger.History())
}
