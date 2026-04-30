package heuristic

// 本文件提供"代码确定性侦察"打分函数（hks2 共识 6 - Explorer 前置）。
//
// ingestor 收到流量后用 Score 打分，分数 < Threshold() 直接丢弃，不调 LLM。
// 100 流量典型场景：70 条静态/心跳 → 0 分丢弃；30 条入主 ReAct 队列。

import "strings"

// Flow 是打分输入的最小接口；ingestor 中实现这些方法即可。
type Flow interface {
	GetMethod() string
	GetURL() string
	GetHeader(key string) string
}

// staticExtensions 静态资源后缀；含任一 → 0 分。
var staticExtensions = []string{".css", ".js", ".png", ".jpg", ".jpeg", ".gif", ".ico", ".woff", ".woff2", ".ttf", ".svg", ".map"}

// pathSensitive 路径敏感词与对应分数（只算最高一项）。
var pathSensitive = []struct {
	sub   string
	score int
}{
	{"/admin/", 30},
	{"/sys/", 30},
	{"/api/admin/", 30},
	{"/api/user/", 20},
	{"/profile", 20},
	{"/order/", 20},
	{"/account", 20},
}

// idParams 路径或 query 含此关键词 → +25（只算最高一项）。
var idParams = []string{"uid=", "user_id=", "userId=", "account_id=", "accountId=", "order_id=", "orderId="}

// stateChangeMethods POST/PUT/DELETE/PATCH → +10。
var stateChangeMethods = map[string]bool{
	"POST": true, "PUT": true, "DELETE": true, "PATCH": true,
}

// Score 给一条流量打分。规则：
//
//	静态资源 → 0
//	否则按 path/idParam/method/auth-header 各档累加
func Score(f Flow) int {
	url := f.GetURL()

	for _, ext := range staticExtensions {
		if strings.HasSuffix(url, ext) || strings.Contains(url, ext+"?") {
			return 0
		}
	}

	s := 0
	for _, p := range pathSensitive {
		if strings.Contains(url, p.sub) {
			s += p.score
			break
		}
	}
	hasID := false
	for _, k := range idParams {
		if strings.Contains(url, k) {
			s += 25
			hasID = true
			break
		}
	}
	isStateChange := stateChangeMethods[strings.ToUpper(f.GetMethod())]
	if isStateChange {
		s += 10
	}
	// auth-header 加分仅在已有 id 参数或状态变更方法时生效；
	// 单一 GET + cookie 访问普通敏感路径不应越过 Threshold。
	if (hasID || isStateChange) && (f.GetHeader("cookie") != "" || f.GetHeader("authorization") != "") {
		s += 10
	}
	return s
}

// Threshold 入队阈值。score < threshold 不入主 ReAct 队列。
func Threshold() int { return 30 }
