package assets_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kryptonn36/multiWorks/internal/agent"
	"github.com/kryptonn36/multiWorks/internal/db"
	"github.com/kryptonn36/multiWorks/internal/ops"
)

func TestAsyncAgentGeneration(t *testing.T) {
	database := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := database.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	owner, viewer, workspace, project, other := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, user := range []uuid.UUID{owner, viewer} {
		exec(`INSERT INTO users(id,email,password_hash) VALUES($1,$2,'fixture')`, user, user.String()+"@async.invalid")
	}
	exec(`INSERT INTO workspaces(id,name,created_by) VALUES($1,'Async agent',$2)`, workspace, owner)
	exec(`INSERT INTO workspace_members(workspace_id,user_id,role) VALUES($1,$2,'owner'),($1,$3,'member')`, workspace, owner, viewer)
	for _, pid := range []uuid.UUID{project, other} {
		exec(`INSERT INTO projects(id,workspace_id,name,created_by) VALUES($1,$2,'Proposals',$3)`, pid, workspace, owner)
		exec(`INSERT INTO project_members(project_id,user_id,role) VALUES($1,$2,'owner'),($1,$3,'viewer')`, pid, owner, viewer)
	}
	operations := ops.NewService(database)
	service := agent.NewService(database, operations, agent.Limits{MaxOps: 20, Timeout: 3 * time.Second})
	writes := 0
	operations.SetPublisher(func(_ uuid.UUID, _ ops.Operation) { writes++ })
	requestCtx, cancelRequest := context.WithCancel(ctx)
	start := time.Now()
	proposal, err := service.Propose(requestCtx, project, owner, "Add a shot of a letter", "normal")
	cancelRequest()
	if err != nil || proposal.Status != "generating" || time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("generation did not enqueue promptly: %v %+v", err, proposal)
	}
	_, err = service.Decide(ctx, project, owner, proposal.ID, true)
	status(t, err, 409)
	// Claim then abandon a lease; another service instance recovers it from SQL.
	job, err := db.New(database).ClaimAgentJob(ctx, 30)
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE agent_jobs SET locked_at=NOW()-INTERVAL '31 seconds' WHERE proposal_id=$1`, job.ProposalID)
	restarted := agent.NewService(database, operations, agent.Limits{MaxOps: 20, Timeout: 3 * time.Second})
	if ok, err := restarted.ProcessOne(ctx); err != nil || !ok {
		t.Fatalf("job recovery: %v %v", ok, err)
	}
	proposal, err = service.Get(ctx, project, owner, proposal.ID)
	if err != nil || proposal.Status != "pending" || writes != 0 {
		t.Fatalf("proposal generation mutated board: %v %+v", err, proposal)
	}
	if _, err = service.Decide(ctx, project, owner, proposal.ID, true); err != nil || writes != 2 {
		t.Fatalf("dependent approval: %v writes=%d", err, writes)
	}
	// Captured context is used even when the live board changes while queued.
	stale, err := service.Propose(ctx, project, owner, "Add another shot", "normal")
	if err != nil {
		t.Fatal(err)
	}
	first := proposal.Operations[0]
	var creation struct {
		SceneID uuid.UUID `json:"scene_id"`
	}
	json.Unmarshal(first.Payload, &creation)
	raw, _ := json.Marshal(map[string]any{"scene_id": creation.SceneID, "title": "Changed while generating"})
	if _, err = operations.Submit(ctx, project, owner, ops.SubmitRequest{ClientID: "async", OpID: "edit", Type: "scene.update", Payload: raw}); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	stale, err = service.Get(ctx, project, owner, stale.ID)
	if err != nil || stale.Status != "pending" || stale.BaseSeq != 2 {
		t.Fatalf("capture changed: %v %+v", err, stale)
	}
	_, err = service.Decide(ctx, project, owner, stale.ID, true)
	status(t, err, 409)
	_, err = service.Propose(ctx, project, viewer, "Add a shot", "normal")
	status(t, err, 403)
	_, err = service.Get(ctx, other, owner, stale.ID)
	status(t, err, 404)
	// Failure tokens are asynchronous and never create operation-log entries.
	count := writes
	for _, token := range []string{"invalid-json", "missing-shot", "oversized", "timeout"} {
		p, err := service.Propose(ctx, project, owner, "Add a shot [mock:"+token+"]", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = restarted.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
		p, err = service.Get(ctx, project, owner, p.ID)
		if err != nil || p.Status != "failed" || p.FailureReason == "" {
			t.Fatalf("token %s: %v %+v", token, err, p)
		}
	}
	if writes != count {
		t.Fatal("failed generation wrote operations")
	}
	// The actual canonical handler acknowledges before background generation.
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("userID", owner) })
	router.POST("/api/projects/:projectID/agent/propose", agent.NewHandler(service).Generate)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest("POST", "/api/projects/"+project.String()+"/agent/propose", strings.NewReader(`{"instruction":"Add a scene"}`)))
	if res.Code != 202 {
		t.Fatalf("async handler status %d: %s", res.Code, res.Body.String())
	}
	var response struct {
		Status string `json:"status"`
		Ops    []any  `json:"proposed_ops"`
	}
	if err = json.Unmarshal(res.Body.Bytes(), &response); err != nil || response.Status != "generating" || response.Ops == nil {
		t.Fatalf("async response: %v %s", err, res.Body.String())
	}
	// Large context is rejected before its JSON is loaded into the application.
	scene := uuid.New()
	exec(`INSERT INTO scenes(id,project_id,title,created_by,position) VALUES($1,$2,'Large board',$3,0)`, scene, other, owner)
	exec(`INSERT INTO shots(id,scene_id,prompt,created_by,position) SELECT gen_random_uuid(),$1,repeat('x',10000),$2,n FROM generate_series(1,40)n`, scene, owner)
	_, err = service.Propose(ctx, other, owner, "Add a shot", "normal")
	status(t, err, 400)
}
