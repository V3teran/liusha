package proxy

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
)

// patchAbsoluteURI 把第一行从 relative form 改成 absolute form：
//
//	GET /path HTTP/1.1\r\nHost: vulnapp\r\n\r\n  →
//	GET http://vulnapp/path HTTP/1.1\r\nHost: vulnapp\r\n\r\n
//
// 不改其它字节。已是 absolute-form / CONNECT / 缺 Host header 的报文原样返回。
//
// 这是 proxify (martian) 兼容 relative-URI 客户端的核心：proxify 默认要求 explicit
// proxy 协议（absolute URI），客户端发 relative + Host header 时 martian 会误判
// 为 TLS handshake 失败回 502。
func patchAbsoluteURI(head []byte) []byte {
	eol := bytes.Index(head, []byte("\r\n"))
	if eol < 0 {
		return head
	}
	line := head[:eol]
	sp1 := bytes.IndexByte(line, ' ')
	if sp1 < 0 {
		return head
	}
	sp2 := bytes.LastIndexByte(line, ' ')
	if sp2 <= sp1 {
		return head
	}
	method := line[:sp1]
	target := line[sp1+1 : sp2]
	httpVer := line[sp2+1:]

	// CONNECT host:port HTTP/1.1 直接透传给 proxify 处理 TLS MITM。
	if bytes.Equal(method, []byte("CONNECT")) {
		return head
	}
	// 已是 absolute form。
	if bytes.HasPrefix(target, []byte("http://")) || bytes.HasPrefix(target, []byte("https://")) {
		return head
	}

	host := extractHostHeader(head[eol+2:])
	if host == "" {
		// 无 Host header 时不强行 patch；让 proxify 自己拒，便于排查。
		return head
	}

	patched := make([]byte, 0, len(head)+len(host)+8)
	patched = append(patched, method...)
	patched = append(patched, ' ')
	patched = append(patched, "http://"...)
	patched = append(patched, host...)
	patched = append(patched, target...)
	patched = append(patched, ' ')
	patched = append(patched, httpVer...)
	patched = append(patched, head[eol:]...)
	return patched
}

// rewriteProxyAuthToOwnerHeader 把 raw HTTP head 中的 Proxy-Authorization basic auth
// 解析成 owner_id + 替换为 X-Liusha-Owner-Id 自定义 header（0061 简化：删 hunter_id）。
//
// 背景（关键 hack）：proxify/martian 在 OnResponseCallback 之前 strip 所有 hop-by-hop
// header（含 Proxy-Authorization）→ server.go onResponse 拿不到，owner_id 关联失败。
// sanitizer 在 patch absolute URI 同一层做 header 转换，把 hop-by-hop 凭证转成普通
// 自定义 header（proxify 不 strip）→ onResponse 可读。
//
// 行为：
//   - 找 `Proxy-Authorization: Basic base64(owner_<uuid>:_)`（case-insensitive）
//   - 解 base64 → 抽 owner_<uuid> 前缀 → 替换该 header line 为 `X-Liusha-Owner-Id: <uuid>`
//   - 找不到 / 格式错 → 删 Proxy-Authorization line（不暴露给上游 server）
//
// 不破坏其它 header 顺序与字节内容；仅替换匹配行。
func rewriteProxyAuthToOwnerHeader(head []byte) []byte {
	eol := bytes.Index(head, []byte("\r\n"))
	if eol < 0 {
		return head
	}
	// 跳过 request-line（patch 阶段已处理）
	body := head[eol+2:]

	for off := 0; off < len(body); {
		lineEnd := bytes.Index(body[off:], []byte("\r\n"))
		if lineEnd < 0 {
			break
		}
		line := body[off : off+lineEnd]
		// header 段终止于空行
		if len(line) == 0 {
			break
		}
		const prefix = "proxy-authorization:"
		if len(line) > len(prefix) && strings.EqualFold(string(line[:len(prefix)]), prefix) {
			value := strings.TrimSpace(string(line[len(prefix):]))
			if ownerID := extractOwnerIDFromBasicAuth(value); ownerID != "" {
				// 拼新 line：X-Liusha-Owner-Id: <uuid>
				newLine := append([]byte("X-Liusha-Owner-Id: "), []byte(ownerID)...)
				// 长度对齐：新旧拼出新 head
				out := make([]byte, 0, len(head)-len(line)+len(newLine))
				out = append(out, head[:eol+2+off]...)
				out = append(out, newLine...)
				out = append(out, head[eol+2+off+lineEnd:]...)
				return out
			}
			// 解析失败：仍然删掉 Proxy-Authorization line（不让它继续暴露给上游 server）
			out := make([]byte, 0, len(head)-len(line)-2)
			out = append(out, head[:eol+2+off]...)
			out = append(out, head[eol+2+off+lineEnd+2:]...)
			return out
		}
		off += lineEnd + 2
	}
	return head
}

// extractOwnerIDFromBasicAuth 解 "Basic base64(owner_<uuid>:_)" → "<uuid>"。
// 失败返空（caller 决定 fallback）。
func extractOwnerIDFromBasicAuth(value string) string {
	const scheme = "Basic "
	const userPrefix = "owner_"
	if !strings.HasPrefix(value, scheme) {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value[len(scheme):]))
	if err != nil {
		return ""
	}
	colon := bytes.IndexByte(decoded, ':')
	if colon <= 0 {
		return ""
	}
	user := string(decoded[:colon])
	if !strings.HasPrefix(user, userPrefix) {
		return ""
	}
	return user[len(userPrefix):]
}

