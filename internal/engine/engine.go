package engine

import (
	"fmt"
	"strings"

	"tkn-shell/internal/export"
	"tkn-shell/internal/parser"
	"tkn-shell/internal/state"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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

// ExecuteCommand processes a parsed command and updates the session state.
func ExecuteCommand(cmd *parser.Command, session *state.Session, prevResult any) (any, error) {
	switch cmd.Kind {
	case "pipeline":
		switch cmd.Action {
		case "create":
			if len(cmd.Args) != 1 {
				return nil, fmt.Errorf("pipeline create expects 1 argument (name), got %d", len(cmd.Args))
			}
			name := cmd.Args[0]
			if _, exists := session.Pipelines[name]; exists {
				return nil, fmt.Errorf("pipeline %s already exists", name)
			}
			newPipeline := &tektonv1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: tektonv1.PipelineSpec{}, // Initialize spec
			}
			session.Pipelines[name] = newPipeline
			session.CurrentPipeline = newPipeline
			session.CurrentTask = nil // Reset current task when a new pipeline is created or selected
			fmt.Printf("Pipeline '%s' created and set as current.\\n", name)
			return newPipeline, nil
		default:
			return nil, fmt.Errorf("unknown action '%s' for kind 'pipeline'", cmd.Action)
		}
	case "task":
		switch cmd.Action {
		case "create":
			if len(cmd.Args) != 1 {
				return nil, fmt.Errorf("task create expects 1 argument (name), got %d", len(cmd.Args))
			}
			name := cmd.Args[0]
			if _, exists := session.Tasks[name]; exists {
				return nil, fmt.Errorf("task %s already exists", name)
			}
			newTask := &tektonv1.Task{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: tektonv1.TaskSpec{}, // Initialize spec
			}
			session.Tasks[name] = newTask
			session.CurrentTask = newTask
			fmt.Printf("Task '%s' created and set as current.\\n", name)

			if session.CurrentPipeline != nil {
				taskExistsInPipeline := false
				for _, pt := range session.CurrentPipeline.Spec.Tasks {
					if pt.Name == name || (pt.TaskRef != nil && pt.TaskRef.Name == name) {
						taskExistsInPipeline = true
						break
					}
				}
				if !taskExistsInPipeline {
					pipelineTask := tektonv1.PipelineTask{
						Name: name,
						TaskRef: &tektonv1.TaskRef{
							Name: name,
							Kind: tektonv1.NamespacedTaskKind,
						},
					}
					session.CurrentPipeline.Spec.Tasks = append(session.CurrentPipeline.Spec.Tasks, pipelineTask)
					fmt.Printf("Task '%s' added to pipeline '%s'.\\n", name, session.CurrentPipeline.Name)
				} else {
					fmt.Printf("Task '%s' already exists in pipeline '%s'.\\n", name, session.CurrentPipeline.Name)
				}
			}
			return newTask, nil
		default:
			return nil, fmt.Errorf("unknown action '%s' for kind 'task'", cmd.Action)
		}
	case "param":
		if cmd.Action != "" {
			return nil, fmt.Errorf("param command does not take an action, got '%s'", cmd.Action)
		}
		if session.CurrentTask == nil {
			return nil, fmt.Errorf("no current task selected. Use 'task create <name>' or select an existing task first")
		}
		if len(cmd.Args) != 2 {
			return nil, fmt.Errorf("param command expects 2 arguments (name=, value), got %d: %v", len(cmd.Args), cmd.Args)
		}

		paramName := strings.TrimSuffix(cmd.Args[0], "=")
		paramValue := cmd.Args[1]

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
		switch cmd.Action {
		case "add":
			if session.CurrentTask == nil {
				return nil, fmt.Errorf("no current task selected. Use 'task create <name>' or 'task select <name>' first")
			}
			stepName := ""
			imageName := ""

			// Parse Args for step name and --image flag
			// First non-flag, non-assignment arg is the step name
			// This simplified parsing might need to be more robust for complex args.
			remainingArgs := []string{}
			for _, arg := range cmd.Args {
				if strings.HasPrefix(arg, "--image=") {
					imageName = strings.TrimPrefix(arg, "--image=")
				} else if arg == "--image" {
					// Next arg should be the image name, handled below
				} else if !strings.HasPrefix(arg, "--") && !strings.Contains(arg, "=") {
					if stepName == "" {
						stepName = arg
					} else {
						// Could be script args, not handled yet by Tekton Step struct directly this way
						remainingArgs = append(remainingArgs, arg)
					}
				} else {
					remainingArgs = append(remainingArgs, arg)
				}
			}

			// If --image was separate, find its value
			if imageName == "" {
				for i, arg := range cmd.Args {
					if arg == "--image" && i+1 < len(cmd.Args) {
						imageName = cmd.Args[i+1]
						// Remove --image and its value from remainingArgs if they were added
						// This part is tricky with the current simple arg parsing.
						break
					}
				}
			}

			if stepName == "" {
				return nil, fmt.Errorf("step name not provided or could not be parsed from args: %v", cmd.Args)
			}
			if imageName == "" {
				return nil, fmt.Errorf("step image not provided for step '%s'. Use '--image <image_name>' or '--image=<image_name>'", stepName)
			}

			// Unquote RawString for script content
			actualScript := cmd.Script
			if strings.HasPrefix(actualScript, "`") && strings.HasSuffix(actualScript, "`") {
				if len(actualScript) >= 2 {
					actualScript = actualScript[1 : len(actualScript)-1]
				}
			}

			// Interpolate params
			imageName = interpolateParams(imageName, session.CurrentTask.Spec.Params)
			scriptContent := interpolateParams(actualScript, session.CurrentTask.Spec.Params)

			newStep := tektonv1.Step{
				Name:   stepName,
				Image:  imageName,
				Script: scriptContent,
				// Args for the command run in the image can also be interpolated if added.
			}
			session.CurrentTask.Spec.Steps = append(session.CurrentTask.Spec.Steps, newStep)
			fmt.Printf("Step '%s' with image '%s' added to task '%s'.\\n", stepName, imageName, session.CurrentTask.Name)
			if scriptContent != "" {
				fmt.Printf("Step '%s' script:\n%s\\n", stepName, scriptContent)
			}
			return session.CurrentTask, nil
		default:
			return nil, fmt.Errorf("unknown action '%s' for kind 'step'", cmd.Action)
		}
	case "export":
		switch cmd.Action {
		case "all":
			yamlOutput, err := export.ExportAll(session)
			if err != nil {
				return nil, fmt.Errorf("failed to export all resources: %w", err)
			}
			if yamlOutput == "" {
				fmt.Println("# No resources to export.")
			} else {
				fmt.Println(yamlOutput)
			}
			return yamlOutput, nil // Return the YAML string as the result
		default:
			return nil, fmt.Errorf("unknown action '%s' for kind 'export'", cmd.Action)
		}
	default:
		return nil, fmt.Errorf("unknown command kind: %s", cmd.Kind)
	}
}
