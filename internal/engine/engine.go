package engine

import (
	"fmt"

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
	case "step":
		switch cmd.Action {
		case "add":
			if session.CurrentTask == nil {
				return nil, fmt.Errorf("no current task selected. Use 'task create <name>' or 'task select <name>' first")
			}
			stepName := ""
			imageName := ""

			if len(cmd.Args) >= 1 {
				stepName = cmd.Args[0] // Assume first arg is name
			}

			for i := 1; i < len(cmd.Args); i++ {
				if cmd.Args[i] == "--image" && i+1 < len(cmd.Args) {
					imageName = cmd.Args[i+1]
					break
				}
			}

			if stepName == "" {
				return nil, fmt.Errorf("step name not provided or could not be parsed from args: %v", cmd.Args)
			}
			if imageName == "" {
				return nil, fmt.Errorf("step image not provided for step '%s'. Use '--image <image_name>'", stepName)
			}

			newStep := tektonv1.Step{
				Name:  stepName,
				Image: imageName,
			}
			session.CurrentTask.Spec.Steps = append(session.CurrentTask.Spec.Steps, newStep)
			fmt.Printf("Step '%s' with image '%s' added to task '%s'.\\n", stepName, imageName, session.CurrentTask.Name)
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