// extractOwnerIDFromHead 从 raw HTTP head 提 Proxy-Authorization → 解 owner_id。
// 找不到 header / 解析失败均返空。仅扫 header 段（请求体不动）。
func extractOwnerIDFromHead(head []byte) string {
	eol := bytes.Index(head, []byte("\r\n"))
	if eol < 0 {
		return ""
	}
	body := head[eol+2:]
	for off := 0; off < len(body); {
		lineEnd := bytes.Index(body[off:], []byte("\r\n"))
		if lineEnd < 0 {
			break
		}
		line := body[off : off+lineEnd]
		if len(line) == 0 {
			break // header 段终止
		}
		const prefix = "proxy-authorization:"
		if len(line) > len(prefix) && strings.EqualFold(string(line[:len(prefix)]), prefix) {
			value := strings.TrimSpace(string(line[len(prefix):]))
			return extractOwnerIDFromBasicAuth(value)
		}
		off += lineEnd + 2
	}
	return ""
}

// proxyAuthChallenge407 是返给 client 的 407 响应。
// chromium 收到 → 触发 CDP Fetch.authRequired → proxy_auth_inject.py 注入 user/pass
// → chromium 重发带 Proxy-Authorization → 这次 extractOwnerIDFromHead 能解出 owner_id。
//
// Connection: close 保证 chromium 不复用本 conn（auth flow 后建新 conn 带凭证），
// 避免老 conn 上后续请求仍走 noauth 路径。
const proxyAuthChallenge407 = "HTTP/1.1 407 Proxy Authentication Required\r\n" +
	"Proxy-Authenticate: Basic realm=\"liusha\"\r\n" +
	"Content-Length: 0\r\n" +
	"Connection: close\r\n" +
	"\r\n"

// extractHostHeader 在 raw header 段（不含 request-line）中找 Host header value。
// 找不到返空串。
func extractHostHeader(headers []byte) string {
	for len(headers) > 0 {
		eol := bytes.Index(headers, []byte("\r\n"))
		if eol <= 0 {
			return ""
		}
		line := headers[:eol]
		if len(line) >= 5 && bytes.EqualFold(line[:5], []byte("Host:")) {
			return string(bytes.TrimSpace(line[5:]))
		}
		headers = headers[eol+2:]
	}
	return ""
}

// readHTTPHead 读 raw HTTP 报文 head（直到 \r\n\r\n 包含）。
// 上限 64KB，超过判畸形返错。
func readHTTPHead(br *bufio.Reader) ([]byte, error) {
	var head []byte
	for {
		line, err := br.ReadSlice('\n')
		head = append(head, line...)
		if err != nil {
			return head, err
		}
		if len(line) == 2 && line[0] == '\r' {
			return head, nil
		}
		if len(head) > 64*1024 {
			return head, errors.New("HTTP head too large")
		}
	}
}

// RunSanitizingForwarder 监听 publicAddr，accept 后把首段 raw bytes 的 relative URI
// patch 成 absolute form 再 forward 给 upstreamAddr（proxify loopback 端口）。
// 双向 io.Copy 直到任一侧断开。阻塞直到 listener 出错；caller 通常在 goroutine 里跑。
//
// 这一层兼容生产环境客户端发 relative URI 的情况——proxify (martian) 默认只接
// absolute-form，前置改写后客户端无感知。
//
// requireProxyAuth=true（internal listener）：缺 Proxy-Authorization / 解不出 owner_id
// 时直接给 client 写 407 challenge，触发 chromium CDP Fetch.authRequired → inject 注入
// → 重发带 auth。external listener 走 false 保留无 auth 兼容（外部流量本就无凭证）。
func RunSanitizingForwarder(publicAddr, upstreamAddr string, requireProxyAuth bool) error {
	ln, err := net.Listen("tcp", publicAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", publicAddr, err)
	}
	for {
		client, err := ln.Accept()
		if err != nil {
			return fmt.Errorf("accept: %w", err)
		}
		go forwardWithRewrite(client, upstreamAddr, requireProxyAuth)
	}
}

func forwardWithRewrite(client net.Conn, upstreamAddr string, requireProxyAuth bool) {
	defer func() { _ = client.Close() }()

	br := bufio.NewReader(client)
	head, err := readHTTPHead(br)
	if err != nil {
		// 首段读失败：直接关，避免半截透传给 upstream 触发 proxify 端怪异错误。
		return
	}

	// internal listener 强制 require auth：缺/解不出 owner_id → 407 challenge。
	// chromium 收 407 → CDP Fetch.authRequired → proxy_auth_inject.py 注入 → 重发。
	// CLI 工具（curl/httpx）本就主动带 Proxy-Authorization → 这条路径不触发。
	if requireProxyAuth && extractOwnerIDFromHead(head) == "" {
		_, _ = client.Write([]byte(proxyAuthChallenge407))
		return
	}

	upstream, err := net.Dial("tcp", upstreamAddr)
	if err != nil {
		return
	}
	defer func() { _ = upstream.Close() }()

	patched := patchAbsoluteURI(head)
	// 把 Proxy-Authorization 转成 X-Liusha-Owner-Id（proxify 在 onResponse 之前会
	// strip hop-by-hop header；自定义 header 才能透传给 server.go 拿 owner_id）。
	patched = rewriteProxyAuthToOwnerHeader(patched)
	if _, err := upstream.Write(patched); err != nil {
		return
	}
	copyBoth(client, upstream, br)
}

// copyBoth 双向流式 copy；任一方向 EOF 后等另一方向也结束。
// clientReader 是包过 bufio 的客户端读端，已含 readHTTPHead 之后未消费的字节。
func copyBoth(client, upstream net.Conn, clientReader io.Reader) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(upstream, clientReader)
		if tcp, ok := upstream.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(client, upstream)
		if tcp, ok := client.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}()
	wg.Wait()
}
