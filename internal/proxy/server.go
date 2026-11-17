package proxy

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/filter"
	"github.com/projectdiscovery/fastdialer/fastdialer"
	"github.com/projectdiscovery/martian/v3"
	"github.com/projectdiscovery/proxify"
	"github.com/projectdiscovery/proxify/pkg/certs"
	"github.com/projectdiscovery/proxify/pkg/logger/elastic"
	"github.com/projectdiscovery/proxify/pkg/logger/kafka"
	"github.com/projectdiscovery/proxify/pkg/types"
	"github.com/rs/zerolog"
	netproxy "golang.org/x/net/proxy"
)

// SnapshotPublisher 抽象快照发布行为，便于单元测试不依赖 Redis（生产实现 XADD）。
// Server 把 proxify SDK 当作进程内 MITM 代理，OnResponseCallback 触发 filter→snapshot→publisher（XADD）。
//
// 设计要点（业界最佳实践 - Stream-based）：
//   - 不输出 jsonl 文件，所有切片留在内存（OutputFile/OutputJsonl 关闭）。
//   - body 在回调中读完必须重建，否则下游客户端拿不到响应。
//   - 过滤通过后立即 XADD 到 Redis Stream；切窗 / 持久化 / 入队由消费者负责（解耦 + 无状态 proxy）。
type SnapshotPublisher interface {
	Publish(ctx context.Context, snap *TrafficSnapshot) error
}

// filterState 是 proxy_filter 组的运行期快照：过滤链 + 两个 body 上限一并原子替换。
// 三者同属 proxy_filter 分组 KV，热改时整体换入，避免链与上限撕裂（半旧半新）。
type filterState struct {
	filter               *filter.TrafficFilter
	maxRequestBodyBytes  int
	maxResponseBodyBytes int
}

// Server 是进程内 MITM 代理：过滤链 + 快照发布 + 双端口监听。
type Server struct {
	proxy       *proxify.Proxy
	dns         *systemDNS // 代理拨号器唯一的 DNS 服务器：把主机名交给系统解析
	stopOnce    sync.Once
	filterState atomic.Pointer[filterState] // proxy_filter 组热改：proxy 组件订阅失效后 SwapFilter 原子换入
	publisher   SnapshotPublisher
	listenAddr  string
	certDir     string
	source      string // 'external' / 'internal'，注入到每条 snapshot 用于 ingestor 分流
	logger      zerolog.Logger
}

// ServerDeps 注入服务依赖；ListenAddr（MITM 代理的监听地址）与 CertDir（根证书目录）必填。
// Source 必填——双 listener 部署时按 listener 区分 'external'（代理入口）/
// 'internal'（agent 工具入口）；ingestor 据此决定是否触发代理流量分析。
type ServerDeps struct {
	Filter     *filter.TrafficFilter
	Publisher  SnapshotPublisher
	Cfg        config.ProxyConfig
	ListenAddr string
	CertDir    string
	Source     string // 必填：'external' / 'internal'
	Logger     zerolog.Logger
	// Resolver 解析代理要连的主机名；nil = 系统解析（net.DefaultResolver）。
	Resolver IPResolver
}

