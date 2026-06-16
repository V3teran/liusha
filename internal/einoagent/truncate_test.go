package einoagent

import (
	"testing"
	"unicode/utf8"
)

// TestTruncate_UTF8Safe 防回归：按字节截断会切断多字节 UTF-8 字符（中文），
// 产生非法 UTF-8 → PG 落库报 invalid byte sequence。truncate 必须返回合法 UTF-8。
func TestTruncate_UTF8Safe(t *testing.T) {
	// "你好世界" 每个汉字 3 字节，共 12 字节；截到 7 字节会切在"世"中间。
	if got := truncate("你好世界", 7); !utf8.ValidString(got) {
		t.Errorf("中文按字节截断后应仍是合法 UTF-8，得 %q", got)
	}
	// 工具输出含真正的非 UTF-8 字节（二进制）也应被清洗成合法 UTF-8。
	if got := truncate("ok\xe6\xff\xfeq", 100); !utf8.ValidString(got) {
		t.Errorf("非 UTF-8 字节应被清洗，得 %q", got)
	}
	// 不超长且合法的串原样返回。
	if got := truncate("hello", 100); got != "hello" {
		t.Errorf("短合法串应原样返回，得 %q", got)
	}
}
