package ops

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"net/http/httptest"
	"testing"
)

func TestReplayRejectsInvalidCursorAndLimitBeforeDatabaseAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewHandler(nil)
	for _, query := range []string{"after_seq=-1", "after_seq=1.5", "after_seq=oops", "after_seq=9223372036854775808", "limit=0", "limit=501", "limit=-1", "limit=wrong"} {
		t.Run(query, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) { c.Set("userID", uuid.New()) })
			router.GET("/api/projects/:projectID/ops", handler.Replay)
			res := httptest.NewRecorder()
			router.ServeHTTP(res, httptest.NewRequest("GET", "/api/projects/"+uuid.NewString()+"/ops?"+query, nil))
			if res.Code != 400 {
				t.Fatalf("status=%d, want 400", res.Code)
			}
		})
	}
}
