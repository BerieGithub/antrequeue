# Architecture notes

## ADR 001 - RabbitMQ for dispatch, not Kafka

**Status:** accepted

### Context

antrequeue needs a broker between submission and execution. Kafka is the
obvious "big" choice, so the decision to not use it for dispatch needs
recording.

### Decision

**RabbitMQ carries the work queue.** Kafka is reserved for the ingest and
audit paths described below.

### Why Kafka is the wrong shape for a task queue

| A task queue needs | Kafka gives |
|---|---|
| Ack or retry a **single** message | Consumers commit **offsets**, not messages. Failing message 5 while 6-10 succeed means skipping it or reprocessing the range |
| Delayed retry (30s, 5m, 1h) | No delay primitive; requires per-tier retry topics or an external scheduler |
| Priority | Strict per-partition ordering; priority means separate topics |
| Many workers on one queue | Parallelism capped at partition count |
| Long-running jobs | Head-of-line blocking, and long polls trigger consumer-group rebalances |

The last row is decisive for this workload. A satellite-imagery scan can run
for minutes and fail on third-party quota. On Kafka that job blocks its
partition and risks rebalance storms. On RabbitMQ it is an unacked message
with a retry policy and a dead-letter exchange.

That is why Celery, Sidekiq and their peers are built on AMQP-style brokers.

### Where Kafka does earn its place

Two places, both about durability and replay rather than dispatch:

1. **Burst ingest.** A mobile client offline for days reconnects and fires
   thousands of payloads at once. Kafka absorbs that durably at high
   throughput, partitioned by entity id so events for one shipment or plot
   stay ordered, and a dispatcher drains it into the work queue at a rate the
   system can survive.
2. **Task lifecycle log.** Publishing `submitted / started / retried / failed /
   succeeded` to Kafka gives a durable, replayable stream for dashboards and
   post-mortems without hammering Postgres.

```
mobile burst ──► KAFKA (durable ingest, partitioned by entity)
                   │
                   ▼
             dispatcher (rate-limited drain = backpressure)
                   │
                   ▼
             RABBITMQ (per-task ack, retry, DLQ, priority)
                   │
                   ▼
                workers ──► lifecycle events ──► KAFKA (audit log)
```

Each broker has exactly one job.

### Simpler alternative

If two brokers is too much operationally, **Redis Streams** offers consumer
groups, per-message acks and enough durability for most of this, using
infrastructure already present. The Kafka + RabbitMQ split can come later.

## ADR 002 - Jobs are durable, delivery of results is not

The job record in Postgres is the source of truth. Webhook callbacks are
best-effort with retries; a consumer that misses one can always poll
`GET /v1/jobs/{id}`. This keeps the callback path simple and makes antrequeue
safe to depend on.
