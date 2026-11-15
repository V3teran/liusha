package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/workspace"
)

func newWorkspaceRawServer(t *testing.T) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ws, err := workspace.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() }) // Windows 上打开着的目录句柄会让临时目录删不掉
	r := gin.New()
	r.GET("/workspaces/raw", getWorkspaceRawHandler(ws))
	return r, ws.Root()
}

func getRaw(r *gin.Engine, rel string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/workspaces/raw?path="+url.QueryEscape(rel), nil))
	return w
}

// 下载不存在的目录：回 404，而不是空的 200 ZIP。
func TestWorkspaceRaw_MissingDirIs404(t *testing.T) {
	r, _ := newWorkspaceRawServer(t)
	w := getRaw(r, "nope/")
	if w.Code != http.StatusNotFound {
		t.Fatalf("不存在的目录应回 404，得 %d", w.Code)
	}
	if w.Header().Get("Content-Disposition") != "" {
		t.Fatal("错误响应不应带附件下载头")
	}
}

// 正常目录：200 + 合法 zip；中文目录名的下载头经 RFC 2231 编码。
func TestWorkspaceRaw_DirZipAndEncodedFilename(t *testing.T) {
	r, root := newWorkspaceRawServer(t)
	dir := filepath.Join(root, "任务一")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := getRaw(r, "任务一/")
	if w.Code != http.StatusOK {
		t.Fatalf("应回 200，得 %d", w.Code)
	}
	if _, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len())); err != nil {
		t.Fatalf("应为合法 zip: %v", err)
	}
	if cd := w.Header().Get("Content-Disposition"); cd != `attachment; filename*=utf-8''%E4%BB%BB%E5%8A%A1%E4%B8%80.zip` {
		t.Fatalf("下载头编码不符: %s", cd)
	}
}

// fakeDirLabels 是 WorkspaceLabelsAPI 的替身：记录收到的 ID，按表返回显示名。
type fakeDirLabels struct {
	gotAssignments, gotTasks []string
}

func (f *fakeDirLabels) DirectoryLabels(_ context.Context, assignmentIDs, taskIDs []string) (map[string]string, map[string]string, error) {
	f.gotAssignments, f.gotTasks = assignmentIDs, taskIDs
	return map[string]string{"11111111-1111-4111-8111-111111111111": "夜间复扫"},
		map[string]string{"22222222-2222-4222-8222-222222222222": "测试商城 shop.local"}, nil
}

// 目录树给批次 / 任务目录带上显示名（前端据此显示与搜索，不再拉全量任务）；非 UUID 的目录不查询。
func TestWorkspaceTree_LabelsBatchAndTaskDirs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ws, err := workspace.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	const asg, tk = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	for _, dir := range []string{asg + "/" + tk + "/inbox", "scratch"} {
		if err := os.MkdirAll(filepath.Join(ws.Root(), filepath.FromSlash(dir)), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	labels := &fakeDirLabels{}
	r := gin.New()
	r.GET("/workspaces/tree", getWorkspaceTreeHandler(ws, labels))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/workspaces/tree", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var body struct {
		Entries []struct {
			Path  string `json:"path"`
			Label string `json:"label"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range body.Entries {
		got[e.Path] = e.Label
	}
	if got[asg+"/"] != "夜间复扫" || got[asg+"/"+tk+"/"] != "测试商城 shop.local" {
		t.Fatalf("批次 / 任务目录应带显示名: %v", got)
	}
	if got["scratch/"] != "" || got[asg+"/"+tk+"/inbox/"] != "" {
		t.Fatalf("其它目录不带显示名: %v", got)
	}
	if len(labels.gotAssignments) != 1 || len(labels.gotTasks) != 1 {
		t.Fatalf("只查询 UUID 形态的批次 / 任务目录: %v %v", labels.gotAssignments, labels.gotTasks)
	}
}
