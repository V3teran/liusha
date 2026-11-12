//go:build integration

package setup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/V3teran/liusha/internal/envfile"
)

// 首装：迁移空库、写出 0600 的环境变量文件（含连接参数与三项密钥），再次执行拒绝覆盖。
func TestRun_FreshInstall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	pg, err := tcpostgres.Run(ctx, "pgvector/pgvector:pg17",
		tcpostgres.WithDatabase("liusha"), tcpostgres.WithUsername("liusha"), tcpostgres.WithPassword("liusha"),
		tcpostgres.BasicWaitStrategies())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	rd, err := tcredis.Run(ctx, "redis:8")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rd.Terminate(context.Background()) })
	redisURL, err := rd.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "liusha.env")
	opts := Options{EnvFile: path, PostgresDSN: dsn, RedisAddr: strings.TrimPrefix(redisURL, "redis://")}
	var out bytes.Buffer
	if err := Run(ctx, opts, &out); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("环境变量文件应为 0600: %v %v", info, err)
	}
	vars, err := envfile.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, v := range vars {
		got[v.Key] = v.Value
	}
	if got["LIUSHA_POSTGRES_DSN"] != dsn || len(got["LIUSHA_LLM_KEY_SECRET"]) != 64 || len(got["LIUSHA_SANDBOX_TOKEN"]) != 64 {
		t.Fatalf("环境变量文件内容不完整: %v", got)
	}
	if pw := got["LIUSHA_ADMIN_INITIAL_PASSWORD"]; len(pw) < 8 || !strings.Contains(out.String(), pw) {
		t.Fatalf("应生成并提示初始管理员密码: %q / %s", pw, out.String())
	}

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var version int64
	if err := conn.QueryRow(ctx, `SELECT version FROM schema_migrations`).Scan(&version); err != nil || version == 0 {
		t.Fatalf("数据库应已迁移: version=%d err=%v", version, err)
	}

	if err := Run(ctx, opts, &out); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("再次执行应拒绝覆盖, got %v", err)
	}
}
