package state

import (
	"context"
	"fmt"

	"tkn-shell/internal/feedback"
	"tkn-shell/internal/kube"

	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RevertFunc defines the function signature for an undo operation.
type RevertFunc func(*Session)

// Session holds the current context of the interactive shell.
type Session struct {
	pipelines       map[string]*v1.Pipeline
	tasks           map[string]*v1.Task
	currentPipeline *v1.Pipeline
	currentTask     *v1.Task
	PastActions     []RevertFunc
}

// NewSession creates a new, empty session.
func NewSession() *Session {
	return &Session{
		pipelines:   make(map[string]*v1.Pipeline),
		tasks:       make(map[string]*v1.Task),
		PastActions: make([]RevertFunc, 0),
	}
}

// Reset clears the current session data.
func (s *Session) Reset() {
	s.pipelines = make(map[string]*v1.Pipeline)
	s.tasks = make(map[string]*v1.Task)
	s.currentPipeline = nil
	s.currentTask = nil
	s.PastActions = make([]RevertFunc, 0)
}

// Getters
func (s *Session) GetPipelines() map[string]*v1.Pipeline { return s.pipelines }
func (s *Session) GetTasks() map[string]*v1.Task         { return s.tasks }
func (s *Session) GetCurrentPipeline() *v1.Pipeline      { return s.currentPipeline }
func (s *Session) GetCurrentTask() *v1.Task              { return s.currentTask }

// Setters
func (s *Session) SetCurrentPipeline(p *v1.Pipeline) { s.currentPipeline = p }
func (s *Session) SetCurrentTask(t *v1.Task)         { s.currentTask = t }

// Add/Delete for maps
func (s *Session) AddPipeline(name string, p *v1.Pipeline) { s.pipelines[name] = p }
func (s *Session) DeletePipeline(name string)              { delete(s.pipelines, name) }
func (s *Session) AddTask(name string, t *v1.Task)         { s.tasks[name] = t }
func (s *Session) DeleteTask(name string)                  { delete(s.tasks, name) }

// PushRevertAction adds a revert function to the stack.
func (s *Session) PushRevertAction(revert RevertFunc) {
	s.PastActions = append(s.PastActions, revert)
}

// PopRevertAction removes and returns the last revert function from the stack.
// Returns nil if the stack is empty.
func (s *Session) PopRevertAction() RevertFunc {
	if len(s.PastActions) == 0 {
		return nil
	}
	lastAction := s.PastActions[len(s.PastActions)-1]
	s.PastActions = s.PastActions[:len(s.PastActions)-1]
	return lastAction
}

// LookupTask retrieves a task by its name from the session.
func (s *Session) LookupTask(name string) (*v1.Task, bool) {
	task, found := s.tasks[name]
	return task, found
}

// RunPipeline constructs and creates a PipelineRun resource in the specified namespace.
func (s *Session) RunPipeline(ctx context.Context, pipelineName string, params []v1.Param, namespace string) (*v1.PipelineRun, error) {
	k8sClient, err := kube.GetKubeClient() // Assuming kube.GetKubeClient() is accessible and provides a compatible client
	if err != nil {
		return nil, fmt.Errorf("failed to get Kubernetes client: %w", err)
	}

	pipeline, exists := s.pipelines[pipelineName]
	if !exists {
		return nil, fmt.Errorf("pipeline '%s' not found in session", pipelineName)
	}

	// Ensure pipeline has a name, which should always be true if it's in the map
	if pipeline.Name == "" {
		return nil, fmt.Errorf("pipeline retrieved from session has no name (key: %s)", pipelineName)
	}

	pipelineRun := &v1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: pipeline.Name + "-run-", // Tekton typically uses GenerateName for PipelineRuns
			Namespace:    namespace,
		},
		Spec: v1.PipelineRunSpec{
			PipelineRef: &v1.PipelineRef{
				Name: pipeline.Name,
			},
			Params: params,
			// TODO: Add support for Workspaces, ServiceAccountName, Timeouts etc. as needed
		},
	}

	// Set APIVersion and Kind for the PipelineRun object before creation.
	// Note: This might be set automatically by the scheme if the client is configured with it.
	// However, it's good practice to set it explicitly if unsure.
	pipelineRun.APIVersion = v1.SchemeGroupVersion.String() // "tekton.dev/v1"
	pipelineRun.Kind = "PipelineRun"

	feedback.Infof("Creating PipelineRun %s in namespace %s...", pipelineRun.GenerateName, pipelineRun.Namespace)
	err = k8sClient.Create(ctx, pipelineRun) // Using client.Create for new objects
	if err != nil {
		return nil, fmt.Errorf("failed to create PipelineRun for pipeline '%s': %w", pipeline.Name, err)
	}
	feedback.Infof("PipelineRun created successfully (name will be generated based on: %s). Actual name assigned by Kubernetes.", pipelineRun.GenerateName)

	// The pipelineRun object will be updated by the API server with the generated name, UID, etc.
	// However, client.Create might not always return the fully populated object immediately
	// depending on the client implementation. For now, we return the object we sent.
	// To get the fully populated object, a subsequent Get might be needed, but that adds complexity.
	return pipelineRun, nil
}

// RunTask constructs and creates a TaskRun resource in the specified namespace.
func (s *Session) RunTask(ctx context.Context, taskName string, params []v1.Param, namespace string) (*v1.TaskRun, error) {
	k8sClient, err := kube.GetKubeClient()
	if err != nil {
		return nil, fmt.Errorf("failed to get Kubernetes client: %w", err)
	}

	task, exists := s.tasks[taskName]
	if !exists {
		return nil, fmt.Errorf("task '%s' not found in session", taskName)
	}

	if task.Name == "" { // Should not happen if taskName is valid key
		return nil, fmt.Errorf("task retrieved from session has no name (key: %s)", taskName)
	}

	taskRun := &v1.TaskRun{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: task.Name + "-run-",
			Namespace:    namespace,
		},
		Spec: v1.TaskRunSpec{
			TaskRef: &v1.TaskRef{
				Name: task.Name,
			},
			Params: params,
			// TODO: Add support for Workspaces, ServiceAccountName, Timeouts etc. as needed
		},
	}

	taskRun.APIVersion = v1.SchemeGroupVersion.String() // "tekton.dev/v1"
	taskRun.Kind = "TaskRun"

	feedback.Infof("Creating TaskRun %s in namespace %s...", taskRun.GenerateName, taskRun.Namespace)
	err = k8sClient.Create(ctx, taskRun)
	if err != nil {
		return nil, fmt.Errorf("failed to create TaskRun for task '%s': %w", task.Name, err)
	}
	feedback.Infof("TaskRun created successfully (name will be generated based on: %s). Actual name assigned by Kubernetes.", taskRun.GenerateName)

	return taskRun, nil
}
