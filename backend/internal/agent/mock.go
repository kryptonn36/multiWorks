package agent

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/kryptonn36/multiWorks/internal/board"
	"github.com/kryptonn36/multiWorks/internal/ops"
	"regexp"
	"strings"
	"time"
)

func mockMode(instruction, mode string) string {
	for _, token := range []struct{ marker, mode string }{
		{"[mock:invalid-json]", "malformed"}, {"[mock:missing-shot]", "missing-shot"}, {"[mock:oversized]", "too-many"}, {"[mock:timeout]", "timeout"},
	} {
		if strings.Contains(instruction, token.marker) {
			return token.mode
		}
	}
	if mode == "" {
		return "normal"
	}
	return mode
}

var draftAction = regexp.MustCompile(`(?i)\b(add|create|draft)\b`)
var sceneAction = regexp.MustCompile(`(?i)\b(add|create|draft)\s+(?:(?:a|an|new)\s+)*scene\b`)
var unsupportedAction = regexp.MustCompile(`(?i)\b(delete|remove|update|fetch|download|execute)\b`)

// Mock is local and deterministic in behavior; UUIDs are generated per proposal.
// Instructions are data, never commands or executable code.
func Mock(ctx context.Context, state board.State, instruction, mode string, maxOps ...int) ([]byte, error) {
	mode = mockMode(instruction, mode)
	if mode == "timeout" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if mode == "normal" || mode == "delayed" {
		delay := 2 * time.Second
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	switch mode {
	case "malformed":
		return []byte(`{"operations": [`), nil
	case "oversized":
		return []byte(strings.Repeat("x", ops.MaxProposalBytes+1)), nil
	case "invalid":
		return []byte(`{"operations":[{"type":"project.delete","payload":{}}]}`), nil
	}
	if mode == "missing-shot" {
		sceneID := uuid.New()
		if len(state.Scenes) > 0 {
			sceneID = state.Scenes[0].ID
		}
		body, _ := json.Marshal(map[string]any{"scene_id": sceneID, "shot_id": "00000000-0000-0000-0000-000000000001", "prompt": "Missing shot"})
		return json.Marshal(map[string]any{"operations": []ops.ProposedOperation{{Type: "shot.update", Payload: body}}})
	}
	if mode == "too-many" {
		items := []ops.ProposedOperation{}
		limit := ops.MaxProposalOperations
		if len(maxOps) > 0 {
			limit = maxOps[0]
		}
		for range limit + 1 {
			body, _ := json.Marshal(map[string]any{"scene_id": uuid.New(), "title": "Oversized proposal"})
			items = append(items, ops.ProposedOperation{Type: "scene.create", Payload: body})
		}
		return json.Marshal(map[string]any{"operations": items})
	}
	proposed := []ops.ProposedOperation{}
	if !draftAction.MatchString(instruction) || unsupportedAction.MatchString(instruction) {
		return json.Marshal(map[string]any{"operations": proposed})
	}

	sceneID := uuid.New()
	if len(state.Scenes) == 0 || sceneAction.MatchString(instruction) {
		body, _ := json.Marshal(map[string]any{"scene_id": sceneID, "title": "Agent draft", "description": instruction})
		proposed = append(proposed, ops.ProposedOperation{Type: "scene.create", Payload: body})
	} else {
		sceneID = state.Scenes[0].ID
	}
	if sceneAction.MatchString(instruction) && !strings.Contains(strings.ToLower(instruction), "shot") {
		return json.Marshal(map[string]any{"operations": proposed})
	}
	body, _ := json.Marshal(map[string]any{"scene_id": sceneID, "shot_id": uuid.New(), "prompt": instruction, "style": "", "notes": "Local mock agent draft; review before approval."})
	proposed = append(proposed, ops.ProposedOperation{Type: "shot.create", Payload: body})
	return json.Marshal(struct {
		Operations []ops.ProposedOperation `json:"operations"`
	}{proposed})
}
