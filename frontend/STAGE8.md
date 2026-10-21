# Stage 8: assets and mock agent

From the repository root, run:

```sh
docker compose up --build
```

Open http://localhost:5173, sign in, and open a project. The API applies migrations `00007_assets_agent.sql` and `00008_agent_jobs.sql` at startup. No third-party agent account or API key is needed.

## Manual testing

| Test | Actions | Expected result |
| --- | --- | --- |
| Upload | Create a scene and shot; click **Upload image**, choose a JPEG, PNG, or WebP and submit. | An attachment appears with processing status, then a thumbnail and ready status. Small images may finish before processing is visible. |
| Deduplicate | Upload the exact same file again, including to another shot in the same project. | A reuse message appears; both shots reference the same asset. The database contains one asset row and one thumbnail job for those bytes. |
| Download | Click **Download original**. | The original bytes download using your authenticated session. |
| Validate uploads | Submit an SVG, text file, image over 10 MiB, or image over the configured pixel limit (25 million by default). | Backend error appears in the dialog; the board is unchanged. Submitting without a file also reaches backend validation. |
| Generate | Under **Agent co-editor**, click **New proposal**, enter an instruction, keep mode `normal`, and submit. | A generating proposal appears immediately, then becomes pending. The board remains unchanged. The local mock proposes a shot in the first scene, creating a scene first if the board is empty. |
| Approve | Review a pending proposal and click **Approve**, then confirm. | All proposed edits commit together through the operation log. The proposal becomes approved. Repeating approval returns the existing result without duplicate edits. |
| Reject | Generate another proposal, then reject it. | Status becomes rejected; the board is unchanged. |
| Stale approval | Generate a proposal; edit the board manually or approve a different proposal; approve the older proposal. | Backend rejects approval with a board-changed message and records stale status. Generate a new proposal to continue. |
| Failure modes | Generate with `malformed`, `invalid`, `oversized`, or `timeout`. | A failed proposal shows a readable reason; no board writes occur. `delayed` waits about 2 seconds then produces a valid pending proposal. `timeout` fails after the configured deadline (15 seconds by default). |
| Live updates | Use an owner/editor session and a second account in an incognito window. Add the account to the workspace and project first. | Both see approved board edits, proposal decisions, and ready thumbnails live. Use separate browser sessions to sign in as different users. |
| Viewer permissions | Give the second account viewer access; attempt upload, proposal generation, approval, and rejection. | Backend rejects every write while permitting board/proposal reads and protected downloads. Controls stay available for testing. |
| Reconnect | Click **Pause live** in one session; upload or decide a proposal in the other; click **Reconnect live**. | Board operations replay contiguously. Asset/proposal metadata refreshes after handoff, including status events missed offline. No asset polling is used; unfinished generation jobs use bounded-lifetime status polling as a fallback. |
| Persistence | Upload an image, then run `docker compose up --build --force-recreate -d`. | The attachment and downloaded bytes remain available. Pending jobs survive restart; an interrupted processing lease is reclaimed after 60 seconds. |
| History | Note the applied sequence after an upload. Delete that shot, preview the earlier version in **History**, and restore it. | Restored shots retain uploaded asset references. The original bytes are kept when a shot is deleted. |

## API and storage

All routes below require the existing bearer token. UUIDs and permissions are checked by the backend. Assets and proposals are project scoped; substituting an ID from another project returns 404.

| Method | Path suffix under `/api/projects/:projectID` | Body / result |
| --- | --- | --- |
| POST | `/shots/:shotID/assets` | Multipart `file`; returns `{asset,deduped}`. |
| GET | `/assets` | `{assets,has_more}`; use `after_id` with the last returned ID for pagination. |
| GET | `/assets/:assetID` | Original image bytes. |
| GET | `/assets/:assetID/thumbnail` | JPEG thumbnail; 409 until ready. |
| POST | `/agent/propose` | `{instruction,mode}`; returns 202 with a generating proposal. |
| GET | `/agent/proposals` | `{proposals,has_more}`; same `after_id` pagination. |
| GET | `/agent/proposals/:proposalID` | Proposal and decision details. |
| POST | `/agent/proposals/:proposalID/approve` | Approves atomically; 409 for stale/terminal decisions. Already-approved retries return the original result. |
| POST | `/agent/proposals/:proposalID/reject` | Rejects a pending proposal. Already-rejected retries return the original result. |

