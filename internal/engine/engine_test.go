package engine_test

import (
	"reflect"
	"testing"

	"tkn-shell/internal/engine"
	"tkn-shell/internal/parser"
	"tkn-shell/internal/state"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

func TestExecuteCommand_PipelineTaskStepChain(t *testing.T) {
	inputLine := "pipeline create ci | task create build | step add compile --image alpine"

	session := state.NewSession()

	parsedLine, err := parser.ParseLine(inputLine)
	if err != nil {
		t.Fatalf("ParseLine(%q) error = %v", inputLine, err)
	}

	var prevResult any = nil
	for _, cmd := range parsedLine.Cmds {
		prevResult, err = engine.ExecuteCommand(cmd, session, prevResult)
		if err != nil {
			t.Fatalf("ExecuteCommand(%+v) error = %v", cmd, err)
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
