package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRenamer struct{ titles []string }

func (f *fakeRenamer) SetTitle(_ context.Context, _ string, title string) error {
	f.titles = append(f.titles, title)
	return nil
}

// 标题不能改成空白：空标题在列表里只能显示一串 ID，原标题也就丢了。
func TestRenameConversation_BlankTitleRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeRenamer{}
	r := gin.New()
	r.PATCH("/conversations/:id", renameConversationHandler(fake))

	for _, body := range []string{`{"title":""}`, `{"title":"   "}`} {
		w := patchJSON(r, "/conversations/c1", body)
		assert.Equal(t, 400, w.Code, body)
	}
	assert.Empty(t, fake.titles, "空白标题不应写入")

	w := patchJSON(r, "/conversations/c1", `{"title":" 新标题 "}`)
	require.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"新标题"}, fake.titles)
}

func patchJSON(h http.Handler, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(w, req)
	return w
}
