package export

import (
	"strings"

	"tkn-shell/internal/state"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1" // Added for SchemeGroupVersion
	"sigs.k8s.io/yaml"
)

// ExportAll marshals all tasks and pipelines in the session to a single YAML string,
// with documents separated by "---".
func ExportAll(s *state.Session) (string, error) {
	var yamlDocs []string

	// Export Tasks
	for _, task := range s.Tasks {
		taskToExport := task.DeepCopy() // Work with a copy
		taskToExport.APIVersion = tektonv1.SchemeGroupVersion.String()
		taskToExport.Kind = "Task"

		taskYAML, err := yaml.Marshal(taskToExport)
		if err != nil {
			return "", err // Consider wrapping error for more context
		}
		yamlDocs = append(yamlDocs, string(taskYAML))
	}

	// Export Pipelines
	for _, pipeline := range s.Pipelines {
		pipelineToExport := pipeline.DeepCopy() // Work with a copy
		pipelineToExport.APIVersion = tektonv1.SchemeGroupVersion.String()
		pipelineToExport.Kind = "Pipeline"

		pipelineYAML, err := yaml.Marshal(pipelineToExport)
		if err != nil {
			return "", err // Consider wrapping error for more context
		}
		yamlDocs = append(yamlDocs, string(pipelineYAML))
	}

	if len(yamlDocs) == 0 {
		return "", nil // Or a message like "# No resources to export"
	}

	return strings.Join(yamlDocs, "\n---\n"), nil
}
