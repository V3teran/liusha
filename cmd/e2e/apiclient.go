package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// e2e 管理员凭据：默认与 scripts/dev 的 LIUSHA_ADMIN_INITIAL_PASSWORD 开发默认值
// 对齐（等价于昔日 changeme-dev-key 的角色），可经 env 覆盖。账号由 api 启动期
// 用 env 密码建号，e2e 只做登录。
var (
	e2eAdminUserDefault = "admin"
	e2eAdminPassDefault = "changeme" + "-admin-pass"
)

// bootstrapSession 登录取得会话 Cookie（随请求发送）。凭据失效即报错——
// e2e 环境的账号由 LIUSHA_ADMIN_INITIAL_PASSWORD 在 api 首启时建立。
func bootstrapSession(base string) (*http.Cookie, error) {
	user := lookupEnv("LIUSHA_E2E_ADMIN_USER")
	if user == "" {
		user = e2eAdminUserDefault
	}
	pass := lookupEnv("LIUSHA_E2E_ADMIN_PASSWORD")
	if pass == "" {
		pass = lookupEnv("LIUSHA_ADMIN_INITIAL_PASSWORD")
	}
	if pass == "" {
		pass = e2eAdminPassDefault
	}
	ck, status, err := tryLogin(base, user, pass)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("login %d（凭据与 api 首启 LIUSHA_ADMIN_INITIAL_PASSWORD 不一致？）", status)
	}
	return ck, nil
}

// sessionCookieName 与后端一致。
const sessionCookieName = "liusha_session"

// authClient 供登录/建号引导用：设超时防 api 不可达时无限挂起
// （DefaultClient 无超时；createChatTask 的长超时客户端不适用于这类轻请求）。
var authClient = &http.Client{Timeout: 30 * time.Second}

// tryLogin POST /api/auth/login，返回（会话 Cookie, 状态码, 错误）。
func tryLogin(base, user, pass string) (*http.Cookie, int, error) {
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	resp, err := authClient.Post(base+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("post login: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			return c, resp.StatusCode, nil
		}
	}
	if resp.StatusCode == http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("login 200 但未下发会话 Cookie")
	}
	return nil, resp.StatusCode, nil
}

// controlTask 走公开 API 下发任务控制指令（pause/resume/terminate）——
// 与前端同一入口。 terminate 曾直插 task_control_event 表，生命周期重构后
// 消费器只认任务行与内容干预，该事件永久无人消费，任务停不下来。
func controlTask(base string, session *http.Cookie, taskID, action string) error {
	body, _ := json.Marshal(map[string]string{"action": action})
	req, err := http.NewRequest(http.MethodPost, base+"/api/tasks/"+taskID+"/control", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build control req: %w", err)
	}
	req.AddCookie(session) // #nosec G104 // e2e 会话凭证
	req.Header.Set("Content-Type", "application/json")
	resp, err := authClient.Do(req)
	if err != nil {
		return fmt.Errorf("post control: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("control %s: HTTP %d", action, resp.StatusCode)
	}
	return nil
}

// createChatTask 调 POST /chat 发起【会话式】扫描，返回 (conversationID, taskID)。
// /chat 建 conversation + 发 SSE 过程事件，前端能实时看到会话——e2e 走此入口使扫描
// 在前端可观察（区别于纯后台无会话的 POST /scan）。taskID 即响应的 task_id，
// brief 是用户自然语言任务简报，后端通过 intent 自动分类决定是否创建扫描任务。
func createChatTask(base string, session *http.Cookie, brief string) (conversationID, taskID string, err error) {
	body, _ := json.Marshal(map[string]string{"brief": brief})

	// 带重试的专用客户端：/chat 是同步端点（内部跑 intent 分类 LLM 调用），
	// 上游 429（资源包限额）时服务端需等外部分类完成，裸 DefaultClient 无超时
	// 且 TCP 一旦被中间层掐断就报 EOF——错误不可读、也不重试。此处：总超时
	// 3min + 对 429/5xx/EOF 指数退避重试 3 次。
	client := &http.Client{
		Timeout: 3 * time.Minute,
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			ResponseHeaderTimeout: 170 * time.Second,
		},
	}
	var resp *http.Response
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(1<<uint(attempt-1)) * 5 * time.Second) // 5s/10s
		}
		req, _ := http.NewRequest(http.MethodPost, base+"/api/chat", bytes.NewReader(body))
		req.AddCookie(session) // #nosec G104 // e2e 会话凭证
		req.Header.Set("Content-Type", "application/json")
		resp, err = client.Do(req)
		if err != nil {
			lastErr = err
			continue // EOF/超时类：重试
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			resp = nil
			continue // 429/5xx：退避重试
		}
		break
	}
	if resp == nil {
		return "", "", fmt.Errorf("post chat（重试 3 次后仍失败）: %w", lastErr)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("chat %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		ConversationID string `json:"conversation_id"`
		TaskID         string `json:"task_id"` // = task_id
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", fmt.Errorf("decode chat: %w", err)
	}
	if out.ConversationID == "" {
		return "", "", fmt.Errorf("chat returned empty conversation_id")
	}
	// e2e 要求必须创建扫描任务。如果 taskID 为空，说明 brief 被 intent 识别为纯聊天，
	// 需要修改 brief 使其包含明确的扫描意图（如：测试、扫描、挖掘漏洞等关键词）。
	if out.TaskID == "" {
		return "", "", fmt.Errorf("chat returned empty task_id (brief 被识别为聊天而非扫描，需要包含扫描关键词)")
	}
	return out.ConversationID, out.TaskID, nil
}
