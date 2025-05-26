package engine

import (
	"context"
	"fmt"

	// "tkn-shell/internal/feedback" // Removed for debug
	"tkn-shell/internal/state"
)

// ValidateSession checks all pipelines and tasks in the current session for validity.
// It collects all errors found.
func ValidateSession(s *state.Session) error {
	var allErrors []error
	ctx := context.Background() // Or apis.WithinSpec(context.Background()) if needed for specific validations
	// feedback.Infof("DEBUG: Validating session...")

	// Validate Pipelines
	for name, p := range s.GetPipelines() {
		// feedback.Infof("DEBUG: Validating Pipeline: %s", name)
		if p == nil {
			// feedback.Infof("DEBUG: Pipeline '%s' is nil in session", name)
			allErrors = append(allErrors, fmt.Errorf("pipeline '%s' is nil in session", name))
			continue
		}
		if err := p.Validate(ctx); err != nil {
			// feedback.Infof("DEBUG: Pipeline '%s' invalid: %v", name, err.Error())
			allErrors = append(allErrors, fmt.Errorf("pipeline '%s' is invalid: %w", name, err))
		}
	}

	// Validate Tasks
	for name, tk := range s.GetTasks() {
		// feedback.Infof("DEBUG: Validating Task: %s", name)
		if tk == nil {
			// feedback.Infof("DEBUG: Task '%s' is nil in session", name)
			allErrors = append(allErrors, fmt.Errorf("task '%s' is nil in session", name))
			continue
		}
		if err := tk.Validate(ctx); err != nil {
			// feedback.Infof("DEBUG: Task '%s' invalid: %v", name, err.Error())
			allErrors = append(allErrors, fmt.Errorf("task '%s' is invalid: %w", name, err))
		}
	}

	if len(allErrors) > 0 {
		// Combine multiple errors into a single error. For simplicity, just join messages.
		// A more sophisticated error type could be used here.
		var errorMessages string
		for i, e := range allErrors {
			if i > 0 {
				errorMessages += "; "
			}
			errorMessages += e.Error()
		}
		return fmt.Errorf("%s", errorMessages)
	}

	return nil
}
