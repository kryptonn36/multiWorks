// Package board contains the transport-independent board model and log reducer.
package board

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/kryptonn36/multiWorks/internal/db"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

type State struct {
	ProjectID uuid.UUID `json:"project_id"`
	LastSeq   int64     `json:"last_seq"`
	Scenes    []Scene   `json:"scenes"`
}
type Scene struct {
	ID          uuid.UUID `json:"id"`
	ProjectID   uuid.UUID `json:"project_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Position    int       `json:"position"`
	CreatedBy   uuid.UUID `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Shots       []Shot    `json:"shots"`
}
type Shot struct {
	AssetIDs  []uuid.UUID `json:"asset_ids"`
	ID        uuid.UUID   `json:"id"`
	SceneID   uuid.UUID   `json:"scene_id"`
	Prompt    string      `json:"prompt"`
	Style     string      `json:"style"`
	Notes     string      `json:"notes"`
	ImageURL  *string     `json:"image_url"`
	Position  int         `json:"position"`
	CreatedBy uuid.UUID   `json:"created_by"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}
type Operation struct {
	ProjectID uuid.UUID       `json:"project_id"`
	Seq       int64           `json:"seq"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	ActorID   uuid.UUID       `json:"actor_id"`
	CreatedAt time.Time       `json:"created_at"`
}

func Load(ctx context.Context, tx *sql.Tx, projectID uuid.UUID) (State, error) {
	var state State
	raw, err := db.New(tx).LoadBoardState(ctx, projectID)
	if err != nil {
		return state, err
	}
	err = json.Unmarshal(raw, &state)
	return Canonical(state), err
}
func Clone(state State) State {
	next := state
	next.Scenes = append([]Scene{}, state.Scenes...)
	for i := range next.Scenes {
		next.Scenes[i].Shots = append([]Shot{}, state.Scenes[i].Shots...)
		for j := range next.Scenes[i].Shots {
			next.Scenes[i].Shots[j].AssetIDs = append([]uuid.UUID{}, state.Scenes[i].Shots[j].AssetIDs...)
		}
	}
	return next
}
func Canonical(state State) State {
	state = Clone(state)
	for i := range state.Scenes {
		scene := &state.Scenes[i]
		scene.CreatedAt = scene.CreatedAt.UTC()
		scene.UpdatedAt = scene.UpdatedAt.UTC()
		for j := range scene.Shots {
			shot := &scene.Shots[j]
			sort.Slice(shot.AssetIDs, func(a, b int) bool { return shot.AssetIDs[a].String() < shot.AssetIDs[b].String() })
			shot.CreatedAt = shot.CreatedAt.UTC()
			shot.UpdatedAt = shot.UpdatedAt.UTC()
		}
	}
	return state
}

