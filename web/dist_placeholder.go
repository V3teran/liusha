//go:build !webdist

package web

import (
	"embed"
	"io/fs"
)

//go:embed placeholder
var placeholder embed.FS

// Embedded 报告前端是否已编入本程序。
const Embedded = false

// Assets 返回占位页：说明本程序未编入前端及如何获得。
func Assets() fs.FS {
	sub, err := fs.Sub(placeholder, "placeholder")
	if err != nil {
		panic(err) // "placeholder" 是合法的静态路径，fs.Sub 不会失败
	}
	return sub
}
