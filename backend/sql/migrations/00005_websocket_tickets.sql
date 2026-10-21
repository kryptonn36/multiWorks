-- +goose Up
CREATE TABLE websocket_tickets (
    ticket_hash TEXT PRIMARY KEY,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX websocket_tickets_expiry_idx ON websocket_tickets(expires_at);

-- +goose Down
DROP TABLE websocket_tickets;
