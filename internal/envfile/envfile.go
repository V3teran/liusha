// Package envfile 读写环境变量文件（KEY=VALUE 每行一条）：liusha setup 生成，各子命令经
// -env-file 读入；格式同时兼容 systemd 的 EnvironmentFile= 与 sh 的 source。
//
// 语法：空行与 # 开头的行忽略；可带 export 前缀；值可不加引号（去两端空白）、加单引号
// （原样）或加双引号（支持 \\ \" \$ \` \n 转义）。
package envfile

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// Var 是一条环境变量。
type Var struct {
	Key   string
	Value string
}

// Load 读取 path 并设置其中的环境变量；进程环境中已存在的变量不被覆盖（显式环境优先）。
func Load(path string) error {
	vars, err := Read(path)
	if err != nil {
		return err
	}
	for _, v := range vars {
		if _, set := os.LookupEnv(v.Key); set {
			continue
		}
		if err := os.Setenv(v.Key, v.Value); err != nil {
			return fmt.Errorf("设置环境变量 %s: %w", v.Key, err)
		}
	}
	return nil
}

// Read 解析 path。
func Read(path string) ([]Var, error) {
	f, err := os.Open(path) // #nosec G304 // 路径来自操作者的命令行参数
	if err != nil {
		return nil, fmt.Errorf("读取环境变量文件: %w", err)
	}
	defer func() { _ = f.Close() }()

	var vars []Var
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, raw, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !validKey(key) {
			return nil, fmt.Errorf("%s 第 %d 行不是 KEY=VALUE", path, n)
		}
		value, err := parseValue(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("%s 第 %d 行: %w", path, n, err)
		}
		vars = append(vars, Var{Key: key, Value: value})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("读取 %s: %w", path, err)
	}
	return vars, nil
}

// Create 新建 path（权限 0600）并写入 vars，每组前可带注释行。文件已存在时返回 fs.ErrExist，
// 不覆盖——其中的密钥一旦替换，用旧密钥加密的数据就无法解密。
func Create(path string, sections []Section) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 // 路径来自操作者的命令行参数
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s 已存在，不覆盖: %w", path, fs.ErrExist)
		}
		return fmt.Errorf("创建环境变量文件: %w", err)
	}
	w := bufio.NewWriter(f)
	for i, s := range sections {
		if i > 0 {
			_, _ = w.WriteString("\n")
		}
		for _, c := range s.Comments {
			_, _ = w.WriteString("# " + c + "\n")
		}
		for _, v := range s.Vars {
			if !validKey(v.Key) {
				_ = f.Close()
				return fmt.Errorf("非法变量名 %q", v.Key)
			}
			_, _ = w.WriteString(v.Key + "=" + quote(v.Value) + "\n")
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return fmt.Errorf("写入环境变量文件: %w", err)
	}
	return f.Close()
}

// Section 是一组带注释的变量。
type Section struct {
	Comments []string
	Vars     []Var
}

func validKey(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		switch {
		case r == '_', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

func parseValue(raw string) (string, error) {
	switch {
	case len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'':
		return raw[1 : len(raw)-1], nil
	case len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"':
		return unescape(raw[1 : len(raw)-1])
	case strings.HasPrefix(raw, "'") || strings.HasPrefix(raw, `"`):
		return "", errors.New("引号未闭合")
	default:
		return raw, nil
	}
}

func unescape(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if i+1 == len(s) {
			return "", errors.New("值以反斜杠结尾")
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case '\\', '"', '$', '`':
			b.WriteByte(s[i])
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String(), nil
}

// quote 以双引号写出值，转义 sh 与 systemd 在双引号内会解释的字符。
func quote(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`", "\n", `\n`)
	return `"` + r.Replace(v) + `"`
}
