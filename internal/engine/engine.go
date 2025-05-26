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

// CommandExecutorSession defines the interface ExecuteCommand uses to interact with the session.
// This allows for easier mocking in tests.
type CommandExecutorSession interface {
	// Pipeline operations
	GetPipelines() map[string]*tektonv1.Pipeline
	SetCurrentPipeline(p *tektonv1.Pipeline)
	GetCurrentPipeline() *tektonv1.Pipeline
	AddPipeline(name string, p *tektonv1.Pipeline)
	DeletePipeline(name string)
	RunPipeline(ctx context.Context, pipelineName string, params []tektonv1.Param, namespace string) (*tektonv1.PipelineRun, error)

	// Task operations
	GetTasks() map[string]*tektonv1.Task
	SetCurrentTask(t *tektonv1.Task)
	GetCurrentTask() *tektonv1.Task
	AddTask(name string, t *tektonv1.Task)
	DeleteTask(name string)
	RunTask(ctx context.Context, taskName string, params []tektonv1.Param, namespace string) (*tektonv1.TaskRun, error)

	// Undo operations
	// Note: state.RevertFunc takes a concrete *state.Session. This is a compromise
	// to avoid more invasive changes to the RevertFunc type itself for now.
	// Mocks will need to provide a PushRevertAction that can accept this type.
	PushRevertAction(revert state.RevertFunc)
	PopRevertAction() state.RevertFunc

	// General state operations
	// ApplyAll(ctx context.Context, ns string) error // ApplyAll is not directly called by ExecuteCommand
	Reset() // Called by "reset" command
}

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
	Apply(session CommandExecutorSession, prevResult any) (any, error)
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
func ExecuteCommand(cmdPos lexer.Position, baseCmd *parser.BaseCommand, session CommandExecutorSession, prevResult any, whenClause *parser.WhenClause) (any, error) {
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
			if _, exists := session.GetPipelines()[name]; exists {
				return nil, errorWithPosition(baseCmd.Pos, "pipeline %s already exists", name)
			}
			newPipeline := &tektonv1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: tektonv1.PipelineSpec{}, // Initialize spec
			}
			session.AddPipeline(name, newPipeline)
			prevCurrentPipeline := session.GetCurrentPipeline() // Capture for undo
			prevCurrentTask := session.GetCurrentTask()         // Capture for undo
			session.SetCurrentPipeline(newPipeline)
			session.SetCurrentTask(nil)

			session.PushRevertAction(func(s *state.Session) {
				s.DeletePipeline(name)
				feedback.Infof("Undo: Pipeline '%s' deleted.", name)
				// Try to restore previous context, if this was the one being made current
				// This logic might need refinement if select also gets undo
				if s.GetCurrentPipeline() != nil && s.GetCurrentPipeline().Name == name {
					s.SetCurrentPipeline(prevCurrentPipeline)
					s.SetCurrentTask(prevCurrentTask) // Restore task context too, as pipeline change clears it
					if s.GetCurrentPipeline() != nil {
						feedback.Infof("Undo: Current pipeline restored to '%s'.", s.GetCurrentPipeline().Name)
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
			p, exists := session.GetPipelines()[name]
			if !exists {
				return nil, errorWithPosition(baseCmd.Pos, "pipeline %s not found", name)
			}
			session.SetCurrentPipeline(p)
			session.SetCurrentTask(nil) // Clear task context when pipeline changes
			feedback.Infof("Pipeline '%s' selected as current.", name)
			return p, nil
		case "run":
			if len(baseCmd.Args) < 1 {
				return nil, errorWithPosition(baseCmd.Pos, "pipeline run expects at least 1 argument (pipeline_name), got 0")
			}
			pipelineName := baseCmd.Args[0]
			_, exists := session.GetPipelines()[pipelineName]
			if !exists {
				return nil, errorWithPosition(baseCmd.Pos, "pipeline '%s' not found in session", pipelineName)
			}

			var runParams []tektonv1.Param
			runNamespace := "default" // Default namespace, can be overridden

			// Start parsing from baseCmd.Args[1]
			args := baseCmd.Args[1:]
			for i := 0; i < len(args); i++ {
				switch args[i] {
				case "param":
					// Check for "param name= value" format first, as it's the primary expectation from parser
					if i+2 < len(args) { // Need at least two tokens after "param": name= and value
						paramNameArg := args[i+1]  // Expected: "name="
						paramValueArg := args[i+2] // Expected: "value"

						if strings.HasSuffix(paramNameArg, "=") {
							paramName := strings.TrimSuffix(paramNameArg, "=")
							if paramName == "" {
								return nil, errorWithPosition(baseCmd.Pos, "invalid param format: param name cannot be empty in '%s'", paramNameArg)
							}
							paramValue := paramValueArg
							// Unquote value
							if len(paramValue) >= 2 {
								firstChar := paramValue[0]
								lastChar := paramValue[len(paramValue)-1]
								if (firstChar == '"' && lastChar == '"') || (firstChar == '\'' && lastChar == '\'') {
									paramValue = paramValue[1 : len(paramValue)-1]
								}
							}
							runParams = append(runParams, tektonv1.Param{
								Name:  paramName,
								Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: paramValue},
							})
							i += 2 // Consumed "name=" and "value"
						} else {
							// This is for "param name value" (e.g. param image "nginx:latest") - invalid
							return nil, errorWithPosition(baseCmd.Pos, "invalid param format: expected <name>=, got '%s'", paramNameArg)
						}
					} else if i+1 < len(args) && strings.Contains(args[i+1], "=") && !strings.HasSuffix(args[i+1], "=") {
						// Fallback for "param name=value" (single token)
						parts := strings.SplitN(args[i+1], "=", 2)
						// Should be len(parts) == 2 due to checks, but verify name part again
						if parts[0] == "" {
							return nil, errorWithPosition(baseCmd.Pos, "invalid param format: param name cannot be empty in <name>=<value>, got '%s'", args[i+1])
						}
						paramName := parts[0]
						paramValue := parts[1]
						// Unquote value
						if len(paramValue) >= 2 {
							firstChar := paramValue[0]
							lastChar := paramValue[len(paramValue)-1]
							if (firstChar == '"' && lastChar == '"') || (firstChar == '\'' && lastChar == '\'') {
								paramValue = paramValue[1 : len(paramValue)-1]
							}
						}
						runParams = append(runParams, tektonv1.Param{
							Name:  paramName,
							Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: paramValue},
						})
						i++ // Consumed "name=value"
					} else {
						// Not enough arguments for any valid param format or malformed.
						if i+1 < len(args) {
							return nil, errorWithPosition(baseCmd.Pos, "invalid param format near '%s'. Expected <name>=<value> or <name>= <value>", args[i+1])
						}
						return nil, errorWithPosition(baseCmd.Pos, "incomplete 'param' definition after 'param' keyword")
					}
				case "namespace":
					if i+1 >= len(args) {
						return nil, errorWithPosition(baseCmd.Pos, "'namespace' keyword must be followed by a namespace name")
					}
					runNamespace = args[i+1]
					i++ // Consumed namespace name
				default:
					return nil, errorWithPosition(baseCmd.Pos, "unexpected argument '%s' for pipeline run", args[i])
				}
			}

			_, err := session.RunPipeline(context.Background(), pipelineName, runParams, runNamespace)
			if err != nil {
				// The RunPipeline method already calls feedback.Infof on success/failure details.
				// We just need to return the error to the REPL to display if it was critical.
				return nil, errorWithPosition(cmdPos, "failed to run pipeline '%s': %v", pipelineName, err)
			}
			// If RunPipeline is successful, it would have printed detailed feedback.
			// We can add a simple confirmation here or rely on RunPipeline's feedback.
			feedback.Infof("Pipeline '%s' run initiated.", pipelineName)
			return nil, nil
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
			if _, exists := session.GetTasks()[name]; exists {
				return nil, errorWithPosition(baseCmd.Pos, "task %s already exists", name)
			}
			newTask := &tektonv1.Task{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: tektonv1.TaskSpec{}, // Initialize spec
			}
			session.AddTask(name, newTask)
			prevCurrentTask := session.GetCurrentTask() // Capture for undo
			session.SetCurrentTask(newTask)

			wasAddedToPipeline := false
			pipelineName := ""
			var originalPipelineTasks []tektonv1.PipelineTask

			if session.GetCurrentPipeline() != nil {
				pipelineName = session.GetCurrentPipeline().Name
				// Store a copy of the pipeline's tasks *before* modification
				originalPipelineTasks = make([]tektonv1.PipelineTask, len(session.GetCurrentPipeline().Spec.Tasks))
				copy(originalPipelineTasks, session.GetCurrentPipeline().Spec.Tasks)

				var existingPipelineTask *tektonv1.PipelineTask
				ptIndex := -1
				for i, pt := range session.GetCurrentPipeline().Spec.Tasks {
					if pt.Name == name || (pt.TaskRef != nil && pt.TaskRef.Name == name) {
						existingPipelineTask = &session.GetCurrentPipeline().Spec.Tasks[i]
						ptIndex = i
						break
					}
				}
				tektonWhens := convertToTektonWhenExpressions(whenClause)
				if existingPipelineTask != nil {
					// This part of create logic seems to imply updating existing, which might be complex for undo
					// For now, focusing on undoing the creation and simple addition.
					session.GetCurrentPipeline().Spec.Tasks[ptIndex].When = tektonWhens
					wasAddedToPipeline = true // Or updated
				} else {
					pipelineTask := tektonv1.PipelineTask{
						Name:    name,
						TaskRef: &tektonv1.TaskRef{Name: name, Kind: tektonv1.NamespacedTaskKind},
						When:    tektonWhens,
					}
					session.GetCurrentPipeline().Spec.Tasks = append(session.GetCurrentPipeline().Spec.Tasks, pipelineTask)
					wasAddedToPipeline = true
				}
			}

			session.PushRevertAction(func(s *state.Session) {
				s.DeleteTask(name)
				feedback.Infof("Undo: Task '%s' deleted.", name)
				if s.GetCurrentTask() != nil && s.GetCurrentTask().Name == name {
					s.SetCurrentTask(prevCurrentTask)
					if s.GetCurrentTask() != nil {
						feedback.Infof("Undo: Current task restored to '%s'.", s.GetCurrentTask().Name)
					} else {
						feedback.Infof("Undo: Current task cleared.")
					}
				}
				if wasAddedToPipeline && pipelineName != "" {
					if p, ok := s.GetPipelines()[pipelineName]; ok {
						p.Spec.Tasks = originalPipelineTasks // Restore the pipeline's task list
						feedback.Infof("Undo: Task '%s' removed from pipeline '%s'.", name, pipelineName)
					}
				}
			})

			feedback.Infof("Task '%s' created and set as current.", name)
			if wasAddedToPipeline {
				if len(convertToTektonWhenExpressions(whenClause)) > 0 {
					feedback.Infof("Task '%s' added to pipeline '%s' with when conditions.", name, session.GetCurrentPipeline().Name)
				} else {
					feedback.Infof("Task '%s' added to pipeline '%s'.", name, session.GetCurrentPipeline().Name)
				}
			}
			return newTask, nil
		case "select":
			if len(baseCmd.Args) != 1 {
				return nil, errorWithPosition(baseCmd.Pos, "task select expects 1 argument (name), got %d", len(baseCmd.Args))
			}
			name := baseCmd.Args[0]
			t, exists := session.GetTasks()[name]
			if !exists {
				return nil, errorWithPosition(baseCmd.Pos, "task '%s' not found", name)
			}
			session.SetCurrentTask(t)
			feedback.Infof("Task '%s' selected as current.", name)
			return t, nil
		case "run":
			if len(baseCmd.Args) < 1 {
				return nil, errorWithPosition(baseCmd.Pos, "task run expects at least 1 argument (task_name), got 0")
			}
			taskName := baseCmd.Args[0]
			_, exists := session.GetTasks()[taskName]
			if !exists {
				return nil, errorWithPosition(baseCmd.Pos, "task '%s' not found in session", taskName)
			}

			var runParams []tektonv1.Param
			runNamespace := "default" // Default namespace

			args := baseCmd.Args[1:]
			for i := 0; i < len(args); i++ {
				switch args[i] {
				case "param":
					// Check for "param name= value" format first, as it's the primary expectation from parser
					if i+2 < len(args) { // Need at least two tokens after "param": name= and value
						paramNameArg := args[i+1]  // Expected: "name="
						paramValueArg := args[i+2] // Expected: "value"

						if strings.HasSuffix(paramNameArg, "=") {
							paramName := strings.TrimSuffix(paramNameArg, "=")
							if paramName == "" {
								return nil, errorWithPosition(baseCmd.Pos, "invalid param format: param name cannot be empty in '%s'", paramNameArg)
							}
							paramValue := paramValueArg
							// Unquote value
							if len(paramValue) >= 2 {
								firstChar := paramValue[0]
								lastChar := paramValue[len(paramValue)-1]
								if (firstChar == '"' && lastChar == '"') || (firstChar == '\'' && lastChar == '\'') {
									paramValue = paramValue[1 : len(paramValue)-1]
								}
							}
							runParams = append(runParams, tektonv1.Param{
								Name:  paramName,
								Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: paramValue},
							})
							i += 2 // Consumed "name=" and "value"
						} else {
							// This is for "param name value" (e.g. param image "nginx:latest") - invalid
							return nil, errorWithPosition(baseCmd.Pos, "invalid param format: expected <name>=, got '%s'", paramNameArg)
						}
					} else if i+1 < len(args) && strings.Contains(args[i+1], "=") && !strings.HasSuffix(args[i+1], "=") {
						// Fallback for "param name=value" (single token)
						parts := strings.SplitN(args[i+1], "=", 2)
						// Should be len(parts) == 2 due to checks, but verify name part again
						if parts[0] == "" {
							return nil, errorWithPosition(baseCmd.Pos, "invalid param format: param name cannot be empty in <name>=<value>, got '%s'", args[i+1])
						}
						paramName := parts[0]
						paramValue := parts[1]
						// Unquote value
						if len(paramValue) >= 2 {
							firstChar := paramValue[0]
							lastChar := paramValue[len(paramValue)-1]
							if (firstChar == '"' && lastChar == '"') || (firstChar == '\'' && lastChar == '\'') {
								paramValue = paramValue[1 : len(paramValue)-1]
							}
						}
						runParams = append(runParams, tektonv1.Param{
							Name:  paramName,
							Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: paramValue},
						})
						i++ // Consumed "name=value"
					} else {
						// Not enough arguments for any valid param format or malformed.
						if i+1 < len(args) {
							return nil, errorWithPosition(baseCmd.Pos, "invalid param format near '%s'. Expected <name>=<value> or <name>= <value>", args[i+1])
						}
						return nil, errorWithPosition(baseCmd.Pos, "incomplete 'param' definition after 'param' keyword")
					}
				case "namespace":
					if i+1 >= len(args) {
						return nil, errorWithPosition(baseCmd.Pos, "'namespace' keyword must be followed by a namespace name")
					}
					runNamespace = args[i+1]
					i++ // Consumed namespace name
				default:
					return nil, errorWithPosition(baseCmd.Pos, "unexpected argument '%s' for task run", args[i])
				}
			}

			// Placeholder for actual run logic - this will be a call to session.RunTask(...)
			_, err := session.RunTask(context.Background(), taskName, runParams, runNamespace)
			if err != nil {
				return nil, errorWithPosition(cmdPos, "failed to run task '%s': %v", taskName, err)
			}
			feedback.Infof("Task '%s' run initiated.", taskName)
			return nil, nil
		default:
			return nil, errorWithPosition(baseCmd.Pos, "unknown action '%s' for kind 'task'", baseCmd.Action)
		}
	case "param":
		if baseCmd.Action != "" {
			return nil, errorWithPosition(baseCmd.Pos, "param command does not take an action, got '%s'", baseCmd.Action)
		}
		if session.GetCurrentTask() == nil {
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

		taskName := session.GetCurrentTask().Name
		var originalParamSpec *tektonv1.ParamSpec
		originalParamIndex := -1
		paramExisted := false

		for i, p := range session.GetCurrentTask().Spec.Params {
			if p.Name == paramName {
				// Deep copy original for revert
				copiedSpec := p.DeepCopy()
				originalParamSpec = copiedSpec
				originalParamIndex = i
				paramExisted = true
				session.GetCurrentTask().Spec.Params[i].Default = &tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: paramValue}
				break
			}
		}
		if !paramExisted {
			newParamSpec := tektonv1.ParamSpec{
				Name:    paramName,
				Type:    tektonv1.ParamTypeString,
				Default: &tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: paramValue},
			}
			session.GetCurrentTask().Spec.Params = append(session.GetCurrentTask().Spec.Params, newParamSpec)
			originalParamIndex = len(session.GetCurrentTask().Spec.Params) - 1 // It's the last one added
		}

		session.PushRevertAction(func(s *state.Session) {
			t, ok := s.GetTasks()[taskName]
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

		feedback.Infof("Param '%s' set to '%s' for task '%s'.", paramName, paramValue, session.GetCurrentTask().Name)
		return session.GetCurrentTask(), nil
	case "step":
		switch baseCmd.Action {
		case "add":
			if session.GetCurrentTask() == nil {
				return nil, errorWithPosition(baseCmd.Pos, "no task in context. Use 'task create <name>' first")
			}
			taskNameForUndo := session.GetCurrentTask().Name // Capture before any potential context change
			originalStepsLen := len(session.GetCurrentTask().Spec.Steps)

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
			imageName = interpolateParams(imageName, session.GetCurrentTask().Spec.Params)
			scriptContent := interpolateParams(actualScript, session.GetCurrentTask().Spec.Params)
			newStep := tektonv1.Step{
				Name:   stepName,
				Image:  imageName,
				Script: scriptContent,
			}
			session.GetCurrentTask().Spec.Steps = append(session.GetCurrentTask().Spec.Steps, newStep)

			session.PushRevertAction(func(s *state.Session) {
				task, ok := s.GetTasks()[taskNameForUndo]
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

			feedback.Infof("Step '%s' with image '%s' added to task '%s'.", stepName, imageName, session.GetCurrentTask().Name)
			if scriptContent != "" {
				feedback.Infof("Step '%s' script:\n%s", stepName, scriptContent)
			}
			return session.GetCurrentTask(), nil
		default:
			return nil, errorWithPosition(baseCmd.Pos, "unknown action '%s' for kind 'step'", baseCmd.Action)
		}
	case "export":
		if baseCmd.Action == "all" {
			// Cast to *state.Session for ValidateSession and ExportAll as they are not part of the interface
			// and expect the concrete type. This is a known compromise.
			concreteSession, ok := session.(*state.Session)
			if !ok {
				return nil, errorWithPosition(cmdPos, "internal error: session is not of type *state.Session for export")
			}
			if err := ValidateSession(concreteSession); err != nil {
				return nil, errorWithPosition(cmdPos, "validation failed before export: %v", err)
			}
			yamlData, err := export.ExportAll(concreteSession)
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
			// Cast to *state.Session for ValidateSession and ApplyAll
			concreteSession, ok := session.(*state.Session)
			if !ok {
				return nil, errorWithPosition(cmdPos, "internal error: session is not of type *state.Session for apply")
			}
			if err := ValidateSession(concreteSession); err != nil {
				return nil, errorWithPosition(cmdPos, "validation failed before apply: %v", err)
			}
			namespace := baseCmd.Args[0]
			err := concreteSession.ApplyAll(context.Background(), namespace) // ApplyAll is a method on *state.Session
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
			if len(session.GetTasks()) == 0 {
				return []string{"No tasks defined."}, nil
			}
			names := make([]string, 0, len(session.GetTasks()))
			for name := range session.GetTasks() {
				names = append(names, name)
			}
			sort.Strings(names)
			return names, nil
		case "pipelines":
			if len(baseCmd.Args) != 0 {
				return nil, errorWithPosition(baseCmd.Pos, "list pipelines expects 0 arguments, got %d", len(baseCmd.Args))
			}
			if len(session.GetPipelines()) == 0 {
				return []string{"No pipelines defined."}, nil
			}
			names := make([]string, 0, len(session.GetPipelines()))
			for name := range session.GetPipelines() {
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
			task, exists := session.GetTasks()[name]
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
			pipeline, exists := session.GetPipelines()[name]
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
			// Cast to *state.Session as RevertFunc expects the concrete type.
			concreteSession, ok := session.(*state.Session)
			if !ok {
				return nil, errorWithPosition(cmdPos, "internal error: session is not of type *state.Session for undo")
			}
			revertFunc(concreteSession)
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
		// Cast to *state.Session for ValidateSession
		concreteSession, ok := session.(*state.Session)
		if !ok {
			return nil, errorWithPosition(cmdPos, "internal error: session is not of type *state.Session for validate")
		}
		err := ValidateSession(concreteSession)
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
