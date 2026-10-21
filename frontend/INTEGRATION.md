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

## Storyboard integration

Project pages now open a Storyboard tab, with existing membership under People. The new `storyboard.js` module reuses the existing API client, dialog, styling, and feedback. All nine operations carry the session bearer token and JSON content type through the same proxy.

| UI action | Method and endpoint | Body / response |
| --- | --- | --- |
| Load / Refresh storyboard | `GET /api/projects/:projectID/board` | `{project_id,scenes:[{id,title,description,position,shots:[...]}]}` |
| Add scene | `POST /api/projects/:projectID/scenes` | `{title,description}` → scene object |
| Edit scene | `PATCH /api/projects/:projectID/scenes/:sceneID` | Selected fields from `{title,description,position}` → scene object |
| Delete scene | `DELETE /api/projects/:projectID/scenes/:sceneID` | Deletes scene and cascading shots; confirmation dialog precedes request |
| Scene ↑ / ↓ / Edit scene order | `PUT /api/projects/:projectID/scenes/order` | `{ids:[all scene IDs in desired order]}` |
| Add shot | `POST /api/projects/:projectID/scenes/:sceneID/shots` | `{prompt,style,notes,image_url}` → shot object |
| Edit shot | `PATCH /api/projects/:projectID/scenes/:sceneID/shots/:shotID` | Selected fields from `{prompt,style,notes,image_url,position}` → shot object |
| Delete shot | `DELETE /api/projects/:projectID/scenes/:sceneID/shots/:shotID` | Deletes only that shot, after confirmation |
| Shot ↑ / ↓ / Edit shot order | `PUT /api/projects/:projectID/scenes/:sceneID/shots/order` | `{ids:[all shot IDs for that scene]}` |

Editing a field checks its Update checkbox automatically. Uncheck fields to omit them from PATCH, or submit no fields to test the backend's empty-update error. Position is a text input encoded as a JSON value when parsable, so negative/non-integer/string values still reach backend validation. Order dialogs accept arbitrary IDs, duplicates, and empty lists for backend validation tests. No controls are hidden by project role: the backend grants reads to project members and writes to owners/editors.

Images are referenced by URL; there is no image generation or upload endpoint. Create shot has an explicit No image checkbox that sends `image_url:null`; typing an image URL unchecks it. Editing an image URL to an empty string sends that exact string. The current backend uses a pointer plus COALESCE for nullable PATCH fields: `image_url:null` cannot clear an existing URL and a null-only patch is rejected as an empty update. The UI does not claim otherwise. HTTP(S) image URLs are previewed; failed images show a placeholder without changing stored data.

Successful mutations reload the board from the server. Load failures have a Retry button; validation/permission failures remain in the dialog; arrow-order failures appear above the board. There is no background polling or optimistic success data. No backend changes were needed for this UI integration.

Storyboard verification: all 11 frontend tests passed, including coverage of all 23 registered routes, PATCH/PUT/DELETE proxy forwarding, nested scene/shot paths, bearer headers, and partial bodies. Existing backend tests passed. The Compose stack rebuilt successfully and applied the storyboard migration. Live Chrome tests against PostgreSQL covered all nine storyboard routes, empty board, invalid title/position errors, partial updates preserving untouched fields, null image creation, clearing an image with an empty string, persisted scene/shot order, invalid/duplicate order IDs, cancelled deletion, shot deletion, scene deletion with its shots, viewer write rejection, and editor write success. Desktop and 390px mobile layouts were inspected; no uncaught browser exceptions occurred. Test accounts and a workspace named `Storyboard verification …` remain in the local database. No existing user scenes/shots were changed or deleted.
