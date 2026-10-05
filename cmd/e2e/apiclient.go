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

// createChatScan 调 POST /chat 发起【会话式】扫描，返回 (conversationID, taskID)。
// /chat 建 conversation + 发 SSE 过程事件，前端能实时看到会话——e2e 走此入口使扫描
// 在前端可观察（区别于纯后台无会话的 POST /scan）。taskID 即响应的 task_id，
// brief 是用户自然语言任务简报，后端通过 msgclass 自动分类决定是否创建扫描任务。
func createChatScan(base, key, brief string) (conversationID, taskID string, err error) {
	body, _ := json.Marshal(map[string]string{"brief": brief})

	// 带重试的专用客户端：/chat 是同步端点（内部跑 msgclass 分类 LLM 调用），
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
		req, _ := http.NewRequest(http.MethodPost, base+"/chat", bytes.NewReader(body))
		req.Header.Set("X-API-Key", key)
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
	// e2e 要求必须创建扫描任务。如果 taskID 为空，说明 brief 被 msgclass 识别为纯聊天，
	// 需要修改 brief 使其包含明确的扫描意图（如：测试、扫描、挖掘漏洞等关键词）。
	if out.TaskID == "" {
		return "", "", fmt.Errorf("chat returned empty task_id (brief 被识别为聊天而非扫描，需要包含扫描关键词)")
	}
	return out.ConversationID, out.TaskID, nil
}