// NewServer 构造内嵌 MITM 代理；首次启动会在 CertDir 下生成 CA。
func NewServer(deps ServerDeps) (*Server, error) {
	if deps.Filter == nil {
		return nil, errors.New("proxy.NewServer: Filter 必填")
	}
	if deps.Publisher == nil {
		return nil, errors.New("proxy.NewServer: Publisher 必填")
	}

	listenAddr := strings.TrimSpace(deps.ListenAddr)
	certDir := strings.TrimSpace(deps.CertDir)
	if listenAddr == "" || certDir == "" {
		return nil, errors.New("proxy.NewServer: ListenAddr 与 CertDir 必填")
	}
	if err := os.MkdirAll(certDir, 0o750); err != nil {
		return nil, fmt.Errorf("创建证书目录 %s 失败: %w", certDir, err)
	}
	// 首次启动会自动生成 CA；已存在则就地加载
	if err := certs.LoadCerts(certDir); err != nil {
		return nil, fmt.Errorf("加载/生成 CA 证书失败: %w", err)
	}

	source := strings.TrimSpace(deps.Source)
	if source != "external" && source != "internal" {
		return nil, fmt.Errorf("proxy.NewServer: Source 必填且必须是 'external' 或 'internal'（实际值 %q）", deps.Source)
	}

	srv := &Server{
		publisher:  deps.Publisher,
		listenAddr: listenAddr,
		certDir:    certDir,
		source:     source,
		logger:     deps.Logger,
	}
	srv.filterState.Store(&filterState{
		filter:               deps.Filter,
		maxRequestBodyBytes:  deps.Cfg.MaxRequestBodyBytes,
		maxResponseBodyBytes: deps.Cfg.MaxResponseBodyBytes,
	})

	opts := &proxify.Options{
		ListenAddrHTTP:     listenAddr,
		Directory:          certDir,
		OutputFile:         "", // 关闭 jsonl 文件输出（彻底 in-process）
		OutputJsonl:        false,
		DumpRequest:        false,
		DumpResponse:       false,
		Verbosity:          types.VerbositySilent,
		OnRequestCallback:  onRequest,
		OnResponseCallback: srv.onResponse,
		// 必填空指针：proxify NewLogger 内部直接读 .Addr，nil 会 panic
		Elastic: &elastic.Options{},
		Kafka:   &kafka.Options{},
	}

	p, err := proxify.NewProxy(opts)
	if err != nil {
		return nil, fmt.Errorf("创建 proxify 实例失败: %w", err)
	}
	resolver := deps.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	sysDNS, err := startSystemDNS(resolver)
	if err != nil {
		p.Dialer.Close()
		return nil, err
	}
	dialer, err := systemResolvingDialer(sysDNS.addr)
	if err != nil {
		p.Dialer.Close()
		_ = sysDNS.Close()
		return nil, err
	}
	// proxify 的拨号器缺省查公共 DNS（见 systemDNS）；换成只查回环 DNS 的拨号器。
	// HTTP 请求与 HTTPS 隧道都在拨号时读取这个字段。
	p.Dialer.Close()
	p.Dialer = dialer
	srv.proxy = p
	srv.dns = sysDNS

	return srv, nil
}

// onRequest 是 proxify 的请求回调。设置它，proxify 就不再把请求交给自带的日志协程：那个
// 协程对每个请求做带请求体的转储（读走并替换 req.Body），与转发上游并发进行，是数据竞争，
// 会让 POST 的请求体残缺地到达目标。回调里保留 proxify 缺省路径对快照必需的两件事：会话
// 标识（响应回调据此关联请求），以及从 Accept-Encoding 去掉 br（快照只解 gzip / deflate）。
func onRequest(req *http.Request, ctx *martian.Context) error {
	ctx.Set("user-data", types.UserData{ID: ctx.ID(), Host: req.Host})
	if ae := req.Header.Get("Accept-Encoding"); ae != "" {
		var kept []string
		for _, enc := range strings.Split(ae, ",") {
			if enc = strings.TrimSpace(enc); enc != "" && !strings.EqualFold(enc, "br") {
				kept = append(kept, enc)
			}
		}
		req.Header.Set("Accept-Encoding", strings.Join(kept, ", "))
	}
	return nil
}

