# MultiWorks

MultiWorks is a collaborative storyboard application for organizing projects into scenes and shots. It uses a Go backend, PostgreSQL, and a JavaScript frontend.

## What it does

- Register and sign in to an account.
- Create workspaces and projects, and add members by email with owner, editor, or viewer roles.
- Create, edit, delete, and reorder scenes and shots.
- Collaborate with live updates and see who is viewing each scene.
- Preview earlier board versions and restore them.
- Upload images to shots and download originals.
- Generate suggestions with a local mock agent, then review, approve, or reject its proposed changes.

## Start the application

Install Docker with Docker Compose, then run this command from the project root:

```sh
docker compose up --build
```

Open the frontend at **http://localhost:5173**. The backend runs at **http://localhost:8080** and PostgreSQL uses port **5432**.

Compose starts all three services and applies database migrations automatically. No additional setup or external service is required. The default configuration is for local development.

Only Docker and Docker Compose are needed on your computer; you do not need to install Go, Node.js, PostgreSQL, sqlc, or Goose separately. The backend build runs sqlc to generate database code, and the backend runs Goose migrations when it starts. A `.env` file is optional because Compose supplies local defaults.

The first build requires internet access to download container images and dependencies. Keep ports **5173**, **8080**, and **5432** available, and wait for the services to finish starting before opening the frontend.

If an older Docker installation crashes in its Bake plugin, update Docker Compose and Buildx. On versions supporting the standard builder, you can also use `COMPOSE_BAKE=false docker compose up --build`.

## Get started

1. Register an account through the frontend. Registration signs you in automatically.
2. Create a workspace, then create a project inside it.
3. Open the project and add scenes and shots.
4. To collaborate, have another person register an account. Add their email to the workspace first, then add them to the project with the appropriate role.
5. Open the project in separate browser profiles to try live collaboration with different accounts.

There are no pre-created accounts.

## Stop the application

```sh
docker compose down
```

Database records and uploaded images are stored in named Docker volumes and remain available after stopping or rebuilding the application.
