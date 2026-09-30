# cainban Phase 5 — Task storage: keep DynamoDB, or move to GitHub Issues?

> **Status:** decision doc + implementation plan (not yet built). Frames the
> choice of where a repo's tasks live, for a **multi-user, multi-agent** setup
> (many people, each running one or more agents, working the same repo's board
> concurrently). Researched against GitHub's REST API rate limits (docs checked
> 2026-09-30).

## The question

Today cainban stores tasks in **DynamoDB**, keyed per repo
(`PK = REPO#<owner>/<repo>#BOARD#<boardID>`, `SK = TASK#<id>`), and uses GitHub
only for **authorization** (the App verifies who may touch a repo). Should tasks
instead live in **GitHub Issues** (GitHub becomes the store, not just the
authorizer)?

## What each option actually is

- **A — keep the service-owned store (DynamoDB).** GitHub stays the authorizer.
  cainban owns the data. Concurrency, ids, and conflict handling are cainban's
  responsibility (DynamoDB primitives).
- **B — GitHub Issues as the source of truth.** Each task *is* an issue. cainban
  becomes a thin MCP facade over the Issues API + the board semantics Issues
  don't natively have (todo/doing/priority via labels). No task DB.

## Research: the numbers that decide it (GitHub REST API, 2026-09-30)

| Limit | Value | Applies to |
| --- | --- | --- |
| Primary rate limit (App installation token) | **5,000 req/hr** min; +50/hr per repo and per user over 20; cap **12,500/hr** | all reads+writes on the installation |
| **Secondary: content creation** | **~80 req/min AND ~500 req/hr** | creating issues/comments — **shared across all users of the installation** |
| Concurrent requests | **100** max, shared REST+GraphQL | the whole installation |
| Secondary-limit response | **403 / 429**, "exceeded a secondary rate limit" | must back off + retry |

The load-bearing fact for the multi-agent concern: **issue/comment creation is
capped at ~80/min and ~500/hr per installation, shared by everyone.** Reads are
cheap against the 5,000/hr pool; *writes* are the scarce resource.

## Pros / cons

### Option A — keep DynamoDB (service-owned store)

**Pros**
- **Writes are effectively unlimited** for our scale — DynamoDB PAY_PER_REQUEST,
  no 500/hr creation ceiling. Many agents hammering `create_task`/
  `update_task_status` is fine.
- **Low, predictable latency** — single-digit-ms reads/writes; no GitHub round
  trip per `list_tasks`.
- **Full control of the model** — todo/doing/done, priority, boards, soft
  delete already exist and map exactly.
- **Works even when GitHub API is degraded** (auth still needs GitHub, but the
  board itself keeps serving reads).
- Concurrency is solvable cleanly with DynamoDB conditional writes (optimistic
  version guard): different tasks never contend (separate items); same-task
  edits resolve deterministically.

**Cons**
- **cainban must solve concurrency itself** (version guard + id allocation via
  an atomic counter). Real work, but bounded and well-trodden.
- **Two places work can appear** if users also file GitHub Issues — unless we
  explicitly decide the board is cainban-only and issues are separate.
- We own a datastore (backup, cost, ops) — already true today, low burden.

### Option B — GitHub Issues as the store

**Pros**
- **Concurrency is GitHub's problem.** Issue numbering, simultaneous edits,
  locking — all handled. This directly answers the multi-agent steer.
- **One surface for humans and agents.** A dev files an issue in the GitHub UI;
  an agent picks it up over MCP. No sync layer, no second store, no drift.
- **No task DB to run.** Less infra.
- Portable/visible: the board is just the repo's issues, inspectable in GitHub.

**Cons**
- **The write ceiling is real and shared: ~80/min, ~500/hr per installation.**
  A burst of agents creating/moving tasks (each status change is often a
  comment or an edit) can trip the secondary limit → 403/429 → the board stalls
  for *everyone* on that installation. This is the exact multi-agent scenario,
  and Issues make writes the bottleneck rather than removing the problem.