WebSocket metadata events use `{type:"asset_status",asset:{...}}` and `{type:"agent_proposal",proposal:{...}}`. Their nested objects have the same shape as REST responses. They do not advance the board cursor. Attachments use the sequenced `asset.attach` operation with `{scene_id,shot_id,asset_id}`. Approved agent edits use the existing sequenced scene/shot operations.

The browser fetches protected images as blobs with its bearer token and revokes object URLs on leaving the project or losing access. JWTs are never placed in image URLs. The worker runs in the API process using persistent PostgreSQL jobs, leased claims, bounded thumbnail dimensions (320 pixels per side), and at most three image-processing attempts. Asset jobs retry with a delay and publish ready/failed status after committing. A claim abandoned at the retry limit is finalized as failed without another image-processing attempt.

Docker stores originals and thumbnails in the `assets_data` named volume mounted at `/data/assets`. Do not remove named volumes when testing persistence. For running outside Docker, `ASSET_STORAGE_ROOT` defaults to `./data/assets` relative to the backend working directory. Storage keys stay internal. Failed database transactions may leave an unreferenced immutable blob; automatic orphan cleanup is not included.

The mock accepts add/create/draft instructions of 1–2000 characters and produces at most `AGENT_MAX_OPS` operations (20 by default) within 64 KiB. It does not interpret general natural-language commands: it supports adding scenes and draft shots. Captured board context is capped at 256 KiB; unknown/conflicting operations, fields, references, and deeply nested JSON are rejected. Proposals are checked against captured board context, then validated again and authorized at approval. A pending proposal requires an unchanged project sequence; there is no automatic rebase.

All runtime database access uses sqlc queries in `backend/sql/queries/assets.sql` and `agent.sql`, with generated Go methods. Batch application reuses `ops.Service.Transact` and the same per-operation validation, idempotency, sequencing, snapshots, and publication path as REST/WebSocket edits.

## Automated checks

```sh
cd frontend
npm test
```

```sh
cd backend
go test ./...
OPS_TEST_DATABASE_URL='postgres://USER:PASSWORD@localhost:5432/DATABASE?sslmode=disable' go test -race ./...
```

Integration tests skip without `OPS_TEST_DATABASE_URL`. The Stage 8 test creates and drops an isolated schema so its thumbnail worker cannot claim application jobs; the database role needs schema creation permission. Other integration tests use isolated project fixtures in the migrated database. Regenerate/check SQL from the repository root with `sqlc generate -f backend/sqlc.yaml`, `sqlc vet -f backend/sqlc.yaml`, and `sqlc diff -f backend/sqlc.yaml`.

Part B details and reproducible instruction tokens: [mock agent README](../backend/internal/agent/README.md).

## Docker configuration

Set overrides in the repository root `.env` before `docker compose up --build`:

```ini
ASSET_MAX_UPLOAD_BYTES=10485760
ASSET_MAX_PIXELS=25000000
AGENT_MAX_OPS=20
AGENT_TIMEOUT_SECONDS=15
```

Startup rejects empty, malformed, non-positive, or out-of-range values. Maximums are 1 GiB per upload, 100 million pixels, 100 operations, and 300 seconds. Images also retain the existing 8192-pixel side limit; responses remain capped at 64 KiB. Docker uses `ASSET_STORAGE_ROOT=/data/assets` inside the existing named volume. API and both workers run in the same container and share that storage and database. The agent deadline is enforced with `context.WithTimeout`, and job leases extend for longer deadlines. Existing volumes are retained during rebuilds.
