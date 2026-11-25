package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// sandboxView 是多镜像部署里沙盒容器的视角：启动流程读的两处，加 runner.url 与沙盒段。
var sandboxView = ProcessView("runner.url", "sandbox")

// templateWith 把模板做过修改后写到临时文件；每处 old 必须恰好出现一次。
func templateWith(t *testing.T, edits ...[2]string) string {
	t.Helper()
	b, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	for _, e := range edits {
		if n := strings.Count(body, e[0]); n != 1 {
			t.Fatalf("模板中 %q 出现 %d 次，期望 1 次", e[0], n)
		}
		body = strings.Replace(body, e[0], e[1], 1)
	}
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// 别的段怎么变都与读这个视角的进程无关：配置文件随主程序更新后，别的段加了键、删了键、取值
// 变得无效、类型都变了，进程照常读到自己的键；其余字段是零值。整份加载面对同一份文件全部拒绝。
func TestLoadView_IgnoresKeysOutsideTheView(t *testing.T) {
	for _, c := range []struct {
		name  string
		edits [][2]string
	}{
		{"加键", [][2]string{{"  concurrency: 3", "  step_llm_timeout_seconds: 300\n  concurrency: 3"}}},
		{"删键", [][2]string{{"  concurrency: 3                     # 同时执行的任务数\n", ""}}},
		{"取值无效", [][2]string{{"  concurrency: 3", "  concurrency: 0"}}},
		{"类型不对", [][2]string{{"  read_timeout_seconds: 15", "  read_timeout_seconds: 十五"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := templateWith(t, c.edits...)
			if _, err := Load(p); err == nil {
				t.Fatalf("整份加载应拒绝这份文件")
			}
			cfg, err := LoadView(p, sandboxView)
			if err != nil {
				t.Fatalf("按视角加载不应受别的段影响: %v", err)
			}
			if cfg.Sandbox.URL != "http://127.0.0.1:8591" || cfg.Runner.URL != "http://127.0.0.1:8590" ||
				cfg.Sandbox.ViewportWidth != 1280 || !filepath.IsAbs(cfg.DataDir) || cfg.Log.Level != "info" {
				t.Fatalf("视角内的键应照常读到: %+v", cfg)
			}
			if cfg.Runner.Concurrency != 0 || cfg.API.ListenAddr != "" || cfg.LLM.EncryptionKey != "" || cfg.Postgres.URL != "" {
				t.Fatalf("视角外的字段应是零值: %+v", cfg)
			}
		})
	}
}

// 视角之内的键仍然严格。
//
// 缺失报「缺少键」并指向同版本的模板，不拿任何值顶替——报错必须是这一种：允许为空的键缺失了，
// 取值校验不会报，「缺少键」是它唯一的防线。
func TestLoadView_RequiresKeysInsideTheView(t *testing.T) {
	t.Run("缺失", func(t *testing.T) {
		for _, c := range []struct {
			name, key string
			edits     [][2]string
		}{
			{"视角里的单键", "runner.url", [][2]string{{"  url: http://127.0.0.1:8590", "  url:"}}},
			{"段里的键", "sandbox.viewport_width", [][2]string{{"  viewport_width: 1280", "  viewport_width:"}}},
			{"允许为空的键", "sandbox.callback_host", [][2]string{{"  callback_host: \"\"\n", ""}}},
			{"启动流程读的键", "log.level", [][2]string{{"  level: info                        # debug / info / warn / error\n", ""}}},
		} {
			t.Run(c.name, func(t *testing.T) {
				_, err := LoadView(templateWith(t, c.edits...), sandboxView)
				if err == nil || !strings.Contains(err.Error(), "缺少键") || !strings.Contains(err.Error(), c.key) {
					t.Fatalf("缺 %s 应报「缺少键」并指出该键，got %v", c.key, err)
				}
			})
		}
	})
	t.Run("取值无效", func(t *testing.T) {
		for _, c := range []struct{ name, key string }{
			{"段里的键", "sandbox.viewport_width"},
			{"启动流程读的键", "data_dir"},
		} {
			t.Run(c.name, func(t *testing.T) {
				edits := [][2]string{{"  viewport_width: 1280", "  viewport_width: 0"}}
				if c.key == "data_dir" {
					edits = [][2]string{{"data_dir: ~/data/liusha", "data_dir: data/liusha"}}
				}
				_, err := LoadView(templateWith(t, edits...), sandboxView)
				if err == nil || !strings.Contains(err.Error(), c.key) {
					t.Fatalf("%s 取值无效应拒绝启动并指出该键，got %v", c.key, err)
				}
			})
		}
		// 跨键的取值关系：整段都在视角里才看得到两个键。
		_, err := LoadView(templateWith(t, [2]string{"  min_conns: 2", "  min_conns: 50"}), ProcessView("postgres"))
		if err == nil || !strings.Contains(err.Error(), "postgres.min_conns") {
			t.Fatalf("视角内跨键的取值关系仍应校验，got %v", err)
		}
		_, err = LoadView(templateWith(t, [2]string{"  viewport_width: 1280", "  viewport_width: 宽"}), sandboxView)
		if err == nil || !strings.Contains(err.Error(), "viewport_width") {
			t.Fatalf("视角内键的类型不对应拒绝启动，got %v", err)
		}
	})
}

// 环境变量只覆盖视角之内的键：AutomaticEnv 只认配置源里已有的键。
func TestLoadView_EnvOverridesOnlyInsideTheView(t *testing.T) {
	t.Setenv("LIUSHA_SANDBOX_TOKEN", "from-env")
	t.Setenv("LIUSHA_RUNNER_CONCURRENCY", "16")
	cfg, err := LoadView(templatePath, sandboxView)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sandbox.Token != "from-env" {
		t.Fatalf("视角内的键应能被环境变量覆盖，got %q", cfg.Sandbox.Token)
	}
	if cfg.Runner.Concurrency != 0 {
		t.Fatalf("视角外的键不该被环境变量带进来，got %d", cfg.Runner.Concurrency)
	}
}

// 视角里写了配置结构不认识的路径是声明写错了：照它加载，会让本该校验的键静默漏掉。
func TestLoadView_RejectsUnknownPaths(t *testing.T) {
	views := []View{{"sandboxx"}, {"runner.urls"}, {""}}
	for _, view := range views {
		if _, err := LoadView(templatePath, view); err == nil || !strings.Contains(err.Error(), "不是配置键") {
			t.Fatalf("视角 %v 含配置结构不认识的路径，应报错，got %v", view, err)
		}
	}
}

// 启动流程在子命令运行之前读数据目录与日志；读配置的进程经 ProcessView 构造视角，这两处不会漏。
func TestProcessView_CoversWhatStartupReads(t *testing.T) {
	view := ProcessView("sandbox")
	want := []string{"data_dir"}
	for _, key := range configKeys(reflect.TypeFor[AppConfig](), "") {
		if strings.HasPrefix(key, "log.") {
			want = append(want, key)
		}
	}
	for _, key := range want {
		if !view.covers(key) {
			t.Fatalf("启动流程读的键 %s 应在视角里", key)
		}
	}
	for _, key := range []string{"sandbox.listen_addr", "runner.url"} {
		if ProcessView().covers(key) {
			t.Fatalf("%s 不是启动流程读的键，不该被 ProcessView 带进来", key)
		}
	}
}
