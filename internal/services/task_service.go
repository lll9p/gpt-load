package services

import (
	"context"
	"errors"
	"sync"
	"time"
)

const ResultTTL = time.Hour

const (
	TaskTypeKeyValidation = "KEY_VALIDATION"
	TaskTypeKeyImport     = "KEY_IMPORT"
	TaskTypeKeyDelete     = "KEY_DELETE"
)

// TaskStatus represents the full lifecycle of a long-running task.
type TaskStatus struct {
	TaskType        string     `json:"task_type"`
	IsRunning       bool       `json:"is_running"`
	GroupName       string     `json:"group_name,omitempty"`
	Processed       int        `json:"processed"`
	Total           int        `json:"total"`
	Result          any        `json:"result,omitempty"`
	Error           string     `json:"error,omitempty"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	DurationSeconds float64    `json:"duration_seconds,omitempty"`
}

type taskState struct {
	mu      sync.Mutex
	status  TaskStatus
	stopped bool
	wg      sync.WaitGroup
}

func (s *KeyService) reserveTask(taskType, groupName string, total int) (*TaskStatus, error) {
	s.tasks.mu.Lock()
	defer s.tasks.mu.Unlock()
	if s.tasks.stopped {
		return nil, errors.New("key service is stopping")
	}
	if s.tasks.status.IsRunning {
		return nil, errors.New("a task is already running, please wait")
	}
	s.tasks.status = TaskStatus{
		TaskType: taskType, IsRunning: true, GroupName: groupName,
		Total: total, StartedAt: time.Now(),
	}
	status := s.tasks.status
	s.tasks.wg.Add(1)
	return &status, nil
}

func (s *KeyService) startTask(taskType, groupName string, total int, run func()) (*TaskStatus, error) {
	status, err := s.reserveTask(taskType, groupName, total)
	if err != nil {
		return nil, err
	}
	go func() {
		defer s.tasks.wg.Done()
		run()
	}()
	return status, nil
}

// GetTaskStatus returns a snapshot; only completed tasks expire.
func (s *KeyService) GetTaskStatus() (*TaskStatus, error) {
	s.tasks.mu.Lock()
	defer s.tasks.mu.Unlock()
	status := s.tasks.status
	if !status.IsRunning && status.FinishedAt != nil {
		if time.Since(*status.FinishedAt) >= ResultTTL {
			s.tasks.status = TaskStatus{}
			return &TaskStatus{}, nil
		}
		finishedAt := *status.FinishedAt
		status.FinishedAt = &finishedAt
	}
	return &status, nil
}

func (s *KeyService) updateProgress(processed int) {
	s.tasks.mu.Lock()
	defer s.tasks.mu.Unlock()
	if s.tasks.status.IsRunning {
		s.tasks.status.Processed = processed
	}
}

func (s *KeyService) endTask(result any, taskErr error) {
	s.tasks.mu.Lock()
	defer s.tasks.mu.Unlock()
	now := time.Now()
	status := &s.tasks.status
	status.IsRunning = false
	status.FinishedAt = &now
	status.DurationSeconds = now.Sub(status.StartedAt).Seconds()
	if taskErr != nil {
		status.Error = taskErr.Error()
	} else {
		status.Result = result
	}
}

// Stop rejects new tasks and waits for started tasks. Do not close the DB on timeout.
func (s *KeyService) Stop(ctx context.Context) error {
	s.tasks.mu.Lock()
	s.tasks.stopped = true
	s.tasks.mu.Unlock()
	done := make(chan struct{})
	go func() {
		s.tasks.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
