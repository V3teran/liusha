package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// liushaSessionCookie 是 liusha 会话 Cookie 名（与后端一致）。
const liushaSessionCookie = "liusha_session"

// liushaClient 经 liusha 公开 HTTP API 建任务、查产出、控制任务——与前端同一路径，
// 不依赖库表结构，也不需要数据库凭据。
type liushaClient struct {
	base    string
	session *http.Cookie
	client  *http.Client
}

// loginLiusha 以账号密码登录取得会话 Cookie，构造 liushaClient。
func loginLiusha(ctx context.Context, base, user, pass string, client *http.Client) (*liushaClient, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	base = strings.TrimRight(base, "/")
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("构造登录请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("登录 liusha 失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("登录 liusha HTTP %d（账号密码不符？）", resp.StatusCode)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == liushaSessionCookie {
			return &liushaClient{base: base, session: ck, client: client}, nil
		}
	}
	return nil, fmt.Errorf("登录 200 但未下发会话 Cookie")
}

// req 发起带会话的请求并解码成功响应。
func (c *liushaClient) req(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("编码请求体: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return fmt.Errorf("构造请求 %s %s: %w", method, path, err)
	}
	req.AddCookie(c.session)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, truncate(string(raw), 256))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("解析 %s %s 响应: %w", method, path, err)
		}
	}
	return nil
}

// benchScenario 是跑分任务的场景：CTF 夺旗（flag 的回报规约在场景里，见 internal/builtin/scenarios/ctf.md）。
const benchScenario = "ctf"

// createTask 以单条 brief 建一个 task（场景 benchScenario），返回 taskID。title/description 便于在控制台归类。
// 端点按行拆 brief、每行一个任务：若一次建出了多个任务（brief 含换行），全部终止并报错，
// 不留下无人跟踪、持续消耗 LLM 额度的任务。
func (c *liushaClient) createTask(ctx context.Context, title, description, brief string) (string, error) {
	body := map[string]any{"title": title, "description": description, "briefs": []string{brief}, "scenario": benchScenario}
	var out dispatchResp
	if err := c.req(ctx, http.MethodPost, "/api/tasks", body, &out); err != nil {
		return "", err
	}
	if len(out.Tasks) == 0 || out.Tasks[0].TaskID == "" {
		return "", fmt.Errorf("建任务未返回 task_id（brief 可能被识别为非扫描意图）")
	}
	if len(out.Tasks) > 1 {
		var errs []error
		for _, t := range out.Tasks {
			if err := c.control(ctx, t.TaskID, "terminate"); err != nil {
				errs = append(errs, fmt.Errorf("终止多建的任务 %s: %w", t.TaskID, err))
			}
		}
		return "", errors.Join(append([]error{fmt.Errorf(
			"一个 brief 建出了 %d 个任务（brief 含换行被按行拆分），已全部终止", len(out.Tasks))}, errs...)...)
	}
	return out.Tasks[0].TaskID, nil
}

func taskPath(taskID, suffix string) string {
	return "/api/tasks/" + url.PathEscape(taskID) + suffix
}

// status 返回任务状态（active/completed/aborted）与是否暂停。
func (c *liushaClient) status(ctx context.Context, taskID string) (taskStatusResp, error) {
	var out taskStatusResp
	err := c.req(ctx, http.MethodGet, taskPath(taskID, ""), nil, &out)
	return out, err
}

// graphNodes 返回任务探索图的节点（flag 兜底提取从 observation 的 content 扫）。
func (c *liushaClient) graphNodes(ctx context.Context, taskID string) ([]graphNode, error) {
	var out graphResp
	if err := c.req(ctx, http.MethodGet, taskPath(taskID, "/exploration/graph"), nil, &out); err != nil {
		return nil, err
	}
	return out.Nodes, nil
}

// usage 返回任务累计 LLM/工具调用数（活性信号；一段时间不增长即停摆）。
func (c *liushaClient) usage(ctx context.Context, taskID string) (int, error) {
	var out usageResp
	if err := c.req(ctx, http.MethodGet, taskPath(taskID, "/usage"), nil, &out); err != nil {
		return 0, err
	}
	return out.LLMInvocations + out.ToolInvocations, nil
}

// control 下发任务控制指令（pause/resume/terminate）。
func (c *liushaClient) control(ctx context.Context, taskID, action string) error {
	body := map[string]string{"action": action}
	return c.req(ctx, http.MethodPost, taskPath(taskID, "/control"), body, nil)
}
