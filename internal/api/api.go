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
	"net/url"
	"strings"
	"time"

	"github.com/BerieGithub/antrequeue/internal/store"
)

const (
	maxBodyBytes       = 1 << 20 // 1 MiB
	defaultMaxAttempts = 5
	maxAttemptsLimit   = 100
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
	ScheduledAt    *time.Time     `json:"scheduled_at"`
}

// validate applies defaults in place and returns a client-facing message when
// the request cannot be accepted.
func (r *submitRequest) validate() string {
	if strings.TrimSpace(r.Type) == "" {
		return "type is required"
	}

	if r.Priority == "" {
		r.Priority = store.PriorityNormal
	}
	if !r.Priority.Valid() {
		return "priority must be one of: low, normal, high, critical"
	}

	if r.MaxAttempts == 0 {
		r.MaxAttempts = defaultMaxAttempts
	}
	if r.MaxAttempts < 1 || r.MaxAttempts > maxAttemptsLimit {
		return "max_attempts must be between 1 and 100"
	}

	if r.CallbackURL != "" {
		u, err := url.Parse(r.CallbackURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return "callback_url must be an absolute http or https URL"
		}
	}

	return ""
}

func (a *API) submit(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, errBody("invalid service secret"))
		return
	}

	var req submitRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid json body"))
		return
	}
	if msg := req.validate(); msg != "" {
		writeJSON(w, http.StatusBadRequest, errBody(msg))
		return
	}

	// Idempotency is resolved inside the store so the lookup and the insert
	// are atomic. Networks retry; job execution should not.
	stored, created, err := a.store.Create(&store.Job{
		Type:           req.Type,
		Payload:        req.Payload,
		Priority:       req.Priority,
		Status:         store.StatusQueued,
		MaxAttempts:    req.MaxAttempts,
		CallbackURL:    req.CallbackURL,
		IdempotencyKey: req.IdempotencyKey,
		ScheduledAt:    req.ScheduledAt,
	})
	if err != nil {
		a.log.Error("create job failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, errBody("could not create job"))
		return
	}
	if !created {
		writeJSON(w, http.StatusOK, stored)
		return
	}

	// TODO(v1): enqueue onto RabbitMQ here (priority queue + DLX).
	a.log.Info("job queued", "id", stored.ID, "type", stored.Type, "priority", stored.Priority)

	writeJSON(w, http.StatusAccepted, stored)
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
	const prefix = "bearer "
	h := r.Header.Get("Authorization")
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return false
	}
	return a.serviceSecret != "" &&
		subtle.ConstantTimeCompare([]byte(h[len(prefix):]), []byte(a.serviceSecret)) == 1
}

func errBody(msg string) map[string]string { return map[string]string{"error": msg} }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
