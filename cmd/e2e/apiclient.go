package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// createChatScan 调 POST /chat 发起【会话式】扫描，返回 (conversationID, taskID)。
// /chat 建 conversation + 发 SSE 过程事件，前端能实时看到会话——e2e 走此入口使扫描
// 在前端可观察（区别于纯后台无会话的 POST /scan）。taskID 即响应的 task_id，
// brief 是用户自然语言任务简报，后端通过 msgclass 自动分类决定是否创建扫描任务。
// ID 参数已废弃（老架构遗留），当前架构自动决定场景。
func createChatScan(base, key, brief, ID string) (conversationID, taskID string, err error) {
	body, _ := json.Marshal(map[string]string{"brief": brief})
	req, _ := http.NewRequest(http.MethodPost, base+"/chat", bytes.NewReader(body))
	req.Header.Set("X-API-Key", key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("post chat: %w", err)
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

// saveCredsBatch 一次录入多 host 凭证（host → []credentialEntry 映射）。
// 各 host 必须与 proxy 看到的 snapshot.Host 一致（含端口形式，如 "localhost:8001"）。
func saveCredsBatch(base, key string, hostCreds map[string][]credentialEntry) error {
	if len(hostCreds) == 0 {
		return nil
	}
	payload := map[string]any{
		"ttl_seconds": 0,
		"credentials": hostCreds,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	req, _ := http.NewRequest(http.MethodPost, base+"/credential/batch", bytes.NewReader(body))
	req.Header.Set("X-API-Key", key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("post credential/batch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("credential/batch %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}
