// Package web 用 //go:embed 把 sitemap viewer 静态前端打进 Go binary。
//
// 设计：
//   - 单页 SPA（vanilla TS via <script type=module>），无 build pipeline
//   - dagre-d3 + d3 通过 jsdelivr 公网 CDN（首次加载有外网依赖；
//     不想公网时把 viewer/vendor 下的本地副本切回去即可）
//   - cmd/api 装配时 `Deps.StaticFS = web.ViewerFS()` 注入；缺省不挂避免意外暴露
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed viewer
var viewerFiles embed.FS

// ViewerFS 返回 /viewer 子树的 http.FileSystem。
// 出错（embed 失败）时返回 nil——cmd/api 注入 nil 时 httpapi 不挂路由，是安全降级。
func ViewerFS() http.FileSystem {
	sub, err := fs.Sub(viewerFiles, "viewer")
	if err != nil {
		return nil
	}
	return http.FS(sub)
}
