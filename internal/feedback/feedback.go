package feedback

import (
	"fmt"
	"io"
	"os"
)

var (
	outputStream io.Writer = os.Stdout
	errorStream  io.Writer = os.Stderr
	// testingLogger interface{ Logf(format string, args ...interface{}) } // To be set by tests
)

// // SetTestingLogger allows tests to inject a logger.
// func SetTestingLogger(logger interface{ Logf(format string, args ...interface{}) }) {
// 	testingLogger = logger
// }

// SetOutputStream sets the stream for informational messages.
func SetOutputStream(w io.Writer) {
	outputStream = w
}

// GetOutputStream returns the current stream for informational messages.
func GetOutputStream() io.Writer {
	return outputStream
}

// SetErrorStream sets the stream for error messages.
func SetErrorStream(w io.Writer) {
	errorStream = w
}

// GetErrorStream returns the current stream for error messages.
func GetErrorStream() io.Writer {
	return errorStream
}

// Infof prints an informational message to the configured output stream.
func Infof(format string, args ...interface{}) {
	// if testingLogger != nil {
	// 	testingLogger.Logf("DEBUG_FEEDBACK: Infof called. outputStream is os.Stdout: %v. Format: %s", outputStream == os.Stdout, format)
	// }
	fmt.Fprintf(outputStream, format+"\n", args...)
}

// Errorf prints an error message to the configured error stream.
// It automatically prepends "Error: " to the message.
func Errorf(format string, args ...interface{}) {
	fmt.Fprintf(errorStream, "Error: "+format+"\n", args...)
}
