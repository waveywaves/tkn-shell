package engine_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"tkn-shell/internal/engine"
	"tkn-shell/internal/parser"
	"tkn-shell/internal/state"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

func TestExecuteCommand_PipelineTaskStepChain(t *testing.T) {
	inputLine := "pipeline create ci | task create build | step add compile --image alpine"
	session := state.NewSession()
	parsedLine, err := parser.ParseLine(inputLine)
	if err != nil {
		t.Fatalf("ParseLine(%q) error = %v", inputLine, err)
	}

	var prevResult any
	var activeWhenClause *parser.WhenClause
	for _, cmdWrapper := range parsedLine.Cmds {
		if cmdWrapper.When != nil {
			activeWhenClause = cmdWrapper.When
			continue
		}
		if cmdWrapper.Cmd != nil {
			prevResult, err = engine.ExecuteCommand(cmdWrapper.Pos, cmdWrapper.Cmd, session, prevResult, activeWhenClause)
			if err != nil {
				t.Fatalf("ExecuteCommand(%+v) error = %v", cmdWrapper.Cmd, err)
			}
			activeWhenClause = nil // Reset after use
		} else if activeWhenClause != nil {
			// A WhenClause was parsed but not followed by a BaseCommand in the same pipe segment.
			// This might be an error or just means it applies to the next segment if piping continues.
			// For now, we assume When applies to an immediately following command in sequence.
			t.Logf("Warning: Dangling WhenClause, no command to apply to in current segment.")
			activeWhenClause = nil // Reset it to avoid affecting unrelated commands
		}
	}

	// Assertions
	// 1. One Pipeline named "ci" exists
	pipeline, ok := session.GetPipelines()["ci"]
	if !ok {
		t.Fatalf("Pipeline 'ci' not found in session")
	}
	if pipeline.Name != "ci" {
		t.Errorf("Expected pipeline name 'ci', got '%s'", pipeline.Name)
	}

	// 2. "ci" Pipeline has one PipelineTask ref "build"
	if len(pipeline.Spec.Tasks) != 1 {
		t.Fatalf("Expected 1 task in pipeline 'ci', got %d", len(pipeline.Spec.Tasks))
	}
	pipelineTask := pipeline.Spec.Tasks[0]
	if pipelineTask.Name != "build" {
		t.Errorf("Expected pipeline task name 'build', got '%s'", pipelineTask.Name)
	}
	if pipelineTask.TaskRef == nil || pipelineTask.TaskRef.Name != "build" {
		t.Errorf("Expected pipeline task to reference task 'build', got ref: %+v", pipelineTask.TaskRef)
	}

	// 3. Task "build" exists
	task, ok := session.GetTasks()["build"]
	if !ok {
		t.Fatalf("Task 'build' not found in session")
	}
	if task.Name != "build" {
		t.Errorf("Expected task name 'build', got '%s'", task.Name)
	}

	// 4. Task "build" has one step called "compile" with image "alpine"
	if len(task.Spec.Steps) != 1 {
		t.Fatalf("Expected 1 step in task 'build', got %d", len(task.Spec.Steps))
	}
	step := task.Spec.Steps[0]
	expectedStep := tektonv1.Step{
		Name:  "compile",
		Image: "alpine",
	}
	if !reflect.DeepEqual(step, expectedStep) {
		t.Errorf("Expected step %+v, got %+v", expectedStep, step)
	}

	// Verify CurrentPipeline and CurrentTask are set as expected
	if session.GetCurrentPipeline() == nil || session.GetCurrentPipeline().Name != "ci" {
		t.Errorf("Expected CurrentPipeline to be 'ci', got %+v", session.GetCurrentPipeline())
	}
	if session.GetCurrentTask() == nil || session.GetCurrentTask().Name != "build" {
		t.Errorf("Expected CurrentTask to be 'build', got %+v", session.GetCurrentTask())
	}
}

func TestExecuteCommand_TaskWithParamAndStepInterpolation(t *testing.T) {
	inputCommands := []string{
		"task create my-task",
		"param appVersion=1.7.3",
		"step add print-version --image some-image `echo $(params.appVersion)`",
	}

	session := state.NewSession()
	var prevResult interface{}
	var err error
	var activeWhenClause *parser.WhenClause // Though not used in this specific test data

	for _, line := range inputCommands {
		parsedLine, parseErr := parser.ParseLine(line)
		if parseErr != nil {
			t.Fatalf("ParseLine(%q) error = %v", line, parseErr)
		}
		// Each line is a separate execution context for this test structure
		activeWhenClause = nil // Reset for each line if it was from a multi-command line test
		for _, cmdWrapper := range parsedLine.Cmds {
			if cmdWrapper.When != nil {
				activeWhenClause = cmdWrapper.When
				continue
			}
			if cmdWrapper.Cmd != nil {
				prevResult, err = engine.ExecuteCommand(cmdWrapper.Pos, cmdWrapper.Cmd, session, prevResult, activeWhenClause)
				if err != nil {
					t.Fatalf("ExecuteCommand for line %q, command %+v error = %v", line, cmdWrapper.Cmd, err)
				}
				activeWhenClause = nil // Reset after use
			}
		}
	}

	// Assertions
	task, ok := session.GetTasks()["my-task"]
	if !ok {
		t.Fatalf("Task 'my-task' not found in session")
	}

	// 1. Check ParamSpec
	if len(task.Spec.Params) != 1 {
		t.Fatalf("Expected 1 param spec in task 'my-task', got %d", len(task.Spec.Params))
	}
	paramSpec := task.Spec.Params[0]
	expectedParamSpec := tektonv1.ParamSpec{
		Name:    "appVersion",
		Type:    tektonv1.ParamTypeString,
		Default: &tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: "1.7.3"},
	}
	if !reflect.DeepEqual(paramSpec, expectedParamSpec) {
		t.Errorf("Expected param spec %+v, got %+v", expectedParamSpec, paramSpec)
	}

	// 2. Check Step with script interpolation
	if len(task.Spec.Steps) != 1 {
		t.Fatalf("Expected 1 step in task 'my-task', got %d", len(task.Spec.Steps))
	}
	step := task.Spec.Steps[0]
	expectedStepName := "print-version"
	expectedImageName := "some-image"
	expectedScript := "echo 1.7.3"

	if step.Name != expectedStepName {
		t.Errorf("Expected step name '%s', got '%s'", expectedStepName, step.Name)
	}
	if step.Image != expectedImageName {
		t.Errorf("Expected step image '%s', got '%s'", expectedImageName, step.Image)
	}
	if step.Script != expectedScript {
		t.Errorf("Expected step script '%s', got '%s'", expectedScript, step.Script)
	}

	// 3. Check YAML output (optional, but good for seeing the full picture)
	yamlBytes, err := yaml.Marshal(task)
	if err != nil {
		t.Fatalf("Failed to marshal task to YAML: %v", err)
	}
	yamlString := string(yamlBytes)

	// Check for param spec in YAML
	if !strings.Contains(yamlString, "name: appVersion") {
		t.Errorf("YAML output does not contain param spec name: appVersion. YAML:\n%s", yamlString)
	}
	// Tekton ParamSpec marshals the default value under a 'default' key
	if !strings.Contains(yamlString, "default: 1.7.3") {
		t.Errorf("YAML output does not contain param spec default value: 1.7.3. YAML:\n%s", yamlString)
	}

	// Check for interpolated script in YAML
	// For a single line script, Tekton might marshal it directly, not always as a literal block.
	// Check for the presence of the script content.
	if !strings.Contains(yamlString, "script: echo 1.7.3") && !strings.Contains(yamlString, "script: |\n    echo 1.7.3") {
		t.Errorf("YAML output does not contain interpolated script 'echo 1.7.3'. YAML:\n%s", yamlString)
	}
}

