// Package migrations 把数据库迁移编进程序：liusha migrate、serve 启动前的自动迁移与
// 集成测试的建库共用这一份文件，没有第二个来源。
//
// 文件命名 NNNN_<描述>.up.sql / .down.sql，编号四位递增；已发布的迁移不可修改。
package migrations

import "embed"

// FS 是全部迁移文件。
//
//go:embed *.sql
var FS embed.FS
