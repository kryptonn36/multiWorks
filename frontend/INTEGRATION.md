# Backend integration map

The implementation was traced through `cmd/server/main.go`, auth middleware and handlers, workspace/project handlers, generated queries, SQL source queries, and migrations. The frontend remains native HTML/CSS/JavaScript with its existing Node proxy and central endpoint definitions. No framework or runtime dependency was added. Member creation accepts `{email,role}` for both workspaces and projects; the backend resolves email to the existing UUID-based membership records.

All requests go through the same-origin `/backend` proxy. Its destination comes from `API_TARGET` in the Node process (Compose supplies `http://api:8080`). JSON requests send `Content-Type: application/json`. Protected requests send `Authorization: Bearer <access_token>` from session storage; passwords are never stored. Protected pages validate restored sessions with `/me`. A protected `401` clears the session and returns to sign in. Sign out is local because no logout or token revocation endpoint exists.

| Feature / UI | Endpoint | Input | Response | Permissions |
| --- | --- | --- | --- | --- |
| Account → Check connection | `GET /health` | None | `{status, service, database}` or `{error}` | Public; manual only, no polling |
| Create account | `POST /api/auth/register` | `{email,password}` | `{user:{id,email,created_at}}` | Public; password 8–128 UTF-8 bytes |
| Sign in; automatic sign in after registration | `POST /api/auth/login` | `{email,password}` | `{user,access_token,token_type,expires_in}` | Public; token expires after 15 minutes |
| My account; restore session | `GET /api/auth/me` | None | `{user}` | Signed in |
| Home / Workspaces | `GET /api/workspaces` | None | `{workspaces:[{id,name,created_by,created_at}]}` | Only current user's workspaces |
| Create workspace dialog | `POST /api/workspaces` | `{name}` | Workspace object | Signed in; creator becomes owner |
| Workspace page | `GET /api/workspaces/:workspaceID` | Workspace ID from selected card | Workspace object | Workspace member |
| Workspace → People | `GET /api/workspaces/:workspaceID/members` | Workspace ID | `{members:[{user_id,email,role,joined_at}]}` | Workspace member |
| Add workspace member / Manage access | `POST /api/workspaces/:workspaceID/members` | `{email,role}`; role `member` or `admin` | `{status:"member added",email,role}` | Workspace owner/admin; target must exist |
| Workspace → Projects | `GET /api/workspaces/:workspaceID/projects` | Workspace ID | `{projects:[{id,workspace_id,name,created_by,last_seq,member_role,created_at,updated_at}]}` | Query only returns projects where user is both workspace and project member |
| Create project dialog | `POST /api/workspaces/:workspaceID/projects` | `{name}` | Project object | Workspace member; creator becomes project owner |
| Project page | `GET /api/projects/:projectID` | Project ID from selected card | Project object | Both workspace and project member |
| Project → People | `GET /api/projects/:projectID/members` | Project ID | `{members:[{user_id,email,role,joined_at}]}` | Both workspace and project member |
| Add project member / Manage access | `POST /api/projects/:projectID/members` | `{email,role}`; role `viewer` or `editor` | `{status:"member added"}` | Project owner only; target must already be workspace member |

## Contract details and limits

- Register returns a user but no token. The UI follows it with a real login request, then opens home. If login fails after successful registration, the UI prompts sign in instead of repeating registration.
- Workspace membership accepts email and resolves it to an internal UUID in the backend. The frontend sends `{email,role}`. Project membership also accepts `{email,role}` and reuses the same case-insensitive lookup. Both forms have optional email suggestions, and both store membership under the resolved internal UUID.
- Membership POST queries perform upserts, so Manage access uses the same endpoint for role changes. All rows, including owners, are editable for testing; the backend determines whether the change is allowed or preserves the owner role.
- Workspace list responses omit membership roles. The workspace detail page derives the current role from the member list; workspace cards only mark the creator as owner.
- All form/business validation belongs to the backend. Forms use `novalidate`, do not impose required/length/email/UUID checks, and send text exactly as entered. Member and role fields use optional suggestions rather than restrictive selects. Member actions remain visible regardless of the displayed role, allowing backend permission failures to be tested.
- Missing/inaccessible resources return 404. The UI displays an access/unavailable message without claiming the resource was deleted.
- There are no rename, delete, task/content editing, invitation-by-email, password reset, logout, or refresh-token routes. These controls were not added. An empty project page shows its details and actual membership only.
- Health checks are manual in the UI. The existing Compose startup marker behavior is preserved.

