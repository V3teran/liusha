package config

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// deploymentEnv 是 .env 里给容器与 docker compose 用、liusha 不读的部署变量（见 .env.example）。
var deploymentEnv = []string{
	"LIUSHA_POSTGRES_PASSWORD", "LIUSHA_REDIS_PASSWORD",
	"LIUSHA_CONSOLE_HOST_PORT", "LIUSHA_PROXY_HOST_PORT",
	"LIUSHA_SANDBOX_HOST_PORT", "LIUSHA_POSTGRES_HOST_PORT", "LIUSHA_REDIS_HOST_PORT",
	"LIUSHA_IMAGE_REPO", "LIUSHA_IMAGE_TAG",
}

// otherEnv 是 LIUSHA_ 命名空间里另外两类不对应配置键的变量：配置文件位置与开发工具（e2e、tsecbench）的参数。
var otherEnv = []string{
	"LIUSHA_CONFIG",
	"LIUSHA_E2E_API_BASE", "LIUSHA_E2E_POLL_DEADLINE_SECONDS", "LIUSHA_E2E_PG_CONTAINER", "LIUSHA_E2E_REDIS_CONTAINER",
	"LIUSHA_TSB_API_BASE", "LIUSHA_TSB_PROBE_URL", "LIUSHA_TSB_USER", "LIUSHA_TSB_PASS", "LIUSHA_TSB_FLAG_RE",
	"LIUSHA_TSB_CONCURRENCY", "LIUSHA_TSB_POLL_SECONDS", "LIUSHA_TSB_DEADLINE_EASY_SECONDS", "LIUSHA_TSB_DEADLINE_MEDIUM_SECONDS", "LIUSHA_TSB_DEADLINE_HARD_SECONDS", "LIUSHA_TSB_IDLE_SECONDS", "LIUSHA_TSB_ONLY",
}

// configEnv 返回全部配置键对应的环境变量名（LIUSHA_<键>）。
func configEnv() map[string]bool {
	out := map[string]bool{}
	for _, k := range configKeys(reflect.TypeFor[AppConfig](), "") {
		out["LIUSHA_"+strings.ToUpper(strings.ReplaceAll(k, ".", "_"))] = true
	}
	return out
}

// LIUSHA_ 是 liusha 的命名空间：名字对上配置键的覆盖该键，其余（部署变量、配置文件位置、开发工具
// 参数）不得与任何配置键同名——否则会被当成配置覆盖、悄悄改掉 config.yaml 的值。
func TestEnvNames_DoNotShadowConfigKeys(t *testing.T) {
	keys := configEnv()
	for _, name := range append(append([]string{}, deploymentEnv...), otherEnv...) {
		if keys[name] {
			t.Errorf("%s 与配置键同名，会被当作配置覆盖", name)
		}
	}
}

// .env.example 里的每个变量（含注释掉的）都以 LIUSHA_ 开头，且要么对应配置键，要么是登记的部署变量。
func TestEnvExample_EveryVariableIsAccountedFor(t *testing.T) {
	b, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatal(err)
	}
	keys := configEnv()
	deploy := map[string]bool{}
	for _, n := range deploymentEnv {
		deploy[n] = true
	}
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^#?([A-Z][A-Z0-9_]*)=`).FindAllStringSubmatch(string(b), -1) {
		name := m[1]
		seen[name] = true
		if !strings.HasPrefix(name, "LIUSHA_") {
			t.Errorf(".env.example 的 %s 不带 LIUSHA_ 前缀", name)
			continue
		}
		if !keys[name] && !deploy[name] {
			t.Errorf(".env.example 的 %s 既不是配置键，也不是登记的部署变量", name)
		}
	}
	for _, n := range deploymentEnv {
		if !seen[n] {
			t.Errorf("登记的部署变量 %s 不在 .env.example 里", n)
		}
	}
}
