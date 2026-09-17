// Package store holds job records. v0 keeps them in memory so the API is
// runnable end to end; v1 swaps this for Postgres behind the same interface.
package store

import (
	"errors"
	"maps"
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
	StatusFailed    Status = "failed" // exhausted retries -> dead letter
	StatusCancelled Status = "cancelled"
)

type Priority string

const (
	PriorityLow      Priority = "low"
	PriorityNormal   Priority = "normal"
	PriorityHigh     Priority = "high"
	PriorityCritical Priority = "critical"
)

// Valid reports whether p is one of the four documented priorities.
func (p Priority) Valid() bool {
	switch p {
	case PriorityLow, PriorityNormal, PriorityHigh, PriorityCritical:
		return true
	}
	return false
}

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

// Clone returns a copy safe to hand to another goroutine.
//
// Payload is copied one level deep. Values inside it come from the submitted
// JSON body and are never mutated after Create, so sharing them is safe; the
// map itself is copied because callers may add keys to their own copy.
func (j *Job) Clone() *Job {
	if j == nil {
		return nil
	}
	c := *j
	if j.Payload != nil {
		c.Payload = maps.Clone(j.Payload)
	}
	if j.ScheduledAt != nil {
		t := *j.ScheduledAt
		c.ScheduledAt = &t
	}
	return &c
}

var (
	ErrNotFound = errors.New("antrequeue: job not found")
	ErrNilJob   = errors.New("antrequeue: nil job")
)

type Store interface {
	// Create inserts j and returns the stored record.
	//
	// If j.IdempotencyKey is already present, the existing job is returned
	// with created=false and nothing is inserted. The lookup and the insert
	// are one atomic step, so concurrent submissions carrying the same key
	// resolve to a single job. A Postgres implementation gets this from
	// INSERT ... ON CONFLICT (idempotency_key) DO NOTHING.
	Create(j *Job) (stored *Job, created bool, err error)

	Get(id string) (*Job, error)
	Update(j *Job) error
}

type Memory struct {
	mu    sync.RWMutex
	jobs  map[string]*Job
	byKey map[string]string
}

func NewMemory() *Memory {
	return &Memory{jobs: make(map[string]*Job), byKey: make(map[string]string)}
}

func (m *Memory) Create(j *Job) (*Job, bool, error) {
	if j == nil {
		return nil, false, ErrNilJob
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if j.IdempotencyKey != "" {
		if id, ok := m.byKey[j.IdempotencyKey]; ok {
			if existing, ok := m.jobs[id]; ok {
				return existing.Clone(), false, nil
			}
		}
	}

	stored := j.Clone()
	if stored.ID == "" {
		stored.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	stored.CreatedAt, stored.UpdatedAt = now, now

	m.jobs[stored.ID] = stored
	if stored.IdempotencyKey != "" {
		m.byKey[stored.IdempotencyKey] = stored.ID
	}
	return stored.Clone(), true, nil
}

func (m *Memory) Get(id string) (*Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	j, ok := m.jobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	return j.Clone(), nil
}

func (m *Memory) Update(j *Job) error {
	if j == nil {
		return ErrNilJob
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.jobs[j.ID]; !ok {
		return ErrNotFound
	}

	stored := j.Clone()
	stored.UpdatedAt = time.Now().UTC()
	m.jobs[stored.ID] = stored
	if stored.IdempotencyKey != "" {
		m.byKey[stored.IdempotencyKey] = stored.ID
	}
	return nil
}
