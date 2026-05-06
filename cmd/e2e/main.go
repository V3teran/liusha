// Package main 是 liusha 端到端验收触发器，按 profile 选漏洞类型与样本。
//
// 完整流程：
//  1. POST /engagement/proxy 懒创建 engagement（同 host 幂等）
//  2. POST /credential/batch 录身份（profile 决定具体身份集）
//  3. 读 sample 文件（一组 raw HTTP/1.1 报文）
//  4. 用 net.Dial 直连 proxify(:8888) 写 raw bytes（proxy 内置 sanitizer 做
//     relative→absolute URI 改写），不解析 headers/body
//  5. 轮询 finding 表，直到 ≥minFindings 条 <kindPrefix>* 且 ≥minKinds 类齐全
//
// Profile 切换：
//   - LIUSHA_E2E_PROFILE=bac（默认）：3 身份正常流量 → 期望 ≥3 条 bac.* / 3 类齐全
//   - LIUSHA_E2E_PROFILE=sqli       ：DVWA 单流量      → 期望 ≥1 条 sqli.*
//
// 触发器只发起"用户正常流量"——具体漏洞由子 ReAct 内部 fetch_credentials +
// replay_matrix（BAC）或 run_command 容器化沙箱（SQLi）多 payload 重放发现。
//
// 想加新漏洞类型：profiles map 加一行 + 写 examples/sample_<vuln>_raw.json 即可。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/vulnfinding"
)

const (
	pollInterval = 15 * time.Second
	pollDeadline = 6 * time.Minute
	dialTimeout  = 10 * time.Second
	rawIOTimeout = 100 * time.Second
)

// credentialEntry 是 /credential/batch 单条身份记录的结构。
type credentialEntry struct {
	Name        string              `json:"name"`
	Role        string              `json:"role"`
	Credentials []map[string]string `json:"credentials"`
}

// profile 描述一个 e2e 验收剧本（BAC / SQLi）：sample 文件 + 身份集 + 验收门槛。
type profile struct {
	name           string
	defaultSamples string
	kindPrefix     string
	minFindings    int
	minKinds       int
	// credsForHost 接收 scope_host 返回该 host 的身份列表（profile 自决定身份组）。
	credsForHost func(host string) []credentialEntry
}

var profiles = map[string]profile{
	"bac": {
		name:           "bac",
		defaultSamples: "examples/sample_bac_raw.json",
		kindPrefix:     "bac.",
		minFindings:    3,
		minKinds:       3,
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "session=admin_sess_a1b2c3"},
				}},
				{Name: "test", Role: "user", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "session=test_sess_d4e5f6"},
				}},
				{Name: "m233241", Role: "user", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "session=m233241_sess_g7h8i9"},
				}},
			}
		},
	},
	"sqli": {
		name:           "sqli",
		defaultSamples: "examples/sample_sqli_raw.json",
		kindPrefix:     "sqli.",
		minFindings:    1,
		minKinds:       1,
		// DVWA 风格：单一已认证 admin 身份（PHPSESSID + security=low 双 cookie 拼成一行）。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=333lg0l6qt4p9u48aktuquo5r3; security=low"},
				}},
			}
		},
	},
}

