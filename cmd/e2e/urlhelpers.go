package main

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

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
