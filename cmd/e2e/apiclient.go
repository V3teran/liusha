package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// createChatScan 调 POST /chat 发起【对话式】active 扫描，返回 (conversationID, taskID)。
// /chat 建 conversation + 发 SSE 过程事件，前端能实时看到对话——e2e active 走此入口使扫描
// 在前端可观察（区别于纯后台无对话的 POST /scan/active）。taskID 即响应的 scan_id，
// brief 是用户自然语言任务简报，后端不解析，整段透传给 hunter LLM。
func createChatScan(base, key, brief string) (conversationID, taskID string, err error) {
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
		ScanID         string `json:"scan_id"` // = task_id
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", fmt.Errorf("decode chat: %w", err)
	}
	if out.ConversationID == "" || out.ScanID == "" {
		return "", "", fmt.Errorf("chat returned empty ids")
	}
	return out.ConversationID, out.ScanID, nil
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
