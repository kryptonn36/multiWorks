# Local mock agent co-editor

This package implements Part B. It uses a local mock with no provider credentials or paid API calls. Mock tokens below are development test controls, not a production AI-provider protocol.

## API

All routes require the existing JWT and a project-scoped membership check.

- `POST /api/projects/:projectID/agent/propose` accepts `{"instruction":"Add a close-up shot of a mysterious letter."}` and responds **202 Accepted** with a persisted proposal whose status is `generating`.
- `GET /api/projects/:projectID/agent/proposals/:proposalID` returns `generating`, `pending`, `failed`, `approved`, `rejected`, or `stale`, with `proposed_ops`, `base_seq`, and any `failure_reason`.
- `GET /api/projects/:projectID/agent/proposals` lists proposals using `after_id` pagination.
- `POST .../agent/proposals/:proposalID/approve` applies a pending proposal atomically. Retrying an approved decision returns its existing committed operations.
- `POST .../agent/proposals/:proposalID/reject` rejects a pending proposal without changing the board. Repeated rejection is idempotent.

The original `POST .../agent/proposals` remains a compatibility alias; it is also asynchronous. The frontend uses `/agent/propose`.

Owner/editor access is required to generate, approve, or reject. Viewers can inspect proposals. A proposal ID from another project returns 404.

## Reproducible mock behavior

Normal instructions must describe an add/create/draft action. `Add a shot ...` adds a shot to the first scene; if the board is empty, it proposes creating a scene first. `Add a scene ...` creates a scene. This is a conservative proposal builder, not general natural-language interpretation. Unsupported commands fail validation without modifying the board.

Append these tokens to an instruction to test failures:

| Token | Expected result |
| --- | --- |
| `[mock:invalid-json]` | Failed: malformed JSON. |
| `[mock:missing-shot]` | Failed: update references a shot absent from the captured project board. |
| `[mock:oversized]` | Failed: more than 20 proposed operations. |
| `[mock:timeout]` | Failed after the 3-second generation deadline. |

Normal generation waits about 2 seconds so progress can be tested. The optional `mode` field also retains the earlier testing modes `normal`, `malformed`, `invalid`, `oversized` (over 64 KiB), `delayed`, `timeout`, `missing-shot`, and `too-many`. Instruction tokens take precedence over `mode`.

## Bounds and validation

Instructions contain 1–2000 Unicode characters. A conservative SQL budget is checked before loading board JSON; the captured context must fit 256 KiB. Context and `base_seq` are read from one repeatable-read database snapshot. Oversized boards receive a clear backend error rather than silently incomplete context.

Provider output is limited to 64 KiB, 20 operations, and eight levels of JSON nesting. Strict decoding rejects unknown or duplicate fields, trailing JSON, malformed values, unsupported operation types, and payload fields unrelated to their operation. Only the operation engine's scene/shot mutations are permitted. Agent proposals cannot set external image URLs, asset references, restore payloads, SQL, or filesystem paths. Prompt text remains inert text.

Field sizes and UUIDs are validated, referenced scenes/shots must exist in the captured project state, and new object IDs are checked against existing database objects. A side-effect-free board rehearsal verifies dependent operations and ordering. One proposal may mutate a given scene/shot identity at most once; deleting a scene after proposing edits to its children is rejected. Creating a scene followed by creating its shots is allowed. Duplicate reorder operations for the same list are rejected.

Approval locks the project through `ops.Service.Transact`, checks current authorization and the base sequence, revalidates, then applies every operation and the approved decision in one transaction. Publication occurs only after commit. A changed base marks the proposal stale; there is no automatic rebase. A later operation failure rolls back all preceding changes, sequence allocations, snapshots, and log entries in that batch.

## Jobs and UI

Migration `00008_agent_jobs.sql` adds durable generation jobs. The API process runs the worker. Job context survives restart; an abandoned processing job is reclaimed after its 30-second lease. Attempts are fenced so an old worker cannot overwrite a later result. Three interrupted generation attempts are followed by a failed proposal. Completed jobs release their stored board context.

The UI renders progress, readable proposed field changes, backend errors, and a board-changed warning. It consumes `agent_proposal` WebSocket events and checks only unfinished generation jobs with the status endpoint as a fallback. That fallback stops after generation finishes or the user leaves the project. It never applies drafts optimistically. Approval updates the board through committed operation events/replay and their actual sequence numbers.

## Tests

Run `go test ./...` from `backend`. Set `OPS_TEST_DATABASE_URL` to a migrated PostgreSQL test database and run `go test -race ./...` for asynchronous job recovery, scoped permissions, approval, stale detection, and rollback tests. The Stage 8 fixtures create and drop a separate schema to isolate their workers. Run `npm test` from `frontend` for UI/API tests.
