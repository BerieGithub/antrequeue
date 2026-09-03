// Package store holds job records. v0 keeps them in memory so the API is
// runnable end to end; v1 swaps this for Postgres behind the same interface.
package store

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusRetrying  Status = "retrying"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"     // exhausted retries -> dead letter
	StatusCancelled Status = "cancelled"
)

type Priority string

const (
	PriorityLow      Priority = "low"
	PriorityNormal   Priority = "normal"
	PriorityHigh     Priority = "high"
	PriorityCritical Priority = "critical"
)

type Job struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	Payload     map[string]any `json:"payload,omitempty"`
	Priority    Priority       `json:"priority"`
	Status      Status         `json:"status"`
	Attempts    int            `json:"attempts"`
	MaxAttempts int            `json:"max_attempts"`

	// CallbackURL receives an HMAC-signed POST when the job settles.
	CallbackURL string `json:"callback_url,omitempty"`

	// IdempotencyKey makes re-submission safe: the same key returns the
	// original job instead of creating a duplicate.
	IdempotencyKey string `json:"idempotency_key,omitempty"`

	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastError   string     `json:"last_error,omitempty"`
}

var ErrNotFound = errors.New("antrequeue: job not found")

type Store interface {
	Create(j *Job) error
	Get(id string) (*Job, error)
	ByIdempotencyKey(key string) (*Job, bool)
	Update(j *Job) error
}

type Memory struct {
	mu     sync.RWMutex
	jobs   map[string]*Job
	byKey  map[string]string
}

func NewMemory() *Memory {
	return &Memory{jobs: make(map[string]*Job), byKey: make(map[string]string)}
}

func (m *Memory) Create(j *Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j.ID == "" {
		j.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	j.CreatedAt, j.UpdatedAt = now, now
	m.jobs[j.ID] = j
	if j.IdempotencyKey != "" {
		m.byKey[j.IdempotencyKey] = j.ID
	}
	return nil
}

func (m *Memory) Get(id string) (*Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	j, ok := m.jobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	return j, nil
}

func (m *Memory) ByIdempotencyKey(key string) (*Job, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.byKey[key]
	if !ok {
		return nil, false
	}
	j, ok := m.jobs[id]
	return j, ok
}

func (m *Memory) Update(j *Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.jobs[j.ID]; !ok {
		return ErrNotFound
	}
	j.UpdatedAt = time.Now().UTC()
	m.jobs[j.ID] = j
	return nil
}