func main() {
	logger := logx.New("e2e")
	ctx := context.Background()

	apiBase := envOr("LIUSHA_API_BASE", "http://localhost:8080")
	apiKey := envOr("LIUSHA_API_KEY", "changeme-dev-key")
	pgDSN := envOr("LIUSHA_POSTGRES_DSN", "postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable")
	proxyURL := envOr("LIUSHA_PROXY_ADDR", "http://localhost:8888")
	vulnBase := envOr("LIUSHA_VULNAPP_BASE", "http://host.docker.internal:8001")

	prof, err := selectProfile(envOr("LIUSHA_E2E_PROFILE", "bac"))
	if err != nil {
		logger.Fatal().Err(err).Msg("select profile")
	}
	samplesPath := envOr("LIUSHA_E2E_SAMPLES", prof.defaultSamples)

	proxyHostPort, err := extractHostPort(proxyURL)
	if err != nil {
		logger.Fatal().Err(err).Msg("parse proxy addr")
	}

	samples, err := loadRawSamples(samplesPath)
	if err != nil {
		logger.Fatal().Err(err).Msg("load samples")
	}

	scopeHost, err := resolveScopeHost(vulnBase, samples)
	if err != nil {
		logger.Fatal().Err(err).Msg("resolve scope host")
	}
	logger.Info().
		Str("profile", prof.name).
		Str("scope_host", scopeHost).
		Str("proxy", proxyHostPort).
		Str("samples", samplesPath).
		Int("sample_count", len(samples)).
		Msg("e2e starting")

	eid, err := createProxyEngagement(apiBase, apiKey, scopeHost)
	if err != nil {
		logger.Fatal().Err(err).Msg("create engagement")
	}
	logger.Info().Str("engagement_id", eid).Msg("engagement ready")

	if err := saveCreds(apiBase, apiKey, scopeHost, prof.credsForHost(scopeHost)); err != nil {
		logger.Fatal().Err(err).Msg("save credentials")
	}
	logger.Info().Msg("credentials enrolled")

	logger.Info().Int("samples", len(samples)).Msg("samples loaded")

	for i, raw := range samples {
		if err := dispatchRaw(proxyHostPort, raw); err != nil {
			logger.Fatal().Err(err).Int("idx", i).Msg("dispatch raw")
		}
		logger.Info().Int("idx", i).Msg("raw dispatched via proxy")
	}

	pool, err := db.NewPgPool(ctx, pgDSN, 5, 1)
	if err != nil {
		logger.Fatal().Err(err).Msg("pg")
	}
	defer pool.Close()
	store := vulnfinding.NewStore(pool)

	deadline := time.Now().Add(pollDeadline)
	for time.Now().Before(deadline) {
		all, err := store.ListByEngagement(ctx, eid)
		if err != nil {
			logger.Warn().Err(err).Msg("list findings")
			time.Sleep(pollInterval)
			continue
		}
		matched := filterByPrefix(all, prof.kindPrefix)
		kinds := countKinds(matched)
		logger.Info().
			Str("profile", prof.name).
			Int("count", len(matched)).
			Interface("kinds", kinds).
			Msg("poll")
		if len(matched) >= prof.minFindings && len(kinds) >= prof.minKinds {
			logger.Info().Int("count", len(matched)).Int("kinds", len(kinds)).Msg("e2e PASS")
			fmt.Printf("✓ e2e 验收通过：%s findings=%d kinds=%d\n", prof.name, len(matched), len(kinds))
			return
		}
		time.Sleep(pollInterval)
	}
	logger.Error().Msg("e2e timeout: 未达 finding/类覆盖门槛")
	os.Exit(1)
}

// selectProfile 按名字解析 profile；未知值报错（避免静默退化）。
func selectProfile(name string) (profile, error) {
	p, ok := profiles[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		known := make([]string, 0, len(profiles))
		for k := range profiles {
			known = append(known, k)
		}
		return profile{}, fmt.Errorf("未知 profile %q（可选：%s）", name, strings.Join(known, ", "))
	}
	return p, nil
}

// resolveScopeHost 决定 engagement scope_host：
//
//	优先级：env LIUSHA_E2E_SCOPE_HOST > 首条样本的 Host: 头去端口 > vulnBase URL 的 host
//
// 这样 SQLi 用外网 DVWA 时无需配 LIUSHA_VULNAPP_BASE，BAC 用本地 vulnapp 时也不破坏旧行为。
func resolveScopeHost(vulnBase string, samples []string) (string, error) {
	if v := os.Getenv("LIUSHA_E2E_SCOPE_HOST"); v != "" {
		return v, nil
	}
	if len(samples) > 0 {
		if h := extractHostFromRaw(samples[0]); h != "" {
			return stripPort(h), nil
		}
	}
	return extractHost(vulnBase)
}

