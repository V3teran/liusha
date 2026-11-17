package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// templatePath 是本包目录下的配置文件模板：发布压缩包与容器部署用的就是这份。
const templatePath = "config.yaml"

// editTemplate 对模板做一处文本替换后写到临时文件；old 必须恰好出现一次，防止模板改动后测试悄悄失效。
func editTemplate(t *testing.T, old, new string) string {
	t.Helper()
	b, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	if n := strings.Count(body, old); n != 1 {
		t.Fatalf("模板中 %q 出现 %d 次，期望 1 次", old, n)
	}
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(strings.Replace(body, old, new, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// 仓库里的 config.yaml 本身必须能加载：它就是交给用户的那份。数据目录按当前用户主目录展开，
// 工作区、日志、证书目录都在它之下。
func TestLoad_Template(t *testing.T) {
	cfg, err := Load(templatePath)
	if err != nil {
		t.Fatalf("config.yaml 应可加载: %v", err)
	}
	if cfg.API.ListenAddr != "0.0.0.0:8580" || cfg.Runner.Concurrency != 6 || cfg.Runner.URL != "http://127.0.0.1:8590" {
		t.Fatalf("取值与 config.yaml 不符: api=%q concurrency=%d runner.url=%q", cfg.API.ListenAddr, cfg.Runner.Concurrency, cfg.Runner.URL)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(home, "data", "liusha")
	if cfg.DataDir != dataDir {
		t.Fatalf("data_dir = %q，期望 %q", cfg.DataDir, dataDir)
	}
	for got, want := range map[string]string{
		cfg.WorkspaceDir(): filepath.Join(dataDir, "workspace"),
		cfg.LogDir():       filepath.Join(dataDir, "logs"),
		cfg.CertDir():      filepath.Join(dataDir, "certs"),
	} {
		if got != want {
			t.Fatalf("数据子目录 = %q，期望 %q", got, want)
		}
	}
	// 密钥在模板里一律留空，经环境变量注入。
	for key, v := range map[string]string{
		"postgres.url": cfg.Postgres.URL, "redis.url": cfg.Redis.URL, "sandbox.token": cfg.Sandbox.Token,
		"llm.encryption_key": cfg.LLM.EncryptionKey, "admin.initial_password": cfg.Admin.InitialPassword,
		"embedding.api_key": cfg.Embedding.APIKey,
	} {
		if v != "" && os.Getenv("LIUSHA_"+strings.ToUpper(strings.ReplaceAll(key, ".", "_"))) == "" {
			t.Fatalf("模板里的密钥 %s 应留空，got %q", key, v)
		}
	}
}

// 没有配置文件就不启动：程序不内置任何配置。
func TestLoad_FileRequired(t *testing.T) {
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "未指定配置文件") {
		t.Fatalf("未指定配置文件应报错，got %v", err)
	}
	missing := filepath.Join(t.TempDir(), "nope.yaml")
	if _, err := Load(missing); err == nil || !strings.Contains(err.Error(), "nope.yaml") {
		t.Fatalf("配置文件不存在应报错并指出路径，got %v", err)
	}
}

// 缺键直接报错并列出缺的键，不拿任何值顶替。
func TestLoad_MissingKeyFails(t *testing.T) {
	for _, c := range []struct{ line, key string }{
		{"  concurrency: 6                     # 同时执行的任务数\n", "runner.concurrency"},
		{"  cookie_secure: false", "api.cookie_secure"},
		{"  level: info                        # debug / info / warn / error\n", "log.level"},
		{"data_dir: ~/data/liusha\n", "data_dir"},
	} {
		p := editTemplate(t, c.line, "")
		_, err := Load(p)
		if err == nil || !strings.Contains(err.Error(), "缺少键") || !strings.Contains(err.Error(), c.key) {
			t.Fatalf("缺 %s 应报错并列出该键，got %v", c.key, err)
		}
	}
}

// 写了值为空（yaml 的 null）等同于没写这个键。
func TestLoad_NullValueIsMissing(t *testing.T) {
	p := editTemplate(t, `  callback_host: ""`, "  callback_host:")
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "sandbox.callback_host") {
		t.Fatalf("值为空的键应按缺键报错，got %v", err)
	}
}

// 不认识的键（拼错、已删除的旧键）报错，不静默忽略。
func TestLoad_UnknownKeyFails(t *testing.T) {
	for _, c := range []struct{ old, new, key string }{
		{"  concurrency: 6", "  concurency: 6\n  concurrency: 6", "concurency"},
		// 已删除的键：LLM 种子、内部约定、改名前的旧键。
		{"data_dir: ~/data/liusha\n", "data_dir: ~/data/liusha\nproviders:\n  main: {type: openai_compat}\n", "providers"},
		{"  stream_max_len: 100000", "  stream_name: x\n  stream_max_len: 100000", "stream_name"},
		{"  concurrency: 6", "  asynq_concurrency: 6\n  concurrency: 6", "asynq_concurrency"},
	} {
		p := editTemplate(t, c.old, c.new)
		if _, err := Load(p); err == nil || !strings.Contains(err.Error(), c.key) {
			t.Fatalf("未知键 %s 应报错并指出该键，got %v", c.key, err)
		}
	}
}

// 没有意义的取值报错，不替换成别的值。
func TestLoad_InvalidValueFails(t *testing.T) {
	for _, c := range []struct{ old, new, key string }{
		{"  concurrency: 6", "  concurrency: 0", "runner.concurrency"},
		{"  level: info", "  level: verbose", "log.level"},
		{"  format: json", "  format: text", "log.format"},
		{"  max_size_bytes: 104857600", "  max_size_bytes: 1000", "log.max_size_bytes"},
		{"  pool_size: 20", "  pool_size: 0", "redis.pool_size"},
		{"  min_conns: 2", "  min_conns: 50", "postgres.min_conns"},
		{"    max_429: 4", "    max_429: 5", "llm.retry.backoff_429_seconds"},
		{"  public_addr: \"\"", "  public_addr: \"58588\"", "proxy.public_addr"},
		{"  public_addr: \"\"", "  public_addr: \":70000\"", "proxy.public_addr"},
		// 相对路径依赖进程工作目录：以服务方式运行时不可靠，拒绝。
		{"data_dir: ~/data/liusha", "data_dir: data/liusha", "data_dir"},
	} {
		p := editTemplate(t, c.old, c.new)
		if _, err := Load(p); err == nil || !strings.Contains(err.Error(), c.key) {
			t.Fatalf("%s 取值无效应报错，got %v", c.key, err)
		}
	}
}

// 文件里的每个键都能被 LIUSHA_<键> 环境变量覆盖（含密钥与数据目录）；值为空串的环境变量不覆盖。
// proxy.public_addr 可写「主机:端口」或只写「:端口」。
func TestLoad_ProxyPublicAddr(t *testing.T) {
	for _, v := range []string{"10.0.0.5:58588", ":58588", "proxy.example.com:58588", "[::1]:8588"} {
		t.Setenv("LIUSHA_PROXY_PUBLIC_ADDR", v)
		cfg, err := Load(templatePath)
		if err != nil || cfg.Proxy.PublicAddr != v {
			t.Fatalf("public_addr=%q 应可加载，got %q err=%v", v, cfg.Proxy.PublicAddr, err)
		}
	}
}

func TestLoad_EnvOverridesFile(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("LIUSHA_DATA_DIR", dataDir)
	t.Setenv("LIUSHA_SANDBOX_URL", "http://sandbox:8591")
	t.Setenv("LIUSHA_RUNNER_CONCURRENCY", "10")
	t.Setenv("LIUSHA_POSTGRES_URL", "postgres://u:p@db:5432/liusha")
	t.Setenv("LIUSHA_REDIS_URL", "redis://:p@redis:6379/0")
	t.Setenv("LIUSHA_LLM_ENCRYPTION_KEY", "ab")
	t.Setenv("LIUSHA_ADMIN_INITIAL_PASSWORD", "changeme-admin")
	t.Setenv("LIUSHA_EMBEDDING_API_KEY", "changeme-embedding")
	t.Setenv("LIUSHA_RUNNER_URL", "")
	cfg, err := Load(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != dataDir || cfg.Sandbox.URL != "http://sandbox:8591" || cfg.Runner.Concurrency != 10 ||
		cfg.Postgres.URL != "postgres://u:p@db:5432/liusha" || cfg.Redis.URL != "redis://:p@redis:6379/0" ||
		cfg.LLM.EncryptionKey != "ab" || cfg.Admin.InitialPassword != "changeme-admin" || cfg.Embedding.APIKey != "changeme-embedding" {
		t.Fatalf("环境变量未覆盖: %+v", cfg)
	}
	if cfg.Runner.URL != "http://127.0.0.1:8590" {
		t.Fatalf("值为空串的环境变量不应覆盖文件，runner.url = %q", cfg.Runner.URL)
	}
}

// browser_python 写空串表示本机惯用名；写了就按写的用。
func TestLoad_BrowserPython(t *testing.T) {
	cfg, err := Load(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sandbox.BrowserPython != "python3" && cfg.Sandbox.BrowserPython != "python" {
		t.Fatalf("browser_python 空串应解析为本机惯用名，got %q", cfg.Sandbox.BrowserPython)
	}
	t.Setenv("LIUSHA_SANDBOX_BROWSER_PYTHON", "/opt/venv/bin/python")
	if cfg, err = Load(templatePath); err != nil || cfg.Sandbox.BrowserPython != "/opt/venv/bin/python" {
		t.Fatalf("显式 browser_python 应原样使用: %q %v", cfg.Sandbox.BrowserPython, err)
	}
}
