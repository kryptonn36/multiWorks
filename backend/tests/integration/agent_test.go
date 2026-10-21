package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/kryptonn36/multiWorks/internal/agent"
	"github.com/kryptonn36/multiWorks/internal/board"
	"github.com/kryptonn36/multiWorks/internal/db"
	"github.com/kryptonn36/multiWorks/internal/ops"
)

func (f *fixture) proposal(t *testing.T, instruction, mode string) agent.Proposal {
	t.Helper()
	p := decode[agent.Proposal](t, f.json(t, f.editor, "POST", f.path("/agent/propose"), map[string]any{"instruction": instruction, "mode": mode}, 202))
	if p.Status != "generating" {
		t.Fatal("proposal request did not return asynchronously")
	}
	if ok, err := f.agent.ProcessOne(context.Background()); !ok || err != nil {
		t.Fatalf("agent job: %v %v", ok, err)
	}
	return decode[agent.Proposal](t, f.json(t, f.editor, "GET", f.path("/agent/proposals/"+p.ID.String()), nil, 200))
}
func TestAgentHTTPApprovalFailuresStaleAndReject(t *testing.T) {
	f := newFixture(t)
	p := f.proposal(t, "Add a close-up shot of a mysterious letter", "normal")
	if p.Status != "pending" || len(p.Operations) != 2 || f.published(f.project) != 0 {
		t.Fatal("generation changed the live board")
	}
	empty := decode[board.State](t, f.json(t, f.owner, "GET", f.path("/board"), nil, 200))
	if empty.LastSeq != 0 || len(empty.Scenes) != 0 {
		t.Fatal("draft operations escaped before approval")
	}
	path := f.path("/agent/proposals/" + p.ID.String())
	f.json(t, f.outsider, "GET", path, nil, 404)
	approved := decode[agent.Proposal](t, f.json(t, f.editor, "POST", path+"/approve", map[string]any{}, 200))
	if approved.Status != "approved" || len(approved.AppliedOperations) != 2 || f.published(f.project) != 2 {
		t.Fatal("approval did not commit the whole batch")
	}
	f.json(t, f.editor, "POST", path+"/approve", map[string]any{}, 200)
	if f.published(f.project) != 2 {
		t.Fatal("approval retry duplicated operations")
	}
	for _, mode := range []string{"malformed", "missing-shot", "invalid", "too-many", "oversized", "timeout"} {
		p := f.proposal(t, "Add a shot", mode)
		if p.Status != "failed" || p.FailureReason == "" || f.published(f.project) != 2 {
			t.Fatalf("unsafe mock %s changed board: %+v", mode, p)
		}
	}
	rejected := f.proposal(t, "Add a rejected shot", "normal")
	result := decode[agent.Proposal](t, f.json(t, f.editor, "POST", f.path("/agent/proposals/"+rejected.ID.String()+"/reject"), map[string]any{}, 200))
	if result.Status != "rejected" || f.published(f.project) != 2 {
		t.Fatal("rejection changed board")
	}
	stale := f.proposal(t, "Add a stale shot", "normal")
	current := decode[board.State](t, f.json(t, f.owner, "GET", f.path("/board"), nil, 200))
	f.submit(t, f.owner, operation("scene.update", map[string]any{"scene_id": current.Scenes[0].ID, "title": "Concurrent human edit"}))
	stalePath := f.path("/agent/proposals/" + stale.ID.String())
	f.json(t, f.editor, "POST", stalePath+"/approve", map[string]any{}, 409)
	stale = decode[agent.Proposal](t, f.json(t, f.editor, "GET", stalePath, nil, 200))
	if stale.Status != "stale" || f.published(f.project) != 3 {
		t.Fatal("stale approval overwrote a human edit")
	}
}
func TestAgentThirdInvalidOperationCommitsNothing(t *testing.T) {
	f := newFixture(t)
	scene, shot := uuid.New(), uuid.New()
	payload := func(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
	proposed := []ops.ProposedOperation{
		{Type: "scene.create", Payload: payload(map[string]any{"scene_id": scene, "title": "Must not persist"})},
		{Type: "shot.create", Payload: payload(map[string]any{"scene_id": scene, "shot_id": shot, "prompt": "Must not persist"})},
		{Type: "shot.update", Payload: payload(map[string]any{"scene_id": scene, "shot_id": uuid.New(), "prompt": "Missing shot"})},
	}
	raw, _ := json.Marshal(proposed)
	// The generation validator would reject this output. Persist a deliberately
	// invalid pending fixture to prove that approval independently revalidates.
	row, err := db.New(f.database).CreateAgentProposal(context.Background(), db.CreateAgentProposalParams{ProjectID: f.project, RequestedBy: uuid.NullUUID{UUID: f.owner.ID, Valid: true}, Instruction: "Adversarial pending proposal", GenerationMode: "normal", Status: "pending", ProposedOps: raw, FailureReason: sql.NullString{}})
	if err != nil {
		t.Fatal(err)
	}
	path := f.path("/agent/proposals/" + row.ID.String())
	f.json(t, f.owner, "POST", path+"/approve", map[string]any{}, 409)
	current := decode[board.State](t, f.json(t, f.owner, "GET", f.path("/board"), nil, 200))
	failed := decode[agent.Proposal](t, f.json(t, f.owner, "GET", path, nil, 200))
	if current.LastSeq != 0 || len(current.Scenes) != 0 || f.published(f.project) != 0 || failed.Status != "failed" || failed.FailureReason == "" {
		t.Fatal("invalid third operation caused a partial commit or remained pending")
	}
	var count int
	if err = f.database.QueryRow(`SELECT COUNT(*) FROM project_ops WHERE project_id=$1`, f.project).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected approval appended %d operations: %v", count, err)
	}
}
