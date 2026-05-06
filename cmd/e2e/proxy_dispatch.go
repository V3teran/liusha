package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

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
