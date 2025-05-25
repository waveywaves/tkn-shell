package state

import (
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

// RevertFunc defines the function signature for an undo operation.
type RevertFunc func(*Session)

// Session holds the current context of the interactive shell.
type Session struct {
	Pipelines       map[string]*v1.Pipeline
	Tasks           map[string]*v1.Task
	CurrentPipeline *v1.Pipeline
	CurrentTask     *v1.Task
	PastActions     []RevertFunc // Slice of functions to revert actions
}

// NewSession creates a new, empty session.
func NewSession() *Session {
	return &Session{
		Pipelines:   make(map[string]*v1.Pipeline),
		Tasks:       make(map[string]*v1.Task),
		PastActions: make([]RevertFunc, 0),
	}
}

// Reset clears the current session data.
func (s *Session) Reset() {
	s.Pipelines = make(map[string]*v1.Pipeline)
	s.Tasks = make(map[string]*v1.Task)
	s.CurrentPipeline = nil
	s.CurrentTask = nil
	s.PastActions = make([]RevertFunc, 0) // Clear past actions as well
}

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
	task, found := s.Tasks[name]
	return task, found
}
