# Operation submissions over WebSocket

Authenticate with the existing single-use ticket flow. On a project socket, send
an operation envelope under `operation`:

```json
{
  "type": "submit_op",
  "operation": {
    "client_id": "stable-client-id",
    "op_id": "unique-intent-id",
    "base_seq": 0,
    "type": "scene.create",
    "payload": {
      "scene_id": "CLIENT_GENERATED_UUID",
      "title": "Opening"
    }
  }
}
```

The actor and project come from the authenticated socket, never the payload.
An owner/editor gets an `ack` containing `client_id`, `op_id`, `operation`,
`seq`, `current_seq`, and `duplicate`. A rejected operation gets `type: "error"`,
`status` (for example 403 for a viewer), `message`, and the submitted client/op IDs
when the envelope could be decoded. Errors leave the socket available for retry.

Both transports call the same `ops.Service.Submit`. It validates the request,
locks the project, checks current membership/role, deduplicates, applies the
mutation, increments the sequence, appends the log, and commits. Only a newly
committed operation invokes the shared publisher. The publisher sends the usual
`operation` event to project subscribers; the submitter additionally gets a
queued `ack`. Publication and acknowledgment can overlap with replay. Apply
operations by contiguous sequence and ignore overlap. Neither `current_seq` nor
an acknowledgment alone proves earlier operations were applied.

Reuse the exact client ID, operation ID, type, and payload after an uncertain
outcome, even when retrying through the other transport. The service returns the
original operation without allocating another sequence or broadcasting again.
Reusing the key with different content returns 409.

The frontend currently submits writes through REST and receives live events over
WebSocket. This backend change enables socket writers without changing that UI
behavior or its existing retry logic.

Run ordinary checks from `backend` with `go test ./...`. Run the PostgreSQL-backed
transport test against a migrated test database with:

```sh
OPS_TEST_DATABASE_URL='your PostgreSQL test connection string' \
  go test -race ./internal/websocket -run TestOperationTransports -v
```

That test creates and removes isolated fixtures and checks editor writes,
viewer rejection, cross-transport retries, validation failures, sequence
conflicts, committed publication, and unchanged state after rejections. It skips
when OPS_TEST_DATABASE_URL is unset.


## Replay-to-live handoff

Registration starts in replay mode under the hub lock. While live publications
are buffered by sequence, the handler reads a membership-checked project sequence
boundary and sends `replay_start`. SQL replay is capped at `seq <= boundary` and
pages through that fixed prefix. After every operation through the boundary is
queued, the hub atomically queues `replay_complete`, discards buffered overlap,
and releases contiguous operations above the boundary. Delayed/out-of-order
post-commit publications remain buffered until earlier sequences arrive. Queue
or buffer overflow closes the slow connection so it can reconnect and replay;
it never silently skips history. The same handoff applies to `resume` messages.
Presence and permission notifications may interleave; operation ordering is what
the boundary protects. Frontend state still advances only after reducer success.

## Prompt permission changes

Project/workspace AddMember handlers also update existing roles. After the
standalone membership SQL statement commits, their configured callbacks signal
the hub. Every socket for the affected user is woken through a coalesced priority
channel. Its single writer re-reads the current role instead of trusting an event
payload, which avoids stale roles from notifications delivered out of order.
Workspace callbacks notify sockets for projects in that workspace. The periodic
five-second check remains a fallback for external SQL changes or missed signals.

A role change produces a `permissions` message with `role` and `message`. Viewer
downgrades retain read access; both operation transports immediately consult the
authoritative role when processing each new write. Missing membership sends
`access_revoked: true` and closes with code 1008. A temporary permission-query
failure closes with retryable code 1013 rather than declaring revocation. The
frontend prioritizes permission messages over slow replay, refreshes the displayed
role, and stops reconnecting after full revocation. Future remove-member handlers
must call the same post-commit callbacks; the current API only supports upserts.

Notifications are prompt after commit, not a zero-latency distributed guarantee.
The hub is local to one API process; multiple replicas require shared publication.
Database-backed transport tests verify role downgrade/upgrade via the real
membership handler, full-removal signals, and periodic fallback. Hub tests force
replay/live overlap and out-of-order publication deterministically.
