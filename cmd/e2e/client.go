package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// taskAPI 经公开 API 观察任务（状态、探索图、当前 run 的存活心跳）：与前端同一路径，e2e 不依赖库表结构，
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

// liveRun 是任务当前存活的一轮 run。
type liveRun struct {
	Status      string    `json:"status"`
	HeartbeatAt time.Time `json:"heartbeat_at"` // 存活心跳：runner 恢复监控判定它是否还在被推进的依据
}

// LiveRun 返回任务当前存活的一轮 run；没有存活的 run（已结束、暂停、两次重投之间）时 ok 为 false。
func (a taskAPI) LiveRun(ctx context.Context, taskID string) (run liveRun, ok bool, err error) {
	var resp struct {
		Run *liveRun `json:"run"`
	}
	if err := a.getJSON(ctx, taskPath(taskID, "/run"), &resp); err != nil {
		return liveRun{}, false, err
	}
	if resp.Run == nil {
		return liveRun{}, false, nil
	}
	return *resp.Run, true, nil
}
