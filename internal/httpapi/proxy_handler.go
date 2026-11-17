// 代理接入 handler（控制台「代理接入」）：代理的运行状态、客户端该连的地址、根证书下载。
//
// GET /proxy：运行状态。代理从未运行过也返回 200（state=offline、各时刻为空）：代理没在运行
// 是一种状态，不是资源不存在。
// GET /proxy/ca.crt：根证书（PEM）。.crt 扩展名各系统都能双击安装；代理从未运行过、还没有证书时 404。

package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/proxystatus"
)

// ProxyStatusAPI 读代理上报的运行状态；*proxystatus.Store 自动满足。
type ProxyStatusAPI interface {
	Get(ctx context.Context) (proxystatus.Record, bool, error)
}

// proxyState 是代理的运行状态取值。
const (
	proxyOnline  = "online"
	proxyOffline = "offline"
)

// proxyStatusResponse 是 GET /proxy 的响应。不含证书正文：证书经 /proxy/ca.crt 下载。
type proxyStatusResponse struct {
	State string `json:"state"`
	// LastSeenAt 是最后一次心跳；从未运行过时为空。
	LastSeenAt    *time.Time `json:"last_seen_at"`
	StartedAt     *time.Time `json:"started_at"`
	LastRequestAt *time.Time `json:"last_request_at"`
	// PublicAddr 是客户端设置代理时填的地址，以「:」开头时主机由前端补为控制台所在主机。
	PublicAddr    string `json:"public_addr"`
	CAFingerprint string `json:"ca_fingerprint"`
}

// proxyCAFilename 是根证书的下载文件名。
const proxyCAFilename = "liusha-ca.crt"

func getProxyStatusHandler(api ProxyStatusAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		r, ok, err := api.Get(c.Request.Context())
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		resp := proxyStatusResponse{State: proxyOffline}
		if ok {
			if r.Online(time.Now()) {
				resp.State = proxyOnline
			}
			resp.LastSeenAt, resp.StartedAt = &r.HeartbeatAt, &r.StartedAt
			resp.LastRequestAt = r.LastRequestAt
			resp.PublicAddr, resp.CAFingerprint = r.PublicAddr, r.CAFingerprint
		}
		c.JSON(http.StatusOK, resp)
	}
}

func getProxyCAHandler(api ProxyStatusAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		r, ok, err := api.Get(c.Request.Context())
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		if !ok || r.CAPEM == "" {
			c.JSON(http.StatusNotFound, gin.H{"error": "代理还没有运行过，尚未生成根证书"})
			return
		}
		c.Header("Content-Disposition", `attachment; filename="`+proxyCAFilename+`"`)
		c.Header("Cache-Control", "no-store")
		c.Data(http.StatusOK, "application/x-x509-ca-cert", []byte(r.CAPEM))
	}
}
