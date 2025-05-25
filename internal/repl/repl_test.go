package repl

import (
	"bytes"
	"strings"
	"testing"

	"tkn-shell/internal/engine"
	"tkn-shell/internal/export"
	"tkn-shell/internal/feedback"
	"tkn-shell/internal/parser"
	"tkn-shell/internal/state"

	"github.com/google/go-cmp/cmp"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

// executeHelper simulates the core logic of the REPL's executor
// without setting up the full go-prompt interface.
// It now directs feedback to the provided errorBuffer.
func executeHelper(input string, sess *state.Session, errorBuffer *bytes.Buffer) {
	input = strings.TrimSpace(input)
	if input == "" || strings.ToLower(input) == "exit" || strings.ToLower(input) == "quit" || strings.ToLower(input) == "help" {
		return
	}

	pipelineLine, err := parser.ParseLine(input)
	if err != nil {
		// Use feedback.Errorf which writes to the configured errorStream (our buffer)
		feedback.Errorf("Parsing command: %v", err)
		return
	}

	var prevResult interface{}
	var activeWhenClause *parser.WhenClause

	for _, cmdWrapper := range pipelineLine.Cmds {
		if cmdWrapper.When != nil {
			activeWhenClause = cmdWrapper.When
			continue
		}
		if cmdWrapper.Cmd != nil {
			result, execErr := engine.ExecuteCommand(cmdWrapper.Pos, cmdWrapper.Cmd, sess, prevResult, activeWhenClause)
			if execErr != nil {
				feedback.Errorf("%v", execErr) // This will write to errorBuffer
			}
			prevResult = result
			activeWhenClause = nil
		}
	}
}

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

func TestREPL_Integration_TranslatedExample(t *testing.T) {
	dslCommands := []string{
		"pipeline create example-pipeline",
		"task create print-bound-state",
		`param values="Was a prelaunch workspace provided? $(workspaces.prelaunch.bound)\nWas a launch workspace provided? $(workspaces.launch.bound)\n"`,
		"step add do-echo --image \"mirror.gcr.io/alpine\"  `echo $(params.values)`",
		"task create run-the-js",
		"step add do-run-js --image \"mirror.gcr.io/node:lts-alpine3.20\"  `#!/usr/bin/env sh\nif [ $(workspaces.prelaunch.bound) == \"true\" ] ; then\n  node \"$(workspaces.prelaunch.path)/init.js\"\nelse\n  echo \"Skipping prelaunch.\"\nfi\nif [ -f \"$(workspaces.launch.path)/main.js\" ] ; then\n  node \"$(workspaces.launch.path)/main.js\"\nelse\n  echo \"Error: missing main.js file in launch workspace!\"\n  exit 1\nfi`",
	}

	// Use a local session for this test to avoid interference with the global 'sess'
	localSess := state.NewSession()
	var errorBuf bytes.Buffer

	// Store original streams and defer restoration
	originalErrorStream := feedback.GetErrorStream()
	originalOutputStream := feedback.GetOutputStream()
	defer func() {
		feedback.SetErrorStream(originalErrorStream)
		feedback.SetOutputStream(originalOutputStream)
	}()

	// Set our buffers to capture feedback
	feedback.SetErrorStream(&errorBuf)
	var outputBuf bytes.Buffer // Capture Infof if needed, though test focuses on errors
	feedback.SetOutputStream(&outputBuf)

	for _, cmd := range dslCommands {
		// Use the local executeHelper, passing the local session
		executeHelper(cmd, localSess, &errorBuf)
	}

	// Assertions
	if errorBuf.Len() > 0 {
		t.Errorf("DSL execution produced errors: %s", errorBuf.String())
	}

	// 1. Check Pipeline
	pipeline, ok := localSess.Pipelines["example-pipeline"]
	if !ok {
		t.Fatalf("Pipeline 'example-pipeline' not found in session")
	}
	if pipeline.Name != "example-pipeline" {
		t.Errorf("Expected pipeline name 'example-pipeline', got '%s'", pipeline.Name)
	}
	if len(pipeline.Spec.Tasks) != 2 {
		t.Fatalf("Expected 2 tasks in pipeline 'example-pipeline', got %d. Tasks: %+v", len(pipeline.Spec.Tasks), pipeline.Spec.Tasks)
	}

	// 2. Check PipelineTasks order and reference
	expectedPipelineTaskNames := []string{"print-bound-state", "run-the-js"}
	for i, ptName := range expectedPipelineTaskNames {
		if i >= len(pipeline.Spec.Tasks) {
			t.Fatalf("Pipeline task index %d out of bounds (len %d)", i, len(pipeline.Spec.Tasks))
		}
		pipelineTask := pipeline.Spec.Tasks[i]
		if pipelineTask.Name != ptName {
			t.Errorf("Expected pipeline task %d to be '%s', got '%s'", i, ptName, pipelineTask.Name)
		}
		if pipelineTask.TaskRef == nil || pipelineTask.TaskRef.Name != ptName {
			t.Errorf("Expected pipeline task '%s' to have a TaskRef name '%s', got ref: %+v", ptName, ptName, pipelineTask.TaskRef)
		}
	}

	// 3. Check "print-bound-state" Task
	task1, ok := localSess.Tasks["print-bound-state"]
	if !ok {
		t.Fatalf("Task 'print-bound-state' not found in session")
	}
	// Check params
	if len(task1.Spec.Params) != 1 {
		t.Fatalf("Expected 1 param for task 'print-bound-state', got %d", len(task1.Spec.Params))
	}
	expectedParamSpec1 := tektonv1.ParamSpec{
		Name: "values",
		Type: tektonv1.ParamTypeString,
		Default: &tektonv1.ParamValue{
			Type:      tektonv1.ParamTypeString,
			StringVal: "Was a prelaunch workspace provided? $(workspaces.prelaunch.bound)\nWas a launch workspace provided? $(workspaces.launch.bound)\n",
		},
	}
	if diff := cmp.Diff(expectedParamSpec1, task1.Spec.Params[0]); diff != "" {
		t.Errorf("Task 'print-bound-state' ParamSpec diff (-want +got):\n%s", diff)
	}
	// Check steps
	if len(task1.Spec.Steps) != 1 {
		t.Fatalf("Expected 1 step for task 'print-bound-state', got %d", len(task1.Spec.Steps))
	}
	expectedStep1 := tektonv1.Step{
		Name:   "do-echo",
		Image:  "mirror.gcr.io/alpine",
		Script: "echo Was a prelaunch workspace provided? $(workspaces.prelaunch.bound)\nWas a launch workspace provided? $(workspaces.launch.bound)\n",
	}
	// Compare only relevant fields for the step, as other fields might be auto-populated
	if task1.Spec.Steps[0].Name != expectedStep1.Name || task1.Spec.Steps[0].Image != expectedStep1.Image || task1.Spec.Steps[0].Script != expectedStep1.Script {
		t.Errorf("Task 'print-bound-state' Step diff.\nWant: %+v\nGot:  %+v", expectedStep1, task1.Spec.Steps[0])
	}

	// 4. Check "run-the-js" Task
	task2, ok := localSess.Tasks["run-the-js"]
	if !ok {
		t.Fatalf("Task 'run-the-js' not found in session")
	}
	if len(task2.Spec.Params) != 0 {
		t.Errorf("Expected 0 params for task 'run-the-js', got %d", len(task2.Spec.Params))
	}
	if len(task2.Spec.Steps) != 1 {
		t.Fatalf("Expected 1 step for task 'run-the-js', got %d", len(task2.Spec.Steps))
	}
	expectedStep2Script := `#!/usr/bin/env sh
if [ $(workspaces.prelaunch.bound) == "true" ] ; then
  node "$(workspaces.prelaunch.path)/init.js"
else
  echo "Skipping prelaunch."
fi
if [ -f "$(workspaces.launch.path)/main.js" ] ; then
  node "$(workspaces.launch.path)/main.js"
else
  echo "Error: missing main.js file in launch workspace!"
  exit 1
fi`
	expectedStep2 := tektonv1.Step{
		Name:   "do-run-js",
		Image:  "mirror.gcr.io/node:lts-alpine3.20",
		Script: expectedStep2Script,
	}
	// Compare only relevant fields for the step
	if task2.Spec.Steps[0].Name != expectedStep2.Name || task2.Spec.Steps[0].Image != expectedStep2.Image || task2.Spec.Steps[0].Script != expectedStep2.Script {
		t.Errorf("Task 'run-the-js' Step diff.\nWant: %+v\nGot:  %+v", expectedStep2, task2.Spec.Steps[0])
	}
}
