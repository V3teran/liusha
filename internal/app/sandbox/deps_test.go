package sandbox_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// sandboxDeps 是沙盒服务端及其依赖允许牵涉的本仓包，相对 internal/。清单之外的包变了不该要求
// 重建沙盒镜像——镜像有 21 GB，只在多镜像部署与开发栈里由部署方本地构建，里面的程序可能与主
// 程序不同版本。要加进来，它得是沙盒的职责所在，或者是这些包的合理依赖且自身不再牵涉别的。
var sandboxDeps = []string{
	"app/sandbox",
	"allowlist", // 工具清单按名单过滤工具目录；零依赖的纯函数
	"config",
	"logx",
	"sandbox",
	"sandbox/browser",
	"sandbox/server",
	"tools/manifest",
}

// 沙盒镜像里编的是整个程序，但哪些变更要求重建它只看沙盒服务端真正依赖什么。沙盒服务端
// （命令执行、进程组、浏览器服务）为自己的职责之外的包背上了依赖，那些包的每次变更都会
// 迫使重建镜像——它曾为一个名单判定函数依赖工具注册表，并由此依赖模型框架与出厂自定义引擎。
func TestSandboxDependsOnlyOnItsOwnPackages(t *testing.T) {
	reached := map[string]bool{}
	var visit func(pkg string)
	visit = func(pkg string) {
		if reached[pkg] {
			return
		}
		reached[pkg] = true
		for _, dep := range internalImports(t, filepath.Join("..", "..", filepath.FromSlash(pkg))) {
			visit(dep)
		}
	}
	visit("app/sandbox")

	var got []string
	for pkg := range reached {
		got = append(got, pkg)
	}
	slices.Sort(got)
	want := slices.Clone(sandboxDeps)
	slices.Sort(want)
	if slices.Equal(got, want) {
		return
	}
	var extra, missing []string
	for _, pkg := range got {
		if !slices.Contains(want, pkg) {
			extra = append(extra, pkg)
		}
	}
	for _, pkg := range want {
		if !slices.Contains(got, pkg) {
			missing = append(missing, pkg)
		}
	}
	t.Fatalf("沙盒服务端的依赖与清单不符。\n多出（这些包的变更会要求重建沙盒镜像；确实属于沙盒才登记）: %v\n缺少: %v", extra, missing)
}

// internalImports 返回 dir 下这个包的非测试文件直接引用的本仓包（相对 internal/）。各平台的文件
// 都算：依赖面按源码的全部计，不看当前平台是否编译它。
func internalImports(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var deps []string
	for _, e := range entries {
		name := e.Name()
		// Go 工具链不看以 _ 或 . 开头的文件。
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") ||
			strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if rel, ok := strings.CutPrefix(path, "github.com/V3teran/liusha/internal/"); ok {
				deps = append(deps, rel)
			}
		}
	}
	return deps
}
