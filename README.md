# Sedobno — food-label scanner and calorie tracker (Go backend)

A Telegram mini-app running in production. You photograph a food label and get an
additive score with red flags; you photograph a plate and get calories and macros.
Paid subscriptions with recurring charges, six background workers, a four-phase
vision pipeline that is never trusted with the final number.

I built and run it alone: product decisions, Go backend, React client, deployment
and the pager.

**Stack** — Go 1.25 · chi · pgx/v5 · PostgreSQL 16 · goose · Docker (distroless) · Caddy · React/TypeScript
**Size** — ~21.7k lines of Go and ~16.9k lines of tests (581 test functions), 38 HTTP endpoints, 32 migrations
**Status** — live, with a separate beta environment; 371 commits since July 2026

---

## If you only have five minutes

This is a product, not a library, so here is the shortest path through the parts that
actually show how I work. Every file below is one I would enjoy being questioned about.

| Where to look | Why it is worth the minute |
|---|---|
| [`store.go` → `RenewSubscriptionSuccess`](api/internal/store/store.go#L597) | Applying a payment **at most once**. The payment row is inserted first inside the transaction; if `ON CONFLICT` inserted nothing, the subscription is left untouched and we return "already applied". An idempotency key protects against charging twice — it does not protect against *applying* one charge twice, which would hand out two periods of access for one payment. |
| [`subsweep.go` → `renewalIdempotenceKey`](api/internal/server/subsweep.go#L209) and [its test](api/internal/server/subsweep_key_test.go) | The key for a recurring charge is derived from subscription state **as it was before** the charge, so every retry of the same period produces the same key. The test pins that invariant rather than the formula — after rolling `expires_at` back by one second on beta produced a second real charge in the merchant account. |
| [`llm/retry.go`](api/internal/llm/retry.go) | Classifying provider failures instead of retrying all of them: 429 and 5xx back off and honour `Retry-After` (capped — a person is staring at a spinner), 4xx and billing errors fail immediately. Retrying a 400 only doubles the wait for an answer that will not change. |
| [`pipeline.go` → `acquire` / `tryAcquire`](api/internal/pipeline/pipeline.go#L975) | A non-blocking admission gate in front of expensive model calls. No free slot means 429 with `Retry-After` *before* any work starts, so a rejected request costs the user nothing from their quota. |
| [`selfcheck.go` → `extractPair`](api/internal/pipeline/selfcheck.go) | Two parallel readings of the same frame to detect the model disagreeing with itself. The control call takes its slot non-blockingly, so under load the second opinion switches itself off first and the user's answer never does. |
| [`testdb.go`](api/internal/testdb/testdb.go) | Storage and handler tests run against a real PostgreSQL in a throwaway container with the production migrations applied. Without Docker the test **fails** rather than skipping quietly — a skipped test that nobody notices is worse than no test. |
| [`cmd/loadtest`](api/cmd/loadtest/main.go) | A load driver written in Go rather than k6, because the simulated clients need valid JWTs signed by the same code that verifies them. Scenario B fires 20 concurrent requests from one user against a quota of 10 and asserts exactly 10 were consumed — both in the responses and in the database. |
| [`llm/providers.go`](api/internal/llm/providers.go#L20) and [`db/db.go`](api/internal/db/db.go) | Two defaults that silently throttle a service: `MaxIdleConnsPerHost = 2` on Go's shared transport, and pgx sizing its pool to `max(4, NumCPU)` — four connections for the whole API on a two-core VPS. Both are set deliberately, with the reasoning kept next to the value. |
| [`docs/incidents/2026-10-09-dockerd-crash.md`](docs/incidents/2026-10-09-dockerd-crash.md) and [`ops/dockerd-crashtest/`](ops/dockerd-crashtest) | A production incident reproduced off the server before changing anything on it. |

## How it is put together

```
cmd/api          process wiring: config, pool, pipelines, HTTP timeouts, signal handling
  └ internal/server      chi router, middleware, 38 handlers, background workers
      └ internal/pipeline, meal, scoring, nutrition, dict      business logic
          └ internal/store                                     SQL over pgx, transactions
              └ internal/llm, tg, yookassa                     outbound integrations
```

Dependencies are passed through constructors; `cmd/api/main.go` is the only place that
knows how the whole thing is assembled. The direct dependency list is five entries long
(chi, pgx, goose, yaml, x/image) — everything else is the standard library.

The scoring itself is deliberately **not** done by the model: phase 1 extracts text from
the photo, phase 2 normalises it against a dictionary of ~290 additives, phase 3 computes
the score from rules in [`config/scoring.yaml`](api/config), and only phase 4 lets a model
phrase an explanation — which is then checked for forbidden claims. A model that invents
an ingredient list also invents a confident transcription of it, so there are explicit
gates for that in [`pipeline/hallucination.go`](api/internal/pipeline/hallucination.go).

## Testing

```bash
cd api
go test ./...                              # unit + integration (needs Docker for PostgreSQL)
TEST_DATABASE_URL=postgres://… go test ./...   # or point at a database you already have
```

581 test functions across 102 files. Handler tests go through `httptest` and a real
database; the LLM providers are behind an interface with a scripted mock, so the pipeline
is tested without spending money. The one thing that costs money — accuracy of the vision
prompts — has its own harness (`make evalpipe`, `make meal-eval`) that runs against real
photos in [`testdata/`](testdata) and is kept out of the normal test run behind a build tag.

## Running it locally

```bash
make env     # .env from .env.example
make up      # postgres + migrations + api + web + caddy
make seed    # load the additive dictionary
curl localhost/api/health
```

The image is multi-stage and lands on `gcr.io/distroless/static-debian12:nonroot`; since
that image has no shell, the health check is a mode of the binary itself (`api healthcheck`).

## Three things to know before you read the code

**The comments are in Russian.** This was a solo project and the reasoning was written for
me. The reasoning is the valuable part, and I am happy to walk through any of it in English.

**It was written with heavy use of AI tooling** (Claude Code). I designed the architecture,
made the product and engineering decisions, drove the implementation and reviewed every
change — the comments recording *why* a decision was made are the record of that. I mention
it because you would see it in the repository anyway, and because knowing where these tools
help and where they quietly make things worse is now part of the job.

**It runs as a single instance on purpose.** Rate limiting is in memory, the analysis cache
and the queue of unfinished saves are tables in PostgreSQL, and there is no Redis or Kafka
here — at this traffic they would be infrastructure to operate rather than a problem solved.
Where that choice bounds the design, the code says so, for example in
[`ratelimit.go`](api/internal/server/ratelimit.go).

---

Russian version of the original README: [README.ru.md](README.ru.md).