func TestExecuteCommand_SelectTask(t *testing.T) {
	session := state.NewSession()

	// Create task1
	inputTask1 := "task create task1"
	pl1, _ := parser.ParseLine(inputTask1)
	_, err := engine.ExecuteCommand(pl1.Cmds[0].Pos, pl1.Cmds[0].Cmd, session, nil, nil)
	if err != nil {
		t.Fatalf("Error creating task1: %v", err)
	}
	if session.GetCurrentTask() == nil || session.GetCurrentTask().Name != "task1" {
		t.Fatalf("Expected CurrentTask to be 'task1' after creation, got %v", session.GetCurrentTask())
	}

	// Create task2
	inputTask2 := "task create task2"
	pl2, _ := parser.ParseLine(inputTask2)
	_, err = engine.ExecuteCommand(pl2.Cmds[0].Pos, pl2.Cmds[0].Cmd, session, nil, nil)
	if err != nil {
		t.Fatalf("Error creating task2: %v", err)
	}
	if session.GetCurrentTask() == nil || session.GetCurrentTask().Name != "task2" {
		t.Fatalf("Expected CurrentTask to be 'task2' after creation, got %v", session.GetCurrentTask())
	}

	// Select task1
	inputSelectTask1 := "task select task1"
	plSelect1, _ := parser.ParseLine(inputSelectTask1)
	selectedObj, err := engine.ExecuteCommand(plSelect1.Cmds[0].Pos, plSelect1.Cmds[0].Cmd, session, nil, nil)
	if err != nil {
		t.Fatalf("Error selecting task1: %v", err)
	}

	if session.GetCurrentTask() == nil || session.GetCurrentTask().Name != "task1" {
		t.Errorf("Expected CurrentTask to be 'task1' after selection, got %v", session.GetCurrentTask())
	}
	selectedTask, ok := selectedObj.(*tektonv1.Task)
	if !ok || selectedTask.Name != "task1" {
		t.Errorf("ExecuteCommand for select task did not return the selected task. Got: %+v", selectedObj)
	}

	// Try to select a non-existent task
	inputBadSelect := "task select nonexist-task"
	plBadSelect, _ := parser.ParseLine(inputBadSelect)
	_, err = engine.ExecuteCommand(plBadSelect.Cmds[0].Pos, plBadSelect.Cmds[0].Cmd, session, nil, nil)
	if err == nil {
		t.Errorf("Expected error when selecting non-existent task, got nil")
	} else if !strings.Contains(err.Error(), "task 'nonexist-task' not found") {
		t.Errorf("Expected error message for non-existent task, got: %v", err)
	}
}

func TestExecuteCommand_SelectPipeline(t *testing.T) {
	session := state.NewSession()

	// Create pipeline1 and a task to set CurrentTask initially
	inputP1 := "pipeline create p1"
	parsedP1, _ := parser.ParseLine(inputP1)
	_, err := engine.ExecuteCommand(parsedP1.Cmds[0].Pos, parsedP1.Cmds[0].Cmd, session, nil, nil)
	if err != nil {
		t.Fatalf("Error creating p1: %v", err)
	}

	inputT1 := "task create t1"
	parsedT1, _ := parser.ParseLine(inputT1)
	_, err = engine.ExecuteCommand(parsedT1.Cmds[0].Pos, parsedT1.Cmds[0].Cmd, session, nil, nil) // CurrentTask is now t1, CurrentPipeline is p1
	if err != nil {
		t.Fatalf("Error creating t1: %v", err)
	}

	// Create pipeline2
	inputP2 := "pipeline create p2"
	parsedP2, _ := parser.ParseLine(inputP2)
	_, err = engine.ExecuteCommand(parsedP2.Cmds[0].Pos, parsedP2.Cmds[0].Cmd, session, nil, nil) // CurrentPipeline is now p2, CurrentTask is nil
	if err != nil {
		t.Fatalf("Error creating p2: %v", err)
	}
	if session.GetCurrentPipeline() == nil || session.GetCurrentPipeline().Name != "p2" {
		t.Fatalf("Expected CurrentPipeline to be 'p2' after creation, got %v", session.GetCurrentPipeline())
	}
	if session.GetCurrentTask() != nil {
		t.Fatalf("Expected CurrentTask to be nil after creating p2, got %v", session.GetCurrentTask())
	}

	// Set CurrentTask to t1 again (it should still exist) and CurrentPipeline to p1
	taskT1, ok := session.GetTasks()["t1"]
	if !ok {
		t.Fatal("Task t1 not found for resetting context")
	}
	session.SetCurrentTask(taskT1)

	pipelineP1, ok := session.GetPipelines()["p1"]
	if !ok {
		t.Fatal("Pipeline p1 not found for resetting context")
	}
	session.SetCurrentPipeline(pipelineP1)

	// Select pipeline p2
	inputSelectP2 := "pipeline select p2"
	parsedSelectP2, _ := parser.ParseLine(inputSelectP2)
	selectedObj, err := engine.ExecuteCommand(parsedSelectP2.Cmds[0].Pos, parsedSelectP2.Cmds[0].Cmd, session, nil, nil)
	if err != nil {
		t.Fatalf("Error selecting p2: %v", err)
	}

	if session.GetCurrentPipeline() == nil || session.GetCurrentPipeline().Name != "p2" {
		t.Errorf("Expected CurrentPipeline to be 'p2' after selection, got %v", session.GetCurrentPipeline())
	}
	if session.GetCurrentTask() != nil {
		t.Errorf("Expected CurrentTask to be nil after selecting pipeline p2, got %v", session.GetCurrentTask())
	}
	selectedPipeline, ok := selectedObj.(*tektonv1.Pipeline)
	if !ok || selectedPipeline.Name != "p2" {
		t.Errorf("ExecuteCommand for select pipeline did not return the selected pipeline. Got: %+v", selectedObj)
	}

	// Try to select a non-existent pipeline
	inputBadSelect := "pipeline select nonexist-pipeline"
	parsedBadSelect, _ := parser.ParseLine(inputBadSelect)
	_, err = engine.ExecuteCommand(parsedBadSelect.Cmds[0].Pos, parsedBadSelect.Cmds[0].Cmd, session, nil, nil)
	if err == nil {
		t.Errorf("Expected error when selecting non-existent pipeline, got nil")
	} else if !strings.Contains(err.Error(), "pipeline nonexist-pipeline not found") {
		t.Errorf("Expected error message for non-existent pipeline, got: %v", err)
	}
}

