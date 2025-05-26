package export

import (
	"sort"
	"tkn-shell/internal/state"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1" // Added for SchemeGroupVersion
	"sigs.k8s.io/yaml"
)

// ExportAll marshals all tasks and pipelines in the session to a single YAML string,
// with documents separated by "---".
func ExportAll(s *state.Session) ([]byte, error) {
	var yamlDocs [][]byte // Changed from []string

	// Export Tasks
	tasks := make([]*tektonv1.Task, 0, len(s.GetTasks()))
	for _, task := range s.GetTasks() {
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].Name < tasks[j].Name
	})

	for _, task := range tasks {
		taskToExport := task.DeepCopy() // Work with a copy
		taskToExport.APIVersion = tektonv1.SchemeGroupVersion.String()
		taskToExport.Kind = "Task"

		taskYAML, err := yaml.Marshal(taskToExport)
		if err != nil {
			return nil, err // Consider wrapping error for more context
		}
		yamlDocs = append(yamlDocs, taskYAML) // No conversion to string
	}

	// Export Pipelines
	pipelines := make([]*tektonv1.Pipeline, 0, len(s.GetPipelines()))
	for _, pipeline := range s.GetPipelines() {
		pipelines = append(pipelines, pipeline)
	}
	sort.Slice(pipelines, func(i, j int) bool {
		return pipelines[i].Name < pipelines[j].Name
	})

	for _, pipeline := range pipelines {
		pipelineToExport := pipeline.DeepCopy() // Work with a copy
		pipelineToExport.APIVersion = tektonv1.SchemeGroupVersion.String()
		pipelineToExport.Kind = "Pipeline"

		pipelineYAML, err := yaml.Marshal(pipelineToExport)
		if err != nil {
			return nil, err // Consider wrapping error for more context
		}
		yamlDocs = append(yamlDocs, pipelineYAML) // No conversion to string
	}

	if len(yamlDocs) == 0 {
		return nil, nil // Or a message like []byte("# No resources to export")
	}

	// Join byte slices with "---" separator
	separator := []byte("\\n---\\n")
	var result []byte
	for i, doc := range yamlDocs {
		if i > 0 {
			result = append(result, separator...)
		}
		result = append(result, doc...)
	}
	return result, nil
}
