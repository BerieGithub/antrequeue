package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/BerieGithub/antrequeue/internal/store"
)

const testSecret = "test-service-secret"

func newTestAPI() http.Handler {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(store.NewMemory(), testSecret, log).Routes()
}

func do(t *testing.T, h http.Handler, method, path, body string, auth bool) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if auth {
		req.Header.Set("Authorization", "Bearer "+testSecret)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeJob(t *testing.T, rec *httptest.ResponseRecorder) store.Job {
	t.Helper()
	var j store.Job
	if err := json.Unmarshal(rec.Body.Bytes(), &j); err != nil {
		t.Fatalf("decode job: %v (body %s)", err, rec.Body.String())
	}
	return j
}

func TestHealthzNeedsNoAuth(t *testing.T) {
	rec := do(t, newTestAPI(), http.MethodGet, "/healthz", "", false)
	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rec.Code)
	}
}

func TestAuth(t *testing.T) {
	h := newTestAPI()

	tests := []struct {
		name   string
		header string
		want   int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"wrong secret", "Bearer nope", http.StatusUnauthorized},
		{"missing scheme", testSecret, http.StatusUnauthorized},
		{"scheme only", "Bearer", http.StatusUnauthorized},
		{"empty token", "Bearer ", http.StatusUnauthorized},
		{"correct", "Bearer " + testSecret, http.StatusAccepted},
		{"lowercase scheme", "bearer " + testSecret, http.StatusAccepted},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(`{"type":"t"}`))
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("got %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestSubmitValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"valid minimal", `{"type":"resize_image"}`, http.StatusAccepted},
		{"malformed json", `{`, http.StatusBadRequest},
		{"missing type", `{"payload":{}}`, http.StatusBadRequest},
		{"blank type", `{"type":"   "}`, http.StatusBadRequest},
		{"unknown priority", `{"type":"t","priority":"urgent"}`, http.StatusBadRequest},
		{"valid priority", `{"type":"t","priority":"critical"}`, http.StatusAccepted},
		{"negative max_attempts", `{"type":"t","max_attempts":-1}`, http.StatusBadRequest},
		{"max_attempts too high", `{"type":"t","max_attempts":101}`, http.StatusBadRequest},
		{"callback not a url", `{"type":"t","callback_url":"not a url"}`, http.StatusBadRequest},
		{"callback wrong scheme", `{"type":"t","callback_url":"ftp://x.example.com/h"}`, http.StatusBadRequest},
		{"callback relative", `{"type":"t","callback_url":"/webhooks/x"}`, http.StatusBadRequest},
		{"callback valid", `{"type":"t","callback_url":"https://x.example.com/h"}`, http.StatusAccepted},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, newTestAPI(), http.MethodPost, "/v1/jobs", tc.body, true)
			if rec.Code != tc.want {
				t.Errorf("got %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestSubmitAppliesDefaults(t *testing.T) {
	rec := do(t, newTestAPI(), http.MethodPost, "/v1/jobs", `{"type":"resize_image"}`, true)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d, want 202", rec.Code)
	}

	j := decodeJob(t, rec)
	if j.Priority != store.PriorityNormal {
		t.Errorf("priority = %q, want %q", j.Priority, store.PriorityNormal)
	}
	if j.MaxAttempts != defaultMaxAttempts {
		t.Errorf("max_attempts = %d, want %d", j.MaxAttempts, defaultMaxAttempts)
	}
	if j.Status != store.StatusQueued {
		t.Errorf("status = %q, want %q", j.Status, store.StatusQueued)
	}
	if j.ID == "" {
		t.Error("id was not returned")
	}
}

// scheduled_at is the same name on the way in as on the way out.
func TestSubmitScheduledAtRoundTrips(t *testing.T) {
	const at = "2026-01-02T03:04:05Z"

	rec := do(t, newTestAPI(), http.MethodPost, "/v1/jobs",
		`{"type":"t","scheduled_at":"`+at+`"}`, true)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d, want 202", rec.Code)
	}

	j := decodeJob(t, rec)
	if j.ScheduledAt == nil {
		t.Fatal("scheduled_at was dropped")
	}
	if got := j.ScheduledAt.UTC().Format("2006-01-02T15:04:05Z"); got != at {
		t.Errorf("scheduled_at = %s, want %s", got, at)
	}
}

func TestSubmitIdempotent(t *testing.T) {
	h := newTestAPI()
	body := `{"type":"report","idempotency_key":"carbon-789-2026"}`

	first := do(t, h, http.MethodPost, "/v1/jobs", body, true)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first submit: got %d, want 202", first.Code)
	}

	second := do(t, h, http.MethodPost, "/v1/jobs",
		`{"type":"different","idempotency_key":"carbon-789-2026"}`, true)
	if second.Code != http.StatusOK {
		t.Fatalf("resubmit: got %d, want 200", second.Code)
	}

	a, b := decodeJob(t, first), decodeJob(t, second)
	if a.ID != b.ID {
		t.Errorf("resubmit returned a new job %q, want the original %q", b.ID, a.ID)
	}
	if b.Type != "report" {
		t.Errorf("resubmit returned type %q, want the original %q", b.Type, "report")
	}
}

func TestSubmitConcurrentIdempotent(t *testing.T) {
	h := newTestAPI()
	body := `{"type":"report","idempotency_key":"same"}`

	const goroutines = 32
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		ids      = map[string]struct{}{}
		accepted int
	)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			rec := do(t, h, http.MethodPost, "/v1/jobs", body, true)
			mu.Lock()
			defer mu.Unlock()
			if rec.Code == http.StatusAccepted {
				accepted++
			}
			var j store.Job
			if err := json.Unmarshal(rec.Body.Bytes(), &j); err == nil {
				ids[j.ID] = struct{}{}
			}
		}()
	}
	wg.Wait()

	if accepted != 1 {
		t.Errorf("got %d 202 responses, want exactly 1", accepted)
	}
	if len(ids) != 1 {
		t.Errorf("got %d distinct job ids, want 1", len(ids))
	}
}

func TestGetJob(t *testing.T) {
	h := newTestAPI()

	submitted := decodeJob(t, do(t, h, http.MethodPost, "/v1/jobs", `{"type":"report"}`, true))

	rec := do(t, h, http.MethodGet, "/v1/jobs/"+submitted.ID, "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if got := decodeJob(t, rec); got.ID != submitted.ID {
		t.Errorf("got id %q, want %q", got.ID, submitted.ID)
	}
}

func TestGetUnknownJob(t *testing.T) {
	rec := do(t, newTestAPI(), http.MethodGet, "/v1/jobs/does-not-exist", "", true)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}

func TestGetRequiresAuth(t *testing.T) {
	rec := do(t, newTestAPI(), http.MethodGet, "/v1/jobs/whatever", "", false)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rec.Code)
	}
}

// An empty configured secret must never authorise a request.
func TestEmptySecretRejectsEverything(t *testing.T) {
	h := New(store.NewMemory(), "", slog.New(slog.NewTextHandler(io.Discard, nil))).Routes()

	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(`{"type":"t"}`))
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rec.Code)
	}
}