// onResponse 是 proxify 的响应回调：
//  1. 走 TrafficFilter；不通过即丢弃（return nil 不影响转发）。
//  2. 读取 req/resp body 并重建（必须，否则代理会断），构造 TrafficSnapshot。
//  3. XADD 到 Redis Stream（TrafficStream）；publish 失败仅 warn 不阻断转发。
func (s *Server) onResponse(resp *http.Response, _ *martian.Context) error {
	if resp == nil || resp.Request == nil {
		return nil
	}

	req := resp.Request

	// 取一次运行期快照：本条流量全程用同一份 filter + body 上限（热改在下条流量生效，不半旧半新）。
	fs := s.filterState.Load()

	// 1) 过滤
	if ok, reason := fs.filter.ShouldProcess(req, resp); !ok {
		s.logger.Debug().
			Str("method", req.Method).
			Str("host", req.URL.Host).
			Str("path", req.URL.Path).
			Int("status", resp.StatusCode).
			Str("reason", reason).
			Msg("流量被过滤")
		return nil
	}

	// 2) 采集 body 副本（请求 / 响应都要）。采集只取前 N 字节供快照，
	//    转发流保持完整原始字节——代理不得改动客户端看到的任何内容。
	reqBody, err := captureBody(&req.Body, fs.maxRequestBodyBytes)
	if err != nil {
		s.logger.Warn().Err(err).Msg("读取请求体失败，跳过该流量")
		return nil
	}
	respBody, err := captureBody(&resp.Body, fs.maxResponseBodyBytes)
	if err != nil {
		s.logger.Warn().Err(err).Msg("读取响应体失败，跳过该流量")
		return nil
	}

	// 2.5) 快照侧按 Content-Encoding 解压——避免 gzip 二进制流（含 \x00）被原样塞进
	//      user prompt 后写 jsonb 时被 PG 拒收。只改快照的头部副本，转发响应原样不动。
	snapHeader := resp.Header.Clone()
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" {
		if decoded, derr := decompressIfEncoded(respBody, encoding, decompressLimit(fs.maxResponseBodyBytes)); derr != nil {
			s.logger.Warn().Err(derr).Str("encoding", encoding).
				Msg("解压响应失败，快照保留原始字节（下游可能含二进制乱码）")
		} else {
			respBody = decoded
			snapHeader.Del("Content-Encoding") // 快照正文已是明文
			snapHeader.Del("Content-Length")   // 长度已变
		}
	}

	// 3) 构造 snapshot 并注入 source。
	// onResponse 仅处理 external（代理）流量，无 agent 概念；沙盒工具流量不经本代理
	// （CLI 直连目标；chromium 经浏览器服务 CDP capture → ingest 单独入字典，source=internal）。
	snap := buildSnapshot(req, resp.StatusCode, snapHeader, reqBody, respBody)
	snap.Source = s.source

	if err := s.publisher.Publish(req.Context(), snap); err != nil {
		s.logger.Warn().Err(err).
			Str("method", snap.Method).Str("host", snap.Host).Str("path", LogPath(snap.URI)).
			Msg("Publisher.Publish 失败（流量已丢弃）")
		return nil
	}
	s.logger.Debug().
		Str("method", snap.Method).Str("host", snap.Host).Str("path", LogPath(snap.URI)).
		Int("status", snap.StatusCode).Msg("traffic 已投递到 stream")
	return nil
}

