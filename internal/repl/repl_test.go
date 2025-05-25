package repl

import (
	"bytes"
	"strings"
	"testing"

	"tkn-shell/internal/export"
	"tkn-shell/internal/feedback"
	"tkn-shell/internal/state"
)

// TestREPL_Integration_ExportAllScenario simulates a sequence of commands through the REPL executor
// and checks the final output of 'export all'.
func TestREPL_Integration_ExportAllScenario(t *testing.T) {
	// Store original feedback streams and defer their restoration
	originalOutStream := feedback.GetOutputStream()
	originalErrStream := feedback.GetErrorStream()
	// feedback.SetTestingLogger(t) // Set the test logger
	defer func() {
		feedback.SetOutputStream(originalOutStream)
		feedback.SetErrorStream(originalErrStream)
		// feedback.SetTestingLogger(nil) // Clear the test logger
	}()

	// Capture stdout
	var outBuf bytes.Buffer
	var errBuf bytes.Buffer // Capture stderr
	feedback.SetOutputStream(&outBuf)
	feedback.SetErrorStream(&errBuf) // Capture errors instead of discarding

	sess = state.NewSession() // Initialize REPL's global session

	// Commands to make the pipeline valid for export
	executor("pipeline create test-p")

	// Add a description to make the PipelineSpec non-empty to pass validation
	p, ok := sess.Pipelines["test-p"]
	if !ok || p == nil {
		t.Fatal("Pipeline 'test-p' not found in session after creation for adding description.")
	}
	p.Spec.Description = "A test pipeline."

	// --- Debug: Check ExportAll directly ---
	debugExportBytes, debugExportErr := export.ExportAll(sess)
	if debugExportErr != nil {
		t.Logf("DEBUG: export.ExportAll directly returned error: %v", debugExportErr)
	}
	t.Logf("DEBUG: export.ExportAll directly returned bytes: %s", string(debugExportBytes))
	// --- End Debug ---

	outBuf.Reset()         // Clear any output from pipeline create command and its feedback
	executor("export all") // This should now pass validation and produce YAML

	output := outBuf.String()
	errOutput := errBuf.String()
	if errOutput != "" {
		t.Logf("Captured stderr during REPL test:\n%s", errOutput)
	}

	// feedback.Infof adds a newline, so check for content within lines
	expectedSubstrings := []string{
		"kind: Pipeline",
		"name: test-p",
		"description: A test pipeline.", // Check for the description
	}

	for _, sub := range expectedSubstrings {
		if !strings.Contains(output, sub) {
			t.Errorf("Output missing expected substring '%s'.\nFull output:\n%s", sub, output)
		}
	}

	if !strings.Contains(output, "apiVersion: tekton.dev/v1") {
		t.Errorf("Output does not seem to contain Tekton APIVersion.\nFull output:\n%s", output)
	}
}
