package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// taskAPI 经公开 API 观察任务（状态、探索图、用量）：与前端同一路径，e2e 不依赖库表结构，
// 也不需要数据库凭据。
type taskAPI struct {
	base    string
	session *http.Cookie
}

// getJSON 带会话 GET base+path 并解码 JSON 响应。
func (a taskAPI) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base+path, nil)
	if err != nil {
		return fmt.Errorf("构造请求 %s: %w", path, err)
	}
	req.AddCookie(a.session)
	resp, err := authClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GET %s: HTTP %d %s", path, resp.StatusCode, body)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("解析 %s 响应: %w", path, err)
	}
	return nil
}

func taskPath(taskID, suffix string) string {
	return "/api/tasks/" + url.PathEscape(taskID) + suffix
}

// Status 返回任务状态（active / completed / aborted）。
func (a taskAPI) Status(ctx context.Context, taskID string) (string, error) {
	var t struct {
		Status string `json:"status"`
	}
	if err := a.getJSON(ctx, taskPath(taskID, ""), &t); err != nil {
		return "", err
	}
	return t.Status, nil
}

// GraphStats 按节点类型统计任务的探索图。
func (a taskAPI) GraphStats(ctx context.Context, taskID string) (GraphStats, error) {
	var g struct {
		Nodes []struct {
			Kind string `json:"kind"`
		} `json:"nodes"`
	}
	if err := a.getJSON(ctx, taskPath(taskID, "/exploration/graph"), &g); err != nil {
		return GraphStats{}, err
	}
	var s GraphStats
	for _, n := range g.Nodes {
		switch n.Kind {
		case "objective":
			s.Objectives++
		case "action":
			s.Actions++
		case "observation":
			s.Observations++
		case "result":
			s.Results++
		}
	}
	return s, nil
}

// ActivityCount 返回任务累计的 LLM 调用与工具调用次数：在一段时间内不增长即没有活动。
func (a taskAPI) ActivityCount(ctx context.Context, taskID string) (int, error) {
	var u struct {
		LLMInvocations  int `json:"llm_calls"`
		ToolInvocations int `json:"tool_calls"`
	}
	if err := a.getJSON(ctx, taskPath(taskID, "/usage"), &u); err != nil {
		return 0, err
	}
	return u.LLMInvocations + u.ToolInvocations, nil
}
