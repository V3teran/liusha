package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// createProxyEngagement 调 POST /engagement/proxy 拿 engagement_id；同 host 幂等。
func createProxyEngagement(base, key, host string) (string, error) {
	body, _ := json.Marshal(map[string]string{"host": host})
	req, _ := http.NewRequest(http.MethodPost, base+"/engagement/proxy", bytes.NewReader(body))
	req.Header.Set("X-API-Key", key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("post engagement/proxy: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("engagement/proxy %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		EngagementID string `json:"engagement_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode engagement/proxy: %w", err)
	}
	if out.EngagementID == "" {
		return "", fmt.Errorf("engagement/proxy returned empty engagement_id")
	}
	return out.EngagementID, nil
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