See README.md for run commands and manual verification steps. Automated client/proxy tests use controlled responses to check request/error behavior; they are not proof of live database integration.

## Verification

Client/proxy tests exercise request formats, token handling, backend/network errors, and all registered routes. Browser checks against the running stack are recorded separately from controlled-response unit tests. Earlier browser checks used the former UUID workspace contract; they are not evidence for the updated email contract.

## Testing preference

See AGENTS.md. This frontend is a testing interface: backend handlers own constraints. The former automatic workspace-prerequisite flow was removed so project requests reach the backend unchanged, including when their prerequisites are not satisfied.

Updated email-contract verification: all 10 frontend tests and existing backend tests passed. Live Chrome checks against Compose/PostgreSQL confirmed email-based workspace addition and role updates. Network inspection confirmed malformed/blank emails, unsupported roles, short passwords, and whitespace/overlong names were submitted unchanged and rejected by the backend. Ordinary workspace members and project viewers could submit membership actions and received the backend's authorization errors. That earlier run used the former project `user_id` contract; project membership has since been changed to email as described above. No uncaught browser errors were observed. These checks created test accounts and a workspace named `Email membership …` in the local database.

Project email lookup is now implemented in `backend/internal/project/handler.go`, reusing `GetUserByEmailForWorkspace` after checking project-owner permissions. The request accepts `{email,role}`, looks up the existing user case-insensitively, and passes `target.ID` into the unchanged UUID-based membership query. No database migration is needed.

Verification for this change: backend tests and all 10 frontend tests passed. Live Chrome checks against Compose/PostgreSQL verified invalid email errors, unknown-user errors, rejection before workspace membership exists, successful lookup using an uppercase email with the correct internal user ID, role changes by email, and denial of member changes by a non-owner. No uncaught browser exceptions occurred. Test accounts and a workspace named `Project email verification …` remain in the local database.

Missing workspace membership now returns an explicit backend message asking to add the user to the project's workspace first (status remains 404). The project member form includes a navigation link to workspace people. This is guidance only: submissions remain unrestricted, and workspace membership is not created automatically. Backend tests, frontend syntax, and diff checks passed for this clarification.

Workspace-member action discoverability: explicit Add workspace member buttons now appear on both workspace tabs and on project pages, beside the separate Add project member action. The project member dialog also has an Add workspace member button that opens the workspace form with the typed email preserved. This only opens a form; requests are still submitted explicitly, and no prerequisite membership is created automatically. Live Chrome/PostgreSQL verification passed for button visibility, email preservation, saving workspace membership, and then saving project membership. No uncaught browser exceptions occurred; syntax and diff checks passed.

## Storyboard operation integration (current)

The board remains at `GET /api/projects/:projectID/board`, and every storyboard mutation is now submitted to `POST /api/projects/:projectID/ops`:

```json
{
  "client_id": "stable-tab-id",
  "op_id": "stable-intent-id",
  "base_seq": 12,
  "type": "shot.update",
  "payload": {"scene_id": "scene-uuid", "shot_id": "shot-uuid", "prompt": "New prompt"}
}
```

