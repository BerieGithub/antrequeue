# antrequeue

> Distributed task orchestration for services that shouldn't own their own workers.

Submit a job over HTTP, antrequeue queues it, enforces rate limits and
backpressure, retries with exponential backoff, dead-letters what still fails,
and calls your webhook when it settles. Works with any language or framework.

## Why not just use Celery?

Celery is excellent, and in-process. It is Python-only and each application
runs its own workers, broker wiring and retry policy.

antrequeue targets a different shape: **several independently deployed
services, in different languages, sharing one orchestration layer.** A Django
app and a FastAPI app both `POST /v1/jobs` and both receive webhook callbacks.
One place to see every background task, one retry policy, one dead-letter
queue, one dashboard.

That is the same niche as AWS SQS + Lambda, Google Cloud Tasks or Upstash
QStash - built so the mechanics are visible rather than hidden behind a cloud
console.

## How it works

```
  your services (Django, FastAPI, anything)
        |  POST /v1/jobs   { type, payload, priority, callback_url }
        v
  +--------------------+
  |  antrequeue API    |  idempotency, validation, durable record
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

## API

```
POST /v1/jobs        submit a job      (Authorization: Bearer <service secret>)
GET  /v1/jobs/{id}   check status
GET  /healthz
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

Returns `202 Accepted` with the job record.

## Design commitments

**Idempotency by key.** Re-submitting the same `idempotency_key` returns the
original job instead of duplicating work. Networks retry; execution must not.

**Retries with exponential backoff**, then a **dead-letter queue**. A job that
exhausts `max_attempts` is preserved for inspection and replay, never silently
dropped.

**Distributed locks (Redis).** Two workers must never run the same job. This
also collapses duplicate work: two tenants requesting the same expensive
satellite scan for the same coordinates take one lock and share one result.

**Backpressure.** The dispatcher drains at a bounded rate. When a downstream
dependency is saturated - a CPU-bound LLM node, a third-party API with quota -
work waits in the queue instead of taking the system down.

**Signed callbacks.** Completion webhooks carry an HMAC signature so your app
can verify the call really came from antrequeue.

## Quick start

```bash
cp .env.example .env       # set ANTREQUEUE_SERVICE_SECRET
docker compose up -d       # postgres, redis, rabbitmq
go mod tidy
make run                   # API on :8090
```

v0 keeps jobs in memory so the API is runnable end to end. Postgres, the
broker and the workers land in v1 behind the same interfaces.

## Roadmap

- [x] Job submission API with idempotency keys and priorities
- [x] In-memory store behind a `store.Store` interface
- [ ] Postgres-backed durable job records
- [ ] RabbitMQ dispatch: priority queues, per-message ack, dead-letter exchange
- [ ] Worker pool with Redis distributed locks
- [ ] Exponential-backoff retries and DLQ replay endpoint
- [ ] Rate-limited dispatcher (backpressure)
- [ ] HMAC-signed webhook callbacks
- [ ] Cron-style scheduled jobs
- [ ] Kafka ingest path for high-volume bursts (see docs/ARCHITECTURE.md)
- [ ] Prometheus metrics and a queue-depth dashboard
- [ ] Python SDK (`antrequeue`) and TypeScript SDK (`@antrequeue/client`)

## License

MIT
