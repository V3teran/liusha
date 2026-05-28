// sanitizer.go — passive proxy 的 raw-bytes URI 改写 forwarder。
//
// v34+：删除 agent (internal) sanitizer 全部路径 — chromium 流量改走 CDP capture →
// ingest endpoint；CLI 工具直连不入字典。本文件只剩 passive 入口的 URI patch。
package proxy

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
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

// RunPassiveSanitizer 监听 publicAddr（external 8888，passive 入口）。
// 仅做 URI patch — 把首段 raw bytes 的 relative URI 改写成 absolute form 再
// forward 给 upstreamAddr（proxify loopback 端口）。
//
// v34+：本仓库当前唯一的 sanitizer — agent 路径已删除（chromium 走 CDP capture，
// CLI 工具直连不入字典）。
func RunPassiveSanitizer(publicAddr, upstreamAddr string) error {
	ln, err := net.Listen("tcp", publicAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", publicAddr, err)
	}
	for {
		client, err := ln.Accept()
		if err != nil {
			return fmt.Errorf("accept: %w", err)
		}
		go handleConn(client, upstreamAddr)
	}
}

func handleConn(client net.Conn, upstreamAddr string) {
	defer func() { _ = client.Close() }()

	br := bufio.NewReader(client)
	head, err := readHTTPHead(br)
	if err != nil {
		// 首段读失败：直接关，避免半截透传给 upstream 触发 proxify 端怪异错误。
		return
	}

	upstream, err := net.Dial("tcp", upstreamAddr)
	if err != nil {
		return
	}
	defer func() { _ = upstream.Close() }()

	patched := patchAbsoluteURI(head)
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