| UI action | Operation type | Payload |
| --- | --- | --- |
| Add scene | `scene.create` | `{scene_id,title,description}` |
| Edit scene | `scene.update` | `{scene_id,...selected title/description fields}` |
| Delete scene | `scene.delete` | `{scene_id}`; also removes its shots |
| Scene ↑ / ↓ / Edit order | `scene.reorder` | `{scene_ids:[all IDs in order]}` |
| Add shot | `shot.create` | `{scene_id,shot_id,prompt,style,notes,image_url}` |
| Edit shot | `shot.update` | `{scene_id,shot_id,...selected prompt/style/notes/image_url fields}` |
| Delete shot | `shot.delete` | `{scene_id,shot_id}` |
| Shot ↑ / ↓ / Edit order | `shot.reorder` | `{scene_id,shot_ids:[all IDs in order]}` |

Fields are sent as entered. Forms retain `novalidate`, free-text roles, and visible controls regardless of role. Position fields were removed from edit dialogs because the operation payload has no position update; reordering uses complete ID arrays. Image null remains an explicit create choice; an empty string clears a displayed image. Null update fields follow the existing backend pointer semantics (unchanged).

### Bootstrap and cursor correctness

The former board response had no consistent watermark. GetBoard now reads `last_seq`, scenes, and shots inside one read-only REPEATABLE READ transaction, returning `{project_id,last_seq,scenes}`. The frontend installs that complete snapshot before setting its initial cursor. This does not infer state from a replay response's `current_seq` or a separately fetched project sequence. It also preserves existing Stage 4 rows that predate the operation log.

`board-sync.js` then requests `GET /api/projects/:projectID/ops?after_seq=LAST_APPLIED&limit=100`. It sorts returned operations ascending, ignores already-applied overlap, and requires contiguous new sequences. Each reducer applies to a copy; only successful application replaces local state and advances the cursor. A failed application leaves earlier successful operations applied and retries from that cursor. `has_more` pages use the actual last applied seq. Empty pages, future `current_seq`, and POST acknowledgements never advance the cursor by themselves. Gaps, invalid pages, unknown operation types, or pagination without progress show an error instead of skipping history.

### Idempotency and lifecycle

Create IDs and operation IDs are generated once per form intent. A per-tab client ID and exact pending envelope are stored in session storage (pending envelopes are scoped by account/project). Unknown network/5xx outcomes preserve the original key, base sequence, and payload. Retry pending write resends the same envelope; if already acknowledged, it only resumes replay. The request is cleared after replay confirms the acknowledged sequence, or on a definitive 4xx rejection before acknowledgment. New intents wait for an unresolved outcome. Navigation stops the old synchronizer; a fresh view bootstraps from a new complete snapshot. No standalone sequence is persisted without its board.

The UI renders replayed results, not optimistic mock data. WebSocket events now deliver changes automatically, while Sync changes remains a manual replay control. There is no health or replay polling timer. Existing auth/token handling and People views are retained.

### Backend changes needed for integration

- Removed all eight direct storyboard write route registrations. GET board and GET/POST ops remain active. Legacy handler definitions remain unregistered.
- Audited sequence/log writes: only ops.Submit increments `projects.last_seq` and inserts `project_ops`, inside the same project-row-locked transaction as the mutation.
- Fixed scene.create to reject a missing/invalid title or oversized description rather than dereferencing a nil title.
- Fixed shot.create to validate `scene_id`, check that scene, and insert the supplied `shot_id`. Previously it treated shot_id as scene_id and inserted an unrelated generated UUID, making valid creation and replay inconsistent.
- Added the atomic board checkpoint described above. No new migration or schema change was needed beyond the user's existing operation-log migration.

### Verification

All 21 frontend tests passed, covering all 17 current route registrations, replay query forwarding, exact envelopes, ordered pagination, overlapping records, gaps, reducer failures, partial fields, duplicate-safe retries, post acknowledgment versus applied cursor, saved pending requests after reload, and permission rejections. Backend tests passed, including the nil-title regression test. JavaScript syntax and diff checks passed.

