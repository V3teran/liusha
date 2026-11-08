package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/V3teran/liusha/internal/skillstore"
	"github.com/V3teran/liusha/internal/task"
)

// API 边界契约：客户端输入错误必须是 4xx，资源不存在必须是 404——
// 不得以 500 冒充服务端故障，也不得以 200 空结果假装成功。

func serve(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRequireUUIDParam(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/things/:id", requireUUIDParam("id"), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	if w := serve(r, http.MethodGet, "/things/not-a-uuid", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("非法 UUID 应 400，得 %d", w.Code)
	}
	if w := serve(r, http.MethodGet, "/things/7d4f3c2a-1b6e-4f0a-9c8d-2e5b1a0f9e33", ""); w.Code != http.StatusNoContent {
		t.Fatalf("合法 UUID 应放行，得 %d", w.Code)
	}
}

func TestRespondErr_NotFoundIs404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", func(c *gin.Context) {
		respondErr(c, http.StatusInternalServerError, fmt.Errorf("get task t1: %w", pgx.ErrNoRows))
	})
	w := serve(r, http.MethodGet, "/x", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("store 的「不存在」应映射 404，得 %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "no rows") {
		t.Fatalf("404 消息不应透出驱动原文：%s", w.Body.String())
	}
}

func TestRespondErr_GenuineFailureStays500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", func(c *gin.Context) { respondErr(c, http.StatusInternalServerError, errors.New("连接池耗尽")) })
	if w := serve(r, http.MethodGet, "/x", ""); w.Code != http.StatusInternalServerError {
		t.Fatalf("真实故障应保持 500，得 %d", w.Code)
	}
}

type fakeTasksList struct{ called bool }

func (f *fakeTasksList) ListFiltered(context.Context, task.ListFilter) ([]task.Summary, error) {
	f.called = true
	return nil, nil
}
func (f *fakeTasksList) CountFiltered(context.Context, task.ListFilter) (int, error) { return 0, nil }
func (f *fakeTasksList) GetSummary(context.Context, string) (task.Summary, error) {
	return task.Summary{}, nil
}

func TestListTasks_UnknownSourceRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api := &fakeTasksList{}
	r := gin.New()
	r.GET("/tasks", listTasksHandler(api))

	w := serve(r, http.MethodGet, "/tasks?source=self", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("未知来源应 400（否则旧取值静默返回空列表），得 %d", w.Code)
	}
	if api.called {
		t.Fatal("非法来源不应触达 store")
	}
	for _, ok := range []string{"", "direct", "proxy"} {
		if w := serve(r, http.MethodGet, "/tasks?source="+ok, ""); w.Code != http.StatusOK {
			t.Fatalf("source=%q 应 200，得 %d", ok, w.Code)
		}
	}
}

func TestListConversations_UnknownSourceRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/conversations", listConversationsHandler(&fakeConversations{}))
	if w := serve(r, http.MethodGet, "/conversations?source=self", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("未知来源应 400，得 %d", w.Code)
	}
	if w := serve(r, http.MethodGet, "/conversations?source=proxy", ""); w.Code != http.StatusOK {
		t.Fatalf("合法来源应 200，得 %d", w.Code)
	}
}

func TestListConversations_EmptyIsArrayNotNull(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/conversations", listConversationsHandler(&fakeConversations{}))
	w := serve(r, http.MethodGet, "/conversations?source=direct", "")
	if !strings.Contains(w.Body.String(), `"conversations":[]`) {
		t.Fatalf("空列表应序列化为 []，得 %s", w.Body.String())
	}
}

type dupSkillAPI struct{ SkillAPI }

func (dupSkillAPI) CreateSkill(context.Context, skillstore.Skill) (skillstore.Skill, error) {
	return skillstore.Skill{}, fmt.Errorf("create skill: %w", &pgconn.PgError{Code: "23505", ConstraintName: "skill_code_key"})
}

func TestCreateSkill_DuplicateCodeIs409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/skills", createSkillHandler(dupSkillAPI{}))
	w := serve(r, http.MethodPost, "/skills",
		`{"code":"sqli","name":"n","description":"d","category":"vuln","body":"b"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("重复 code 应 409，得 %d (%s)", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "SQLSTATE") {
		t.Fatalf("冲突消息应为业务语言，不透出驱动原文：%s", w.Body.String())
	}
}
