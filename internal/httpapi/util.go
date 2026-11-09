package httpapi

import (
	"errors"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
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

// pageOf 对内存列表做可选分页：请求未带 page 时返回全部（选择器一次拉全），
// 带 page 时按 page/size 切片。total 恒为分页前的条数。
func pageOf[T any](c *gin.Context, items []T, defaultSize, maxSize int) (page []T, total int) {
	total = len(items)
	raw := c.Query("page")
	if raw == "" {
		return items, total
	}
	p, size := parsePagination(raw, c.Query("size"), defaultSize, maxSize)
	start := (p - 1) * size
	if start >= total {
		return []T{}, total
	}
	return items[start:min(start+size, total)], total
}

// matchesQuery 报告任一字段是否包含查询词（不区分大小写；空查询词恒为真）。
func matchesQuery(q string, fields ...string) bool {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return true
	}
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

// isForeignKeyViolation 判定错误是否为 DB 外键约束冲突（pg 23503）。
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
