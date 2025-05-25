package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"tkn-shell/internal/export"
	"tkn-shell/internal/feedback"
	"tkn-shell/internal/parser"
	"tkn-shell/internal/state"

	"sigs.k8s.io/yaml"

	"github.com/alecthomas/participle/v2/lexer"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/selection"
)

// Tekton Operator constants (local definition as a workaround)
const (
	operatorIn    = selection.In
	operatorNotIn = selection.NotIn
)

// errorWithPosition creates an error message that includes the line and column.
func errorWithPosition(pos lexer.Position, message string, args ...interface{}) error {
	prefix := ""
	if pos.Filename != "" || pos.Line != 0 || pos.Column != 0 { // Check if Pos is initialized
		prefix = fmt.Sprintf("line %d, column %d: ", pos.Line, pos.Column)
	}
	return fmt.Errorf(prefix+message, args...)
}

// Node represents an executable node in the AST.
// For now, this is not directly implemented by parser.Command due to package restrictions.
// Instead, ExecuteCommand function handles the logic for a parser.Command.
type Node interface {
	Apply(session *state.Session, prevResult any) (any, error)
}

// interpolateParams replaces $(params.name) with the param's default value in a string.
func interpolateParams(str string, params []tektonv1.ParamSpec) string {
	for _, p := range params {
		if p.Default != nil {
			str = strings.ReplaceAll(str, fmt.Sprintf("$(params.%s)", p.Name), p.Default.StringVal)
		}
	}
	return str
}

func convertToTektonWhenExpressions(whenClause *parser.WhenClause) []tektonv1.WhenExpression {
	if whenClause == nil || len(whenClause.Conditions) == 0 {
		return nil
	}
	tektonWhens := []tektonv1.WhenExpression{}
	for _, cond := range whenClause.Conditions {
		var op selection.Operator
		switch cond.Operator {
		case "==":
			op = operatorIn
		case "!=":
			op = operatorNotIn
		default:
			feedback.Errorf("%s Unknown when operator '%s', skipping condition.",
				errorWithPosition(cond.Pos, ""), cond.Operator)
			continue
		}
		tektonWhens = append(tektonWhens, tektonv1.WhenExpression{
			Input:    cond.Left,
			Operator: selection.Operator(op),
			Values:   []string{cond.Right},
		})
	}
	return tektonWhens
}

