package ops

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"testing"
)

func TestSceneCreateRejectsMissingOrInvalidTitleBeforeDatabaseWrite(t *testing.T) {
	empty := ""
	blank := "   "
	overlong := strings.Repeat("x", 201)
	for _, title := range []*string{nil, &empty, &blank, &overlong} {
		err := applyOperation(context.Background(), nil, uuid.New(), uuid.New(), "scene.create", payload{SceneID: uuid.NewString(), Title: title})
		var operationErr *operationError
		if !errors.As(err, &operationErr) || operationErr.status != http.StatusBadRequest {
			t.Fatalf("expected a 400 operation error, got %v", err)
		}
	}
}
