package ops

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kryptonn36/multiWorks/internal/board"
)

const MaxProposalBytes = 64 * 1024
const MaxProposalOperations = 20

type ProposedOperation struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// ValidateProposalJSON bounds untrusted provider output before typed decoding.
func ValidateProposalJSON(raw []byte) error {
	if len(raw) > MaxProposalBytes {
		return fmt.Errorf("agent response exceeds the 64 KiB limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var value func(int) error
	value = func(depth int) error {
		if depth > 8 {
			return fmt.Errorf("agent JSON exceeds the nesting depth limit")
		}
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("agent returned malformed JSON")
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return fmt.Errorf("agent returned malformed JSON")
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return fmt.Errorf("agent JSON contains a duplicate field")
				}
				seen[name] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("agent returned malformed JSON")
		}
		if _, err := decoder.Token(); err != nil {
			return fmt.Errorf("agent returned malformed JSON")
		}
		return nil
	}
	if err := value(1); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("agent returned trailing JSON")
	}

	return nil
}

var proposalFields = map[string]string{
	"scene.create": "scene_id title description", "scene.update": "scene_id title description", "scene.delete": "scene_id", "scene.reorder": "scene_ids",
	"shot.create": "scene_id shot_id prompt style notes", "shot.update": "scene_id shot_id prompt style notes", "shot.delete": "scene_id shot_id", "shot.reorder": "scene_id shot_ids",
}

// ValidateProposal runs a bounded, side-effect-free rehearsal. Approval still uses
// the shared SQL operation engine, inside one transaction, for final validation.
func ValidateProposal(state board.State, operations []ProposedOperation) error {
	raw, err := json.Marshal(operations)
	if err != nil || len(raw) > MaxProposalBytes || len(operations) == 0 || len(operations) > MaxProposalOperations {
		return fmt.Errorf("proposal must contain 1-20 operations within 64 KiB")
	}
	actor := uuid.New()
	touched := map[string]bool{}
	childEdits := map[string]bool{}
	for i, operation := range operations {
		if err := ValidateProposalJSON(operation.Payload); err != nil {
			return err
		}
		allowed, ok := proposalFields[operation.Type]
		if !ok {
			return fmt.Errorf("operation %d has an unknown operation type", i+1)
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(operation.Payload, &fields) != nil || fields == nil {
			return fmt.Errorf("operation %d has an invalid payload", i+1)
		}
		for field := range fields {
			if !strings.Contains(" "+allowed+" ", " "+field+" ") {
				return fmt.Errorf("operation %d contains unsupported field %s", i+1, field)
			}
		}
		var p payload
		decoder := json.NewDecoder(bytes.NewReader(operation.Payload))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&p) != nil {
			return fmt.Errorf("operation %d has an invalid payload", i+1)
		}
		valid := true
		scene := func() bool { _, ok := parseID(p.SceneID); return ok }
		shot := func() bool { _, ok := parseID(p.ShotID); return ok }
		switch operation.Type {
		case "scene.create":
			valid = scene() && p.Title != nil && validName(*p.Title, 200) && validOptional(p.Description, 5000)
		case "scene.update":
			valid = scene() && (p.Title != nil || p.Description != nil) && (p.Title == nil || validName(*p.Title, 200)) && validOptional(p.Description, 5000)
		case "scene.delete":
			valid = scene()
		case "scene.reorder":
			_, err = parseIDs(p.SceneIDs, 500)
			valid = err == nil
		case "shot.create", "shot.update":
			valid = scene() && shot() && validOptional(p.Prompt, 10000) && validOptional(p.Style, 500) && validOptional(p.Notes, 5000)
			if operation.Type == "shot.update" {
				valid = valid && (p.Prompt != nil || p.Style != nil || p.Notes != nil || p.ImageURL != nil)
			}
		case "shot.delete":
			valid = scene() && shot()
		case "shot.reorder":
			_, err = parseIDs(p.ShotIDs, 1000)
			valid = scene() && err == nil
		default:
			valid = false
		}
		if !valid {
			return fmt.Errorf("operation %d is invalid or unsupported", i+1)
		}
		if id, ok := parseID(p.SceneID); ok {
			p.SceneID = id.String()
		}
		if id, ok := parseID(p.ShotID); ok {
			p.ShotID = id.String()
		}
		key := ""
		switch operation.Type {
		case "scene.create", "scene.update", "scene.delete":
			key = "scene:" + p.SceneID
		case "shot.create", "shot.update", "shot.delete":
			key = "shot:" + p.ShotID
			childEdits[p.SceneID] = true
		case "scene.reorder":
			key = "scene-order"
		case "shot.reorder":
			key = "shot-order:" + p.SceneID
			childEdits[p.SceneID] = true
		}
		if touched[key] || operation.Type == "scene.delete" && childEdits[p.SceneID] {
			return fmt.Errorf("operation %d duplicates or conflicts with another proposed edit", i+1)
		}
		touched[key] = true
		state, err = board.Apply(state, board.Operation{ProjectID: state.ProjectID, Seq: state.LastSeq + 1, Type: operation.Type, Payload: operation.Payload, ActorID: actor, CreatedAt: time.Now()})
		if err != nil {
			return fmt.Errorf("operation %d: %w", i+1, err)
		}
		if err = board.Validate(state, state.ProjectID); err != nil {
			return fmt.Errorf("operation %d: %w", i+1, err)
		}
	}
	return nil
}
