//go:build webdist

package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Embedded 报告前端是否已编入本程序。
const Embedded = true

// Assets 返回前端构建产物（根目录即 index.html 所在目录）。
func Assets() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // "dist" 是合法的静态路径，fs.Sub 不会失败
	}
	return sub
}
