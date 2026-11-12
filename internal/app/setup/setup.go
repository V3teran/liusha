// Package setup 是二进制部署的首装引导（liusha setup）：检查依赖、迁移数据库、生成密钥，
// 写出供 liusha serve -env-file 与 systemd EnvironmentFile= 共用的环境变量文件。
package setup

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/envfile"
	"github.com/V3teran/liusha/internal/logx"
)

// Options 是首装参数。
type Options struct {
	EnvFile       string // 要生成的环境变量文件
	PostgresDSN   string
	RedisAddr     string
	RedisPassword string
	WorkspaceDir  string // 空 = 内置默认
}

// Run 执行首装：校验参数 → 连 Redis → 迁移数据库 → 生成密钥并写出环境变量文件 → 打印后续步骤。
// 环境变量文件已存在时拒绝执行：其中的密钥加密密钥一旦替换，已保存的 LLM 密钥就无法解密。
func Run(ctx context.Context, opts Options, out io.Writer) error {
	if opts.EnvFile == "" {
		return errors.New("缺少 -env-file")
	}
	if opts.PostgresDSN == "" {
		return errors.New("缺少 -postgres-dsn（PostgreSQL 连接串，库需已安装 pgvector 扩展）")
	}
	if _, err := os.Stat(opts.EnvFile); err == nil {
		return fmt.Errorf("%s 已存在：首装已完成。修改连接参数请直接编辑该文件；不要重新生成密钥（已保存的 LLM 密钥会无法解密）", opts.EnvFile)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("检查 %s: %w", opts.EnvFile, err)
	}

	rdb, err := db.NewRedis(ctx, config.RedisConfig{Addr: opts.RedisAddr, Password: opts.RedisPassword})
	if err != nil {
		return fmt.Errorf("连接 Redis: %w", err)
	}
	_ = rdb.Close()
	if err := db.Migrate(ctx, opts.PostgresDSN, logx.New("setup")); err != nil {
		return fmt.Errorf("迁移数据库: %w", err)
	}

	secrets, err := generateSecrets()
	if err != nil {
		return err
	}
	if err := envfile.Create(opts.EnvFile, sections(opts, secrets)); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, `首装完成，配置与密钥已写入 %[1]s（权限 0600，妥善保管，勿提交到代码仓库）。

初始管理员：admin / %[2]s（仅首次启动时创建，登录后请立即修改密码）

启动：
  liusha serve -env-file %[1]s

控制台 http://<本机地址>:8580，MITM 代理 <本机地址>:8588。
工具在本机执行：工具页会标出未安装的工具；浏览器能力需要装了 browser-use 的 Python
（LIUSHA_SANDBOX_BROWSER_PYTHON 指定解释器）。LLM 在控制台「LLM 配置」页接入。
`, opts.EnvFile, secrets.adminPassword)
	return nil
}

type secrets struct {
	llmKeySecret  string
	sandboxToken  string
	adminPassword string
}

func generateSecrets() (secrets, error) {
	var s secrets
	key := make([]byte, 32) // AES-256 密钥（LIUSHA_LLM_KEY_SECRET 要求 32 字节 hex）
	token := make([]byte, 32)
	pw := make([]byte, 18)
	for _, b := range [][]byte{key, token, pw} {
		if _, err := rand.Read(b); err != nil {
			return s, fmt.Errorf("生成随机密钥: %w", err)
		}
	}
	s.llmKeySecret = hex.EncodeToString(key)
	s.sandboxToken = hex.EncodeToString(token)
	s.adminPassword = base64.RawURLEncoding.EncodeToString(pw)
	return s, nil
}

func sections(opts Options, s secrets) []envfile.Section {
	conn := []envfile.Var{
		{Key: "LIUSHA_POSTGRES_DSN", Value: opts.PostgresDSN},
		{Key: "LIUSHA_REDIS_ADDR", Value: opts.RedisAddr},
	}
	if opts.RedisPassword != "" {
		conn = append(conn, envfile.Var{Key: "LIUSHA_REDIS_PASSWORD", Value: opts.RedisPassword})
	}
	if opts.WorkspaceDir != "" {
		conn = append(conn, envfile.Var{Key: "LIUSHA_SANDBOX_WORKSPACE_DIR", Value: opts.WorkspaceDir})
	}
	return []envfile.Section{
		{
			Comments: []string{
				"liusha 配置与密钥（liusha setup 生成）。",
				"用法：liusha serve -env-file <本文件>，或 systemd 的 EnvironmentFile=<本文件>。",
				"其他配置项按 LIUSHA_<键> 追加，如 LIUSHA_API_LISTEN_ADDR、LIUSHA_SANDBOX_CALLBACK_HOST。",
				"",
				"连接",
			},
			Vars: conn,
		},
		{
			Comments: []string{"密钥：LIUSHA_LLM_KEY_SECRET 加密已保存的 LLM 密钥，生成后不要更改"},
			Vars: []envfile.Var{
				{Key: "LIUSHA_LLM_KEY_SECRET", Value: s.llmKeySecret},
				{Key: "LIUSHA_SANDBOX_TOKEN", Value: s.sandboxToken},
				{Key: "LIUSHA_ADMIN_INITIAL_PASSWORD", Value: s.adminPassword},
			},
		},
	}
}
