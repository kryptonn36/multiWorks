package board

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestReducerCoversBoardOperationsWithoutMutatingPriorState(t *testing.T) {
	project, actor, first, second, shotA, shotB := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	state := State{ProjectID: project, Scenes: []Scene{}}
	apply := func(kind string, payload any) {
		t.Helper()
		raw, _ := json.Marshal(payload)
		next, err := Apply(state, Operation{ProjectID: project, Seq: state.LastSeq + 1, Type: kind, Payload: raw, ActorID: actor, CreatedAt: time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)})
		if err != nil {
			t.Fatal(err)
		}
		state = next
	}
	apply("scene.create", map[string]any{"scene_id": first, "title": " First "})
	original := Clone(state)
	apply("scene.create", map[string]any{"scene_id": second, "title": "Second"})
	apply("shot.create", map[string]any{"scene_id": first, "shot_id": shotA, "prompt": "Wide", "style": "Blue", "notes": "Slow"})
	apply("shot.create", map[string]any{"scene_id": first, "shot_id": shotB, "prompt": "Close-up"})
	apply("shot.update", map[string]any{"scene_id": first, "shot_id": shotA, "prompt": "Revised", "image_url": nil})
	apply("scene.update", map[string]any{"scene_id": first, "description": "Changed"})
	apply("shot.reorder", map[string]any{"scene_id": first, "shot_ids": []uuid.UUID{shotB, shotA}})
	apply("scene.reorder", map[string]any{"scene_ids": []uuid.UUID{second, first}})
	if len(original.Scenes) != 1 || original.Scenes[0].Title != "First" || len(original.Scenes[0].Shots) != 0 {
		t.Fatal("prior board mutated")
	}
	if state.Scenes[1].Shots[0].ID != shotB || state.Scenes[1].Shots[1].Style != "Blue" {
		t.Fatal("order or partial fields lost")
	}
	apply("shot.delete", map[string]any{"scene_id": first, "shot_id": shotB})
	apply("scene.delete", map[string]any{"scene_id": second})
	if len(state.Scenes) != 1 || len(state.Scenes[0].Shots) != 1 {
		t.Fatal("delete reducer mismatch")
	}
	previous := state.LastSeq
	apply("restore_board", map[string]any{"seq": 1, "board_state": original})
	if state.LastSeq != previous+1 || len(state.Scenes[0].Shots) != 0 {
		t.Fatal("restore reset the sequence instead of content")
	}
	raw, _ := json.Marshal(map[string]any{"scene_ids": []uuid.UUID{first, first}})
	if _, err := Apply(state, Operation{ProjectID: project, Seq: state.LastSeq + 1, Type: "scene.reorder", Payload: raw}); err == nil {
		t.Fatal("invalid order accepted")
	}
}
