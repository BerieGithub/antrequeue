package store

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestCreateAssignsIDAndTimestamps(t *testing.T) {
	m := NewMemory()

	stored, created, err := m.Create(&Job{Type: "resize_image", Status: StatusQueued})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !created {
		t.Fatal("created = false, want true for a fresh job")
	}
	if stored.ID == "" {
		t.Error("ID was not assigned")
	}
	if stored.CreatedAt.IsZero() || stored.UpdatedAt.IsZero() {
		t.Error("timestamps were not set")
	}
}

func TestCreateDoesNotMutateCaller(t *testing.T) {
	m := NewMemory()

	in := &Job{Type: "resize_image"}
	if _, _, err := m.Create(in); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if in.ID != "" {
		t.Errorf("Create wrote ID %q back onto the caller's job", in.ID)
	}
}

func TestCreateSameIdempotencyKeyReturnsExisting(t *testing.T) {
	m := NewMemory()

	first, created, err := m.Create(&Job{Type: "report", IdempotencyKey: "k1"})
	if err != nil || !created {
		t.Fatalf("first Create: created=%v err=%v", created, err)
	}

	second, created, err := m.Create(&Job{Type: "something_else", IdempotencyKey: "k1"})
	if err != nil {
		t.Fatalf("second Create: %v", err)
	}
	if created {
		t.Error("created = true, want false for a repeated idempotency key")
	}
	if second.ID != first.ID {
		t.Errorf("got id %q, want the original %q", second.ID, first.ID)
	}
	if second.Type != "report" {
		t.Errorf("got type %q, want the original job back unchanged", second.Type)
	}
}

// Two submissions racing on one idempotency key must settle on a single job.
// Before the check and the insert were made atomic, both callers missed the
// lookup and inserted, and the second overwrote the key index so the first
// job became unreachable.
func TestCreateConcurrentSameIdempotencyKey(t *testing.T) {
	m := NewMemory()

	const goroutines = 64
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		ids     = map[string]struct{}{}
		creates int
	)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			stored, created, err := m.Create(&Job{Type: "report", IdempotencyKey: "same"})
			if err != nil {
				t.Errorf("Create: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			ids[stored.ID] = struct{}{}
			if created {
				creates++
			}
		}()
	}
	wg.Wait()

	if creates != 1 {
		t.Errorf("created = true %d times, want exactly 1", creates)
	}
	if len(ids) != 1 {
		t.Errorf("got %d distinct job ids, want 1", len(ids))
	}
	if n := m.len(); n != 1 {
		t.Errorf("store holds %d jobs, want 1", n)
	}
}

func TestCreateWithoutKeyIsNeverDeduplicated(t *testing.T) {
	m := NewMemory()

	a, _, err := m.Create(&Job{Type: "report"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	b, created, err := m.Create(&Job{Type: "report"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !created || a.ID == b.ID {
		t.Error("jobs without an idempotency key must be independent")
	}
}

func TestGetReturnsCopy(t *testing.T) {
	m := NewMemory()

	at := time.Now().UTC()
	stored, _, err := m.Create(&Job{
		Type:        "report",
		Payload:     map[string]any{"company_id": 789},
		ScheduledAt: &at,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := m.Get(stored.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	got.Status = StatusFailed
	got.Payload["company_id"] = 0
	*got.ScheduledAt = at.Add(time.Hour)

	fresh, err := m.Get(stored.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if fresh.Status == StatusFailed {
		t.Error("mutating a returned job changed the stored status")
	}
	if fresh.Payload["company_id"] != 789 {
		t.Error("mutating a returned payload changed the stored payload")
	}
	if !fresh.ScheduledAt.Equal(at) {
		t.Error("mutating a returned ScheduledAt changed the stored value")
	}
}

func TestGetNotFound(t *testing.T) {
	m := NewMemory()
	if _, err := m.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestUpdate(t *testing.T) {
	m := NewMemory()

	stored, _, err := m.Create(&Job{Type: "report", Status: StatusQueued})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	stored.Status = StatusSucceeded
	if err := m.Update(stored); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := m.Get(stored.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusSucceeded {
		t.Errorf("got status %q, want %q", got.Status, StatusSucceeded)
	}
	if got.UpdatedAt.Before(got.CreatedAt) {
		t.Error("UpdatedAt went backwards")
	}
}

func TestUpdateNotFound(t *testing.T) {
	m := NewMemory()
	if err := m.Update(&Job{ID: "nope"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestNilJob(t *testing.T) {
	m := NewMemory()
	if _, _, err := m.Create(nil); !errors.Is(err, ErrNilJob) {
		t.Errorf("Create(nil): got %v, want ErrNilJob", err)
	}
	if err := m.Update(nil); !errors.Is(err, ErrNilJob) {
		t.Errorf("Update(nil): got %v, want ErrNilJob", err)
	}
}

// Concurrent readers and writers on one job must not race. This is the check
// the -race gate in CI exists for.
func TestConcurrentReadWrite(t *testing.T) {
	m := NewMemory()

	stored, _, err := m.Create(&Job{Type: "report", Payload: map[string]any{"n": 1}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			got, err := m.Get(stored.ID)
			if err != nil {
				t.Errorf("Get: %v", err)
				return
			}
			_ = got.Payload["n"]
			_ = got.Status
		}()
		go func() {
			defer wg.Done()
			j := stored.Clone()
			j.Status = StatusRunning
			if err := m.Update(j); err != nil {
				t.Errorf("Update: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestPriorityValid(t *testing.T) {
	for _, p := range []Priority{PriorityLow, PriorityNormal, PriorityHigh, PriorityCritical} {
		if !p.Valid() {
			t.Errorf("%q should be valid", p)
		}
	}
	for _, p := range []Priority{"", "urgent", "HIGH", "banana"} {
		if p.Valid() {
			t.Errorf("%q should not be valid", p)
		}
	}
}

func (m *Memory) len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.jobs)
}
