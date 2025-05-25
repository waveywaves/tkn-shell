package repl

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"tkn-shell/internal/feedback"
	"tkn-shell/internal/state"
)

// TestREPL_Integration_ExportAllScenario simulates a sequence of commands through the REPL executor
// and checks the final output of 'export all'.
func TestREPL_Integration_ExportAllScenario(t *testing.T) {
	// Store original feedback streams and defer their restoration
	originalOutStream := feedback.GetOutputStream()
	originalErrStream := feedback.GetErrorStream()
	defer feedback.SetOutputStream(originalOutStream)
	defer feedback.SetErrorStream(originalErrStream)

	// Capture stdout
	var outBuf bytes.Buffer
	feedback.SetOutputStream(&outBuf)
	// Discard stderr for this test, or capture it if specific error messages are expected
	feedback.SetErrorStream(io.Discard)

	// Initialize the REPL's global session (as done in Run())
	sess = state.NewSession()

	commands := []string{
		"pipeline create ci",
		"task create build | step add compile --image alpine",
		"export all",
	}

	for _, cmd := range commands {
		// Reset buffer before each command if we only want to capture 'export all'
		// For this test, we want to accumulate output from 'export all' specifically.
		// Commands like 'pipeline create' or 'task create' also produce feedback.Infof output.
		// If we only wanted the export, we'd reset outBuf before "export all".
		// However, the engine's 'export all' command itself prints directly via feedback.Infof.
		// So, clearing the buffer just before "export all" is correct if we want *only* its output.

		if cmd == "export all" {
			outBuf.Reset() // Clear any output from previous setup commands
		}
		executor(cmd) // Use the actual executor from the repl package
	}

	output := outBuf.String()

	// feedback.Infof adds a newline, so check for content within lines
	expectedSubstrings := []string{
		"kind: Pipeline",
		"name: ci",
		"kind: Task",
		"name: build",
		"name: compile",
		"image: alpine",
	}

	for _, sub := range expectedSubstrings {
		if !strings.Contains(output, sub) {
			t.Errorf("Output missing expected substring '%s'.\nFull output:\n%s", sub, output)
		}
	}

	// Additionally, check that the 'export all' output is somewhat structured as YAML
	// (basic check, not full YAML parse)
	if !strings.Contains(output, "apiVersion: tekton.dev/v1") {
		t.Errorf("Output does not seem to contain Tekton APIVersion.\nFull output:\n%s", output)
	}

}
