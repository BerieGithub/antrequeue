// Package api exposes the job submission surface.
//
//	POST /v1/jobs        submit a job
//	GET  /v1/jobs/{id}   check status
//	GET  /healthz
package api

import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/BerieGithub/antrequeue/internal/store"
)

type API struct {
	store         store.Store
	serviceSecret string
	log           *slog.Logger
}

func New(s store.Store, serviceSecret string, log *slog.Logger) *API {
	return &API{store: s, serviceSecret: serviceSecret, log: log}
}

func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("POST /v1/jobs", a.submit)
	mux.HandleFunc("GET /v1/jobs/{id}", a.get)
	return mux
}

func (a *API) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type submitRequest struct {
	Type           string         `json:"type"`
	Payload        map[string]any `json:"payload"`
	Priority       store.Priority `json:"priority"`
	MaxAttempts    int            `json:"max_attempts"`
	CallbackURL    string         `json:"callback_url"`
	IdempotencyKey string         `json:"idempotency_key"`
	ScheduleAt     *time.Time     `json:"schedule_at"`
}

func (a *API) submit(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, errBody("invalid service secret"))
		return
	}

	var req submitRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid json body"))
		return
	}
	if strings.TrimSpace(req.Type) == "" {
		writeJSON(w, http.StatusBadRequest, errBody("type is required"))
		return
	}

	// Idempotency: re-submitting the same key returns the original job rather
	// than duplicating work. Networks retry; job execution should not.
	if req.IdempotencyKey != "" {
		if existing, ok := a.store.ByIdempotencyKey(req.IdempotencyKey); ok {
			writeJSON(w, http.StatusOK, existing)
			return
		}
	}

	if req.Priority == "" {
		req.Priority = store.PriorityNormal
	}
	if req.MaxAttempts <= 0 {
		req.MaxAttempts = 5
	}

	j := &store.Job{
		Type:           req.Type,
		Payload:        req.Payload,
		Priority:       req.Priority,
		Status:         store.StatusQueued,
		MaxAttempts:    req.MaxAttempts,
		CallbackURL:    req.CallbackURL,
		IdempotencyKey: req.IdempotencyKey,
		ScheduledAt:    req.ScheduleAt,
	}
	if err := a.store.Create(j); err != nil {
		a.log.Error("create job failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, errBody("could not create job"))
		return
	}

	// TODO(v1): enqueue onto RabbitMQ here (priority queue + DLX).
	a.log.Info("job queued", "id", j.ID, "type", j.Type, "priority", j.Priority)

	writeJSON(w, http.StatusAccepted, j)
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, errBody("invalid service secret"))
		return
	}
	j, err := a.store.Get(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, errBody("job not found"))
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (a *API) authorized(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return false
	}
	return a.serviceSecret != "" &&
		subtle.ConstantTimeCompare([]byte(h[7:]), []byte(a.serviceSecret)) == 1
}

func errBody(msg string) map[string]string { return map[string]string{"error": msg} }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
