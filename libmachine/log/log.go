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

func Debug(args ...any) {
	logger.Debug(args...)
}

func Debugf(fmtString string, args ...any) {
	logger.Debugf(fmtString, args...)
}

func Error(args ...any) {
	logger.Error(args...)
}

func Errorf(fmtString string, args ...any) {
	logger.Errorf(fmtString, args...)
}

func Info(args ...any) {
	logger.Info(args...)
}

func Infof(fmtString string, args ...any) {
	logger.Infof(fmtString, args...)
}

func Warn(args ...any) {
	logger.Warn(args...)
}

func Warnf(fmtString string, args ...any) {
	logger.Warnf(fmtString, args...)
}

func SetDebug(enabled bool) {
	debug = enabled
	logger.SetDebug(enabled)
}

// SetFormat replaces the package logger; history logged before the call is dropped.
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

func WithField(key string, value any) MachineLogger {
	return logger.WithFields(Fields{key: value})
}

func WithFields(fields Fields) MachineLogger {
	return logger.WithFields(fields)
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