// ExecuteCommand processes a base command and updates the session state.
// It can optionally apply a WhenClause to certain created resources (e.g., PipelineTask).
func ExecuteCommand(cmdPos lexer.Position, baseCmd *parser.BaseCommand, session *state.Session, prevResult any, whenClause *parser.WhenClause) (any, error) {
	if baseCmd == nil {
		return nil, errorWithPosition(cmdPos, "ExecuteCommand called with nil baseCmd")
	}

	switch baseCmd.Kind {
	case "pipeline":
		switch baseCmd.Action {
		case "create":
			if len(baseCmd.Args) != 1 {
				return nil, errorWithPosition(baseCmd.Pos, "pipeline create expects 1 argument (name), got %d", len(baseCmd.Args))
			}
			name := baseCmd.Args[0]
			if _, exists := session.Pipelines[name]; exists {
				return nil, errorWithPosition(baseCmd.Pos, "pipeline %s already exists", name)
			}
			newPipeline := &tektonv1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: tektonv1.PipelineSpec{}, // Initialize spec
			}
			session.Pipelines[name] = newPipeline
			prevCurrentPipeline := session.CurrentPipeline // Capture for undo
			prevCurrentTask := session.CurrentTask         // Capture for undo
			session.CurrentPipeline = newPipeline
			session.CurrentTask = nil

			session.PushRevertAction(func(s *state.Session) {
				delete(s.Pipelines, name)
				feedback.Infof("Undo: Pipeline '%s' deleted.", name)
				// Try to restore previous context, if this was the one being made current
				// This logic might need refinement if select also gets undo
				if s.CurrentPipeline != nil && s.CurrentPipeline.Name == name {
					s.CurrentPipeline = prevCurrentPipeline
					s.CurrentTask = prevCurrentTask // Restore task context too, as pipeline change clears it
					if s.CurrentPipeline != nil {
						feedback.Infof("Undo: Current pipeline restored to '%s'.", s.CurrentPipeline.Name)
					} else {
						feedback.Infof("Undo: Current pipeline cleared.")
					}
				}
			})

			feedback.Infof("Pipeline '%s' created and set as current.", name)
			return newPipeline, nil
		case "select":
			if len(baseCmd.Args) != 1 {
				return nil, errorWithPosition(baseCmd.Pos, "pipeline select expects 1 argument (name), got %d", len(baseCmd.Args))
			}
			name := baseCmd.Args[0]
			p, exists := session.Pipelines[name]
			if !exists {
				return nil, errorWithPosition(baseCmd.Pos, "pipeline %s not found", name)
			}
			session.CurrentPipeline = p
			session.CurrentTask = nil // Clear task context when pipeline changes
			feedback.Infof("Pipeline '%s' selected as current.", name)
			return p, nil
		default:
			return nil, errorWithPosition(baseCmd.Pos, "unknown action '%s' for kind 'pipeline'", baseCmd.Action)
		}
	case "task":
		switch baseCmd.Action {
		case "create":
			if len(baseCmd.Args) != 1 {
				return nil, errorWithPosition(baseCmd.Pos, "task create expects 1 argument (name), got %d", len(baseCmd.Args))
			}
			name := baseCmd.Args[0]
			if _, exists := session.Tasks[name]; exists {
				return nil, errorWithPosition(baseCmd.Pos, "task %s already exists", name)
			}
			newTask := &tektonv1.Task{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: tektonv1.TaskSpec{}, // Initialize spec
			}
			session.Tasks[name] = newTask
			prevCurrentTask := session.CurrentTask // Capture for undo
			session.CurrentTask = newTask

			wasAddedToPipeline := false
			pipelineName := ""
			var originalPipelineTasks []tektonv1.PipelineTask

			if session.CurrentPipeline != nil {
				pipelineName = session.CurrentPipeline.Name
				// Store a copy of the pipeline's tasks *before* modification
				originalPipelineTasks = make([]tektonv1.PipelineTask, len(session.CurrentPipeline.Spec.Tasks))
				copy(originalPipelineTasks, session.CurrentPipeline.Spec.Tasks)

				var existingPipelineTask *tektonv1.PipelineTask
				ptIndex := -1
				for i, pt := range session.CurrentPipeline.Spec.Tasks {
					if pt.Name == name || (pt.TaskRef != nil && pt.TaskRef.Name == name) {
						existingPipelineTask = &session.CurrentPipeline.Spec.Tasks[i]
						ptIndex = i
						break
					}
				}
				tektonWhens := convertToTektonWhenExpressions(whenClause)
				if existingPipelineTask != nil {
					// This part of create logic seems to imply updating existing, which might be complex for undo
					// For now, focusing on undoing the creation and simple addition.
					session.CurrentPipeline.Spec.Tasks[ptIndex].When = tektonWhens
					wasAddedToPipeline = true // Or updated
				} else {
					pipelineTask := tektonv1.PipelineTask{
						Name:    name,
						TaskRef: &tektonv1.TaskRef{Name: name, Kind: tektonv1.NamespacedTaskKind},
						When:    tektonWhens,
					}
					session.CurrentPipeline.Spec.Tasks = append(session.CurrentPipeline.Spec.Tasks, pipelineTask)
					wasAddedToPipeline = true
				}
			}

			session.PushRevertAction(func(s *state.Session) {
				delete(s.Tasks, name)
				feedback.Infof("Undo: Task '%s' deleted.", name)
				if s.CurrentTask != nil && s.CurrentTask.Name == name {
					s.CurrentTask = prevCurrentTask
					if s.CurrentTask != nil {
						feedback.Infof("Undo: Current task restored to '%s'.", s.CurrentTask.Name)
					} else {
						feedback.Infof("Undo: Current task cleared.")
					}
				}
				if wasAddedToPipeline && pipelineName != "" {
					if p, ok := s.Pipelines[pipelineName]; ok {
						p.Spec.Tasks = originalPipelineTasks // Restore the pipeline's task list
						feedback.Infof("Undo: Task '%s' removed from pipeline '%s'.", name, pipelineName)
					}
				}
			})

			feedback.Infof("Task '%s' created and set as current.", name)
			if wasAddedToPipeline {
				if len(convertToTektonWhenExpressions(whenClause)) > 0 {
					feedback.Infof("Task '%s' added to pipeline '%s' with when conditions.", name, session.CurrentPipeline.Name)
				} else {
					feedback.Infof("Task '%s' added to pipeline '%s'.", name, session.CurrentPipeline.Name)
				}
			}
			return newTask, nil
		case "select":
			if len(baseCmd.Args) != 1 {
				return nil, errorWithPosition(baseCmd.Pos, "task select expects 1 argument (name), got %d", len(baseCmd.Args))
			}
			name := baseCmd.Args[0]
			t, exists := session.Tasks[name]
			if !exists {
				return nil, errorWithPosition(baseCmd.Pos, "task '%s' not found", name)
			}
			session.CurrentTask = t
			feedback.Infof("Task '%s' selected as current.", name)
			return t, nil
		default:
			return nil, errorWithPosition(baseCmd.Pos, "unknown action '%s' for kind 'task'", baseCmd.Action)
		}
	case "param":
		if baseCmd.Action != "" {
			return nil, errorWithPosition(baseCmd.Pos, "param command does not take an action, got '%s'", baseCmd.Action)
		}
		if session.CurrentTask == nil {
			return nil, errorWithPosition(baseCmd.Pos, "no current task selected. Use 'task create <name>' or select an existing task first")
		}
		if len(baseCmd.Args) != 2 {
			return nil, errorWithPosition(baseCmd.Pos, "param command expects 2 arguments (name=, value), got %d: %v", len(baseCmd.Args), baseCmd.Args)
		}
		paramName := strings.TrimSuffix(baseCmd.Args[0], "=")
		paramValue := baseCmd.Args[1]

		// Remove quotes if present from paramValue
		if (strings.HasPrefix(paramValue, "\"") && strings.HasSuffix(paramValue, "\"")) || (strings.HasPrefix(paramValue, "`") && strings.HasSuffix(paramValue, "`")) {
			if len(paramValue) >= 2 {
				paramValue = paramValue[1 : len(paramValue)-1]
			}
		}

		taskName := session.CurrentTask.Name
		var originalParamSpec *tektonv1.ParamSpec
		originalParamIndex := -1
		paramExisted := false

		for i, p := range session.CurrentTask.Spec.Params {
			if p.Name == paramName {
				// Deep copy original for revert
				copiedSpec := p.DeepCopy()
				originalParamSpec = copiedSpec
				originalParamIndex = i
				paramExisted = true
				session.CurrentTask.Spec.Params[i].Default = &tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: paramValue}
				break
			}
		}
		if !paramExisted {
			newParamSpec := tektonv1.ParamSpec{
				Name:    paramName,
				Type:    tektonv1.ParamTypeString,
				Default: &tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: paramValue},
			}
			session.CurrentTask.Spec.Params = append(session.CurrentTask.Spec.Params, newParamSpec)
			originalParamIndex = len(session.CurrentTask.Spec.Params) - 1 // It's the last one added
		}

		session.PushRevertAction(func(s *state.Session) {
			t, ok := s.Tasks[taskName]
			if !ok {
				feedback.Errorf("Undo: Task '%s' not found for reverting param '%s'.", taskName, paramName)
				return
			}
			if paramExisted {
				if originalParamSpec != nil && originalParamIndex < len(t.Spec.Params) && t.Spec.Params[originalParamIndex].Name == paramName {
					t.Spec.Params[originalParamIndex].Default = originalParamSpec.Default // Restore original value
					feedback.Infof("Undo: Param '%s' in task '%s' restored to previous value.", paramName, taskName)
				} else {
					feedback.Errorf("Undo: Failed to restore param '%s' for task '%s'. Original state unclear.", paramName, taskName)
				}
			} else { // Param was newly added
				if originalParamIndex < len(t.Spec.Params) && t.Spec.Params[originalParamIndex].Name == paramName {
					t.Spec.Params = append(t.Spec.Params[:originalParamIndex], t.Spec.Params[originalParamIndex+1:]...)
					feedback.Infof("Undo: Param '%s' removed from task '%s'.", paramName, taskName)
				} else {
					feedback.Errorf("Undo: Failed to remove param '%s' for task '%s'. Original state unclear.", paramName, taskName)
				}
			}
		})

		feedback.Infof("Param '%s' set to '%s' for task '%s'.", paramName, paramValue, session.CurrentTask.Name)
		return session.CurrentTask, nil
	case "step":
		switch baseCmd.Action {
		case "add":
			if session.CurrentTask == nil {
				return nil, errorWithPosition(baseCmd.Pos, "no task in context. Use 'task create <name>' first")
			}
			taskNameForUndo := session.CurrentTask.Name // Capture before any potential context change
			originalStepsLen := len(session.CurrentTask.Spec.Steps)

			stepName := ""
			imageName := ""
			for _, arg := range baseCmd.Args {
				if strings.HasPrefix(arg, "--image=") {
					imageName = strings.TrimPrefix(arg, "--image=")
				} else if arg == "--image" {
				} else if !strings.HasPrefix(arg, "--") && !strings.Contains(arg, "=") {
					if stepName == "" {
						stepName = arg
					}
				}
			}
			if imageName == "" {
				for i, arg := range baseCmd.Args {
					if arg == "--image" && i+1 < len(baseCmd.Args) {
						imageName = baseCmd.Args[i+1]
						break
					}
				}
			}
			if stepName == "" {
				return nil, errorWithPosition(baseCmd.Pos, "step name not provided or could not be parsed from args: %v", baseCmd.Args)
			}
			if imageName == "" {
				return nil, errorWithPosition(baseCmd.Pos, "step image not provided for step '%s'. Use '--image <image_name>' or '--image=<image_name>'", stepName)
			}
			actualScript := baseCmd.Script
			if strings.HasPrefix(actualScript, "`") && strings.HasSuffix(actualScript, "`") {
				if len(actualScript) >= 2 {
					actualScript = actualScript[1 : len(actualScript)-1]
				}
			}
			imageName = interpolateParams(imageName, session.CurrentTask.Spec.Params)
			scriptContent := interpolateParams(actualScript, session.CurrentTask.Spec.Params)
			newStep := tektonv1.Step{
				Name:   stepName,
				Image:  imageName,
				Script: scriptContent,
			}
			session.CurrentTask.Spec.Steps = append(session.CurrentTask.Spec.Steps, newStep)

			session.PushRevertAction(func(s *state.Session) {
				task, ok := s.Tasks[taskNameForUndo]
				if !ok {
					feedback.Errorf("Undo: Task '%s' not found for reverting step add.", taskNameForUndo)
					return
				}
				if len(task.Spec.Steps) > originalStepsLen { // Check if a step was actually added
					removedStep := task.Spec.Steps[len(task.Spec.Steps)-1]
					task.Spec.Steps = task.Spec.Steps[:len(task.Spec.Steps)-1]
					feedback.Infof("Undo: Step '%s' removed from task '%s'.", removedStep.Name, taskNameForUndo)
				} else {
					feedback.Infof("Undo: No step to remove from task '%s' or steps changed unexpectedly.", taskNameForUndo)
				}
			})

			feedback.Infof("Step '%s' with image '%s' added to task '%s'.", stepName, imageName, session.CurrentTask.Name)
			if scriptContent != "" {
				feedback.Infof("Step '%s' script:\n%s", stepName, scriptContent)
			}
			return session.CurrentTask, nil
		default:
			return nil, errorWithPosition(baseCmd.Pos, "unknown action '%s' for kind 'step'", baseCmd.Action)
		}
	case "export":
		if baseCmd.Action == "all" {
			if err := ValidateSession(session); err != nil {
				return nil, errorWithPosition(cmdPos, "validation failed before export: %v", err)
			}
			yamlData, err := export.ExportAll(session)
			if err != nil {
				return nil, errorWithPosition(cmdPos, "failed to export: %v", err)
			}
			return yamlData, nil
		}
		return nil, errorWithPosition(baseCmd.Pos, "unknown action '%s' for export. Try 'export all'", baseCmd.Action)
	case "apply":
		if baseCmd.Action == "all" {
			if len(baseCmd.Args) != 1 {
				return nil, errorWithPosition(baseCmd.Pos, "apply all expects 1 argument (namespace), got %d", len(baseCmd.Args))
			}
			if err := ValidateSession(session); err != nil {
				return nil, errorWithPosition(cmdPos, "validation failed before apply: %v", err)
			}
			namespace := baseCmd.Args[0]
			err := session.ApplyAll(context.Background(), namespace)
			if err != nil {
				return nil, errorWithPosition(cmdPos, "failed to apply: %v", err)
			}
			feedback.Infof("All resources applied to namespace '%s'.", namespace) // ApplyAll prints per-resource status
			return nil, nil
		}
		return nil, errorWithPosition(baseCmd.Pos, "unknown action '%s' for apply. Try 'apply all <namespace>'", baseCmd.Action)
	case "list": // List is read-only
		switch baseCmd.Action {
		case "tasks":
			if len(baseCmd.Args) != 0 {
				return nil, errorWithPosition(baseCmd.Pos, "list tasks expects 0 arguments, got %d", len(baseCmd.Args))
			}
			if len(session.Tasks) == 0 {
				return []string{"No tasks defined."}, nil
			}
			names := make([]string, 0, len(session.Tasks))
			for name := range session.Tasks {
				names = append(names, name)
			}
			sort.Strings(names)
			return names, nil
		case "pipelines":
			if len(baseCmd.Args) != 0 {
				return nil, errorWithPosition(baseCmd.Pos, "list pipelines expects 0 arguments, got %d", len(baseCmd.Args))
			}
			if len(session.Pipelines) == 0 {
				return []string{"No pipelines defined."}, nil
			}
			names := make([]string, 0, len(session.Pipelines))
			for name := range session.Pipelines {
				names = append(names, name)
			}
			sort.Strings(names)
			return names, nil
		case "stepactions":
			if len(baseCmd.Args) != 0 {
				return nil, errorWithPosition(baseCmd.Pos, "list stepactions expects 0 arguments, got %d", len(baseCmd.Args))
			}
			return []string{"list stepactions is not implemented yet"}, nil
		default:
			return nil, errorWithPosition(baseCmd.Pos, "unknown action '%s' for kind 'list'. Try 'tasks', 'pipelines', or 'stepactions'.", baseCmd.Action)
		}
	case "show": // Show is read-only
		switch baseCmd.Action {
		case "task":
			if len(baseCmd.Args) != 1 {
				return nil, errorWithPosition(baseCmd.Pos, "show task expects 1 argument (name), got %d", len(baseCmd.Args))
			}
			name := baseCmd.Args[0]
			task, exists := session.Tasks[name]
			if !exists {
				return nil, errorWithPosition(baseCmd.Pos, "task '%s' not found", name)
			}
			taskToShow := task.DeepCopy()
			taskToShow.APIVersion = tektonv1.SchemeGroupVersion.String()
			taskToShow.Kind = "Task"
			yamlBytes, err := yaml.Marshal(taskToShow)
			if err != nil {
				return nil, errorWithPosition(baseCmd.Pos, "failed to marshal task '%s' to YAML: %w", name, err)
			}
			return yamlBytes, nil
		case "pipeline":
			if len(baseCmd.Args) != 1 {
				return nil, errorWithPosition(baseCmd.Pos, "show pipeline expects 1 argument (name), got %d", len(baseCmd.Args))
			}
			name := baseCmd.Args[0]
			pipeline, exists := session.Pipelines[name]
			if !exists {
				return nil, errorWithPosition(baseCmd.Pos, "pipeline '%s' not found", name)
			}
			pipelineToShow := pipeline.DeepCopy()
			pipelineToShow.APIVersion = tektonv1.SchemeGroupVersion.String()
			pipelineToShow.Kind = "Pipeline"
			yamlBytes, err := yaml.Marshal(pipelineToShow)
			if err != nil {
				return nil, errorWithPosition(baseCmd.Pos, "failed to marshal pipeline '%s' to YAML: %w", name, err)
			}
			return yamlBytes, nil
		default:
			return nil, errorWithPosition(baseCmd.Pos, "unknown action '%s' for kind 'show'. Try 'task <name>' or 'pipeline <name>'.", baseCmd.Action)
		}
	case "undo":
		if len(baseCmd.Args) > 0 || baseCmd.Action != "" {
			return nil, errorWithPosition(baseCmd.Pos, "undo command does not take arguments or actions")
		}
		revertFunc := session.PopRevertAction()
		if revertFunc != nil {
			revertFunc(session)
			// feedback.Infof("Last action undone.") // Feedback is now in the RevertFunc
		} else {
			feedback.Infof("No actions to undo.")
		}
		return nil, nil
	case "reset":
		if len(baseCmd.Args) > 0 || baseCmd.Action != "" {
			return nil, errorWithPosition(baseCmd.Pos, "reset command does not take arguments or actions")
		}
		session.Reset()
		feedback.Infof("Session reset. All pipelines, tasks, and undo history cleared.")
		return nil, nil
	case "validate":
		if len(baseCmd.Args) > 0 || baseCmd.Action != "" {
			return nil, errorWithPosition(baseCmd.Pos, "validate command does not take arguments or actions")
		}
		err := ValidateSession(session)
		if err != nil {
			return nil, errorWithPosition(cmdPos, "validation failed: %v", err)
		}
		feedback.Infof("✅ no issues")
		return nil, nil
	default:
		return nil, errorWithPosition(baseCmd.Pos, "unknown command kind '%s'", baseCmd.Kind)
	}
}

func getStepByName(task *tektonv1.Task, stepName string) (tektonv1.Step, int, bool) {
	for i, step := range task.Spec.Steps {
		if step.Name == stepName {
			return step, i, true
		}
	}
	return tektonv1.Step{}, -1, false
}