func TestExecuteCommand_ListCommands(t *testing.T) {
	session := state.NewSession()

	// Helper to execute a command and check for []string result or error
	executeListCmd := func(input string, expectError bool, expectedErrorMsgSubstring string) []string {
		t.Helper()
		pl, err := parser.ParseLine(input)
		if err != nil {
			t.Fatalf("ParseLine(%q) failed: %v", input, err)
		}
		if len(pl.Cmds) != 1 {
			t.Fatalf("Expected 1 command from ParseLine(%q), got %d", input, len(pl.Cmds))
		}
		cmdWrapper := pl.Cmds[0]

		result, execErr := engine.ExecuteCommand(cmdWrapper.Pos, cmdWrapper.Cmd, session, nil, nil)

		if expectError {
			if execErr == nil {
				t.Fatalf("ExecuteCommand(%q) expected error, got nil", input)
			}
			if expectedErrorMsgSubstring != "" && !strings.Contains(execErr.Error(), expectedErrorMsgSubstring) {
				t.Fatalf("ExecuteCommand(%q) error '%v' does not contain '%s'", input, execErr, expectedErrorMsgSubstring)
			}
			return nil
		} else if execErr != nil {
			t.Fatalf("ExecuteCommand(%q) unexpected error: %v", input, execErr)
		}

		strResult, ok := result.([]string)
		if !ok {
			t.Fatalf("ExecuteCommand(%q) expected []string result, got %T: %+v", input, result, result)
		}
		return strResult
	}

	// Test list tasks (empty)
	expectedEmptyTasks := []string{"No tasks defined."}
	actualEmptyTasks := executeListCmd("list tasks", false, "")
	if !reflect.DeepEqual(actualEmptyTasks, expectedEmptyTasks) {
		t.Errorf("list tasks (empty) got %v, want %v", actualEmptyTasks, expectedEmptyTasks)
	}

	// Create some tasks - use engine.ExecuteCommand directly for these setup commands
	createCmd := func(input string) {
		t.Helper()
		pl, err := parser.ParseLine(input)
		if err != nil {
			t.Fatalf("ParseLine(%q) for create failed: %v", input, err)
		}
		_, execErr := engine.ExecuteCommand(pl.Cmds[0].Pos, pl.Cmds[0].Cmd, session, nil, nil)
		if execErr != nil {
			t.Fatalf("ExecuteCommand(%q) for create failed: %v", input, execErr)
		}
	}
	createCmd("task create task-c")
	createCmd("task create task-a")
	createCmd("task create task-b")

	// Test list tasks (populated and sorted)
	expectedTasks := []string{"task-a", "task-b", "task-c"}
	actualTasks := executeListCmd("list tasks", false, "")
	if !reflect.DeepEqual(actualTasks, expectedTasks) {
		t.Errorf("list tasks (populated) got %v, want %v", actualTasks, expectedTasks)
	}

	// Test list pipelines (empty)
	expectedEmptyPipelines := []string{"No pipelines defined."}
	actualEmptyPipelines := executeListCmd("list pipelines", false, "")
	if !reflect.DeepEqual(actualEmptyPipelines, expectedEmptyPipelines) {
		t.Errorf("list pipelines (empty) got %v, want %v", actualEmptyPipelines, expectedEmptyPipelines)
	}

	// Create some pipelines
	createCmd("pipeline create pipeline-z")
	createCmd("pipeline create pipeline-x")

	// Test list pipelines (populated and sorted)
	expectedPipelines := []string{"pipeline-x", "pipeline-z"}
	actualPipelines := executeListCmd("list pipelines", false, "")
	if !reflect.DeepEqual(actualPipelines, expectedPipelines) {
		t.Errorf("list pipelines (populated) got %v, want %v", actualPipelines, expectedPipelines)
	}

	// Test list stepactions (stubbed)
	expectedStepactions := []string{"list stepactions is not implemented yet"}
	actualStepactions := executeListCmd("list stepactions", false, "")
	if !reflect.DeepEqual(actualStepactions, expectedStepactions) {
		t.Errorf("list stepactions got %v, want %v", actualStepactions, expectedStepactions)
	}

	// Test invalid list action
	_ = executeListCmd("list foobar", true, "unknown action 'foobar' for kind 'list'")

	// Test list tasks with arguments (should fail)
	_ = executeListCmd("list tasks extra-arg", true, "list tasks expects 0 arguments")
}

