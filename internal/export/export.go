package export

import (
	"strings"

	"tkn-shell/internal/state"

	"sigs.k8s.io/yaml"
)

// ExportAll marshals all tasks and pipelines in the session to a single YAML string,
// with documents separated by "---".
func ExportAll(s *state.Session) (string, error) {
	var yamlDocs []string

	// Export Tasks
	for _, task := range s.Tasks {
		taskYAML, err := yaml.Marshal(task)
		if err != nil {
			return "", err // Consider wrapping error for more context
		}
		yamlDocs = append(yamlDocs, string(taskYAML))
	}

	// Export Pipelines
	for _, pipeline := range s.Pipelines {
		pipelineYAML, err := yaml.Marshal(pipeline)
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
