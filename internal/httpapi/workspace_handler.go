package httpapi

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/workspace"
)

// WorkspaceFileAPI 是工作区文件操作面的窄接口（*workspace.Store 自动满足）。
type WorkspaceFileAPI interface {
	Tasks() ([]workspace.TaskDir, error)
	Tree(taskID string) ([]workspace.Entry, error)
	Read(taskID, rel string) (workspace.FileContent, error)
	Write(taskID, rel string, data []byte) error
	Mkdir(taskID, rel string) error
	Delete(taskID, rel string) error
}

const maxUploadBytes = 8 << 20 // 8 MiB：写入口的内容上限

// listWorkspacesHandler 处理 GET /workspaces：任务工作区枚举（最新活动在前）。
func listWorkspacesHandler(api WorkspaceFileAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		tasks, err := api.Tasks()
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		if tasks == nil {
			tasks = []workspace.TaskDir{}
		}
		c.JSON(http.StatusOK, gin.H{"tasks": tasks})
	}
}

// getWorkspaceTreeHandler 处理 GET /workspaces/:task/tree：任务文件树。
func getWorkspaceTreeHandler(api WorkspaceFileAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		entries, err := api.Tree(c.Param("task"))
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

// getWorkspaceFileHandler 处理 GET /workspaces/:task/content?path=：读文件。
// 二进制以 base64 返回并标记 binary（前端禁用编辑）。
func getWorkspaceFileHandler(api WorkspaceFileAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		fc, err := api.Read(c.Param("task"), c.Query("path"))
		if err != nil {
			respondErr(c, http.StatusNotFound, err)
			return
		}
		c.JSON(http.StatusOK, fc)
	}
}

// putWorkspaceFileHandler 处理 PUT /workspaces/:task/content?path=：创建/覆盖
// 文件（body 为原始字节；query dir=1 时创建目录）。
func putWorkspaceFileHandler(api WorkspaceFileAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		rel := c.Query("path")
		if c.Query("dir") == "1" {
			if err := api.Mkdir(c.Param("task"), rel); err != nil {
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
		if err := api.Write(c.Param("task"), rel, data); err != nil {
			respondErr(c, http.StatusBadRequest, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"path": rel, "size": len(data)})
	}
}

// deleteWorkspacePathHandler 处理 DELETE /workspaces/:task?path=：删文件/目录
// （目录递归；任务根目录受保护）。
func deleteWorkspacePathHandler(api WorkspaceFileAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := api.Delete(c.Param("task"), c.Query("path")); err != nil {
			respondErr(c, http.StatusBadRequest, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"deleted": c.Query("path")})
	}
}
