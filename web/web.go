// Package web 把控制台前端（web/dist）编进程序，由 api 组件直接提供。
//
// 只有带构建标签 webdist 编译时才嵌入 web/dist（须先 pnpm build）；不带标签时嵌入一张
// 占位页——go build / go test / go vet 因此不依赖前端构建，开发时前端走 vite dev server。
// 发布构建（镜像、Releases 压缩包）一律先构建前端再带标签编译。
package web
