package state

import (
	"context"
	"fmt"

	"tkn-shell/internal/kube"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const fieldManager = "tkn-shell"

// ApplyAll applies all defined Pipelines and Tasks in the session to the specified namespace.
func (s *Session) ApplyAll(ctx context.Context, ns string) error {
	k8sClient, err := kube.GetKubeClient()
	if err != nil {
		return fmt.Errorf("failed to get Kubernetes client: %w", err)
	}

	var applyErrors []error

	// Apply Pipelines
	for _, p := range s.Pipelines {
		pToApply := p.DeepCopy()
		pToApply.APIVersion = tektonv1.SchemeGroupVersion.String()
		pToApply.Kind = "Pipeline"
		pToApply.Namespace = ns

		fmt.Printf("Applying Pipeline %s/%s...\n", pToApply.Namespace, pToApply.Name)
		patch := client.Apply
		err = k8sClient.Patch(ctx, pToApply, patch, client.FieldOwner(fieldManager), client.ForceOwnership)
		if err != nil {
			applyErrors = append(applyErrors, fmt.Errorf("failed to apply Pipeline %s/%s: %w", pToApply.Namespace, pToApply.Name, err))
		} else {
			fmt.Printf("Pipeline %s/%s applied successfully.\n", pToApply.Namespace, pToApply.Name)
		}
	}

	// Apply Tasks
	for _, tk := range s.Tasks {
		tkToApply := tk.DeepCopy()
		tkToApply.APIVersion = tektonv1.SchemeGroupVersion.String()
		tkToApply.Kind = "Task"
		tkToApply.Namespace = ns

		fmt.Printf("Applying Task %s/%s...\n", tkToApply.Namespace, tkToApply.Name)
		patch := client.Apply
		err = k8sClient.Patch(ctx, tkToApply, patch, client.FieldOwner(fieldManager), client.ForceOwnership)
		if err != nil {
			applyErrors = append(applyErrors, fmt.Errorf("failed to apply Task %s/%s: %w", tkToApply.Namespace, tkToApply.Name, err))
		} else {
			fmt.Printf("Task %s/%s applied successfully.\n", tkToApply.Namespace, tkToApply.Name)
		}
	}

	if len(applyErrors) > 0 {
		// Consider joining errors if there are many
		return fmt.Errorf("encountered %d error(s) during apply: %v", len(applyErrors), applyErrors)
	}

	return nil
}
