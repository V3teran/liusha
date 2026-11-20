package db

import (
	"bytes"
	"encoding/json"
)

// scrubMarker 是 JSON 文本里 NUL 的转义形式：合法 JSON（编码器对字符串中的 NUL
// 产出该转义），但 PostgreSQL jsonb 拒收（SQLSTATE 22P05，unsupported Unicode
// escape sequence）。
var scrubMarker = []byte(`\u0000`)

// ScrubJSONB 洗去 JSON 字节里 PostgreSQL jsonb 不收的字符：字符串值中的 NUL。
// 复现证据与配方捕获的是工具输出和响应原文，二进制内容混入 NUL 是常态而非异常；
// 不清洗时整个写入被数据库拒绝（实测：验证审计因证据含 NUL 整条丢失）。
//
// 字符串值剔除 NUL，其余内容原样保留；不含 NUL 时零拷贝返回。裸 0x00 字节本身
// 不是合法 JSON，出现即解析失败、按原样返回——让数据库的合法性校验给出真实错误，
// 不在边界上掩盖本已损坏的数据。
func ScrubJSONB(raw []byte) []byte {
	if len(raw) == 0 {
		return raw
	}
	if bytes.IndexByte(raw, 0) < 0 && !bytes.Contains(raw, scrubMarker) {
		return raw
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return raw
	}
	scrubValue(&v)
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

// scrubValue 就地剔除字符串值中的 NUL（对象、数组递归）。
func scrubValue(v *any) {
	switch x := (*v).(type) {
	case string:
		if bytes.IndexByte([]byte(x), 0) >= 0 {
			// 保持 string 类型：换成 []byte 会被编码器当字节串 base64
			*v = string(bytes.ReplaceAll([]byte(x), []byte{0}, nil))
		}
	case map[string]any:
		for k, e := range x {
			if bytes.IndexByte([]byte(k), 0) >= 0 {
				delete(x, k)
				continue
			}
			scrubValue(&e)
			x[k] = e
		}
	case []any:
		for i := range x {
			scrubValue(&x[i])
		}
	}
}
