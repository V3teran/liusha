package proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/filter"
	"github.com/projectdiscovery/martian/v3"
	"github.com/projectdiscovery/proxify"
	"github.com/projectdiscovery/proxify/pkg/certs"
	"github.com/projectdiscovery/proxify/pkg/logger/elastic"
	"github.com/projectdiscovery/proxify/pkg/logger/kafka"
	"github.com/projectdiscovery/proxify/pkg/types"
	"github.com/rs/zerolog"
)

// 默认值（避免 magic numbers 散落各处）。
const (
	defaultListenAddr = "0.0.0.0:8888"
	defaultCertSubdir = ".liusha"
)

// Server 把 proxify SDK 当作进程内 MITM 代理，OnResponseCallback 触发 filter→snapshot→aggregator。
//
// 设计要点：
//   - 不输出 jsonl 文件，所有切片留在内存（OutputFile/OutputJsonl 关闭）。
//   - body 在回调中读完必须重建，否则下游客户端拿不到响应。
//   - filter / aggregator 通过 deps 注入，便于单元测试不起 proxify。
type Server struct {
	proxy      *proxify.Proxy
	filter     *filter.TrafficFilter
	aggregator *Aggregator
	cfg        config.ProxyConfig
	listenAddr string
	certDir    string
	logger     zerolog.Logger
}

// ServerDeps 注入服务依赖；ListenAddr / CertDir 为空时走默认值。
type ServerDeps struct {
	Filter     *filter.TrafficFilter
	Aggregator *Aggregator
	Cfg        config.ProxyConfig
	ListenAddr string
	CertDir    string
	Logger     zerolog.Logger
}

// NewServer 构造内嵌 MITM 代理；首次启动会在 CertDir 下生成 CA。
func NewServer(deps ServerDeps) (*Server, error) {
	if deps.Filter == nil {
		return nil, errors.New("proxy.NewServer: Filter 必填")
	}
	if deps.Aggregator == nil {
		return nil, errors.New("proxy.NewServer: Aggregator 必填")
	}

	listenAddr := strings.TrimSpace(deps.ListenAddr)
	if listenAddr == "" {
		listenAddr = defaultListenAddr
	}

	certDir, err := resolveCertDir(deps.CertDir)
	if err != nil {
		return nil, fmt.Errorf("解析证书目录失败: %w", err)
	}
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建证书目录 %s 失败: %w", certDir, err)
	}
	// 首次启动会自动生成 CA；已存在则就地加载
	if err := certs.LoadCerts(certDir); err != nil {
		return nil, fmt.Errorf("加载/生成 CA 证书失败: %w", err)
	}

	srv := &Server{
		filter:     deps.Filter,
		aggregator: deps.Aggregator,
		cfg:        deps.Cfg,
		listenAddr: listenAddr,
		certDir:    certDir,
		logger:     deps.Logger,
	}

	opts := &proxify.Options{
		ListenAddrHTTP:     listenAddr,
		Directory:          certDir,
		OutputFile:         "", // 关闭 jsonl 文件输出（彻底 in-process）
		OutputJsonl:        false,
		DumpRequest:        false,
		DumpResponse:       false,
		Verbosity:          types.VerbositySilent,
		OnResponseCallback: srv.onResponse,
		// 必填空指针：proxify NewLogger 内部直接读 .Addr，nil 会 panic
		Elastic: &elastic.Options{},
		Kafka:   &kafka.Options{},
	}

	p, err := proxify.NewProxy(opts)
	if err != nil {
		return nil, fmt.Errorf("创建 proxify 实例失败: %w", err)
	}
	srv.proxy = p

	return srv, nil
}