func TestExecuteCommand_ShowCommands(t *testing.T) {
	session := state.NewSession()

	// Helper to execute a command (create or show)
	executeShowCmd := func(input string, expectError bool, expectedErrorMsgSubstring string) []byte {
		t.Helper()
		pl, err := parser.ParseLine(input)
		if err != nil {
			t.Fatalf("ParseLine(%q) failed: %v", input, err)
		}
		if len(pl.Cmds) != 1 {
			t.Fatalf("Expected 1 command from ParseLine(%q), got %d", input, len(pl.Cmds))
		}
		cmdWrapper := pl.Cmds[0]

		result, execErr := engine.ExecuteCommand(cmdWrapper.Pos, cmdWrapper.Cmd, session, nil, nil)

		if expectError {
			if execErr == nil {
				t.Fatalf("ExecuteCommand(%q) expected error, got nil", input)
			}
			if expectedErrorMsgSubstring != "" && !strings.Contains(execErr.Error(), expectedErrorMsgSubstring) {
				t.Fatalf("ExecuteCommand(%q) error '%v' does not contain '%s'", input, execErr, expectedErrorMsgSubstring)
			}
			return nil
		} else if execErr != nil {
			t.Fatalf("ExecuteCommand(%q) unexpected error: %v", input, execErr)
		}

		// For create commands, result might not be []byte, so allow nil return if not expecting error
		if result == nil {
			return nil
		}

		byteResult, ok := result.([]byte)
		if !ok {
			// If it's not []byte, it might be a create command returning the object (e.g., *tektonv1.Task)
			// In this specific helper, for create commands, we don't need to return the object itself, just succeed.
			// The actual 'show' commands are the ones where we care about the []byte output.
			if cmdWrapper.Cmd.Kind == "task" && cmdWrapper.Cmd.Action == "create" {
				return nil // Successful create, but no YAML output from this command itself
			}
			if cmdWrapper.Cmd.Kind == "pipeline" && cmdWrapper.Cmd.Action == "create" {
				return nil // Successful create
			}
			t.Fatalf("ExecuteCommand(%q) expected []byte result for show, got %T: %+v", input, result, result)
		}
		return byteResult
	}

	// Create a task
	executeShowCmd("task create build-task", false, "")

	// Show the task
	yamlOutput := executeShowCmd("show task build-task", false, "")
	if len(yamlOutput) == 0 {
		t.Fatal("show task build-task returned empty YAML")
	}
	yamlString := string(yamlOutput)

	if !strings.Contains(yamlString, "kind: Task") {
		t.Errorf("show task output missing 'kind: Task'. Got:\n%s", yamlString)
	}
	if !strings.Contains(yamlString, "name: build-task") {
		t.Errorf("show task output missing 'name: build-task'. Got:\n%s", yamlString)
	}
	if !strings.Contains(yamlString, "apiVersion: tekton.dev/v1") { // or v1beta1 if that's what SchemeGroupVersion produces
		t.Errorf("show task output missing correct apiVersion. Got:\n%s", yamlString)
	}

	// Show non-existent task
	_ = executeShowCmd("show task non-existent-task", true, "task 'non-existent-task' not found")

	// Create a pipeline
	executeShowCmd("pipeline create build-pipeline", false, "")

	// Show the pipeline
	yamlOutputPipeline := executeShowCmd("show pipeline build-pipeline", false, "")
	if len(yamlOutputPipeline) == 0 {
		t.Fatal("show pipeline build-pipeline returned empty YAML")
	}
	yamlStringPipeline := string(yamlOutputPipeline)

	if !strings.Contains(yamlStringPipeline, "kind: Pipeline") {
		t.Errorf("show pipeline output missing 'kind: Pipeline'. Got:\n%s", yamlStringPipeline)
	}
	if !strings.Contains(yamlStringPipeline, "name: build-pipeline") {
		t.Errorf("show pipeline output missing 'name: build-pipeline'. Got:\n%s", yamlStringPipeline)
	}
	if !strings.Contains(yamlStringPipeline, "apiVersion: tekton.dev/v1") {
		t.Errorf("show pipeline output missing correct apiVersion. Got:\n%s", yamlStringPipeline)
	}

	// Show non-existent pipeline
	_ = executeShowCmd("show pipeline non-existent-pipeline", true, "pipeline 'non-existent-pipeline' not found")

	// Test invalid show action
	_ = executeShowCmd("show foobar some-name", true, "unknown action 'foobar' for kind 'show'")

	// Test show task with wrong number of args
	_ = executeShowCmd("show task", true, "show task expects 1 argument")
	_ = executeShowCmd("show task task1 task2", true, "show task expects 1 argument")
}

