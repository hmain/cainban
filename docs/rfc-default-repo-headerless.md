# RFC: Default-repo on first grant, header-free MCP config

Status: Proposed
Author: cainban maintainers
Date: 2026-10-05
Scope: `cmd/cainban-connect` (grant write), `web/public/cainban-mcp-setup.md`, `web/src/App.tsx`

## Summary

Set a user's `default_repo` when they connect their first repo, then stop
putting the repo in an HTTP header in the config we hand agents. A single-repo
user's MCP config becomes `url` + `clientId` only. This removes the
`X-Cainban-Repo` header that KiroCrew's settings view masks as
`[REDACTED: credential]`, so the mask has nothing to act on, and it removes a
field that can be mangled or dropped on a client restart.

## Problem

The KiroCrew dashboard masks every MCP `headers` value as
`[REDACTED: credential]` on display, because headers are where bearer tokens
normally live. `X-Cainban-Repo` is not a secret — it carries an `owner/repo`
string, and authorization is the token's `repos` claim, never the header — but
the scrubber cannot tell the two apart. Three documentation PRs (#44, #45, #46,
#47) explained the mask is cosmetic; none stopped it, because the scrubber runs
on every display of a header value. The only way to stop the mask is to not
send a header.

A user also reported the stored config emptied to `{"mcpServers": {}}` after a
restart, and the header value surviving as `[REDACTED]` — both are failure
modes of carrying a credential-shaped field the host rewrites.

## Why the header is removable

cainban resolves the target repo in this order
(`src/systems/auth/resolver.go`, `resolveTarget`):

1. explicit MCP tool argument (`argRepo`), per call;
2. the `X-Cainban-Repo` request header;
3. the token's `default_repo` claim.

The header and the arg are both UNTRUSTED for authorization — they only SELECT
which granted repo to act on. Authorization is membership in the validated
`repos` claim. So for a user granted exactly one repo, the header is pure
redundancy: `default_repo` names the same repo with nothing in the config.

## Why header-free does not work today

Verified against the code, not assumed:

- The connect handler writes a grant with `PutGrant`
  (`src/systems/connect/handler.go:397`) but never calls `SetDefaultRepo`.
- `SetDefaultRepo` exists (`src/systems/grants/grants.go:327`) and is tested,
  but is unused outside tests — confirmed by a repo-wide search.
- The pre-token Lambda mints `default_repo` only from the grants table's META
  item via `GetDefaultRepo` (`cmd/cainban-pretoken/main.go`), onto both the id
  and access tokens.
- With a grant but no META item, the token carries `repos` and no
  `default_repo`. A call with no header and no arg resolves to `target == ""`,
  which `Resolve` turns into an authenticated-but-UNSCOPED tenant
  (`resolver.go`). The store path fails closed on unscoped, and the MCP server
  returns `no target repo for this request: set the X-Cainban-Repo header or a
  default_repo claim before calling a tool` (`src/systems/mcp/server.go:233`).

So dropping the header right now would turn the cosmetic mask into a real 403 on
every tool call. The default must be set first.

## Proposal

### Backend: set default on first grant

In `handleRepoPost`, after `PutGrant` succeeds, set the subject's default repo
IF they have no default yet. The first repo a user connects becomes their
default; connecting more repos does not clobber it.

- Read the current default (`GetDefaultRepo`). Only call `SetDefaultRepo` when
  it is empty. This keeps the write idempotent and never overrides a default a
  multi-repo user chose.
- The default-set step is best-effort relative to the grant: a `SetDefaultRepo`
  error is logged but MUST NOT fail the grant response (the grant already
  landed; a missing default degrades to today's header-required behavior, not a
  broken grant).
- `SetDefaultRepo` already normalizes the repo and is scoped to the validated
  `sub` — no new input-trust surface.

This changes what a grant WRITES. It needs a Go deploy of the `cainban-connect`
Lambda, not just an Amplify rebuild, and a `cdk diff` read in full before
deploy.

### Docs and prompt: header-free config for the common case

- `web/public/cainban-mcp-setup.md` and the `App.tsx` agent prompt: present the
  single-repo config as `url` + `clientId` only (dashboard paste shape) or
  `type` + `url` + `client_id` only (on-disk file shape), with no `headers`
  block.
- Keep the `X-Cainban-Repo` header DOCUMENTED for the multi-repo case (one
  header pins one connection to one repo), with the existing cosmetic-mask note
  retained there — a multi-repo user who adds the header still sees the mask,
  and that is correct and now rare.

## Scope boundary: multi-repo switching

Switching repos within one session needs the per-call `argRepo` exposed as a
real MCP tool parameter. `agent-via-mcp.md` notes it is implemented in the
resolver but not yet surfaced in the tool schemas. That is the true multi-repo
answer and a cleaner long-term direction than any header, but it is OUT OF SCOPE
here — this RFC only removes the credential-shaped field for the common
single-repo path. Exposing `argRepo` is a tracked follow-up.

## Alternatives considered

- **Keep documenting the mask as cosmetic (status quo).** Rejected: the
  scrubber runs on every display; documentation cannot win against it, and the
  user has now hit the mask after the docs shipped.
- **Expose `argRepo` as a tool parameter instead.** Deferred: it is the right
  multi-repo mechanism but a larger change, and overkill solely to remove the
  mask for single-repo users. Does not conflict with this RFC.
- **Teach the host scrubber that `X-Cainban-Repo` is not a secret.** Rejected:
  it is another product's internal, out of cainban's control, and would be a
  per-header allowlist the host does not expose.

## Risks

- First change in this effort to touch auth/grant backend behavior rather than
  docs. Mitigation: small, fail-safe relative to the grant, unit-tested, and
  deployed behind a read `cdk diff`.
- A user who already connected a repo BEFORE this ships has a grant with no
  default. Their header-free config would 403. Mitigation: either backfill the
  META default for existing single-grant subjects, or have them re-run connect
  once (connecting an already-granted repo re-runs `PutGrant` and would then set
  the default). The RFC recommends a one-time backfill so no user action is
  needed; this is a separate migration step called out below.
- Multi-repo users relying on the header are unaffected — the header path stays.

## Testing

- Unit: `handleRepoPost` sets the default on the FIRST grant and does NOT
  clobber an existing default on a SECOND grant (fake grant store, assert
  `SetDefaultRepo` called once with the first repo).
- Unit: a `SetDefaultRepo` error does not fail the grant response.
- Integration: with a single-repo token carrying `default_repo`, a header-free
  tool call resolves to the right partition (not unscoped) and `list_tasks`
  returns.
- Regression: a multi-repo token with the header still resolves to the headered
  repo; a named-but-ungranted repo still 403s.

## Migration

One-time backfill: for each subject in the grants table with exactly one
`GRANT#` item and no `META` default, write the META default to that repo. Idempotent
and safe to re-run. Run after the backend deploy, before switching the docs to
header-free, so no live single-repo user is dropped to unscoped.

## Rollout order

1. Deploy the backend change (`cainban-connect`) — new grants set a default.
2. Run the backfill for existing single-grant subjects.
3. Ship the header-free docs/prompt (Amplify rebuild).
4. Verify a header-free config lists the board from a fresh session.
