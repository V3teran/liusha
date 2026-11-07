// Package httpapi: 会话附件上传 handler（POST /attachments、POST /conversations/:id/attachments）。
//
// multipart 字段 file + 可选 conversation_id（路径参数路由则取 :id）：
// 会话已绑任务 → 直落 <assignment>/<task>/inbox（沙盒立即可见）；会话存在未绑
// 任务 → 落该会话暂存区；会话不存在（新会话尚未创建）→ 落 pending 暂存区，
// 目录名服务端生成随响应返回，发送消息时凭 Ref.Path 认领。
// 大小上限在 multipart 解析前挂 MaxBytesReader——超限不做全量接收。

package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/V3teran/liusha/internal/attachment"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/workspace"
)

// AttachmentUploader 是附件落位窄接口（*attachment.Store 满足）。
type AttachmentUploader interface {
	SaveUpload(dest attachment.Dest, r io.Reader, filename string, sizeCap int64) (attachment.Ref, error)
	SweepStalePending(maxAge time.Duration)
}

// AttachmentContext 解析 会话→任务→批次 链（供附件落位定 inbox）。
// *conversation.Store + *task.Store 的组合满足（cmd/api 注入组合体）。
type AttachmentContext interface {
	ResolveTaskID(ctx context.Context, conversationID string) (string, error)
	GetSummary(ctx context.Context, id string) (task.Summary, error)
}

// pendingStaleAfter 是未认领 pending 暂存目录的超期时长（每次上传顺手清扫）。
const pendingStaleAfter = 24 * time.Hour

// multipartOverhead 是 multipart 表单边界与字段名等固定开销的预算。
const multipartOverhead = 64 << 10

// uploadAttachmentHandler 处理两种路由形态的上传：convID 从路径参数或表单
// 字段 conversation_id 取（空 = 新会话尚未创建 → pending 暂存）。
func uploadAttachmentHandler(up AttachmentUploader, ctx AttachmentContext, sizeCap int64, convIDOf func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		convID := convIDOf(c)

		// 上限在 multipart 解析前挂上（第一道闸）；表单边界/字段的固有开销按
		// 64 KiB 计——超限文件在这一层就拒收，不做全量接收。文件本体超过
		// sizeCap 的精确拦截在落盘层（WriteFrom 按字节截断），两道闸语义不同。
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, sizeCap+multipartOverhead)
		fh, err := c.FormFile("file")
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{
					"error":    fmt.Sprintf("附件超过上限 %d MiB", sizeCap>>20),
					"size_cap": sizeCap,
				})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": "file required（multipart 字段 file）"})
			return
		}

		var assignmentID, taskID string
		if convID != "" {
			taskID, err = ctx.ResolveTaskID(c.Request.Context(), convID)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found", "conversation_id": convID})
					return
				}
				respondErr(c, http.StatusInternalServerError, err)
				return
			}
			if taskID != "" {
				sm, gerr := ctx.GetSummary(c.Request.Context(), taskID)
				if gerr != nil {
					respondErr(c, http.StatusInternalServerError, gerr)
					return
				}
				assignmentID = sm.AssignmentID
			}
		}

		uploadID := ""
		if convID == "" {
			if uploadID, err = newUploadID(); err != nil {
				respondErr(c, http.StatusInternalServerError, err)
				return
			}
		}

		f, err := fh.Open()
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		defer func() { _ = f.Close() }()

		ref, err := up.SaveUpload(attachment.Dest{
			ConversationID: convID,
			AssignmentID:   assignmentID,
			TaskID:         taskID,
			UploadID:       uploadID,
		}, f, fh.Filename, sizeCap)
		if err != nil {
			if errors.Is(err, workspace.ErrTooLarge) {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{
					"error":    fmt.Sprintf("附件超过上限 %d MiB", sizeCap>>20),
					"size_cap": sizeCap,
				})
				return
			}
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		up.SweepStalePending(pendingStaleAfter)

		c.JSON(http.StatusCreated, gin.H{
			"name":            ref.Name,
			"size":            ref.Size,
			"path":            ref.Path,
			"conversation_id": convID,
			"upload_id":       uploadID,
		})
	}
}

// newUploadID 生成 pending 暂存目录名（128 bit CSPRNG hex）。
func newUploadID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("attachment: 生成 upload id: %w", err)
	}
	return hex.EncodeToString(b), nil
}