// onResponse 是 proxify 的响应回调：
//  1. 走 TrafficFilter；不通过即丢弃（return nil 不影响转发）。
//  2. 读取 req/resp body 并重建（必须，否则代理会断），构造 TrafficSnapshot。
//  3. 投递到 Aggregator；错误吞掉（聚合层不感知存储语义）。
func (s *Server) onResponse(resp *http.Response, _ *martian.Context) error {
	if resp == nil || resp.Request == nil {
		return nil
	}

	req := resp.Request

	// 1) 过滤
	if ok, reason := s.filter.ShouldProcess(req, resp); !ok {
		s.logger.Debug().
			Str("method", req.Method).
			Str("host", req.URL.Host).
			Str("path", req.URL.Path).
			Int("status", resp.StatusCode).
			Str("reason", reason).
			Msg("流量被过滤")
		return nil
	}

	// 2) 读+重建 body（请求 / 响应都要）
	reqBody, err := readAndRebuildBody(&req.Body, s.cfg.MaxRequestBodySize)
	if err != nil {
		s.logger.Warn().Err(err).Msg("读取请求体失败，跳过该流量")
		return nil
	}
	respBody, err := readAndRebuildBody(&resp.Body, s.cfg.MaxResponseBodySize)
	if err != nil {
		s.logger.Warn().Err(err).Msg("读取响应体失败，跳过该流量")
		return nil
	}

	// 3) 构造 snapshot
	snap := buildSnapshot(req, resp, reqBody, respBody)
	s.aggregator.Add(snap)
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

// Stop 停止 proxify 并 flush aggregator。可重复调用。
//
// 注意：proxify v0.0.16 的 Stop() 是 no-op，listener 没有显式关闭点；
// 因此调用方应在进程退出时依赖 listener 随进程关闭。
func (s *Server) Stop() {
	if s.proxy != nil {
		s.proxy.Stop()
	}
	if s.aggregator != nil {
		s.aggregator.Stop()
	}
}

// resolveCertDir 把 ~ 展开成 $HOME/.liusha；空字符串走默认。
func resolveCertDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, defaultCertSubdir), nil
	}
	if strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, dir[2:]), nil
	}
	return dir, nil
}

// readAndRebuildBody 读完 body 并重建 io.NopCloser，保证下游可继续读取。
//
//	maxSize > 0 时使用 LimitReader 截断，超过部分丢弃；
//	maxSize <= 0 表示不限。
//	返回的 []byte 是新切片，安全独立持有。
func readAndRebuildBody(bodyPtr *io.ReadCloser, maxSize int) ([]byte, error) {
	if bodyPtr == nil || *bodyPtr == nil {
		return nil, nil
	}
	src := *bodyPtr
	defer src.Close()

	var reader io.Reader = src
	if maxSize > 0 {
		reader = io.LimitReader(src, int64(maxSize))
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	*bodyPtr = io.NopCloser(bytes.NewReader(data))
	return data, nil
}

// buildSnapshot 把 net/http 请求 + 响应 + 已读 body 拼成 TrafficSnapshot。
func buildSnapshot(req *http.Request, resp *http.Response, reqBody, respBody []byte) *TrafficSnapshot {
	host := req.URL.Host
	if host == "" {
		host = req.Host
	}
	if host == "" {
		host = req.Header.Get("Host")
	}
	host = stripPort(host)

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

	return &TrafficSnapshot{
		ID:              generateSnapshotID(req.Method, host, uri, reqBody),
		Host:            host,
		Method:          req.Method,
		Scheme:          scheme,
		URI:             uri,
		StatusCode:      resp.StatusCode,
		RequestHeaders:  flattenHeaders(req.Header),
		ResponseHeaders: flattenHeaders(resp.Header),
		RequestBody:     reqBody,
		ResponseBody:    respBody,
		Timestamp:       time.Now().UTC(),
	}
}

// flattenHeaders 把 http.Header（map[string][]string）拍成 map[string]string，多值用 "," 拼。
// key 全部小写化，便于后续匹配（与 TrafficSnapshot 文档一致）。
func flattenHeaders(h http.Header) map[string]string {
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

// stripPort 去掉 host 末尾的端口（IPv6 暂只处理 ":port" 形式，liusha 流量场景足够）。
func stripPort(host string) string {
	if host == "" {
		return host
	}
	// IPv6 地址形如 [::1]:8080
	if strings.HasPrefix(host, "[") {
		if idx := strings.LastIndex(host, "]"); idx > 0 {
			return host[:idx+1]
		}
	}
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		// 排除 scheme 中的 ":"
		if !strings.ContainsRune(host[idx+1:], ':') {
			return host[:idx]
		}
	}
	return host
}

// generateSnapshotID = sha256(method | host | uri | body) 的十六进制；与 TrafficDeduplicator.CalculateHash 同算法，
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
