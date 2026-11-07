package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/attachment"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/workspace"
)

// fakeAttachmentContext 是 会话→任务→批次 解析链的测试替身。
type fakeAttachmentContext struct {
	taskID       string
	assignmentID string
}

func (f *fakeAttachmentContext) ResolveTaskID(_ context.Context, _ string) (string, error) {
	return f.taskID, nil
}

func (f *fakeAttachmentContext) GetSummary(_ context.Context, _ string) (task.Summary, error) {
	return task.Summary{AssignmentID: f.assignmentID}, nil
}

func newAttachmentTestServer(t *testing.T, sizeCap int64, ctx *fakeAttachmentContext) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ws, err := workspace.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := attachment.NewStore(ws)
	r := gin.New()
	h := uploadAttachmentHandler(store, ctx, sizeCap,
		func(c *gin.Context) string { return c.PostForm("conversation_id") })
	r.POST("/attachments", h)
	return r, ws.Root()
}

// multipartBody 拼一段最小 multipart 表单（fields 依次为键值对，file 为文件字段）。
func multipartBody(filename, content string, fields map[string]string) string {
	var b strings.Builder
	for k, v := range fields {
		b.WriteString("--BOUND\r\nContent-Disposition: form-data; name=\"" + k + "\"\r\n\r\n" + v + "\r\n")
	}
	b.WriteString("--BOUND\r\nContent-Disposition: form-data; name=\"file\"; filename=\"" + filename + "\"\r\n")
	b.WriteString("Content-Type: application/octet-stream\r\n\r\n" + content + "\r\n--BOUND--\r\n")
	return b.String()
}

func postAttachment(r *gin.Engine, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/attachments", strings.NewReader(body))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=BOUND")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestUploadAttachment_Pending 覆盖新会话流程：无 conversation_id → pending 暂存，
// 响应回 name/size/path/upload_id。
func TestUploadAttachment_Pending(t *testing.T) {
	r, _ := newAttachmentTestServer(t, 1<<20, &fakeAttachmentContext{})
	w := postAttachment(r, multipartBody("targets.txt", "192.168.1.1", nil))
	if w.Code != http.StatusCreated {
		t.Fatalf("应 201，得 %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `".staging/pending/`) || !strings.Contains(w.Body.String(), `"targets.txt"`) {
		t.Fatalf("响应应含 pending 路径与净化名: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"upload_id":"`) {
		t.Fatalf("pending 上传应回 upload_id: %s", w.Body.String())
	}
}

// TestUploadAttachment_ToInbox 会话已绑任务：解析链给 assignmentID，直落 inbox。
func TestUploadAttachment_ToInbox(t *testing.T) {
	ctx := &fakeAttachmentContext{taskID: "0f0e0d0c-1111-2222-3333-444444444444", assignmentID: "abababab-1111-2222-3333-444444444444"}
	r, _ := newAttachmentTestServer(t, 1<<20, ctx)
	w := postAttachment(r, multipartBody("dict.txt", "password", map[string]string{"conversation_id": "c1"}))
	if w.Code != http.StatusCreated {
		t.Fatalf("应 201，得 %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), ctx.assignmentID) || !strings.Contains(w.Body.String(), "/inbox/") {
		t.Fatalf("应直落 inbox: %s", w.Body.String())
	}
}

// TestUploadAttachment_MissingFile：缺 file 字段 400。
func TestUploadAttachment_MissingFile(t *testing.T) {
	r, _ := newAttachmentTestServer(t, 1<<20, &fakeAttachmentContext{})
	body := "--BOUND\r\nContent-Disposition: form-data; name=\"conversation_id\"\r\n\r\nc1\r\n--BOUND--\r\n"
	w := postAttachment(r, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("缺 file 应 400，得 %d: %s", w.Code, w.Body.String())
	}
}

// TestUploadAttachment_Oversize413：文件本体超 sizeCap → 落盘层截断 → 413，
// 且盘上不残留半成品（暂存根被清干净）。
func TestUploadAttachment_Oversize413(t *testing.T) {
	r, root := newAttachmentTestServer(t, 8, &fakeAttachmentContext{}) // 上限 8 字节
	w := postAttachment(r, multipartBody("big.bin", "0123456789ABCDEF", nil))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超限应 413，得 %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "附件超过上限") {
		t.Fatalf("错误体应说明上限: %s", w.Body.String())
	}
	stagingRoot := filepath.Join(root, attachment.StagingTop)
	if entries, err := filepath.Glob(filepath.Join(stagingRoot, "**", "*")); err == nil {
		for _, e := range entries {
			t.Fatalf("超限半成品应被清理，残留: %s", e)
		}
	}
}
