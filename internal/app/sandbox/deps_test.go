package sandbox_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/app/sandbox"
	"github.com/V3teran/liusha/internal/config"
)

// 沙盒镜像里编的是整个程序，但哪些变更要求重建它只看沙盒服务端真正依赖什么。沙盒服务端
// （命令执行、进程组、浏览器服务）为自己的职责之外的包背上了依赖，那些包的每次变更都会
// 迫使重建镜像——它曾为一个名单判定函数依赖工具注册表，并由此依赖模型框架与出厂自定义引擎。
//
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

func TestSandboxDependsOnlyOnItsOwnPackages(t *testing.T) {
	reached := map[string]bool{}
	var visit func(pkg string)
	visit = func(pkg string) {
		if reached[pkg] {
			return
		}
		reached[pkg] = true
		for _, dep := range internalImports(t, parsePackage(t, filepath.Join("..", "..", filepath.FromSlash(pkg)))) {
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

// 沙盒入口读的每个配置项都在 ConfigView 里，ConfigView 列的每一项也都有人读。
//
// liusha sandbox 子命令按 ConfigView 加载配置文件：视角之外的键不读，字段是零值。入口新读一个键
// 却没登记，不会编译失败，只会在多镜像部署的沙盒容器里悄悄拿到零值；反过来，视角里留着一个
// 已经没人读的键，会让沙盒在启动时要求一个用不到的键必须存在。
func TestSandboxReadsConfigOnlyWithinItsView(t *testing.T) {
	files := parsePackage(t, ".")

	// 配置只经类型是 config.AppConfig 的形参与变量被读到。
	holders := map[string]bool{}
	isConfig := func(e ast.Expr) bool {
		sel, ok := e.(*ast.SelectorExpr)
		return ok && identifier(sel.X) == "config" && sel.Sel.Name == "AppConfig"
	}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Field:
				if isConfig(x.Type) {
					for _, name := range x.Names {
						holders[name.Name] = true
					}
				}
			case *ast.ValueSpec:
				if isConfig(x.Type) {
					for _, name := range x.Names {
						holders[name.Name] = true
					}
				}
			}
			return true
		})
	}
	if len(holders) == 0 {
		t.Fatal("本包里没有类型是 config.AppConfig 的形参或变量：入口的签名变了，这个守护要跟着改")
	}

	var sels []*ast.SelectorExpr
	called := map[*ast.SelectorExpr]bool{} // 作为调用目标的选择器：cfg.Method()
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				sels = append(sels, x)
			case *ast.CallExpr:
				if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
					called[sel] = true
				}
			}
			return true
		})
	}
	part := map[*ast.SelectorExpr]bool{} // 是更长一段链的前缀：cfg.Runner.URL 里的 cfg.Runner
	for _, sel := range sels {
		if prefix, ok := sel.X.(*ast.SelectorExpr); ok {
			part[prefix] = true
		}
	}

	var reads []string // 配置键
	for _, sel := range sels {
		chain := selectorChain(sel)
		if len(chain) < 2 || !holders[chain[0]] {
			continue
		}
		if called[sel] {
			if !dataDirMethods[chain[1]] {
				t.Errorf("沙盒入口调用了 AppConfig 的方法 %s：它读的键要登记进 ConfigView（只读数据目录的方法列在 dataDirMethods）", chain[1])
			}
			continue
		}
		if part[sel] {
			continue
		}
		key, ok := configKey(chain[1:])
		if !ok {
			t.Errorf("%s 不是 AppConfig 上的配置字段", strings.Join(chain, "."))
			continue
		}
		reads = append(reads, key)
	}

	for _, key := range reads {
		if !covered(sandbox.ConfigView, key) {
			t.Errorf("沙盒入口读了 %s，但它不在 ConfigView 里：按视角加载时它是零值", key)
		}
	}
	for _, p := range sandbox.ConfigView {
		if p == "data_dir" || p == "log" { // 启动流程读的
			continue
		}
		if !slices.ContainsFunc(reads, func(key string) bool { return covered(config.View{p}, key) }) {
			t.Errorf("ConfigView 列了 %s，但沙盒入口不读它", p)
		}
	}
}

// dataDirMethods 是 AppConfig 上只读数据目录的方法。视角经 ProcessView 构造，data_dir 总在其中。
var dataDirMethods = map[string]bool{"WorkspaceDir": true, "LogDir": true, "CertDir": true}

func identifier(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// selectorChain 把 a.b.c 还原成名字序列；链底不是标识符时返回 nil。
func selectorChain(e ast.Expr) []string {
	var chain []string
	for {
		switch x := e.(type) {
		case *ast.SelectorExpr:
			chain = append([]string{x.Sel.Name}, chain...)
			e = x.X
		case *ast.Ident:
			return append([]string{x.Name}, chain...)
		default:
			return nil
		}
	}
}

// configKey 把 AppConfig 上的字段路径（Runner.URL）换成配置键（runner.url）。
func configKey(path []string) (string, bool) {
	t := reflect.TypeFor[config.AppConfig]()
	var key string
	for _, name := range path {
		field, ok := t.FieldByName(name)
		if !ok {
			return "", false
		}
		tag, _, _ := strings.Cut(field.Tag.Get("mapstructure"), ",")
		if tag == "" || tag == "-" {
			return "", false
		}
		if key != "" {
			key += "."
		}
		key += tag
		t = field.Type
	}
	return key, true
}

// covered 报告视角里的某一项是否覆盖配置键 key（自身，或其下的键）。
func covered(view config.View, key string) bool {
	return slices.ContainsFunc(view, func(p string) bool { return key == p || strings.HasPrefix(key, p+".") })
}

const internalModule = "github.com/V3teran/liusha/internal/"

// internalImports 返回这些文件直接引用的本仓包（相对 internal/）。
func internalImports(t *testing.T, files []*ast.File) []string {
	t.Helper()
	var deps []string
	for _, f := range files {
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if rel, ok := strings.CutPrefix(path, internalModule); ok {
				deps = append(deps, rel)
			}
		}
	}
	return deps
}

// parsePackage 解析 dir 下这个包的非测试文件。各平台的文件都算：按源码的全部计，不看当前平台
// 是否编译它。
func parsePackage(t *testing.T, dir string) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		// Go 工具链不看以 _ 或 . 开头的文件。
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") ||
			strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	return files
}
