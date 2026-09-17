# antrequeue

> Distributed task orchestration for services that shouldn't own their own workers.

**Status: v0, early.**
The submission API runs end to end today against an in-memory store.
The dispatcher, broker, workers, retries, dead-letter queue and signed callbacks described below are the design target, not shipped behaviour.
[Design goals](#design-goals) marks what is built and what is not, and the [roadmap](#roadmap) tracks the rest.
Nothing here is production-ready yet.

The goal: submit a job over HTTP, antrequeue queues it, enforces rate limits and backpressure, retries with exponential backoff, dead-letters what still fails, and calls your webhook when it settles.
Works with any language or framework.

## Why not just use Celery?

Celery is excellent, and in-process.
It is Python-only and each application runs its own workers, broker wiring and retry policy.

antrequeue targets a different shape: **several independently deployed services, in different languages, sharing one orchestration layer.**
A Django app and a FastAPI app both `POST /v1/jobs` and both receive webhook callbacks.
One place to see every background task, one retry policy, one dead-letter queue, one dashboard.

That is the same niche as AWS SQS + Lambda, Google Cloud Tasks or Upstash QStash, built so the mechanics are visible rather than hidden behind a cloud console.

## Target architecture

Only the API box exists today.
The rest is what the design is aiming at, recorded so the shape is reviewable before it is built.

```
  your services (Django, FastAPI, anything)
        |  POST /v1/jobs   { type, payload, priority, callback_url }
        v
  +--------------------+
  |  antrequeue API    |  idempotency, validation, durable record   <- v0: this
  +--------------------+
        |
        v
  +--------------------+
  |  dispatcher        |  rate-limited drain  <- backpressure valve
  +--------------------+
        |
        v
  +--------------------+
  |  RabbitMQ          |  per-message ack, priority, retry, DLX
  +--------------------+
        |
        v
  +--------------------+
  |  workers           |  Redis distributed lock -> execute -> settle
  +--------------------+
        |
        v
   HMAC-signed webhook back to your service
```

The broker choice is argued in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md), including when Redis Streams is the better answer and you should not use this.

## API

```
POST /v1/jobs        submit a job      (Authorization: Bearer <service secret>)
GET  /v1/jobs/{id}   check status      (Authorization: Bearer <service secret>)
GET  /healthz        liveness, no auth
```

```bash
curl -X POST http://localhost:8090/v1/jobs \
  -H "Authorization: Bearer $ANTREQUEUE_SERVICE_SECRET" \
  -H "Content-Type: application/json" \
  -d '{
        "type": "generate_carbon_report",
        "payload": {"company_id": 789, "year": 2026},
        "priority": "high",
        "max_attempts": 5,
        "callback_url": "https://your-app.example.com/webhooks/antrequeue",
        "idempotency_key": "carbon-report-789-2026"
      }'
```

### Submission fields

| Field | Required | Default | Notes |
|---|---|---|---|
| `type` | yes | | Non-blank job type |
| `payload` | no | `{}` | Arbitrary JSON object, body capped at 1 MiB |
| `priority` | no | `normal` | One of `low`, `normal`, `high`, `critical` |
| `max_attempts` | no | `5` | 1 to 100 |
| `callback_url` | no | | Absolute `http` or `https` URL |
| `idempotency_key` | no | | Re-submitting this key returns the original job |
| `scheduled_at` | no | | RFC 3339 timestamp |

### Responses

| Code | Meaning |
|---|---|
| `202` | Job accepted and queued |
| `200` | An `idempotency_key` matched an existing job, returned unchanged |
| `400` | Malformed body or a field that failed validation |
| `401` | Missing or wrong bearer secret |
| `404` | No job with that id |

## Design goals

**Idempotency by key.** *Built.*
Re-submitting the same `idempotency_key` returns the original job instead of duplicating work, and the lookup and insert are one atomic step so concurrent submissions cannot both win.
Networks retry; execution must not.

**Retries with exponential backoff, then a dead-letter queue.** *Planned.*
A job that exhausts `max_attempts` should be preserved for inspection and replay, never silently dropped.

**Distributed locks (Redis).** *Planned.*
Two workers must never run the same job.
This should also collapse duplicate work: two tenants requesting the same expensive satellite scan for the same coordinates take one lock and share one result.

**Backpressure.** *Planned.*
The dispatcher drains at a bounded rate.
When a downstream dependency is saturated, a CPU-bound LLM node or a third-party API with quota, work should wait in the queue instead of taking the system down.

**Signed callbacks.** *Planned.*
Completion webhooks should carry an HMAC signature so your app can verify the call really came from antrequeue.

## Quick start

```bash
cp .env.example .env       # set ANTREQUEUE_SERVICE_SECRET
go mod tidy
make run                   # API on :8090
```

The API refuses to start without `ANTREQUEUE_SERVICE_SECRET`.
v0 keeps jobs in memory, so nothing survives a restart and there is no reason to run the containers yet.
`docker compose up -d` brings up the Postgres, Redis and RabbitMQ that v1 will need.

## Development

```bash
make test        # portable
make test-race   # what CI gates on, needs cgo and a C toolchain
make fmt vet
```

CI runs gofmt, `go vet`, a build and the race-enabled test suite on every push and pull request.

## Roadmap

- [x] Job submission API with idempotency keys, priorities and input validation
- [x] In-memory store behind a `store.Store` interface
- [ ] Postgres-backed durable job records
- [ ] RabbitMQ dispatch: priority queues, per-message ack, dead-letter exchange
- [ ] Worker pool with Redis distributed locks
- [ ] Exponential-backoff retries and DLQ replay endpoint
- [ ] Rate-limited dispatcher (backpressure)
- [ ] HMAC-signed webhook callbacks
- [ ] Cron-style scheduled jobs
- [ ] Kafka ingest path for high-volume bursts (see [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md))
- [ ] Prometheus metrics and a queue-depth dashboard
- [ ] Python SDK (`antrequeue`) and TypeScript SDK (`@antrequeue/client`)

## License

MIT
