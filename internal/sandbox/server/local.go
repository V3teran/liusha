package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// maxIngestBody 是单条回灌流量的上限（含 base64 的请求 / 响应体）。
const maxIngestBody = 32 << 20

// ingestTimeout 是转发一条流量给 runner 的超时。
const ingestTimeout = 10 * time.Second

// localAPI 是沙盒给本机任务进程用的接口：只监听回环地址，凭本机口令访问。
//
//	POST   /browsers             确保某个浏览器身份的服务在运行（由沙盒启动，归属任务）
//	DELETE /browsers/{identity}  强制结束某个浏览器身份的服务（它已无响应时）
//	POST   /ingest               浏览器服务回灌抓到的流量，沙盒带令牌转发给 runner
//
// 地址与口令随命令环境下发（LIUSHA_SANDBOX_LOCAL_URL / LIUSHA_SANDBOX_LOCAL_KEY），每次启动
// 重新生成。沙盒令牌只在沙盒进程内；本机口令挡的是本机其他用户（看不到别人进程的环境）——
// 同用户的任务进程本就能操作同一沙盒的一切。回环 TCP 而不是 unix 套接字：全平台同一机制
// （Windows 上的 Python 没有 unix 套接字）。
type localAPI struct {
	url string // http://127.0.0.1:<端口>；ListenAndServe 启动后才有
	key string
}

// newLocalKey 生成本机口令。
func newLocalKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("生成本机口令: %v", err)) // 系统随机源不可用，无从安全运行
	}
	return hex.EncodeToString(b)
}

// listenLocal 在回环地址的随机端口监听本机接口。
func listenLocal() (net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("监听本机接口: %w", err)
	}
	return ln, nil
}

// localHandler 是本机接口的处理器。
func (s *Server) localHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /browsers", s.handleBrowserStart)
	mux.HandleFunc("DELETE /browsers/{identity}", s.handleBrowserKill)
	mux.Handle("POST /ingest", s.ingestHandler())
	want := []byte(s.local.key)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			writeError(w, http.StatusUnauthorized, "本机口令无效")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// browserRequest 是 POST /browsers 的请求体。
type browserRequest struct {
	AssignmentID string `json:"assignment_id"`
	TaskID       string `json:"task_id"`
	Identity     string `json:"identity"`
}

func (s *Server) handleBrowserStart(w http.ResponseWriter, r *http.Request) {
	var req browserRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "decode request: %v", err)
		return
	}
	if !isPathSafe(req.AssignmentID) || !isPathSafe(req.TaskID) || !ownsIdentity(req.TaskID, req.Identity) {
		writeError(w, http.StatusBadRequest, "assignment_id / task_id / identity 不合法")
		return
	}
	if err := s.browsers.ensure(s, req); err != nil {
		writeError(w, http.StatusInternalServerError, "启动浏览器服务: %v", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleBrowserKill(w http.ResponseWriter, r *http.Request) {
	identity := r.PathValue("identity")
	taskID := r.URL.Query().Get("task_id")
	if !isPathSafe(taskID) || !ownsIdentity(taskID, identity) {
		writeError(w, http.StatusBadRequest, "task_id / identity 不合法")
		return
	}
	s.browsers.kill(s, taskID, identity)
	w.WriteHeader(http.StatusNoContent)
}

// ownsIdentity 报告浏览器身份是否属于任务：身份形如 <任务ID>-<身份名>，整体路径安全（它是
// 端点文件名，也是释放时按任务匹配的前缀）。
func ownsIdentity(taskID, identity string) bool {
	return isPathSafe(identity) && strings.HasPrefix(identity, taskID+"-") && len(identity) > len(taskID)+1
}

// ingestHandler 把浏览器服务回灌的流量带沙盒令牌转发给 runner，回传 runner 的状态码；未配置
// 回灌地址时丢弃（204）。
func (s *Server) ingestHandler() http.Handler {
	client := &http.Client{Timeout: ingestTimeout}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.IngestURL == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.cfg.IngestURL,
			http.MaxBytesReader(w, r.Body, maxIngestBody))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "构造转发请求: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+s.cfg.Token)
		resp, err := client.Do(req)
		if err != nil {
			writeError(w, http.StatusBadGateway, "转发给 runner 失败: %v", err)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, io.LimitReader(resp.Body, 4096))
	})
}
