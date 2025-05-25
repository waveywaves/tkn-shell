package state

import (
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

// Session holds the current context of the interactive shell.
type Session struct {
	Pipelines       map[string]*v1.Pipeline
	Tasks           map[string]*v1.Task
	CurrentPipeline *v1.Pipeline
	CurrentTask     *v1.Task
}

// NewSession creates a new, empty session.
func NewSession() *Session {
	return &Session{
		Pipelines: make(map[string]*v1.Pipeline),
		Tasks:     make(map[string]*v1.Task),
	}
}

// Reset clears the current session data.
func (s *Session) Reset() {
	s.Pipelines = make(map[string]*v1.Pipeline)
	s.Tasks = make(map[string]*v1.Task)
	s.CurrentPipeline = nil
	s.CurrentTask = nil
}

// LookupTask retrieves a task by its name from the session.
func (s *Session) LookupTask(name string) (*v1.Task, bool) {
	task, found := s.Tasks[name]
	return task, found
}