func TestExecuteCommand_UndoResetCommands(t *testing.T) {
	session := state.NewSession()

	executeCmd := func(input string) {
		t.Helper()
		pl, err := parser.ParseLine(input)
		if err != nil {
			t.Fatalf("ParseLine(%q) failed: %v", input, err)
		}
		if len(pl.Cmds) != 1 {
			t.Fatalf("Expected 1 command from ParseLine(%q), got %d", input, len(pl.Cmds))
		}
		_, execErr := engine.ExecuteCommand(pl.Cmds[0].Pos, pl.Cmds[0].Cmd, session, nil, nil)
		if execErr != nil {
			t.Fatalf("ExecuteCommand(%q) failed: %v", input, execErr)
		}
	}

	// 1. Undo empty stack
	executeCmd("undo") // Should print "Nothing to undo."
	// For undo, PopRevertAction is on the interface, but PastActions itself is not.
	// We'd need to expose a LenPastActions or similar if we want to test this here through the interface.
	// For now, this test relies on the concrete *state.Session to check PastActions len.
	concreteSessionForUndoTest := session // Assuming session is *state.Session here.
	if len(concreteSessionForUndoTest.PastActions) != 0 {
		t.Errorf("Expected PastActions to be empty after undo on empty stack, got %d", len(concreteSessionForUndoTest.PastActions))
	}

	// 2. Create pipeline, then undo
	executeCmd("pipeline create p-undo")
	if _, ok := session.GetPipelines()["p-undo"]; !ok {
		t.Fatal("Pipeline p-undo not created")
	}
	if session.GetCurrentPipeline() == nil || session.GetCurrentPipeline().Name != "p-undo" {
		t.Fatal("CurrentPipeline not set to p-undo")
	}
	executeCmd("undo")
	if _, ok := session.GetPipelines()["p-undo"]; ok {
		t.Error("Pipeline p-undo still exists after undo")
	}
	if session.GetCurrentPipeline() != nil {
		t.Error("CurrentPipeline not reset after undoing its creation")
	}
	if len(concreteSessionForUndoTest.PastActions) != 0 {
		t.Errorf("Expected PastActions to be empty, got %d", len(concreteSessionForUndoTest.PastActions))
	}

	// 3. Create task, then undo
	executeCmd("task create t-undo")
	if _, ok := session.GetTasks()["t-undo"]; !ok {
		t.Fatal("Task t-undo not created")
	}
	if session.GetCurrentTask() == nil || session.GetCurrentTask().Name != "t-undo" {
		t.Fatal("CurrentTask not set to t-undo")
	}
	executeCmd("undo")
	if _, ok := session.GetTasks()["t-undo"]; ok {
		t.Error("Task t-undo still exists after undo")
	}
	if session.GetCurrentTask() != nil {
		t.Error("CurrentTask not reset after undoing its creation")
	}

	// 4. Create task in pipeline, then undo
	executeCmd("pipeline create p-for-task-undo")
	executeCmd("task create t-in-p-undo") // This task is added to p-for-task-undo
	pipelineForTaskUndo := session.GetPipelines()["p-for-task-undo"]
	if len(pipelineForTaskUndo.Spec.Tasks) != 1 || pipelineForTaskUndo.Spec.Tasks[0].Name != "t-in-p-undo" {
		t.Fatalf("Task t-in-p-undo not added to pipeline p-for-task-undo, spec: %+v", pipelineForTaskUndo.Spec.Tasks)
	}
	executeCmd("undo") // Undo task create t-in-p-undo
	if _, ok := session.GetTasks()["t-in-p-undo"]; ok {
		t.Error("Task t-in-p-undo still exists after undo")
	}
	if len(pipelineForTaskUndo.Spec.Tasks) != 0 {
		t.Errorf("Task t-in-p-undo not removed from pipeline p-for-task-undo spec after undo, got: %+v", pipelineForTaskUndo.Spec.Tasks)
	}
	executeCmd("undo") // Undo pipeline create p-for-task-undo
	if _, ok := session.GetPipelines()["p-for-task-undo"]; ok {
		t.Error("Pipeline p-for-task-undo still exists after second undo")
	}

	// 5. Add step, then undo
	executeCmd("task create task-for-step-undo")
	executeCmd("step add step1 --image alpine")
	taskForStepUndo := session.GetTasks()["task-for-step-undo"]
	if len(taskForStepUndo.Spec.Steps) != 1 {
		t.Fatal("Step not added")
	}
	executeCmd("undo")
	if len(taskForStepUndo.Spec.Steps) != 0 {
		t.Errorf("Step not removed after undo, steps: %+v", taskForStepUndo.Spec.Steps)
	}

	// 6. Set new param, then undo
	executeCmd("task create task-for-param-undo")
	executeCmd("param newParam=val1")
	taskForParamUndo := session.GetTasks()["task-for-param-undo"]
	if len(taskForParamUndo.Spec.Params) != 1 {
		t.Fatal("New param not added")
	}
	executeCmd("undo")
	if len(taskForParamUndo.Spec.Params) != 0 {
		t.Errorf("New param not removed after undo, params: %+v", taskForParamUndo.Spec.Params)
	}

	// 7. Set existing param, then undo
	executeCmd("task create task-for-param-update-undo")
	executeCmd("param existingParam=val1") // first set
	// Ensure the task for param update is used for getting the original param
	taskForParamUpdateUndoPreUpdate := session.GetTasks()["task-for-param-update-undo"]
	if taskForParamUpdateUndoPreUpdate == nil || len(taskForParamUpdateUndoPreUpdate.Spec.Params) == 0 {
		t.Fatal("Task for param update or its param not found before update.")
	}
	originalParam := taskForParamUpdateUndoPreUpdate.Spec.Params[0].DeepCopy()
	executeCmd("param existingParam=val2") // update
	taskForParamUpdateUndo := session.GetTasks()["task-for-param-update-undo"]
	if taskForParamUpdateUndo == nil || len(taskForParamUpdateUndo.Spec.Params) == 0 || taskForParamUpdateUndo.Spec.Params[0].Default.StringVal != "val2" {
		t.Fatal("Param not updated to val2")
	}
	executeCmd("undo") // undo update to val2
	if len(taskForParamUpdateUndo.Spec.Params) == 0 || !reflect.DeepEqual(taskForParamUpdateUndo.Spec.Params[0].Default, originalParam.Default) {
		t.Errorf("Param not restored to val1 after undo. Got: %+v, Expected Default: %+v", taskForParamUpdateUndo.Spec.Params[0], originalParam.Default)
	}

	// 8. Reset session
	executeCmd("pipeline create p-reset")
	executeCmd("task create t-reset")
	if len(concreteSessionForUndoTest.PastActions) == 0 {
		t.Fatal("Expected PastActions to have items before reset")
	}
	executeCmd("reset")
	if len(session.GetPipelines()) != 0 || len(session.GetTasks()) != 0 || session.GetCurrentPipeline() != nil || session.GetCurrentTask() != nil {
		t.Error("Session not empty after reset")
	}
	if len(concreteSessionForUndoTest.PastActions) != 0 {
		t.Error("PastActions not cleared after reset")
	}
}

func TestExecuteCommand_Validate(t *testing.T) {
	session := state.NewSession()
	parsedLine, _ := parser.ParseLine("validate")

	// Initial validation: should pass
	_, err := engine.ExecuteCommand(parsedLine.Cmds[0].Pos, parsedLine.Cmds[0].Cmd, session, nil, nil)
	if err != nil {
		t.Errorf("Expected initial 'validate' to pass, got %v", err)
	}

	// Create an invalid pipeline (e.g., empty spec, though Validate might not catch this specific case without tasks)
	// For a more robust test, create a pipeline that violates a specific Tekton validation rule.
	// Here, we'll create a pipeline and a task, but the task is not added to the pipeline, which isn't invalid per se.
	// Let's instead create a task with an invalid name (e.g. with special characters not allowed by k8s naming conventions)
	// However, our parser might reject this first. Tekton's Validate() is more about structural and cross-field consistency.

	// Create a valid pipeline and task for now.
	ptLine, _ := parser.ParseLine("pipeline create my-p | task create my-t")
	var prevRes interface{}
	for _, cmdWrapper := range ptLine.Cmds {
		prevRes, err = engine.ExecuteCommand(cmdWrapper.Pos, cmdWrapper.Cmd, session, prevRes, nil)
		if err != nil {
			t.Fatalf("Setup command failed: %v", err)
		}
	}

	// A pipeline with no tasks is valid. A task with no steps is also valid by default.
	// To make it invalid, we could add a task with a name that is too long, or with invalid characters.
	// The base `Task.Validate` checks metadata names using `validate.ObjectMetadata`.
	invalidTaskName := strings.Repeat("a", 254) // Exceeds k8s name length limit
	session.AddTask(invalidTaskName, &tektonv1.Task{
		ObjectMeta: metav1.ObjectMeta{Name: invalidTaskName},
		Spec:       tektonv1.TaskSpec{Steps: []tektonv1.Step{{Name: "s1", Image: "img"}}},
	})

	_, err = engine.ExecuteCommand(parsedLine.Cmds[0].Pos, parsedLine.Cmds[0].Cmd, session, nil, nil)
	if err == nil {
		t.Errorf("Expected 'validate' to fail with invalid task name, but it passed")
	} else {
		if !strings.Contains(err.Error(), "metadata.name") && !strings.Contains(err.Error(), "long") {
			t.Errorf("Expected validation error to mention metadata name length, got: %v", err)
		}
		t.Logf("Got expected validation error: %v", err) // Log error for visibility
	}
	session.DeleteTask(invalidTaskName) // cleanup

	// Test validation is called before export all
	exportCmd, _ := parser.ParseLine("export all")
	session.AddTask(invalidTaskName, &tektonv1.Task{
		ObjectMeta: metav1.ObjectMeta{Name: invalidTaskName},
		Spec:       tektonv1.TaskSpec{Steps: []tektonv1.Step{{Name: "s1", Image: "img"}}},
	})
	_, err = engine.ExecuteCommand(exportCmd.Cmds[0].Pos, exportCmd.Cmds[0].Cmd, session, nil, nil)
	if err == nil {
		t.Errorf("Expected 'export all' to fail validation, but it passed")
	} else if !strings.Contains(err.Error(), "validation failed before export") {
		t.Errorf("Expected error message for 'export all' to indicate pre-export validation failure, got: %v", err)
	}
	session.DeleteTask(invalidTaskName) // cleanup

	// Test validation is called before apply all
	applyCmd, _ := parser.ParseLine("apply all ns")
	session.AddTask(invalidTaskName, &tektonv1.Task{
		ObjectMeta: metav1.ObjectMeta{Name: invalidTaskName},
		Spec:       tektonv1.TaskSpec{Steps: []tektonv1.Step{{Name: "s1", Image: "img"}}},
	})
	_, err = engine.ExecuteCommand(applyCmd.Cmds[0].Pos, applyCmd.Cmds[0].Cmd, session, nil, nil)
	if err == nil {
		t.Errorf("Expected 'apply all' to fail validation, but it passed")
	} else if !strings.Contains(err.Error(), "validation failed before apply") {
		t.Errorf("Expected error message for 'apply all' to indicate pre-apply validation failure, got: %v", err)
	}
	session.DeleteTask(invalidTaskName) // cleanup
}

