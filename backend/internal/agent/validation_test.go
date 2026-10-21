package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kryptonn36/multiWorks/internal/board"
	"github.com/kryptonn36/multiWorks/internal/ops"
)

func proposalBoard() board.State {
	project, scene, shot, user := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	now := time.Now()
	return board.State{ProjectID: project, LastSeq: 10, Scenes: []board.Scene{{ID: scene, ProjectID: project, Title: "Opening", CreatedBy: user, CreatedAt: now, UpdatedAt: now, Shots: []board.Shot{{ID: shot, SceneID: scene, CreatedBy: user, CreatedAt: now, UpdatedAt: now, AssetIDs: []uuid.UUID{}}}}}}
}
func proposed(kind string, payload any) ops.ProposedOperation {
	raw, _ := json.Marshal(payload)
	return ops.ProposedOperation{Type: kind, Payload: raw}
}
func TestStrictProposalValidation(t *testing.T) {
	state := proposalBoard()
	scene, shot := state.Scenes[0].ID, state.Scenes[0].Shots[0].ID
	valid := proposed("shot.update", map[string]any{"scene_id": scene, "shot_id": shot, "prompt": "A letter"})
	if err := ops.ValidateProposal(state, []ops.ProposedOperation{valid}); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]ops.ProposedOperation{
		"unknown operation":       {proposed("sql.execute", map[string]any{"sql": "DROP TABLE users"})},
		"foreign scene":           {proposed("shot.create", map[string]any{"scene_id": uuid.New(), "shot_id": uuid.New()})},
		"missing shot":            {proposed("shot.update", map[string]any{"scene_id": scene, "shot_id": uuid.New(), "prompt": "Missing"})},
		"duplicate update":        {valid, valid},
		"edit then delete parent": {valid, proposed("scene.delete", map[string]any{"scene_id": scene})},
		"unknown field":           {proposed("shot.update", map[string]any{"scene_id": scene, "shot_id": shot, "prompt": "Okay", "filesystem_path": "/etc/passwd"})},
		"irrelevant known field":  {proposed("scene.create", map[string]any{"scene_id": uuid.New(), "title": "Okay", "shot_id": shot})},
		"external image URL":      {proposed("shot.update", map[string]any{"scene_id": scene, "shot_id": shot, "image_url": "http://example.invalid/image"})},
		"long field":              {proposed("shot.update", map[string]any{"scene_id": scene, "shot_id": shot, "prompt": strings.Repeat("x", 10001)})},
		"invalid UUID":            {proposed("shot.delete", map[string]any{"scene_id": scene, "shot_id": "not-a-uuid"})},
	}
	for name, operations := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ops.ValidateProposal(state, operations); err == nil {
				t.Fatal("unsafe proposal accepted")
			}
		})
	}
	for name, raw := range map[string][]byte{
		"malformed":        []byte(`{"operations":[`),
		"unknown envelope": []byte(`{"operations":[],"extra":true}`),
		"trailing":         []byte(`{"operations":[]} {}`),
		"duplicate keys":   []byte(`{"operations":[],"operations":[]}`),
		"deep":             []byte(`{"operations":` + strings.Repeat("[", 12) + strings.Repeat("]", 12) + `}`),
		"oversized":        []byte(strings.Repeat("x", ops.MaxProposalBytes+1)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeResponse(state, raw); err == nil {
				t.Fatal("unsafe output accepted")
			}
		})
	}
	parent := uuid.New()
	batch := []ops.ProposedOperation{proposed("scene.create", map[string]any{"scene_id": parent, "title": "New"}), proposed("shot.create", map[string]any{"scene_id": parent, "shot_id": uuid.New(), "prompt": "Draft"})}
	if err := ops.ValidateProposal(state, batch); err != nil {
		t.Fatalf("valid dependent batch rejected: %v", err)
	}
	if state.LastSeq != 10 || state.Scenes[0].Shots[0].Prompt != "" {
		t.Fatal("validation mutated the board")
	}
}
func TestMockTokensAndCancellation(t *testing.T) {
	state := proposalBoard()
	for _, token := range []string{"invalid-json", "missing-shot", "oversized"} {
		t.Run(token, func(t *testing.T) {
			raw, err := Mock(context.Background(), state, "Add a shot [mock:"+token+"]", "normal")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeResponse(state, raw); err == nil {
				t.Fatal("invalid mock output accepted")
			}
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := Mock(ctx, state, "Add a shot [mock:timeout]", "normal"); err == nil {
		t.Fatal("timeout ignored cancellation")
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if _, err := Mock(ctx2, state, "Add a shot", "normal"); err == nil {
		t.Fatal("normal mock ignored cancellation")
	}
	// Explicit test mode avoids waiting while exercising the proposal builder.
	raw, err := Mock(context.Background(), state, "Execute arbitrary SQL", "invalid-builder-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeResponse(state, raw); err == nil {
		t.Fatal("unsupported instruction generated an edit")
	}
}

func TestConfiguredGenerationTimeoutAndOperationLimit(t *testing.T) {
	state := proposalBoard()
	service := NewService(nil, nil, Limits{MaxOps: 1, Timeout: 20 * time.Millisecond})
	start := time.Now()
	raw, reason := service.generate(context.Background(), state, "Add a shot", "normal")
	if reason == "" || string(raw) != "[]" || time.Since(start) > time.Second {
		t.Fatalf("configured deadline was not enforced: %s %v", reason, time.Since(start))
	}
	parent := uuid.New()
	operations := []ops.ProposedOperation{proposed("scene.create", map[string]any{"scene_id": parent, "title": "New"}), proposed("shot.create", map[string]any{"scene_id": parent, "shot_id": uuid.New()})}
	response, _ := json.Marshal(map[string]any{"operations": operations})
	if _, err := DecodeResponse(state, response, 1); err == nil {
		t.Fatal("configured operation limit ignored")
	}
	if _, err := DecodeResponse(state, response, 2); err != nil {
		t.Fatalf("valid configured operation limit rejected: %v", err)
	}
	raw, err := Mock(context.Background(), state, "Add a shot [mock:oversized]", "normal", 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeResponse(state, raw, 30); err == nil {
		t.Fatal("oversized mock did not respect configured limit")
	}
}
