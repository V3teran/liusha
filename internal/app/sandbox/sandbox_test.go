package sandbox

import (
	"os"
	"runtime"
	"testing"
)

// 同一用户的多个沙盒实例（监听地址不同）各用各的运行目录，且目录只对本用户可访问：
// 浏览器服务的端点（端口与口令）在其中。
func TestRuntimeDir_PerListenAddressAndPrivate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", home)

	a, err := runtimeDir("127.0.0.1:8591")
	if err != nil {
		t.Fatal(err)
	}
	b, err := runtimeDir("[::1]:9591")
	if err != nil {
		t.Fatal(err)
	}
	again, err := runtimeDir("127.0.0.1:8591")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("不同监听地址共用运行目录 %s", a)
	}
	if a != again {
		t.Fatalf("同一地址重启应沿用原目录：%s != %s", a, again)
	}
	if runtime.GOOS == "windows" {
		return // 目录在用户自己的配置文件夹下，权限由其访问控制列表决定，没有模式位可查
	}
	for _, dir := range []string{a, b} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("%s 权限 = %v，期望 0700", dir, info.Mode().Perm())
		}
	}
}