func TestExecuteCommand_ExportAll_Successful(t *testing.T) {
	session := state.NewSession()
	p := &tektonv1.Pipeline{ObjectMeta: metav1.ObjectMeta{Name: "p1"}, Spec: tektonv1.PipelineSpec{Description: "d1"}}
	session.AddPipeline("p1", p)

	exportCmdLine, _ := parser.ParseLine("export all")
	cmdToExec := exportCmdLine.Cmds[0].Cmd

	result, err := engine.ExecuteCommand(exportCmdLine.Cmds[0].Pos, cmdToExec, session, nil, nil)
	if err != nil {
		t.Fatalf("ExecuteCommand('export all') failed: %v", err)
	}

	yamlBytes, ok := result.([]byte)
	if !ok {
		t.Fatalf("ExecuteCommand('export all') expected []byte result, got %T", result)
	}

	yamlString := string(yamlBytes)
	if !strings.Contains(yamlString, "name: p1") {
		t.Errorf("Expected YAML to contain 'name: p1', got: %s", yamlString)
	}
	if !strings.Contains(yamlString, "description: d1") {
		t.Errorf("Expected YAML to contain 'description: d1', got: %s", yamlString)
	}
}

// mockSessionForRun is a simplified mock of state.Session for testing run commands.
// It only implements the methods and fields relevant to the run command logic.
type mockSessionForRun struct {
	*state.Session                 // Embed original session for non-mocked parts if needed
	RunPipelineCalledWith struct { // To store arguments passed to RunPipeline
		Ctx          context.Context
		PipelineName string
		Params       []tektonv1.Param
		Namespace    string
	}
	RunPipelineError error // To simulate errors from RunPipeline

	RunTaskCalledWith struct {
		Ctx       context.Context
		TaskName  string
		Params    []tektonv1.Param
		Namespace string
	}
	RunTaskError error
}

// Ensure mockSessionForRun implements CommandExecutorSession
var _ engine.CommandExecutorSession = (*mockSessionForRun)(nil)

// GetPipelines implements engine.CommandExecutorSession
func (m *mockSessionForRun) GetPipelines() map[string]*tektonv1.Pipeline {
	if m.Session == nil { // Ensure Session is initialized
		m.Session = state.NewSession()
	}
	return m.Session.GetPipelines()
}

// SetCurrentPipeline implements engine.CommandExecutorSession
func (m *mockSessionForRun) SetCurrentPipeline(p *tektonv1.Pipeline) {
	if m.Session == nil {
		m.Session = state.NewSession()
	}
	m.Session.SetCurrentPipeline(p)
}

// GetCurrentPipeline implements engine.CommandExecutorSession
func (m *mockSessionForRun) GetCurrentPipeline() *tektonv1.Pipeline {
	if m.Session == nil {
		return nil
	}
	return m.Session.GetCurrentPipeline()
}

// AddPipeline implements engine.CommandExecutorSession
func (m *mockSessionForRun) AddPipeline(name string, p *tektonv1.Pipeline) {
	if m.Session == nil {
		m.Session = state.NewSession()
	}
	m.Session.AddPipeline(name, p)
}

// DeletePipeline implements engine.CommandExecutorSession
func (m *mockSessionForRun) DeletePipeline(name string) {
	if m.Session == nil {
		return
	}
	m.Session.DeletePipeline(name)
}

// GetTasks implements engine.CommandExecutorSession
func (m *mockSessionForRun) GetTasks() map[string]*tektonv1.Task {
	if m.Session == nil {
		m.Session = state.NewSession()
	}
	return m.Session.GetTasks()
}

// SetCurrentTask implements engine.CommandExecutorSession
func (m *mockSessionForRun) SetCurrentTask(t *tektonv1.Task) {
	if m.Session == nil {
		m.Session = state.NewSession()
	}
	m.Session.SetCurrentTask(t)
}

// GetCurrentTask implements engine.CommandExecutorSession
func (m *mockSessionForRun) GetCurrentTask() *tektonv1.Task {
	if m.Session == nil {
		return nil
	}
	return m.Session.GetCurrentTask()
}

// AddTask implements engine.CommandExecutorSession
func (m *mockSessionForRun) AddTask(name string, t *tektonv1.Task) {
	if m.Session == nil {
		m.Session = state.NewSession()
	}
	m.Session.AddTask(name, t)
}

// DeleteTask implements engine.CommandExecutorSession
func (m *mockSessionForRun) DeleteTask(name string) {
	if m.Session == nil {
		return
	}
	m.Session.DeleteTask(name)
}

// PushRevertAction implements engine.CommandExecutorSession
func (m *mockSessionForRun) PushRevertAction(revert state.RevertFunc) {
	if m.Session == nil {
		m.Session = state.NewSession()
	}
	m.Session.PushRevertAction(revert)
}

