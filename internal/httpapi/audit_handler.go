// Package httpapi: 审计事件查看 handler（前端「审计日志」页，GET /audit）。
//
// audit_log 是系统级敏感操作的审计链（登录、任务建/中止、凭证写删），此前只有写入方没有
// 查看入口。本 handler 只读：筛选（操作者模糊、动作与目标类型精确、时间范围）+ 服务端分页，
// 与动作词表（audit.FacetActions）一起给出，前端筛选下拉与写入方同源。

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/audit"
)

// AuditReader 是审计事件只读窄接口（*audit.Store 自动满足）；nil 时不注册审计路由（单测）。
type AuditReader interface {
	List(ctx context.Context, f audit.Filter) (audit.Page, error)
}

// 审计页分页参数：审计量级小，默认一页 50 条，上限 500。
const (
	defaultAuditPageSize = 50
	maxAuditPageSize     = 500
)

// listAuditHandler 处理 GET /audit?actor=&action=&target_kind=&start=&end=&page=&size=。
// 响应 {items, total, page, size, actions}：actions 是动作词表全集（筛选下拉数据源，
// 与写入方常量同源，不随筛选变化）。
func listAuditHandler(api AuditReader) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, size := parsePagination(c.Query("page"), c.Query("size"), defaultAuditPageSize, maxAuditPageSize)
		f := audit.Filter{
			Actor:      c.Query("actor"),
			Action:     c.Query("action"),
			TargetKind: c.Query("target_kind"),
		}
		var err error
		if f.Start, err = parseAuditTime(c.Query("start")); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "start 不是合法时间: " + err.Error()})
			return
		}
		if f.End, err = parseAuditTime(c.Query("end")); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "end 不是合法时间: " + err.Error()})
			return
		}
		if f.Action != "" && !auditActionKnown(f.Action) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "未知动作 " + f.Action})
			return
		}
		f.Limit = size
		f.Offset = (page - 1) * size

		res, err := api.List(c.Request.Context(), f)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		out := make([]gin.H, 0, len(res.Items))
		for _, e := range res.Items {
			out = append(out, auditEventJSON(e))
		}
		c.JSON(http.StatusOK, gin.H{
			"items": out, "total": res.Total, "page": page, "size": size,
			"actions": audit.FacetActions,
		})
	}
}

// auditActionKnown 校验动作参数在词表内（防拼错的筛选悄悄变成空结果）。
func auditActionKnown(action string) bool {
	for _, a := range audit.FacetActions {
		if a == action {
			return true
		}
	}
	return false
}

// parseAuditTime 解析 RFC3339 时间参数；空值返回零时间（不限）。
func parseAuditTime(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, raw)
}

// auditEventJSON 是审计事件响应的单一序列化点。metadata 解包为对象透出（写入方保证合法
// JSON；防御性容错——真不合法时按原样置空，不让一个坏行拖垮整页）。
func auditEventJSON(e audit.Event) gin.H {
	var meta any
	if len(e.Metadata) > 0 {
		if err := json.Unmarshal(e.Metadata, &meta); err != nil {
			meta = nil
		}
	}
	return gin.H{
		"id": e.ID, "actor": e.Actor, "action": e.Action,
		"target_kind": e.TargetKind, "target_id": e.TargetID,
		"metadata": meta, "created_at": e.CreatedAt,
	}
}
