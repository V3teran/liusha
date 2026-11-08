package httpapi

import (
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/workspace"
)

// WorkspaceFileAPI 是工作区文件操作面的窄接口（*workspace.Store 自动满足）。
type WorkspaceFileAPI interface {
	Tree() ([]workspace.Entry, error)
	Read(rel string) (workspace.FileContent, error)
	Write(rel string, data []byte) error
	Mkdir(rel string) error
	Delete(rel string) error
	OpenFile(rel string) (*os.File, os.FileInfo, error)
	ZipTo(rel string, w io.Writer) error
}

const maxUploadBytes = 8 << 20 // 8 MiB：写入口的内容上限

// getWorkspaceTreeHandler 处理 GET /workspaces/tree：整棵工作区文件树
// （顶层任务目录按活动时间最新在前）。
func getWorkspaceTreeHandler(api WorkspaceFileAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		entries, err := api.Tree()
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		if entries == nil {
			entries = []workspace.Entry{}
		}
		c.JSON(http.StatusOK, gin.H{"entries": entries})
	}
}

// getWorkspaceFileHandler 处理 GET /workspaces/content?path=：读文件。
// 二进制以 base64 返回并标记 binary（前端禁用编辑）。
func getWorkspaceFileHandler(api WorkspaceFileAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		fc, err := api.Read(c.Query("path"))
		if err != nil {
			respondErr(c, http.StatusNotFound, err)
			return
		}
		c.JSON(http.StatusOK, fc)
	}
}

// getWorkspaceRawHandler 处理 GET /workspaces/raw?path=：下载通道。
// 文件原样字节流（Content-Disposition 附件）；目录打包为 zip 流式输出。
func getWorkspaceRawHandler(api WorkspaceFileAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		rel := c.Query("path")
		// 下载文件名取末段；目录路径以 / 结尾，先去尾斜杠再取（否则目录一律得到空名）。
		base := path.Base(strings.TrimSuffix(rel, "/"))
		if base == "." || base == "/" || base == "" {
			base = "workspace"
		}
		// 目录 → zip 流。ZipTo 在写出任何字节前校验目录存在性：此时失败仍可回真实状态码；
		// 已开始输出后的失败只能截断连接，必须留痕（客户端只会看到损坏的下载）。
		if strings.HasSuffix(rel, "/") {
			c.Header("Content-Type", "application/zip")
			c.Header("Content-Disposition", attachmentDisposition(base+".zip"))
			if err := api.ZipTo(rel, c.Writer); err != nil {
				if !c.Writer.Written() {
					c.Writer.Header().Del("Content-Disposition")
					respondErr(c, http.StatusNotFound, err)
					return
				}
				apiLog.Warn().Err(err).Str("path", rel).Msg("目录打包中途失败，下载已截断")
				_ = c.Error(err)
			}
			return
		}
		// 文件 → 原样字节流
		f, info, err := api.OpenFile(rel)
		if err != nil {
			respondErr(c, http.StatusNotFound, err)
			return
		}
		defer func() { _ = f.Close() }()
		c.Header("Content-Disposition", attachmentDisposition(base))
		c.DataFromReader(http.StatusOK, info.Size(), "application/octet-stream", f, nil)
	}
}

// putWorkspaceFileHandler 处理 PUT /workspaces/content?path=：创建/覆盖
// 文件（body 为原始字节；query dir=1 时创建目录）。
func putWorkspaceFileHandler(api WorkspaceFileAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		rel := c.Query("path")
		if c.Query("dir") == "1" {
			if err := api.Mkdir(rel); err != nil {
				respondErr(c, http.StatusBadRequest, err)
				return
			}
			c.JSON(http.StatusOK, gin.H{"path": rel, "created": "dir"})
			return
		}
		data, err := io.ReadAll(io.LimitReader(c.Request.Body, maxUploadBytes+1))
		if err != nil {
			respondErr(c, http.StatusBadRequest, err)
			return
		}
		if len(data) > maxUploadBytes {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "内容超过 8MiB 上限"})
			return
		}
		if err := api.Write(rel, data); err != nil {
			respondErr(c, http.StatusBadRequest, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"path": rel, "size": len(data)})
	}
}

// deleteWorkspacePathHandler 处理 DELETE /workspaces?path=：删文件/目录
// （目录递归；工作区根目录受保护）。
func deleteWorkspacePathHandler(api WorkspaceFileAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := api.Delete(c.Query("path")); err != nil {
			respondErr(c, http.StatusBadRequest, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"deleted": c.Query("path")})
	}
}

// attachmentDisposition 生成下载头：文件名经 RFC 2231 编码——工作区允许中文与带引号的名字，
// 直接拼进 filename="…" 会产生非法头（引号截断、非 ASCII 字节）。
func attachmentDisposition(filename string) string {
	return mime.FormatMediaType("attachment", map[string]string{"filename": filename})
}
