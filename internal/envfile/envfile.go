// Package envfile 读取环境变量文件（KEY=VALUE 每行一条）：部署方由 .env.example 复制填写，
// 各子命令经 -env-file 读入；同一文件也可直接用作 systemd 的 EnvironmentFile= 或被 sh source。
//
// 语法：空行与 # 开头的行忽略；可带 export 前缀；值可不加引号（去两端空白）、
// 加单引号（原样）或加双引号（与 sh 相同：反斜杠只转义 \\ \" \$ \`）。
package envfile

import (
	"bufio"
	"errors"
	"fmt"
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
		case '\\', '"', '$', '`':
			b.WriteByte(s[i])
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String(), nil
}
