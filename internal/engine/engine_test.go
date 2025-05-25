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
