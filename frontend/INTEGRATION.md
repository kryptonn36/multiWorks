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
