//go:build integration

package db

import (
	"context"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/db/migrations"
)

func upFiles(t *testing.T) []string {
	t.Helper()
	names, err := fs.Glob(migrations.FS, "*.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	return names
}

func parseVersion(t *testing.T, name string) uint {
	t.Helper()
	num, _, _ := strings.Cut(name, "_")
	v, err := strconv.ParseUint(num, 10, 64)
	if err != nil {
		t.Fatalf("迁移文件名不合法 %s: %v", name, err)
	}
	return uint(v)
}

func currentVersion(t *testing.T, conn *pgx.Conn) (uint, bool) {
	t.Helper()
	var v int64
	var dirty bool
	if err := conn.QueryRow(context.Background(), `SELECT version, dirty FROM schema_migrations`).Scan(&v, &dirty); err != nil {
		t.Fatalf("读 schema_migrations: %v", err)
	}
	return uint(v), dirty
}

// 空库：迁移到最新版本，重复执行是空操作。
func TestMigrate_EmptyDatabase(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(t)
	if err := Migrate(ctx, dsn, zerolog.Nop()); err != nil {
		t.Fatalf("首次迁移: %v", err)
	}
	if err := Migrate(ctx, dsn, zerolog.Nop()); err != nil {
		t.Fatalf("重复迁移应为空操作: %v", err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	names := upFiles(t)
	want := parseVersion(t, names[len(names)-1])
	if v, dirty := currentVersion(t, conn); v != want || dirty {
		t.Fatalf("版本 = %d dirty=%v，期望 %d", v, dirty, want)
	}
}

// 存量库：此前由 golang-migrate 命令行工具（postgres 驱动）迁移到倒数第二个版本，
// 版本表格式与其一致；内置迁移器应只补跑最后一个版本。
func TestMigrate_ContinuesFromCommandLineTool(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(t)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()

	names := upFiles(t)
	for _, n := range names[:len(names)-1] {
		b, err := fs.ReadFile(migrations.FS, n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, string(b)); err != nil {
			t.Fatalf("应用 %s: %v", n, err)
		}
	}
	prev := parseVersion(t, names[len(names)-2])
	if _, err := conn.Exec(ctx, `CREATE TABLE schema_migrations (version bigint NOT NULL PRIMARY KEY, dirty boolean NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO schema_migrations VALUES ($1, false)`, int64(prev)); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(ctx, dsn, zerolog.Nop()); err != nil {
		t.Fatalf("接续迁移: %v", err)
	}
	want := parseVersion(t, names[len(names)-1])
	if v, dirty := currentVersion(t, conn); v != want || dirty {
		t.Fatalf("版本 = %d dirty=%v，期望 %d", v, dirty, want)
	}
}

// 上次迁移中途失败（dirty）：拒绝执行并说明原因，不在半成品结构上继续。
func TestMigrate_RefusesDirtyDatabase(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(t)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, `CREATE TABLE schema_migrations (version bigint NOT NULL PRIMARY KEY, dirty boolean NOT NULL);
		INSERT INTO schema_migrations VALUES (196, true)`); err != nil {
		t.Fatal(err)
	}
	err = Migrate(ctx, dsn, zerolog.Nop())
	if err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("dirty 库应拒绝迁移，got %v", err)
	}
}
