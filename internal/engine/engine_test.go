package engine_test

import (
	"reflect"
	"strings"
	"testing"

	"tkn-shell/internal/engine"
	"tkn-shell/internal/parser"
	"tkn-shell/internal/state"

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
	pipeline, ok := session.Pipelines["ci"]
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
	task, ok := session.Tasks["build"]
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
	if session.CurrentPipeline == nil || session.CurrentPipeline.Name != "ci" {
		t.Errorf("Expected CurrentPipeline to be 'ci', got %+v", session.CurrentPipeline)
	}
	if session.CurrentTask == nil || session.CurrentTask.Name != "build" {
		t.Errorf("Expected CurrentTask to be 'build', got %+v", session.CurrentTask)
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
	task, ok := session.Tasks["my-task"]
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
	if session.CurrentTask == nil || session.CurrentTask.Name != "task1" {
		t.Fatalf("Expected CurrentTask to be 'task1' after creation, got %v", session.CurrentTask)
	}

	// Create task2
	inputTask2 := "task create task2"
	pl2, _ := parser.ParseLine(inputTask2)
	_, err = engine.ExecuteCommand(pl2.Cmds[0].Pos, pl2.Cmds[0].Cmd, session, nil, nil)
	if err != nil {
		t.Fatalf("Error creating task2: %v", err)
	}
	if session.CurrentTask == nil || session.CurrentTask.Name != "task2" {
		t.Fatalf("Expected CurrentTask to be 'task2' after creation, got %v", session.CurrentTask)
	}

	// Select task1
	inputSelectTask1 := "task select task1"
	plSelect1, _ := parser.ParseLine(inputSelectTask1)
	selectedObj, err := engine.ExecuteCommand(plSelect1.Cmds[0].Pos, plSelect1.Cmds[0].Cmd, session, nil, nil)
	if err != nil {
		t.Fatalf("Error selecting task1: %v", err)
	}

	if session.CurrentTask == nil || session.CurrentTask.Name != "task1" {
		t.Errorf("Expected CurrentTask to be 'task1' after selection, got %v", session.CurrentTask)
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
	if session.CurrentPipeline == nil || session.CurrentPipeline.Name != "p2" {
		t.Fatalf("Expected CurrentPipeline to be 'p2' after creation, got %v", session.CurrentPipeline)
	}
	if session.CurrentTask != nil {
		t.Fatalf("Expected CurrentTask to be nil after creating p2, got %v", session.CurrentTask)
	}

	// Set CurrentTask to t1 again (it should still exist) and CurrentPipeline to p1
	session.CurrentTask = session.Tasks["t1"]
	session.CurrentPipeline = session.Pipelines["p1"]

	// Select pipeline p2
	inputSelectP2 := "pipeline select p2"
	parsedSelectP2, _ := parser.ParseLine(inputSelectP2)
	selectedObj, err := engine.ExecuteCommand(parsedSelectP2.Cmds[0].Pos, parsedSelectP2.Cmds[0].Cmd, session, nil, nil)
	if err != nil {
		t.Fatalf("Error selecting p2: %v", err)
	}

	if session.CurrentPipeline == nil || session.CurrentPipeline.Name != "p2" {
		t.Errorf("Expected CurrentPipeline to be 'p2' after selection, got %v", session.CurrentPipeline)
	}
	if session.CurrentTask != nil {
		t.Errorf("Expected CurrentTask to be nil after selecting pipeline p2, got %v", session.CurrentTask)
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
	} else if !strings.Contains(err.Error(), "pipeline 'nonexist-pipeline' not found") {
		t.Errorf("Expected error message for non-existent pipeline, got: %v", err)
	}
}
