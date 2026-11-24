package llm

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// retryAtPattern 匹配上游在错误体里写明的重置时刻，如「您的限额将在 2026-11-23 19:47:33 重置」。
// 各家网关的措辞没有标准，这里只登记实测见过的写法；新的写法按需增补，认不出的按
// 「上游没有告知」处理。
var retryAtPattern = regexp.MustCompile(`(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})`)

// retryAtLayout 是 retryAtPattern 捕获内容的布局。
const retryAtLayout = "2006-01-02 15:04:05"

// retryAtLocation 是不带时区的重置时刻所在的时区。国内网关写的本地时间一律按北京时间读：
// 按 UTC 读会差 8 小时——睡过头比多撞几次贵得多。
var retryAtLocation = time.FixedZone("CST", 8*60*60)

// retryAfterHeader 解析 Retry-After 响应头（HTTP 标准）：延迟秒数或绝对日期。
// 头缺失或无法解析时返回零值。
func retryAfterHeader(h http.Header, now time.Time) time.Time {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return time.Time{}
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return time.Time{}
		}
		return now.Add(time.Duration(secs) * time.Second)
	}
	if at, err := http.ParseTime(v); err == nil {
		return at
	}
	return time.Time{}
}

// retryAtFromMessage 从错误消息里找出上游写明的重置时刻。找不到、或解析出的时刻
// 已经过去，都返回零值：已经过去的时刻不构成等待依据，调用方按退避表重试即可。
func retryAtFromMessage(msg string, now time.Time) time.Time {
	m := retryAtPattern.FindStringSubmatch(msg)
	if m == nil {
		return time.Time{}
	}
	at, err := time.ParseInLocation(retryAtLayout, m[1], retryAtLocation)
	if err != nil || !at.After(now) {
		return time.Time{}
	}
	return at
}

// retryAtOf 取一次限流响应告知的恢复时刻：标准的 Retry-After 头优先，其次是错误体里
// 写明的重置时间。都没有时返回零值。
func retryAtOf(header http.Header, msg string, now time.Time) time.Time {
	if at := retryAfterHeader(header, now); !at.IsZero() {
		return at
	}
	return retryAtFromMessage(msg, now)
}

// RetryAfter 返回这个错误要求调用方至少等待的时长；上游没有告知时返回 false。
// 只对限流（429）有意义：其它错误码即使带了 Retry-After 也不构成等待依据。
func RetryAfter(err error, now time.Time) (time.Duration, bool) {
	var he *HTTPError
	if !errors.As(err, &he) || he.Code != http.StatusTooManyRequests || he.RetryAt.IsZero() {
		return 0, false
	}
	d := he.RetryAt.Sub(now)
	if d <= 0 {
		return 0, false
	}
	return d, true
}