func Validate(state State, projectID uuid.UUID) error {
	if state.ProjectID != projectID || state.LastSeq < 0 || state.Scenes == nil {
		return errors.New("invalid board state")
	}
	seen := map[uuid.UUID]bool{}
	shotsSeen := map[uuid.UUID]bool{}
	for _, scene := range state.Scenes {
		if scene.ID == uuid.Nil || scene.ProjectID != projectID || seen[scene.ID] || scene.Position < 0 || scene.CreatedBy == uuid.Nil || scene.CreatedAt.IsZero() || scene.UpdatedAt.IsZero() || strings.TrimSpace(scene.Title) == "" || len(scene.Title) > 200 || len(scene.Description) > 5000 || scene.Shots == nil {
			return errors.New("invalid restored scene")
		}
		seen[scene.ID] = true
		for _, shot := range scene.Shots {
			if shot.ID == uuid.Nil || shot.SceneID != scene.ID || shotsSeen[shot.ID] || shot.Position < 0 || shot.CreatedBy == uuid.Nil || shot.CreatedAt.IsZero() || shot.UpdatedAt.IsZero() || len(shot.Prompt) > 10000 || len(shot.Style) > 500 || len(shot.Notes) > 5000 {
				return errors.New("invalid restored shot")
			}
			shotsSeen[shot.ID] = true
			assets := map[uuid.UUID]bool{}
			for _, id := range shot.AssetIDs {
				if id == uuid.Nil || assets[id] {
					return errors.New("invalid asset reference")
				}
				assets[id] = true
			}
		}
	}
	return nil
}
func Replace(ctx context.Context, tx *sql.Tx, state State) error {
	if err := Validate(state, state.ProjectID); err != nil {
		return err
	}
	if _, err := db.New(tx).DeleteBoardScenes(ctx, state.ProjectID); err != nil {
		return err
	}
	for _, sc := range state.Scenes {
		if _, err := db.New(tx).RestoreScene(ctx, db.RestoreSceneParams{ID: sc.ID, ProjectID: state.ProjectID, Title: sc.Title, Description: sc.Description, Position: int64(sc.Position), CreatedBy: sc.CreatedBy, CreatedAt: sc.CreatedAt, UpdatedAt: sc.UpdatedAt}); err != nil {
			return err
		}
		for _, sh := range sc.Shots {
			if _, err := db.New(tx).RestoreShot(ctx, db.RestoreShotParams{ID: sh.ID, SceneID: sc.ID, Prompt: sh.Prompt, Style: sh.Style, Notes: sh.Notes, ImageUrl: db.NullableString(sh.ImageURL), Position: int64(sh.Position), CreatedBy: sh.CreatedBy, CreatedAt: sh.CreatedAt, UpdatedAt: sh.UpdatedAt}); err != nil {
				return err
			}
			for _, assetID := range sh.AssetIDs {
				queries := db.New(tx)
				if _, err := queries.GetProjectAsset(ctx, db.GetProjectAssetParams{ProjectID: state.ProjectID, AssetID: assetID}); err != nil {
					return err
				}
				if err := queries.AttachShotAsset(ctx, db.AttachShotAssetParams{ProjectID: state.ProjectID, ShotID: sh.ID, AssetID: assetID, AttachedBy: uuid.NullUUID{UUID: sh.CreatedBy, Valid: true}}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func SaveSnapshot(ctx context.Context, tx *sql.Tx, projectID, userID uuid.UUID, seq int64) error {
	state, err := Load(ctx, tx, projectID)
	if err != nil {
		return err
	}
	if state.LastSeq != seq {
		return errors.New("snapshot sequence mismatch")
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = db.New(tx).CreateSnapshot(ctx, db.CreateSnapshotParams{ProjectID: projectID, Seq: seq, Payload: raw, UserID: uuid.NullUUID{UUID: userID, Valid: true}})
	return err
}
func EnsureBaseline(ctx context.Context, tx *sql.Tx, projectID, userID uuid.UUID, seq int64) error {
	var exists bool
	hasSnapshotResult, hasSnapshotErr := db.New(tx).HasSnapshot(ctx, projectID)
	exists = hasSnapshotResult
	if err := hasSnapshotErr; err != nil {
		return err
	}
	if exists {
		return nil
	}
	return SaveSnapshot(ctx, tx, projectID, userID, seq)
}

func Apply(state State, op Operation) (State, error) {
	if op.ProjectID != state.ProjectID || op.Seq != state.LastSeq+1 {
		return state, errors.New("operation history has a sequence gap")
	}
	next := Clone(state)
	op.CreatedAt = op.CreatedAt.UTC()
	var p struct {
		SceneID     uuid.UUID   `json:"scene_id"`
		AssetID     uuid.UUID   `json:"asset_id"`
		ShotID      uuid.UUID   `json:"shot_id"`
		Title       *string     `json:"title"`
		Description *string     `json:"description"`
		Prompt      *string     `json:"prompt"`
		Style       *string     `json:"style"`
		Notes       *string     `json:"notes"`
		ImageURL    *string     `json:"image_url"`
		SceneIDs    []uuid.UUID `json:"scene_ids"`
		ShotIDs     []uuid.UUID `json:"shot_ids"`
		BoardState  *State      `json:"board_state"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return state, err
	}
	value := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	sceneIndex := -1
	for i := range next.Scenes {
		if next.Scenes[i].ID == p.SceneID {
			sceneIndex = i
			break
		}
	}
	switch op.Type {
	case "restore_board":
		if p.BoardState == nil {
			return state, errors.New("restore operation has no board state")
		}
		if err := Validate(*p.BoardState, state.ProjectID); err != nil {
			return state, err
		}
		next = Clone(*p.BoardState)
	case "scene.create":
		if p.Title == nil || sceneIndex >= 0 || p.SceneID == uuid.Nil {
			return state, errors.New("invalid scene creation in history")
		}
		position := 0
		for _, s := range next.Scenes {
			if s.Position >= position {
				position = s.Position + 1
			}
		}
		next.Scenes = append(next.Scenes, Scene{ID: p.SceneID, ProjectID: state.ProjectID, Title: strings.TrimSpace(*p.Title), Description: value(p.Description), Position: position, CreatedBy: op.ActorID, CreatedAt: op.CreatedAt, UpdatedAt: op.CreatedAt, Shots: []Shot{}})
	case "scene.reorder":
		if len(p.SceneIDs) != len(next.Scenes) {
			return state, errors.New("incomplete scene order in history")
		}
		seen := map[uuid.UUID]bool{}
		for pos, id := range p.SceneIDs {
			found := false
			for i := range next.Scenes {
				if next.Scenes[i].ID == id && !seen[id] {
					next.Scenes[i].Position = pos
					next.Scenes[i].UpdatedAt = op.CreatedAt
					seen[id] = true
					found = true
					break
				}
			}
			if !found {
				return state, errors.New("invalid scene order in history")
			}
		}
	default:
		if sceneIndex < 0 {
			return state, errors.New("history references a scene missing from its baseline")
		}
		sc := &next.Scenes[sceneIndex]
		switch op.Type {
		case "scene.update":
			if p.Title != nil {
				sc.Title = *p.Title
			}
			if p.Description != nil {
				sc.Description = *p.Description
			}
			sc.UpdatedAt = op.CreatedAt
		case "scene.delete":
			next.Scenes = append(next.Scenes[:sceneIndex], next.Scenes[sceneIndex+1:]...)
		case "shot.create":
			position := 0
			for _, sh := range sc.Shots {
				if sh.ID == p.ShotID {
					return state, errors.New("duplicate shot in history")
				}
				if sh.Position >= position {
					position = sh.Position + 1
				}
			}
			sc.Shots = append(sc.Shots, Shot{AssetIDs: []uuid.UUID{}, ID: p.ShotID, SceneID: sc.ID, Prompt: value(p.Prompt), Style: value(p.Style), Notes: value(p.Notes), ImageURL: p.ImageURL, Position: position, CreatedBy: op.ActorID, CreatedAt: op.CreatedAt, UpdatedAt: op.CreatedAt})
		case "shot.reorder":
			if len(p.ShotIDs) != len(sc.Shots) {
				return state, errors.New("incomplete shot order in history")
			}
			seen := map[uuid.UUID]bool{}
			for pos, id := range p.ShotIDs {
				found := false
				for i := range sc.Shots {
					if sc.Shots[i].ID == id && !seen[id] {
						sc.Shots[i].Position = pos
						sc.Shots[i].UpdatedAt = op.CreatedAt
						seen[id] = true
						found = true
						break
					}
				}
				if !found {
					return state, errors.New("invalid shot order in history")
				}
			}
		case "asset.attach":
			found := false
			for i := range sc.Shots {
				if sc.Shots[i].ID == p.ShotID {
					found = true
					exists := false
					for _, id := range sc.Shots[i].AssetIDs {
						if id == p.AssetID {
							exists = true
						}
					}
					if !exists {
						sc.Shots[i].AssetIDs = append(sc.Shots[i].AssetIDs, p.AssetID)
					}
				}
			}
			if !found || p.AssetID == uuid.Nil {
				return state, errors.New("invalid asset attachment in history")
			}
		case "shot.update", "shot.delete":
			index := -1
			for i := range sc.Shots {
				if sc.Shots[i].ID == p.ShotID {
					index = i
					break
				}
			}
			if index < 0 {
				return state, errors.New("history references a missing shot")
			}
			if op.Type == "shot.delete" {
				sc.Shots = append(sc.Shots[:index], sc.Shots[index+1:]...)
			} else {
				sh := &sc.Shots[index]
				if p.Prompt != nil {
					sh.Prompt = *p.Prompt
				}
				if p.Style != nil {
					sh.Style = *p.Style
				}
				if p.Notes != nil {
					sh.Notes = *p.Notes
				}
				if p.ImageURL != nil {
					sh.ImageURL = p.ImageURL
				}
				sh.UpdatedAt = op.CreatedAt
			}
		default:
			return state, errors.New("unsupported historical operation: " + op.Type)
		}
	}
	next.LastSeq = op.Seq
	sort.Slice(next.Scenes, func(i, j int) bool {
		a, b := next.Scenes[i], next.Scenes[j]
		if a.Position != b.Position {
			return a.Position < b.Position
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ID.String() < b.ID.String()
	})
	for i := range next.Scenes {
		shots := next.Scenes[i].Shots
		sort.Slice(shots, func(i, j int) bool {
			a, b := shots[i], shots[j]
			if a.Position != b.Position {
				return a.Position < b.Position
			}
			if !a.CreatedAt.Equal(b.CreatedAt) {
				return a.CreatedAt.Before(b.CreatedAt)
			}
			return a.ID.String() < b.ID.String()
		})
	}
	return Canonical(next), nil
}