Live Chrome testing against Compose/PostgreSQL used three independent clients and a new test project. It verified all eight operation types; rejected input without sequence allocation; a second client catching up using replay rather than GET board; an intervening remote write before a local acknowledgment; 101 updates fetched as pages after seq 12 then 112; dropped POST acknowledgments after server commit; retries with identical envelopes; pending shot recovery after a page reload; all eight legacy write URLs returning 404/405; 115 contiguous log sequences and exactly one row per idempotency key; and Viewer writes rejected without cursor advancement. Mobile layout fit the viewport, and there were no uncaught browser exceptions.

Test accounts and a workspace named `Ops verification …` remain in the local database. Only that run's own test scenes/shots were deleted. Earlier Stage 4 test results do not establish operation-log behavior; the checks above were run against the new pipeline.


## WebSocket integration

`projectSocket.js` obtains a fresh ticket for every connection/reconnect, uses the existing Node proxy for upgrades, sends heartbeats and view_scene, and stops timers on disposal. `board-sync.js` buffers gaps and shares contiguous application between live messages and HTTP/socket replay. `storyboard.js` renders status and presence using membership emails/initials, refreshing membership names for previously unknown collaborators. The existing forms and REST operation submission remain unchanged.

Backend additions: bearer-protected POST `/api/projects/:projectID/ws-ticket`, atomic hash-based ticket consumption (30 seconds; one use; project-bound), exact origin checks via FRONTEND_ORIGINS, and migration 00005. GET `/api/projects/:projectID/ws` uses ticket validation outside bearer middleware, then rechecks project membership. One writer drains the client queue; cancellation uses a separate Done channel so concurrent replay cannot panic by sending to a closed channel. Permission checks run independently of read deadlines, avoiding reuse of a failed Gorilla read. Multi-tab presence chooses the most recently active socket for each user.

Verification: frontend tests cover fresh ticket reconnection, cursor/scene restoration, heartbeat/disposal cleanup, overlapping replay, buffered gaps, and reducer failure. Backend tests exercise origin validation and concurrent hub producers/disconnects with the race detector. See README.md for manual collaboration checks and deployment origin configuration.

Live verification against Docker/PostgreSQL and two headless Chrome sessions passed: authenticated presence with email lookup, ticket reuse rejection, missing bearer rejection for issuance, unapproved origin rejection, JWT-free WebSocket URLs, immediate scene delivery, viewed-scene presence, idle/heartbeat stability, fresh-ticket reconnect with missed-operation recovery, and disconnect presence cleanup. The browser sessions reported no uncaught JavaScript exceptions. This does not test multiple API replicas; the hub remains process-local.

The existing storyboard browser regression also passed with live delivery enabled: all eight operation types, partial updates, ordering and cascading deletion, lost-acknowledgment retry, pending-write recovery after reload, legacy write-route rejection, contiguous operation history, viewer write rejection, and mobile layout. Live redraws retain pending button states; deletion clears a now-missing viewed scene.


## Ordered reconnect and permission notifications

The socket now uses `replay_start` / `replay_complete` with a fixed `boundary_seq`.
Live operations remain in a server buffer until bounded replay completes, then
are released contiguously. The frontend serializes operation delivery, verifies
successful application through the boundary before reporting online, and keeps
its existing sequence buffer/replay fallback. Pause live / Reconnect live controls
allow testing missed edits without replacing the board with a fresh snapshot.

Membership upserts publish post-commit signals; affected writers refresh actual
permissions and send role-change or full-revocation messages. The open storyboard
updates Your access and shows a message, while all write controls remain available
for backend constraint testing. Full revocation stops reconnect and heartbeat
timers. Permission notifications bypass slow frontend replay. The periodic server
check remains the fallback. Current membership routes support upserts, not removal.

Two Chrome sessions against the Docker stack verified multi-page reconnect after
125 missed updates, boundary-capped replay followed by live delivery, prompt
editor-to-viewer downgrade via Manage access, backend rejection of both REST and
socket writes, restoration to editor without reload, presence, and navigation
cleanup. The normal frontend write path remains REST /ops. See README.md for the
manual test steps, including the pause/reconnect controls.