// PopRevertAction implements engine.CommandExecutorSession
func (m *mockSessionForRun) PopRevertAction() state.RevertFunc {
	if m.Session == nil {
		return nil
	}
	return m.Session.PopRevertAction()
}

// Reset implements engine.CommandExecutorSession
func (m *mockSessionForRun) Reset() {
	if m.Session == nil {
		m.Session = state.NewSession() // Or handle error if Reset is called on nil embedded session
	} else {
		m.Session.Reset()
	}
	// Reset mock-specific fields if any
	m.RunPipelineCalledWith = struct {
		Ctx          context.Context
		PipelineName string
		Params       []tektonv1.Param
		Namespace    string
	}{}
	m.RunPipelineError = nil
	m.RunTaskCalledWith = struct {
		Ctx       context.Context
		TaskName  string
		Params    []tektonv1.Param
		Namespace string
	}{}
	m.RunTaskError = nil
}

// RunPipeline is the mock implementation.
func (m *mockSessionForRun) RunPipeline(ctx context.Context, pipelineName string, params []tektonv1.Param, namespace string) (*tektonv1.PipelineRun, error) {
	m.RunPipelineCalledWith.Ctx = ctx
	m.RunPipelineCalledWith.PipelineName = pipelineName
	m.RunPipelineCalledWith.Params = params
	m.RunPipelineCalledWith.Namespace = namespace
	if m.RunPipelineError != nil {
		return nil, m.RunPipelineError
	}
	// Return a dummy PipelineRun, actual content doesn't matter much for this engine test
	return &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{Name: pipelineName + "-run-dummy"}}, nil
}

// RunTask is the mock implementation.
func (m *mockSessionForRun) RunTask(ctx context.Context, taskName string, params []tektonv1.Param, namespace string) (*tektonv1.TaskRun, error) {
	m.RunTaskCalledWith.Ctx = ctx
	m.RunTaskCalledWith.TaskName = taskName
	m.RunTaskCalledWith.Params = params
	m.RunTaskCalledWith.Namespace = namespace
	if m.RunTaskError != nil {
		return nil, m.RunTaskError
	}
	return &tektonv1.TaskRun{ObjectMeta: metav1.ObjectMeta{Name: taskName + "-run-dummy"}}, nil
}

func TestExecuteCommand_PipelineRun(t *testing.T) {
	tests := []struct {
		name                 string
		inputLine            string
		setupSession         func(*mockSessionForRun) // To setup initial state, like existing pipelines
		wantError            bool
		wantErrorMsgContains string
		wantPipelineName     string
		wantParams           []tektonv1.Param
		wantNamespace        string
	}{
		{
			name:      "run pipeline basic",
			inputLine: "pipeline run my-pipeline",
			setupSession: func(ms *mockSessionForRun) {
				ms.AddPipeline("my-pipeline", &tektonv1.Pipeline{ObjectMeta: metav1.ObjectMeta{Name: "my-pipeline"}})
			},
			wantPipelineName: "my-pipeline",
			wantNamespace:    "default",
			wantParams:       nil,
		},
		{
			name:      "run pipeline with namespace",
			inputLine: "pipeline run my-pipeline namespace custom-ns",
			setupSession: func(ms *mockSessionForRun) {
				ms.AddPipeline("my-pipeline", &tektonv1.Pipeline{ObjectMeta: metav1.ObjectMeta{Name: "my-pipeline"}})
			},
			wantPipelineName: "my-pipeline",
			wantNamespace:    "custom-ns",
			wantParams:       nil,
		},
		{
			name:      "run pipeline with single param",
			inputLine: `pipeline run my-pipeline param image="nginx:latest"`,
			setupSession: func(ms *mockSessionForRun) {
				ms.AddPipeline("my-pipeline", &tektonv1.Pipeline{ObjectMeta: metav1.ObjectMeta{Name: "my-pipeline"}})
			},
			wantPipelineName: "my-pipeline",
			wantNamespace:    "default",
			wantParams:       []tektonv1.Param{{Name: "image", Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: "nginx:latest"}}},
		},
		{
			name:      "run pipeline with multiple params and namespace",
			inputLine: `pipeline run my-pipeline param imageTag="v1.0" namespace prod param replicas=3`,
			setupSession: func(ms *mockSessionForRun) {
				ms.AddPipeline("my-pipeline", &tektonv1.Pipeline{ObjectMeta: metav1.ObjectMeta{Name: "my-pipeline"}})
			},
			wantPipelineName: "my-pipeline",
			wantNamespace:    "prod",
			wantParams: []tektonv1.Param{
				{Name: "imageTag", Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: "v1.0"}},
				{Name: "replicas", Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: "3"}},
			},
		},
		{
			name:                 "run pipeline not found",
			inputLine:            "pipeline run non-existent-pipeline",
			setupSession:         func(ms *mockSessionForRun) { /* no pipeline setup */ },
			wantError:            true,
			wantErrorMsgContains: "pipeline 'non-existent-pipeline' not found",
		},
		{
			name:      "run pipeline with invalid param format",
			inputLine: "pipeline run my-pipeline param image", // Missing =value
			setupSession: func(ms *mockSessionForRun) {
				ms.AddPipeline("my-pipeline", &tektonv1.Pipeline{ObjectMeta: metav1.ObjectMeta{Name: "my-pipeline"}})
			},
			wantError:            true,
			wantErrorMsgContains: "invalid param format",
		},
		{
			name:      "run pipeline error from session",
			inputLine: "pipeline run my-pipeline",
			setupSession: func(ms *mockSessionForRun) {
				ms.AddPipeline("my-pipeline", &tektonv1.Pipeline{ObjectMeta: metav1.ObjectMeta{Name: "my-pipeline"}})
				ms.RunPipelineError = fmt.Errorf("kube API error")
			},
			wantError:            true,
			wantErrorMsgContains: "kube API error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSess := &mockSessionForRun{
				Session: state.NewSession(), // Initialize embedded session for Pipelines map
			}
			if tt.setupSession != nil {
				tt.setupSession(mockSess)
			}

			parsedLine, err := parser.ParseLine(tt.inputLine)
			if err != nil {
				t.Fatalf("ParseLine(%q) error = %v", tt.inputLine, err)
			}
			if len(parsedLine.Cmds) == 0 || parsedLine.Cmds[0].Cmd == nil {
				t.Fatalf("ParseLine(%q) did not produce a valid command", tt.inputLine)
			}
			cmdToExecute := parsedLine.Cmds[0].Cmd

			// Execute the command with the mock session
			_, execErr := engine.ExecuteCommand(cmdToExecute.Pos, cmdToExecute, mockSess, nil, nil)

			// Assertions
			if tt.wantError {
				if execErr == nil {
					t.Errorf("ExecuteCommand() expected error, got nil")
				} else if tt.wantErrorMsgContains != "" && !strings.Contains(execErr.Error(), tt.wantErrorMsgContains) {
					t.Errorf("ExecuteCommand() error = %v, wantErr %s", execErr, tt.wantErrorMsgContains)
				}
			} else {
				if execErr != nil {
					t.Errorf("ExecuteCommand() unexpected error = %v", execErr)
				}
				if !tt.wantError { // Only check call details if no error was expected
					if mockSess.RunPipelineCalledWith.PipelineName != tt.wantPipelineName {
						t.Errorf("RunPipeline called with PipelineName = %s; want %s", mockSess.RunPipelineCalledWith.PipelineName, tt.wantPipelineName)
					}
					if mockSess.RunPipelineCalledWith.Namespace != tt.wantNamespace {
						t.Errorf("RunPipeline called with Namespace = %s; want %s", mockSess.RunPipelineCalledWith.Namespace, tt.wantNamespace)
					}
					if !reflect.DeepEqual(mockSess.RunPipelineCalledWith.Params, tt.wantParams) {
						t.Errorf("RunPipeline called with Params = %v; want %v", mockSess.RunPipelineCalledWith.Params, tt.wantParams)
					}
				}
			}
		})
	}
}

