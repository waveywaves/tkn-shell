package engine

import (
	"context"
	"fmt"
	"strings"

	"tkn-shell/internal/export"
	"tkn-shell/internal/parser"
	"tkn-shell/internal/state"

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
			fmt.Printf("%s Unknown when operator '%s', skipping condition.\n",
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
			session.CurrentPipeline = newPipeline
			session.CurrentTask = nil
			fmt.Printf("Pipeline '%s' created and set as current.\n", name)
			return newPipeline, nil
		case "select":
			if len(baseCmd.Args) != 1 {
				return nil, errorWithPosition(baseCmd.Pos, "pipeline select expects 1 argument (name), got %d", len(baseCmd.Args))
			}
			name := baseCmd.Args[0]
			p, exists := session.Pipelines[name]
			if !exists {
				return nil, errorWithPosition(baseCmd.Pos, "pipeline '%s' not found", name)
			}
			session.CurrentPipeline = p
			session.CurrentTask = nil // Reset current task when a new pipeline is selected
			fmt.Printf("Pipeline '%s' selected as current. Current task cleared.\n", name)
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
			session.CurrentTask = newTask
			fmt.Printf("Task '%s' created and set as current.\n", name)

			if session.CurrentPipeline != nil {
				// Check if task already exists in pipeline, if so, maybe update its WhenExpressions?
				// For now, we add a new PipelineTask or update an existing one's WhenExpressions.
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
					// Task already in pipeline, update its WhenExpressions
					session.CurrentPipeline.Spec.Tasks[ptIndex].When = tektonWhens
					if len(tektonWhens) > 0 {
						fmt.Printf("When conditions updated for task '%s' in pipeline '%s'.\n", name, session.CurrentPipeline.Name)
					} else {
						// If whenClause is nil/empty, ensure When is nil (clearing previous conditions)
						session.CurrentPipeline.Spec.Tasks[ptIndex].When = nil
					}
				} else {
					pipelineTask := tektonv1.PipelineTask{
						Name: name, // Name of the PipelineTask instance
						TaskRef: &tektonv1.TaskRef{
							Name: name, // Name of the Task definition
							Kind: tektonv1.NamespacedTaskKind,
						},
						When: tektonWhens,
					}
					session.CurrentPipeline.Spec.Tasks = append(session.CurrentPipeline.Spec.Tasks, pipelineTask)
					fmt.Printf("Task '%s' added to pipeline '%s'", name, session.CurrentPipeline.Name)
					if len(tektonWhens) > 0 {
						fmt.Printf(" with when conditions.\n")
					} else {
						fmt.Printf(".\n")
					}
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
			// Optionally, if a task is selected, we might want to ensure CurrentPipeline is the one it belongs to,
			// if we start associating tasks with pipelines more directly in the session state beyond Pipeline.Spec.Tasks.
			// For now, just selecting the task is fine.
			fmt.Printf("Task '%s' selected as current.\n", name)
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

		found := false
		for i, p := range session.CurrentTask.Spec.Params {
			if p.Name == paramName {
				session.CurrentTask.Spec.Params[i].Default = &tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: paramValue}
				found = true
				break
			}
		}
		if !found {
			newParamSpec := tektonv1.ParamSpec{
				Name:    paramName,
				Type:    tektonv1.ParamTypeString,
				Default: &tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: paramValue},
			}
			session.CurrentTask.Spec.Params = append(session.CurrentTask.Spec.Params, newParamSpec)
		}
		fmt.Printf("Param '%s' set to '%s' for task '%s'.\\n", paramName, paramValue, session.CurrentTask.Name)
		return session.CurrentTask, nil
	case "step":
		switch baseCmd.Action {
		case "add":
			if session.CurrentTask == nil {
				return nil, errorWithPosition(baseCmd.Pos, "no task in context. Use 'task create <name>' first")
			}
			stepName := ""
			imageName := ""
			// Parse Args for step name and --image flag
			for _, arg := range baseCmd.Args {
				if strings.HasPrefix(arg, "--image=") {
					imageName = strings.TrimPrefix(arg, "--image=")
				} else if arg == "--image" {
					// Next arg should be the image name, handled below
				} else if !strings.HasPrefix(arg, "--") && !strings.Contains(arg, "=") {
					if stepName == "" {
						stepName = arg
					} // else other args not captured explicitly for step fields yet
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
			fmt.Printf("Step '%s' with image '%s' added to task '%s'.\\n", stepName, imageName, session.CurrentTask.Name)
			if scriptContent != "" {
				fmt.Printf("Step '%s' script:\n%s\\n", stepName, scriptContent)
			}
			return session.CurrentTask, nil
		default:
			return nil, errorWithPosition(baseCmd.Pos, "unknown action '%s' for kind 'step'", baseCmd.Action)
		}
	case "export":
		switch baseCmd.Action {
		case "all":
			yamlOutput, err := export.ExportAll(session)
			if err != nil {
				return nil, errorWithPosition(baseCmd.Pos, "failed to export all resources: %w", err)
			}
			if yamlOutput == "" {
				fmt.Println("# No resources to export.")
			} else {
				fmt.Println(yamlOutput)
			}
			return yamlOutput, nil
		default:
			return nil, errorWithPosition(baseCmd.Pos, "unknown action '%s' for kind 'export'", baseCmd.Action)
		}
	case "apply":
		switch baseCmd.Action {
		case "all":
			if len(baseCmd.Args) != 1 {
				return nil, errorWithPosition(baseCmd.Pos, "apply all expects 1 argument (namespace), got %d", len(baseCmd.Args))
			}
			ns := baseCmd.Args[0]
			if ns == "" {
				return nil, errorWithPosition(baseCmd.Pos, "namespace cannot be empty for apply all")
			}
			fmt.Printf("Applying all resources to namespace '%s'...\n", ns)
			// Use context.Background() for now, can be made configurable if needed
			err := session.ApplyAll(context.Background(), ns)
			if err != nil {
				return nil, errorWithPosition(baseCmd.Pos, "failed to apply resources: %w", err)
			}
			fmt.Println("All resources applied successfully.")
			return nil, nil // Or some status object
		default:
			return nil, errorWithPosition(baseCmd.Pos, "unknown action '%s' for kind 'apply'", baseCmd.Action)
		}
	default:
		return nil, errorWithPosition(baseCmd.Pos, "unknown command kind: %s", baseCmd.Kind)
	}
}
