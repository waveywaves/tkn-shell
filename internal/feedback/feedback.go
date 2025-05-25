package feedback

import (
	"fmt"
	"os"
)

// Infof prints an informational message to stdout.
func Infof(format string, args ...interface{}) {
	fmt.Fprintf(os.Stdout, format+"\n", args...)
}

// Errorf prints an error message to stderr.
// It automatically prepends "Error: " to the message.
func Errorf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
}
