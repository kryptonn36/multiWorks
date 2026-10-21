# MultiWorks frontend

Simple forms, cards, detail pages, and people lists for the existing backend. Uses the existing native HTML/CSS/JavaScript frontend, Node proxy, green styling, endpoint definitions, and Docker workflow. No dependencies to install.

## Run

From the repository root:

```sh
docker compose up --build
```

Open **http://localhost:5173**. Compose starts PostgreSQL, the Go backend (which applies migrations), and the frontend. The frontend proxy uses `API_TARGET=http://api:8080` inside Docker. The backend is also available at **http://localhost:8080**.

If your installed Compose Bake plugin crashes as it did during earlier verification:

```sh
COMPOSE_BAKE=false docker compose up --build
```

Stop with `docker compose down`; the named PostgreSQL volume is retained. Health checks stop making HTTP calls after the first successful backend startup check. The UI never polls health.

For frontend development without Docker (Node.js 20+), start your backend separately, then:

```sh
cd frontend
npm run dev
```

The proxy defaults to `http://localhost:8080`. To change it:

```sh
API_TARGET=http://localhost:9090 FRONTEND_PORT=5174 npm run dev
```

`API_TARGET` is the backend origin, without `/api`. No backend CORS changes are required.

## Manual UI test checklist

1. **Register:** Choose Create an account. Enter a new email and an 8–128 byte password. Submit once. You should be signed in automatically and see home with an empty workspaces state. Duplicate email and invalid passwords should show an error.
2. **Login and profile:** Open My account; verify your email and creation date. Copy the Account ID. Sign out, try a wrong password (expect an error), then sign in with the correct password. Reload; your session should be restored in the same tab.
3. **Connection:** In My account, click Check connection. A readable success or failure message appears. There are no repeating UI health calls.
4. **Workspaces:** From home, click Create workspace, enter a name, and submit. Its detail page should open. Return to Workspaces to see the new card. Search by name and click the card to retrieve it again. Submit blank or overlong names to see the backend reject them; the frontend does not block them.
5. **Projects:** In the workspace’s Projects tab, click Create project. Enter a name and submit. Verify the project detail page, dates, owner role, and people list. Go back to the workspace and open it from the Projects list.
6. **Workspace members:** Register a second account in a separate session. The **Add workspace member** button is visible on the workspace Projects tab, workspace People tab, and project page. It is also available inside the project member dialog and carries over the entered email. Open workspace → People → Add member and enter its **email** and a role. The request sends `{email, role}`. Try a blank/invalid/unregistered email or unsupported role to inspect the backend error. Manage access also submits the member's email, not their UUID.
7. **Project members:** Open project → Add member. Enter the registered person’s email and a role. Both member endpoints now accept `{email, role}` and resolve email to the internal user ID in their handlers. Submit Viewer/Editor for valid cases, or invalid emails/roles to test backend errors. Project members must already belong to the workspace. The frontend sends project requests directly, without automatically adding workspace membership.
8. **Permissions:** In the second account, open the shared workspace/project. Member actions remain available to every signed-in user who can open the page; submit them to exercise the backend's permission checks. For example, an ordinary workspace member's attempt to add a workspace member should return 403. Owners can also submit role changes for owner rows to observe the backend's owner-preservation behavior.
9. **Expired session / unavailable pages:** Let the token expire (15 minutes), then navigate to a protected page; expect sign in and a session-expired message. To exercise 404, change a workspace or project ID in the browser hash to an unknown UUID; expect an unavailable/access message. Stop the API and navigate or submit a form to verify a readable failure state and retry button. Restart the API afterward.

All forms use `novalidate`. There are no frontend required/length/email/UUID checks, role restrictions, or automatic input trimming; values are sent as entered. Suggestions are optional and backend errors are shown as returned. Requests disable the submitting button while pending; dialogs display validation/API errors and stay open on failure. Successful creation opens the new detail page. Membership changes refresh people lists and show a confirmation. Passwords are never persisted; only the access token is stored in this tab’s session storage. Sign out clears it. There is no backend logout/refresh route.

## Integration details and checks

[INTEGRATION.md](INTEGRATION.md) maps every implemented endpoint, exact request/response contract, permissions, UI location, and backend limitations. Project membership now accepts `{email, role}`, matching workspace membership; IDs remain internal to the backend. Unsupported rename/delete/content editing controls are not present.

```sh
cd frontend
npm test
```

The checks cover Go route coverage, all proxied endpoints, static assets, exact client request formats, automatic login after registration, token expiry, and API/network failures. Client/proxy tests use controlled responses and do not establish end-to-end correctness against PostgreSQL. There is no frontend build, lint, or typecheck script; the frontend serves native browser modules.

## Test storyboard scenes and shots

Open a workspace, then a project. Its default **Storyboard** tab uses real data; **People** contains project membership.

1. **Create scenes:** Click Add scene; enter Title and Description. Submit a blank title to test the backend error, then create two valid scenes.
2. **Edit scenes:** Click Edit scene. Change Title or Description; the corresponding Update checkbox is selected automatically. Only selected fields are sent. You can also select Position and enter `0`, a negative value, or an invalid value to test backend validation.
3. **Create shots:** Click Add shot inside a scene. Enter Prompt, Style, and Notes. Leave No image checked to send null, or type an Image URL to preview an HTTP(S) image after saving. Add two shots to one scene.
4. **Edit shots:** Click Edit on a shot and change only Prompt. Other fields remain unchanged unless their Update boxes are selected. Clearing the Image URL sends an empty string; the current backend does not clear existing image URLs with a null PATCH.
5. **Reorder:** Scene and shot ↑ / ↓ buttons immediately save all IDs in the new order. Reload the page to verify persistence. Edit scene order / Edit shot order opens a list of IDs, one per line, which also lets you submit duplicates, missing IDs, invalid IDs, or an empty list to see backend errors.
6. **Delete:** Click a shot's Delete button and confirm; other shots remain. Click Delete scene and confirm to delete that scene and its shots. Cancel sends no deletion request.
7. **Permissions:** Add a second account to the workspace and project as Viewer. Its storyboard loads, and write controls remain visible; submitting a scene/shot change should show `editor or owner role required`. Change the account to Editor and retry; the backend should allow changes.
8. **Reload / session:** Refresh the board or page to verify saved content and order. Expired/invalid sessions return to sign in. Stop the API and use Refresh to check the board error/retry state.

No frontend business-validation rules are added. Submission buttons are disabled only while a request is pending. The existing Compose command builds both applications and applies the storyboard migration automatically on backend startup.
