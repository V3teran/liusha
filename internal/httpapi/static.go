package httpapi

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// 控制台前端的静态资源服务（单页应用）。
//
//   - /assets/ 下是带内容哈希的构建产物：一年 + immutable 长缓存；缺失即 404（不回落，
//     否则浏览器拿 HTML 当脚本执行）。
//   - 其余文件（index.html、图标）每次协商（no-cache + ETag），发版后立即生效。
//   - 不存在的其他路径回落 index.html，由前端路由处理（history 模式）。
//   - 可压缩类型按 Accept-Encoding 返回 gzip；压缩结果按文件缓存（嵌入资源不可变）。
//   - /api 下的路径一律不由这里处理：未注册的接口返回 JSON 404，不被前端页面遮挡。

// assetsDir 是构建产物中带内容哈希的资源目录（vite 默认 build.assetsDir）。
const assetsDir = "assets/"

// minGzipSize 以下的文件不压缩（压缩收益抵不过开销）。
const minGzipSize = 1024

// staticFile 是一个已载入内存的静态文件（嵌入资源只读，载入后不变）。
type staticFile struct {
	body        []byte
	gz          []byte // nil = 不压缩
	etag        string // 原文表示的 ETag
	gzipETag    string // gzip 表示的 ETag（两种表示字节不同，ETag 必须不同，否则缓存会串用）
	contentType string
}

// staticSite 从 fsys 提供单页应用的静态资源。
type staticSite struct {
	fsys  fs.FS
	files sync.Map // 文件路径 → *staticFile
}

func newStaticSite(fsys fs.FS) *staticSite { return &staticSite{fsys: fsys} }

// handle 是 gin 的 NoRoute 处理器：已注册路由之外的全部请求进入这里。
func (s *staticSite) handle(c *gin.Context) {
	p := c.Request.URL.Path
	if isAPIPath(p) {
		c.JSON(http.StatusNotFound, gin.H{"error": "接口不存在"})
		return
	}
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		c.Status(http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(path.Clean("/"+p), "/")
	if name == "" {
		name = "index.html"
	}
	f, err := s.load(name)
	if errors.Is(err, fs.ErrNotExist) && !strings.HasPrefix(name, assetsDir) {
		name = "index.html"
		f, err = s.load(name)
	}
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			c.Status(http.StatusNotFound)
			return
		}
		apiLog.Error().Err(err).Str("file", name).Msg("读取前端静态资源失败")
		c.Status(http.StatusInternalServerError)
		return
	}

	h := c.Writer.Header()
	if strings.HasPrefix(name, assetsDir) {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	h.Set("Content-Type", f.contentType)
	body, etag := f.body, f.etag
	if f.gz != nil {
		h.Add("Vary", "Accept-Encoding")
		if acceptsGzip(c.Request.Header.Get("Accept-Encoding")) {
			h.Set("Content-Encoding", "gzip")
			body, etag = f.gz, f.gzipETag
		}
	}
	h.Set("ETag", etag)
	if etagMatches(c.Request.Header.Get("If-None-Match"), etag) {
		c.Status(http.StatusNotModified)
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	c.Status(http.StatusOK)
	if c.Request.Method == http.MethodHead {
		return
	}
	_, _ = c.Writer.Write(body)
}

// load 载入并缓存一个文件；目录与不存在的路径返回 fs.ErrNotExist。
func (s *staticSite) load(name string) (*staticFile, error) {
	if v, ok := s.files.Load(name); ok {
		return v.(*staticFile), nil
	}
	info, err := fs.Stat(s.fsys, name)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fs.ErrNotExist
	}
	body, err := fs.ReadFile(s.fsys, name)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	tag := hex.EncodeToString(sum[:16])
	f := &staticFile{
		body:        body,
		etag:        `"` + tag + `"`,
		contentType: contentTypeOf(name, body),
	}
	if len(body) >= minGzipSize && compressible(f.contentType) {
		f.gz = gzipBytes(body)
		f.gzipETag = `"` + tag + `-gzip"`
	}
	v, _ := s.files.LoadOrStore(name, f)
	return v.(*staticFile), nil
}

func contentTypeOf(name string, body []byte) string {
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return t
	}
	return http.DetectContentType(body)
}

func compressible(contentType string) bool {
	t, _, _ := strings.Cut(contentType, ";")
	t = strings.TrimSpace(t)
	return strings.HasPrefix(t, "text/") ||
		t == "application/javascript" || t == "text/javascript" ||
		t == "application/json" || t == "image/svg+xml"
}

func gzipBytes(b []byte) []byte {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression) // 级别为合法常量，不会出错
	_, _ = zw.Write(b)                                       // 写入内存缓冲不会失败
	_ = zw.Close()
	return buf.Bytes()
}

// acceptsGzip 判断 Accept-Encoding 是否接受 gzip（q=0 视为拒绝）。
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		coding, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(coding), "gzip") && strings.TrimSpace(coding) != "*" {
			continue
		}
		q := strings.ReplaceAll(strings.TrimSpace(params), " ", "")
		return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
	}
	return false
}

// etagMatches 判断 If-None-Match 是否命中（支持列表与 *；弱比较）。
func etagMatches(header, etag string) bool {
	for _, t := range strings.Split(header, ",") {
		t = strings.TrimPrefix(strings.TrimSpace(t), "W/")
		if t == "*" || t == etag {
			return true
		}
	}
	return false
}