// Run 启动 proxify（阻塞 HTTP listener），ctx 取消时 stop。
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.proxy.Run()
	}()

	s.logger.Info().Str("addr", s.listenAddr).Str("cert_dir", s.certDir).Msg("MITM 代理已启动")

	select {
	case <-ctx.Done():
		s.Stop()
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

// Stop 停止 proxify。可重复调用。
//
// 注意：proxify v0.0.16 的 Stop() 是 no-op，listener 没有显式关闭点；
// 因此调用方应在进程退出时依赖 listener 随进程关闭。
// Publisher 是无状态的，无需关闭（rdb 由调用方关）。
func (s *Server) Stop() {
	s.stopOnce.Do(func() {
		if s.proxy != nil {
			s.proxy.Stop()
			s.proxy.Dialer.Close()
		}
		if s.dns != nil {
			_ = s.dns.Close()
		}
	})
}

// systemResolvingDialer 构造只向 dnsAddr 查询的拨号器：不读系统 resolv.conf 与 hosts 文件
// （系统解析已包含它们），不配公共 DNS；回环 DNS 出错时直接回退到系统解析。
//
// 连接交给标准库拨号器直连解析结果中的首个地址（IPv4 在前）。不用这个拨号器自带的连接方式：
// 它把每个「主机:端口」第一次连接的结果记在不过期的缓存里，第一次连不上（目标还没起、正在
// 重启）之后，到这个目标的请求全部直接失败，直到代理重启；设置了 ProxyDialer 就不走那个缓存。
func systemResolvingDialer(dnsAddr string) (*fastdialer.Dialer, error) {
	opts := fastdialer.DefaultOptions
	opts.BaseResolvers = []string{dnsAddr}
	opts.ResolversFile = false
	opts.HostsFile = false
	opts.EnableFallback = true
	opts.CacheType = fastdialer.Memory
	var direct netproxy.Dialer = &net.Dialer{Timeout: opts.DialerTimeout, KeepAlive: opts.DialerKeepAlive}
	opts.ProxyDialer = &direct
	d, err := fastdialer.NewDialer(opts)
	if err != nil {
		return nil, fmt.Errorf("创建代理拨号器: %w", err)
	}
	return d, nil
}

// SwapFilter 原子换入新的过滤链 + body 上限（proxy_filter 组热改）。
// proxy 组件订阅 settingstore proxy_filter 失效后，用最新 ProxyFilterSettings 重建 filter 调用本方法；
// 换入后下一条流量即走新规则，进行中的流量仍用其加载时的快照，无锁无撕裂。
func (s *Server) SwapFilter(f *filter.TrafficFilter, maxReqBody, maxRespBody int) {
	s.filterState.Store(&filterState{
		filter:               f,
		maxRequestBodyBytes:  maxReqBody,
		maxResponseBodyBytes: maxRespBody,
	})
}

// decompressIfEncoded 按 Content-Encoding 解压响应 body。
//
// 支持 gzip / deflate（标准库）。br（Brotli）需要第三方库（andybalholm/brotli），
// 暂不支持——遇到时返回原样字节，调用方决定是否容忍。
//
// 设计动机：HTTP 服务端常按 Accept-Encoding 协商压缩响应（DVWA 等典型场景），
// 压缩字节流含  /  等 binary，被原样塞进 user prompt 后写入 PG jsonb
// 列会被拒收（PG 不允许 jsonb 字符串含  ）。在最早的 proxy 层解压一次，
// 下游 traffic / runner / LLM 都用解压后的明文。
//
// 失败时返回原 body + error（让 caller 决定降级策略）。
// limit 是解压输出的硬上限（字节）：压缩体的采集上限挡不住高膨胀率的解压炸弹，
// 解压侧必须独立钳制，超出部分截断丢弃。
func decompressIfEncoded(body []byte, encoding string, limit int64) ([]byte, error) {
	encoding = strings.ToLower(strings.TrimSpace(encoding))
	if encoding == "" || encoding == "identity" {
		return body, nil
	}
	var reader io.Reader
	switch encoding {
	case "gzip":
		gz, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return body, fmt.Errorf("gzip.NewReader: %w", err)
		}
		defer func() { _ = gz.Close() }()
		reader = gz
	case "deflate":
		fr := flate.NewReader(bytes.NewReader(body))
		defer func() { _ = fr.Close() }()
		reader = fr
	default:
		// br / zstd 等暂不支持——保留原始字节，下游若含二进制需自处理。
		return body, nil
	}
	decoded, err := io.ReadAll(io.LimitReader(reader, limit))
	if err != nil {
		return body, fmt.Errorf("read decompressed (%s): %w", encoding, err)
	}
	return decoded, nil
}

// defaultDecompressLimit 是未配置响应采集上限时的解压输出兜底上限。
const defaultDecompressLimit = 32 << 20

// decompressLimit 让解压输出上限与响应采集上限同口径：快照正文无论压缩与否都不超过它。
func decompressLimit(maxResponseBodyBytes int) int64 {
	if maxResponseBodyBytes > 0 {
		return int64(maxResponseBodyBytes)
	}
	return defaultDecompressLimit
}

