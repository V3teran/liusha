package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// createPassiveScan 调 POST /scan/passive 拿 engagement_id；同 host 幂等。
// 与 createActiveScan 对仗：passive 开"被动接流量入口"，active 触发"主动扫描"。
func createPassiveScan(base, key, host string) (string, error) {
	body, _ := json.Marshal(map[string]string{"host": host})
	req, _ := http.NewRequest(http.MethodPost, base+"/scan/passive", bytes.NewReader(body))
	req.Header.Set("X-API-Key", key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("post scan/passive: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("scan/passive %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		EngagementID string `json:"engagement_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode scan/passive: %w", err)
	}
	if out.EngagementID == "" {
		return "", fmt.Errorf("scan/passive returned empty engagement_id")
	}
	return out.EngagementID, nil
}

// createActiveScan 调 POST /scan/active 拿 (engagement_id, agent_run_id)。
// brief 是用户自然语言任务简报（含目标 URL/IP / 账号密码 / 测试方向等），
// 后端不解析，整段透传给 hunter LLM。
func createActiveScan(base, key, brief string) (string, string, error) {
	body, _ := json.Marshal(map[string]string{"brief": brief})
	req, _ := http.NewRequest(http.MethodPost, base+"/scan/active", bytes.NewReader(body))
	req.Header.Set("X-API-Key", key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("post scan/active: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("scan/active %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		EngagementID string `json:"engagement_id"`
		AgentRunID   string `json:"agent_run_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", fmt.Errorf("decode scan/active: %w", err)
	}
	if out.EngagementID == "" || out.AgentRunID == "" {
		return "", "", fmt.Errorf("scan/active returned empty ids")
	}
	return out.EngagementID, out.AgentRunID, nil
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