// extractHostFromRaw 从一条 raw HTTP/1.1 报文里抓 Host: 头值（去端口前的原始字符串）。
// 不做严格 RFC 解析——CRLF 换行 + 空格大小写不敏感即可。
func extractHostFromRaw(raw string) string {
	for _, line := range strings.Split(raw, "\r\n") {
		if line == "" {
			break
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		if strings.EqualFold(key, "host") {
			return strings.TrimSpace(line[colon+1:])
		}
	}
	return ""
}

// stripPort 去掉 host 末尾的 :port（保留纯主机名/IP，与 proxy 落库 snapshot.Host 对齐）。
func stripPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// extractHost 从 URL 提取去端口的 host（与 proxy 落库 snapshot.Host 对齐）。
func extractHost(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse %q: %w", rawURL, err)
	}
	h := u.Hostname()
	if h == "" {
		return "", fmt.Errorf("URL %q 无 host", rawURL)
	}
	return h, nil
}

// extractHostPort 从代理 URL 提取 host:port，net.Dial 用。
func extractHostPort(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse %q: %w", rawURL, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("URL %q 无 host:port", rawURL)
	}
	return u.Host, nil
}

// loadRawSamples 读 raw HTTP 报文样本数组（每条是一份完整的 HTTP/1.1 报文字符串）。
func loadRawSamples(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s 空数组", path)
	}
	return out, nil
}

// dispatchRaw 把 raw HTTP/1.1 报文通过 proxy 转发到目标。
// 写出前注入 Connection: close 头（若用户 sample 中没有），让上游响应完即关连接 →
// io.Copy 立即拿到 EOF，避免 keepalive 等 100s timeout 让 proxify 误标 502。
func dispatchRaw(proxyHostPort, rawRequest string) error {
	conn, err := net.DialTimeout("tcp", proxyHostPort, dialTimeout)
	if err != nil {
		return fmt.Errorf("dial proxy %s: %w", proxyHostPort, err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(rawIOTimeout))

	patched := ensureConnectionClose(rawRequest)
	if _, err := conn.Write([]byte(patched)); err != nil {
		return fmt.Errorf("write raw: %w", err)
	}
	if _, err := io.Copy(io.Discard, conn); err != nil && !isExpectedReadEnd(err) {
		return fmt.Errorf("read response: %w", err)
	}
	return nil
}

// ensureConnectionClose 在 raw HTTP/1.1 报文头末尾追加 Connection: close 头。
// 已含同名头（任何大小写）则原样返回。无 \r\n\r\n 分隔（畸形）也原样返回。
//
// 这是为绕过 HTTP/1.1 默认 keepalive：proxify 转发上游响应后保持连接，client
// 的 io.Copy 等不到 EOF → 走 100s deadline → proxify 把整个 transaction 标 502。
func ensureConnectionClose(raw string) string {
	const headEnd = "\r\n\r\n"
	idx := strings.Index(raw, headEnd)
	if idx < 0 {
		return raw
	}
	head := raw[:idx]
	for _, line := range strings.Split(head, "\r\n") {
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(line[:colon]), "connection") {
			return raw
		}
	}
	return head + "\r\nConnection: close" + raw[idx:]
}

// isExpectedReadEnd 把代理写完响应主动关连接的情形当正常。
func isExpectedReadEnd(err error) bool {
	if err == nil || errors.Is(err, io.EOF) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return strings.Contains(err.Error(), "use of closed network connection")
}

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

// saveCreds 录入 host 身份的 session cookie。host 必须与 proxy 看到的
// snapshot.Host 一致（去端口形式）；creds 由 profile 决定具体身份组。
func saveCreds(base, key, host string, creds []credentialEntry) error {
	payload := map[string]any{
		"ttl_seconds": 0,
		"credentials": map[string]any{
			host: creds,
		},
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

// filterByPrefix 过滤 kind 以 prefix 开头的 finding（profile 用）。
func filterByPrefix(all []vulnfinding.VulnFinding, prefix string) []vulnfinding.VulnFinding {
	out := make([]vulnfinding.VulnFinding, 0, len(all))
	for _, f := range all {
		if strings.HasPrefix(f.Kind, prefix) {
			out = append(out, f)
		}
	}
	return out
}

// countKinds 统计 finding 切片中各 kind 的出现次数。
func countKinds(fs []vulnfinding.VulnFinding) map[string]int {
	out := make(map[string]int, len(fs))
	for _, f := range fs {
		out[f.Kind]++
	}
	return out
}
