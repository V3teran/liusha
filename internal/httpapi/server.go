// Package httpapi 承载对外的 REST 接口。Server 负责装配 Gin 引擎、
// 挂载中间件、注册 v1 路由，并提供可优雅关停的 http.Server 封装。
package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/engagement"
	engstore "github.com/V3teran/liusha/internal/engagement/store"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// EngagementStore 把 handler 所需的 engagement 存储行为收窄为最小接口，
// 便于测试时注入内存实现，也使 server 不必直接依赖具体的 Postgres 实现。
type EngagementStore interface {
	Create(ctx context.Context, e engagement.Engagement) error
	Get(ctx context.Context, id uuid.UUID) (engagement.Engagement, error)
	ListByTenant(ctx context.Context, tenantID uuid.UUID, limit int) ([]engagement.Engagement, error)
	Archive(ctx context.Context, id uuid.UUID, expectedVersion int64) error
}

// Server 把 gin.Engine 与 http.Server 绑定在一起，由 main 负责调用 Run/Shutdown。
type Server struct {
	cfg    config.Config
	engSvc EngagementStore
	engine *gin.Engine
	http   *http.Server
}

// New 构造 Server：生产环境切换 gin Release 模式，注入 Recovery + 链路追踪中间件，
// 然后注册全部路由，并把 gin 引擎包进带有超时设置的 http.Server。
func New(cfg config.Config, engSvc EngagementStore) *Server {
	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Recovery(), logx.TraceMiddleware())

	s := &Server{cfg: cfg, engSvc: engSvc, engine: r}
	s.routes()
	s.http = &http.Server{
		Addr:              cfg.APIAddr,
		Handler:           r,
		ReadHeaderTimeout: time.Duration(cfg.API.ReadTimeoutSeconds) * time.Second,
		WriteTimeout:      time.Duration(cfg.API.WriteTimeoutSeconds) * time.Second,
	}
	return s
}

// Handler 暴露底层 http.Handler，便于测试时直接 ServeHTTP，不必真正监听端口。
func (s *Server) Handler() http.Handler { return s.engine }

// routes 注册健康检查与 /api/v1 路由组；具体 engagement handler 在 Task 13 填充。
func (s *Server) routes() {
	s.engine.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	v1 := s.engine.Group("/api/v1")
	{
		v1.POST("/engagements", s.createEngagement)
		v1.GET("/engagements", s.listEngagements)
		v1.GET("/engagements/:id", s.getEngagement)
		v1.POST("/engagements/:id/archive", s.archiveEngagement)
	}
}

// Run 阻塞监听配置的地址；ErrServerClosed 视为优雅关停成功而非失败。
func (s *Server) Run() error {
	logx.From(context.Background()).Info().Str("addr", s.cfg.APIAddr).Msg("api 开始监听")
	if err := s.http.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown 透传给底层 http.Server，交由调用方控制关停超时。
func (s *Server) Shutdown(ctx context.Context) error { return s.http.Shutdown(ctx) }

// respondError 统一错误响应结构：业务码 + 展示消息；同时写一条 Warn 日志便于排查。
func respondError(c *gin.Context, status int, code, msg string) {
	logx.From(c.Request.Context()).Warn().
		Int("status", status).
		Str("code", code).
		Msg(msg)
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": msg}})
}

// 以下四个 handler 为占位，实际逻辑由 Task 13 填充；暂时统一返回 501 便于端到端拉通。
func (s *Server) createEngagement(c *gin.Context) {
	respondError(c, http.StatusNotImplemented, "not_implemented", "待 Task 13 实现")
}
func (s *Server) listEngagements(c *gin.Context) {
	respondError(c, http.StatusNotImplemented, "not_implemented", "待 Task 13 实现")
}
func (s *Server) getEngagement(c *gin.Context) {
	respondError(c, http.StatusNotImplemented, "not_implemented", "待 Task 13 实现")
}
func (s *Server) archiveEngagement(c *gin.Context) {
	respondError(c, http.StatusNotImplemented, "not_implemented", "待 Task 13 实现")
}

// 编译期断言：Postgres store 必须满足 handler 所需的最小接口。
var _ EngagementStore = (*engstore.Postgres)(nil)