// captureBody 读取 body 的前 maxSize 字节作为快照副本，并把转发流重建为
// 「已读前缀 + 未读剩余」的完整原始字节流（maxSize <= 0 表示整体读取）。
// 返回的 []byte 是独立副本，转发方后续读取不受影响。
func captureBody(bodyPtr *io.ReadCloser, maxSize int) ([]byte, error) {
	if bodyPtr == nil || *bodyPtr == nil {
		return nil, nil
	}
	src := *bodyPtr
	var reader io.Reader = src
	if maxSize > 0 {
		reader = io.LimitReader(src, int64(maxSize))
	}
	head, err := io.ReadAll(reader)
	if err != nil {
		_ = src.Close()
		return nil, err
	}
	*bodyPtr = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head), src), src}
	return append([]byte(nil), head...), nil
}

// buildSnapshot 把 net/http 请求 + 响应状态/头（快照侧副本）+ 已采集 body 拼成 TrafficSnapshot。
func buildSnapshot(req *http.Request, statusCode int, respHeader http.Header, reqBody, respBody []byte) *TrafficSnapshot {
	hostPort := req.URL.Host
	if hostPort == "" {
		hostPort = req.Host
	}
	if hostPort == "" {
		hostPort = req.Header.Get("Host")
	}
	// snapshot.Host 全链路含端口：凭证库键与漏洞主机据此区分同主机的不同端口。
	// 过滤链的主机匹配由 HostFilter.ShouldProcess 自行去端口。
	host := hostPort

	scheme := strings.ToLower(req.URL.Scheme)
	if scheme == "" {
		if req.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}

	uri := req.URL.RequestURI()
	if uri == "" {
		uri = req.URL.Path
	}

	// 拆解：path 用于 sniffer 路由判断；query 多值 map 用于 sniffer 结构化操作。
	// req.URL.Query() 返回 url.Values（即 map[string][]string）；空 query 时不写入字段（json omitempty）。
	var query map[string][]string
	if q := req.URL.Query(); len(q) > 0 {
		query = q
	}

	return &TrafficSnapshot{
		ID:              generateSnapshotID(req.Method, host, uri, reqBody),
		Host:            host,
		HostPort:        hostPort,
		Method:          req.Method,
		Scheme:          scheme,
		HTTPVersion:     req.Proto,
		URI:             uri,
		Path:            req.URL.Path,
		Query:           query,
		StatusCode:      statusCode,
		RequestHeaders:  flattenRequestHeaders(req.Header),
		ResponseHeaders: flattenResponseHeaders(respHeader),
		RequestBody:     reqBody,
		ResponseBody:    respBody,
		Timestamp:       time.Now().UTC(),
	}
}

// flattenRequestHeaders 把请求头拍平成 map[string]string，多值用 "," join。
// HTTP/1.1 请求头无多 header 行场景（Cookie/Accept 等内部已用 ; 或 , 分隔），
// 用 string 简化下游使用（sniffer 不需要类型断言）。key 全部小写化便于匹配。
func flattenRequestHeaders(h http.Header) map[string]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, vs := range h {
		key := strings.ToLower(k)
		if len(vs) == 1 {
			out[key] = vs[0]
			continue
		}
		out[key] = strings.Join(vs, ",")
	}
	return out
}

// flattenResponseHeaders 保留响应头多值切片，与 http.Header 原 shape 对齐。
//
// 关键原因：Set-Cookie 按 RFC 6265 必须独立多行，且 cookie value 内允许逗号 —— ", " join
// 会让 "a=1; Path=/, b=2; Path=/" 形态的拼接破坏后续 cookie 解析。这里深拷贝切片，
// 避免下游意外修改影响 http.Header 内部状态。key 全部小写化便于匹配。
func flattenResponseHeaders(h http.Header) map[string][]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string][]string, len(h))
	for k, vs := range h {
		key := strings.ToLower(k)
		cp := make([]string, len(vs))
		copy(cp, vs)
		out[key] = cp
	}
	return out
}

// generateSnapshotID = sha256(method | host | uri | body) 的十六进制摘要，
// 便于跨进程识别同一条流量。
func generateSnapshotID(method, host, uri string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte("|"))
	h.Write([]byte(host))
	h.Write([]byte("|"))
	h.Write([]byte(uri))
	h.Write([]byte("|"))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}
