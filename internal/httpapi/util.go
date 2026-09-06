package httpapi

import (
	"strconv"
)

// atoiOr 解析十进制整数，失败回退 def。
func atoiOr(s string, def int) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

// rawOrEmpty 兜底空 []byte——避免前端拿到 null 而是合法 JSON 字面量。
func rawOrEmpty(b []byte, empty string) []byte {
	if len(b) == 0 {
		return []byte(empty)
	}
	return b
}

// parsePagination 解析分页参数 page 和 size，返回标准化后的值。
// page 小于 1 时归一化为 1；size 在 [1, maxSize] 范围内 clamp，缺省为 defaultSize。
func parsePagination(pageStr, sizeStr string, defaultSize, maxSize int) (page, size int) {
	page = atoiOr(pageStr, 1)
	if page < 1 {
		page = 1
	}
	size = atoiOr(sizeStr, defaultSize)
	if size < 1 {
		size = defaultSize
	}
	if size > maxSize {
		size = maxSize
	}
	return page, size
}