func TestExecuteCommand_TaskRun(t *testing.T) {
	tests := []struct {
		name                 string
		inputLine            string
		setupSession         func(*mockSessionForRun)
		wantError            bool
		wantErrorMsgContains string
		wantTaskName         string
		wantParams           []tektonv1.Param
		wantNamespace        string
	}{
		{
			name:      "run task basic",
			inputLine: "task run my-task",
			setupSession: func(ms *mockSessionForRun) {
				ms.AddTask("my-task", &tektonv1.Task{ObjectMeta: metav1.ObjectMeta{Name: "my-task"}})
			},
			wantTaskName:  "my-task",
			wantNamespace: "default",
			wantParams:    nil,
		},
		{
			name:      "run task with namespace",
			inputLine: "task run my-task namespace custom-ns",
			setupSession: func(ms *mockSessionForRun) {
				ms.AddTask("my-task", &tektonv1.Task{ObjectMeta: metav1.ObjectMeta{Name: "my-task"}})
			},
			wantTaskName:  "my-task",
			wantNamespace: "custom-ns",
			wantParams:    nil,
		},
		{
			name:      "run task with single param",
			inputLine: `task run my-task param image="nginx:latest"`,
			setupSession: func(ms *mockSessionForRun) {
				ms.AddTask("my-task", &tektonv1.Task{ObjectMeta: metav1.ObjectMeta{Name: "my-task"}})
			},
			wantTaskName:  "my-task",
			wantNamespace: "default",
			wantParams:    []tektonv1.Param{{Name: "image", Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: "nginx:latest"}}},
		},
		{
			name:      "run task with multiple params and namespace",
			inputLine: `task run my-task param imageTag="v1.0" namespace prod param replicas=3`,
			setupSession: func(ms *mockSessionForRun) {
				ms.AddTask("my-task", &tektonv1.Task{ObjectMeta: metav1.ObjectMeta{Name: "my-task"}})
			},
			wantTaskName:  "my-task",
			wantNamespace: "prod",
			wantParams: []tektonv1.Param{
				{Name: "imageTag", Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: "v1.0"}},
				{Name: "replicas", Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: "3"}},
			},
		},
		{
			name:                 "run task not found",
			inputLine:            "task run non-existent-task",
			setupSession:         func(ms *mockSessionForRun) { /* no task setup */ },
			wantError:            true,
			wantErrorMsgContains: "task 'non-existent-task' not found",
		},
		{
			name:      "run task with invalid param format",
			inputLine: "task run my-task param image", // Missing =value
			setupSession: func(ms *mockSessionForRun) {
				ms.AddTask("my-task", &tektonv1.Task{ObjectMeta: metav1.ObjectMeta{Name: "my-task"}})
			},
			wantError:            true,
			wantErrorMsgContains: "invalid param format",
		},
		{
			name:      "run task error from session",
			inputLine: "task run my-task",
			setupSession: func(ms *mockSessionForRun) {
				ms.AddTask("my-task", &tektonv1.Task{ObjectMeta: metav1.ObjectMeta{Name: "my-task"}})
				ms.RunTaskError = fmt.Errorf("kube API error for task")
			},
			wantError:            true,
			wantErrorMsgContains: "kube API error for task",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSess := &mockSessionForRun{
				Session: state.NewSession(),
			}
			if tt.setupSession != nil {
				tt.setupSession(mockSess)
			}

			parsedLine, err := parser.ParseLine(tt.inputLine)
			if err != nil {
				t.Fatalf("ParseLine(%q) error = %v", tt.inputLine, err)
			}
			if len(parsedLine.Cmds) == 0 || parsedLine.Cmds[0].Cmd == nil {
				t.Fatalf("ParseLine(%q) did not produce a valid command", tt.inputLine)
			}
			cmdToExecute := parsedLine.Cmds[0].Cmd

			// Execute the command with the mock session
			_, execErr := engine.ExecuteCommand(cmdToExecute.Pos, cmdToExecute, mockSess, nil, nil)

			// Assertions
			if tt.wantError {
				if execErr == nil {
					t.Errorf("ExecuteCommand() expected error, got nil")
				} else if tt.wantErrorMsgContains != "" && !strings.Contains(execErr.Error(), tt.wantErrorMsgContains) {
					t.Errorf("ExecuteCommand() error = %v, wantErr %s", execErr, tt.wantErrorMsgContains)
				}
			} else {
				if execErr != nil {
					t.Errorf("ExecuteCommand() unexpected error = %v", execErr)
				}
				if !tt.wantError { // Only check call details if no error was expected
					if mockSess.RunTaskCalledWith.TaskName != tt.wantTaskName {
						t.Errorf("RunTask called with TaskName = %s; want %s", mockSess.RunTaskCalledWith.TaskName, tt.wantTaskName)
					}
					if mockSess.RunTaskCalledWith.Namespace != tt.wantNamespace {
						t.Errorf("RunTask called with Namespace = %s; want %s", mockSess.RunTaskCalledWith.Namespace, tt.wantNamespace)
					}
					if !reflect.DeepEqual(mockSess.RunTaskCalledWith.Params, tt.wantParams) {
						t.Errorf("RunTask called with Params = %v; want %v", mockSess.RunTaskCalledWith.Params, tt.wantParams)
					}
				}
			}
		})
	}
}
