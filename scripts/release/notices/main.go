// notices 生成发布产物附带的第三方组件声明（THIRD_PARTY_NOTICES）。
//
//	go run ./scripts/release/notices -binary <liusha 程序> [-web <前端声明>] > THIRD_PARTY_NOTICES
//
// Go 部分以程序内嵌的构建信息为准（实际链接进程序的模块，不含仅测试用的依赖），许可证正文取自
// vendor/ 下该模块目录（逐级向上查找，覆盖子模块路径）；前端部分由 web-notices.mjs 生成后拼接。
package main

import (
	"debug/buildinfo"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// licenseNames 是许可证文件名前缀（大小写不敏感）。
var licenseNames = []string{"license", "licence", "copying", "unlicense", "notice"}

func main() {
	binary := flag.String("binary", "", "已构建的 liusha 程序")
	web := flag.String("web", "", "前端依赖声明（web-notices.mjs 的输出）")
	vendor := flag.String("vendor", "vendor", "Go vendor 目录")
	flag.Parse()
	if *binary == "" {
		fmt.Fprintln(os.Stderr, "缺少 -binary")
		os.Exit(2)
	}
	if err := run(*binary, *web, *vendor); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(binary, web, vendor string) error {
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		return fmt.Errorf("读取构建信息: %w", err)
	}
	deps := info.Deps
	sort.Slice(deps, func(i, j int) bool { return deps[i].Path < deps[j].Path })

	var b strings.Builder
	fmt.Fprintf(&b, "liusha 第三方组件声明\n\n本程序包含下列第三方组件，各自依其许可证分发。\n\n")
	fmt.Fprintf(&b, "Go 模块（%d 个）\n", len(deps))
	for _, d := range deps {
		mod := d
		if d.Replace != nil {
			mod = d.Replace
		}
		fmt.Fprintf(&b, "\n%s\n%s %s\n%s\n", strings.Repeat("=", 72), mod.Path, mod.Version, strings.Repeat("-", 72))
		text, err := licenseText(vendor, mod.Path)
		if err != nil {
			return err
		}
		b.WriteString(text)
	}
	if web != "" {
		w, err := os.ReadFile(web) // #nosec G304 // 构建脚本参数
		if err != nil {
			return fmt.Errorf("读取前端声明: %w", err)
		}
		b.WriteString("\n\n")
		b.Write(w)
	}
	_, err = os.Stdout.WriteString(b.String())
	return err
}

// licenseText 返回模块的许可证正文：从 vendor/<模块路径> 逐级向上找许可证文件。
func licenseText(vendor, module string) (string, error) {
	for dir := module; dir != "." && dir != "/"; dir = filepath.Dir(dir) {
		entries, err := os.ReadDir(filepath.Join(vendor, dir))
		if err != nil {
			continue
		}
		var texts []string
		for _, e := range entries {
			if e.IsDir() || !isLicenseFile(e.Name()) {
				continue
			}
			body, err := os.ReadFile(filepath.Join(vendor, dir, e.Name())) // #nosec G304 // vendor 内文件
			if err != nil {
				return "", fmt.Errorf("读取 %s 的许可证: %w", module, err)
			}
			texts = append(texts, strings.TrimSpace(string(body)))
		}
		if len(texts) > 0 {
			return strings.Join(texts, "\n\n") + "\n", nil
		}
	}
	return fmt.Sprintf("（该模块的源码未附许可证文件，见 https://%s）\n", module), nil
}

func isLicenseFile(name string) bool {
	lower := strings.ToLower(name)
	for _, p := range licenseNames {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}