- **Latency**: every `list_tasks` is a GitHub round trip (100s of ms, subject to
  the 5,000/hr read pool) unless we add a read cache — which reintroduces a
  store and partial sync.
- **Issues don't model a kanban board.** open/closed is 2 states; we need
  todo/doing/done → map "doing" and priority to **labels** (GitHub feature
  coupling, label CRUD, race on label state).
- **Hard GitHub dependency**: API availability and rate limits gate the whole
  product; can't run against a repo when GitHub is down.
- **Migration cost**: retire `dynamo`/`task`/`board` store, add Issues API
  methods to `src/systems/github` (none today), re-point all 8 MCP tools, add
  `Issues: write` to the App permissions, re-verify the whole tenancy model
  against Issues.

## Recommendation

**Keep DynamoDB as the store (Option A), and add optimistic-concurrency so
multi-agent writes are safe.** Reasoning:

1. The multi-agent concern is about **write concurrency**, and Issues make
   writes *worse*, not better: the shared 80/min·500/hr content-creation cap is
   exactly what a fleet of agents would hit, stalling the board for everyone.
   DynamoDB has no such write ceiling at our scale.
2. Latency and model-fit both favor the service store; Issues would need a cache
   + a label-mapping hack to even represent the board.
3. Option A is the smaller change — it *adds* a concurrency guard to code that
   already exists, versus Option B which *rewrites* the storage layer and takes
   a hard external dependency.

Use GitHub for what it's uniquely good at (identity + per-repo authorization,
already built) and keep the high-frequency task writes in a store built for
them.

> If the goal were "the board must be visible/editable in the GitHub UII with
> zero cainban surface," Option B would win. But for many agents writing
> concurrently, A is safer and simpler.

## Implementation plan — Option A: multi-agent-safe DynamoDB store

**P5A.1 — optimistic concurrency on task writes.**
- Add a `version` (int) to the task item; every `update_task*` is a DynamoDB
  `UpdateItem` with `ConditionExpression: version = :expected`, incrementing it.
- On condition failure (another agent wrote first), **re-read and retry** a
  bounded number of times; surface a clean "task changed, retry" MCP error if
  still contended. Different tasks never contend (separate items) — only
  same-task edits do.

**P5A.2 — race-free board-task-id allocation.**
- Replace any read-then-write id assignment with an **atomic counter item**
  (`SK = COUNTER#TASK`) incremented via `UpdateItem ADD`, so two agents creating
  a task simultaneously can never receive the same `board_task_id`.

**P5A.3 — concurrency tests.**
- Table-driven tests firing N concurrent `create_task`/`update_task_status`
  against a fake/store, asserting no lost updates, no duplicate ids, and that
  same-task contention resolves to a single consistent value.

**P5A.4 — (optional) activity feed for observability.**
- Append-only `SK = EVENT#<ts>#<agent>` items per mutation, so a human can see
  who changed what — cheap audit without changing the source of truth.

**Invariants preserved:** tenant isolation (`REPO#<owner>/<repo>#` prefix),
signature-first auth, no secrets in code. No GitHub App permission change.

## If you still prefer Option B (Issues) — the plan would be

1. Add Issues API methods to `src/systems/github` (create/list/get/update/close,
   label CRUD). Add `Issues: write` to the App permissions (re-consent).
2. Re-point the 8 MCP tools at Issues; map status→state+label, priority→label.
3. Add a read-through cache to survive the 5,000/hr read pool + latency; define
   its invalidation.
4. Add **write rate-limiting + backoff** (respect 80/min·500/hr, handle 403/429)
   so a burst degrades gracefully instead of erroring — this is mandatory, not
   optional, for the multi-agent case.
5. Retire `dynamo`/`task`/`board` (or keep as the cache).
6. Re-verify tenancy: a request may only touch issues in the repo its token
   grants.

This is the larger, higher-risk path and inherits GitHub's write ceiling.
