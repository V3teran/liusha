package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/V3teran/liusha/internal/config"
	engstore "github.com/V3teran/liusha/internal/engagement/store"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/mcpserver"
	"github.com/V3teran/liusha/internal/scope"
)

func main() {
	cfgPath := os.Getenv("LIUSHA_CONFIG")
	if cfgPath == "" {
		cfgPath = "config/config.yaml"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		panic("加载配置失败: " + err.Error())
	}
	logx.Init(cfg.LogLevel, cfg.Env == "development")
	ctx := logx.WithTraceID(context.Background(), logx.NewTraceID())

	engagementIDStr := os.Getenv("LIUSHA_ENGAGEMENT_ID")
	if engagementIDStr == "" {
		logx.From(ctx).Fatal().Msg("必须设置 LIUSHA_ENGAGEMENT_ID 环境变量")
	}
	engID, err := uuid.Parse(engagementIDStr)
	if err != nil {
		logx.From(ctx).Fatal().Err(err).Msg("LIUSHA_ENGAGEMENT_ID 不是合法 UUID")
	}

	pool, err := pgxpool.New(ctx, cfg.PostgresDSN)
	if err != nil {
		logx.From(ctx).Fatal().Err(err).Msg("连接 postgres 失败")
	}
	defer pool.Close()

	engSt := engstore.NewPostgres(pool)
	eng, err := engSt.Get(ctx, engID)
	if err != nil {
		logx.From(ctx).Fatal().Err(err).Str("engagement_id", engID.String()).Msg("加载 engagement 失败")
	}

	policy, err := buildPolicy(eng.Scope)
	if err != nil {
		logx.From(ctx).Fatal().Err(err).Msg("构建 ScopePolicy 失败")
	}

	runner, err := mcpserver.NewDockerRunner("liusha-vuln-tools")
	if err != nil {
		logx.From(ctx).Fatal().Err(err).Msg("初始化 DockerRunner 失败")
	}

	srv, err := mcpserver.New(policy)
	if err != nil {
		logx.From(ctx).Fatal().Err(err).Msg("初始化 MCP server 失败")
	}

	registerTools(srv, runner, policy)

	logx.From(ctx).Info().Msg("liusha-mcp-server 启动（stdio 模式）")
	if err := srv.ServeStdio(); err != nil {
		logx.From(ctx).Fatal().Err(err).Msg("MCP server 退出")
	}
}

func registerTools(srv *mcpserver.Server, runner *mcpserver.DockerRunner, policy *scope.Policy) {
	nuclei := mcpserver.WithScopeGuard(policy, mcpserver.ToolFunc(
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			target, _ := args["target"].(string)
			tags, _ := args["tags"].(string)
			cmd := []string{"nuclei", "-u", target, "-silent", "-j"}
			if tags != "" {
				cmd = append(cmd, "-tags", tags)
			}
			out, err := runner.Run(ctx, cmd)
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(out), nil
		},
	))
	_ = srv.RegisterTool("nuclei_scan", "对目标 URL 运行 nuclei 漏洞扫描", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"target": map[string]any{"type": "string", "description": "目标 URL"},
			"tags":   map[string]any{"type": "string", "description": "nuclei 标签过滤（可选）"},
		},
		"required": []string{"target"},
	}, nuclei)

	ffuf := mcpserver.ToolFunc(
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			target, _ := args["target"].(string)
			wordlistName, _ := args["wordlist_name"].(string)
			// 白名单校验：禁止用户传任意路径，避免通过 ffuf 读取容器内任意文件。
			wordlistPath, err := mcpserver.ResolveFfufWordlist(wordlistName)
			if err != nil {
				return nil, err
			}
			cmd := []string{"ffuf", "-u", target + "/FUZZ", "-w", wordlistPath, "-s"}
			out, err := runner.Run(ctx, cmd)
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(out), nil
		},
	)
	_ = srv.RegisterTool("ffuf_fuzz", "对目标 URL 进行目录爆破", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"target": map[string]any{"type": "string", "description": "目标 URL"},
			"wordlist_name": map[string]any{
				"type":        "string",
				"enum":        mcpserver.FfufWordlistNames(),
				"description": "字典枚举（common/big/raft-small），默认 common",
			},
		},
		"required": []string{"target"},
	}, ffuf)

	httpx := mcpserver.WithScopeGuard(policy, mcpserver.ToolFunc(
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			target, _ := args["target"].(string)
			cmd := []string{"httpx", "-u", target, "-silent", "-json", "-title", "-tech-detect"}
			out, err := runner.Run(ctx, cmd)
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(out), nil
		},
	))
	_ = srv.RegisterTool("httpx_probe", "探测目标 URL 并返回响应元数据", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"target": map[string]any{"type": "string", "description": "目标 URL"},
		},
		"required": []string{"target"},
	}, httpx)
}

func buildPolicy(s map[string]any) (*scope.Policy, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var rules scope.Rules
	if err := json.Unmarshal(b, &rules); err != nil {
		return nil, err
	}
	return scope.NewPolicy(rules)
}
