package finding

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/V3teran/liusha/internal/evaluator"
)

// findingStore 是交付存储的最小写接口（*Store 天然满足，测试可替身）。
type findingStore interface {
	Save(ctx context.Context, f Finding) (Finding, error)
}

// ClaimSink 把通用复现门坐实的主张落成本域交付物（finding 表）。
// 领域评级与定位的全部映射在此：severity 缺省、host 从证据 URL 抽取、
// 交付载荷组装——通用复现门只透传载荷，不解释领域字段。
//
// 依赖方向：领域交付包认识通用契约（evaluator.ConfirmedClaim），
// 通用门对领域零感知。
type ClaimSink struct {
	store      findingStore
	agentRunID string // 归属 agent_run.id；空 = 无归属 run（补交付），落 NULL
	// taskHost 是主机线索全部落空时的最终兜底（任务锚定的目标主机——task 行
	// 的 target_host）。nil = 无兜底，定位不到主机的发现以空主机入账。
	taskHost func(ctx context.Context, taskID string) (string, error)
	// onSaved 是落库成功后的观察者（会话时间线的漏洞卡事件源）；
	// nil=无观察者。观察者失败不影响交付本体。
	onSaved func(ctx context.Context, f Finding)
}

// NewClaimSink 构造交付管道。
func NewClaimSink(store findingStore, agentRunID string) *ClaimSink {
	return &ClaimSink{store: store, agentRunID: agentRunID}
}

// WithTaskHostFallback 注入任务级主机兜底（链式）。坐实材料抽不出主机时查任务锚定的
// 目标主机——它是任务的权威指向；缺这层兜底，复现配方文本化后 URL 抽取全部落空的交付
// 会整批记成无主机（线上曾因此丢弃）。
func (s *ClaimSink) WithTaskHostFallback(fn func(ctx context.Context, taskID string) (string, error)) *ClaimSink {
	s.taskHost = fn
	return s
}

// WithObserver 注入落库成功后的观察者（链式）。
func (s *ClaimSink) WithObserver(fn func(ctx context.Context, f Finding)) *ClaimSink {
	s.onSaved = fn
	return s
}

// Record 落一条交付物。坐实了的主张必须存得下：每一列都收进它的取值域——评级与类型
// 归进各自的词表，主机定位不到时如实留空，陈述原样入账。唯一不落账的情形是任务行读失败
// （见 resolveHost），错误原样交还，留给补交付。
func (s *ClaimSink) Record(ctx context.Context, c evaluator.ConfirmedClaim) error {
	if s == nil || s.store == nil {
		return nil
	}

	summary := c.Statement
	if summary == "" {
		summary = fmt.Sprintf("Confirmed claim (node %s)", c.ResultNodeID)
	}
	// 中立→安全域词汇翻译发生在交付边界：priority→severity、suggestion→remediation；
	// impact 同名直传。
	severity := NormalizeSeverity(c.Priority)
	if rating := strings.ToLower(strings.TrimSpace(c.Priority)); rating != "" && !slices.Contains(Severities, rating) {
		findingLog.Info().Str("task_id", c.TaskID).Str("result_node_id", c.ResultNodeID).
			Str("severity", c.Priority).Msg("发现严重度不在词表内，按缺省档入账")
	}
	host, err := s.resolveHost(ctx, c)
	if err != nil {
		return err
	}

	// evaluation 列的证据链形态：验证结论/成色/理由提为第一层键（前端按
	// 键直取渲染），复放证据与机器断言明细收进 evidence 子对象。嵌套倾倒
	// 形态（evaluation.evaluation.*）让前端只能平铺出乱码 JSON 块——
	// 台账是给人看的，结构为消费方设计。
	evidence := map[string]interface{}{}
	if len(c.Evidence) > 0 {
		if err := json.Unmarshal(c.Evidence, &evidence); err != nil {
			evidence = map[string]interface{}{"raw": string(c.Evidence)}
		}
	}
	tier := c.Tier
	if tier == "" {
		tier = "unknown"
	}
	judgeReasoning := ""
	if r, ok := evidence["reasoning"].(string); ok {
		judgeReasoning = r
	}
	evaluation, err := json.Marshal(map[string]interface{}{
		"node_id":           c.ResultNodeID,
		"verification_id":   c.VerificationID,
		"verification_tier": tier,
		"judge_reasoning":   judgeReasoning,
		"impact":            c.Impact,
		"duration_ms":       c.DurationMs,
		"evidence":          evidence,
	})
	if err != nil {
		return fmt.Errorf("marshal evaluation: %w", err)
	}

	// target 缺席时从复现配方派生（method/url/path）——台账的定位列不该
	// 依赖 LLM 是否记得写 target；配方是机器事实，派生永不失真。
	if len(c.Target) == 0 || string(c.Target) == "{}" || string(c.Target) == "null" {
		c.Target = deriveTarget(c.Repro)
	}

	// 类型收进台账的封闭词表：评估者给出词表之外的类别（场景被改写、或没遵守词表）或没给出时
	// 归入「其他」，原值记日志备查
	class := NormalizeClass(c.Class)
	if class == ClassOther && c.Class != "" && c.Class != ClassOther {
		findingLog.Info().Str("task_id", c.TaskID).Str("result_node_id", c.ResultNodeID).
			Str("class", c.Class).Msg("发现类型不在词表内，归入其他")
	}

	var agentRunID *string
	if s.agentRunID != "" {
		agentRunID = &s.agentRunID
	}
	saved, err := s.store.Save(ctx, Finding{
		TaskID:       c.TaskID,
		ResultNodeID: c.ResultNodeID,
		AgentRunID:   agentRunID,
		Host:         host,
		Summary:      summary,
		Severity:     severity,
		Class:        class,
		Remediation:  c.Suggestion, // 处理建议随主张坐实落台账（空串由 store NULLIF 归 NULL）
		Evaluation:   evaluation,
		Target:       c.Target,
		Repro:        c.Repro,
	})
	if err != nil {
		return err
	}
	if s.onSaved != nil {
		s.onSaved(ctx, saved)
	}
	return nil
}

// resolveHost 定位发现所在的主机：坐实材料里抽得到就用抽到的，抽不到回任务锚定的目标主机。
// 任务行读失败时返回错误——此刻按空主机落账，幂等会让空主机永久留在这条记录上；不落账
// 则由补交付带着主机收账。查到了就是空的（任务目标里没有可识别的主机），主机如实留空。
func (s *ClaimSink) resolveHost(ctx context.Context, c evaluator.ConfirmedClaim) (string, error) {
	host := c.HostHint
	if host == "" || host == "unknown" {
		host = deriveHost(c.Evidence, c.Repro, c.Target)
	}
	if host != "" || s.taskHost == nil {
		return host, nil
	}
	taskHost, err := s.taskHost(ctx, c.TaskID)
	if err != nil {
		return "", fmt.Errorf("读取任务目标主机: %w", err)
	}
	if taskHost == "unknown" {
		return "", nil
	}
	return taskHost, nil
}

// deriveTarget 从复现配方的 request 派生定位四元组 {method,url,path}。
// 配方是坐实主张的机器事实——定位从它派生比信任 LLM 转述更可靠。
func deriveTarget(repro json.RawMessage) json.RawMessage {
	var recipe struct {
		Recipe struct {
			Request struct {
				Method string `json:"method"`
				URL    string `json:"url"`
			} `json:"request"`
			// Steps 是 steps 配方的自由文本步骤；首条「METHOD /path」行足以
			// 定位端点——台账的定位列不因配方形状不同而缺失。
			Steps string `json:"steps"`
		} `json:"recipe"`
	}
	if json.Unmarshal(repro, &recipe) != nil {
		return nil
	}
	if recipe.Recipe.Request.URL == "" && recipe.Recipe.Steps != "" {
		method, path, ok := firstRequestLine(recipe.Recipe.Steps)
		if !ok {
			return nil
		}
		out, _ := json.Marshal(map[string]string{"method": method, "path": path})
		return out
	}
	if recipe.Recipe.Request.URL == "" {
		return nil
	}
	u, err := url.Parse(recipe.Recipe.Request.URL)
	if err != nil {
		return nil
	}
	method := recipe.Recipe.Request.Method
	if method == "" {
		method = "GET"
	}
	out, _ := json.Marshal(map[string]string{
		"method": method,
		"url":    recipe.Recipe.Request.URL,
		"path":   u.Path,
	})
	return out
}

// firstRequestLine 从文本步骤中找首条「METHOD /path」请求行。method 限已知
// HTTP 动词（防止普通散文里碰巧出现的全大写词误配）；path 截去查询串前的
// 空白边界（dedup 生成列本就会丢弃查询串）。
func firstRequestLine(steps string) (method, path string, ok bool) {
	for _, m := range []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"} {
		for _, line := range strings.Split(steps, "\n") {
			fields := strings.Fields(line)
			for i, f := range fields {
				if f != m || i+1 >= len(fields) {
					continue
				}
				p := fields[i+1]
				if q := strings.IndexByte(p, '?'); q >= 0 {
					p = p[:q]
				}
				if p == "" || !strings.HasPrefix(p, "/") {
					continue
				}
				return m, p, true
			}
		}
	}
	return "", "", false
}

// deriveHost 从坐实材料的可用线索抽主机：证据 url → 复现配方首请求 url →
// 目标指向。地址的形态不唯一（host:port 只是最常见的一种），抽不出时落空串
// 而非占位符——空是诚实的「无此维度」，占位符是伪装的数据。
func deriveHost(evidence, repro, target json.RawMessage) string {
	var withURL struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(evidence, &withURL) == nil {
		if h := hostOf(withURL.URL); h != "" {
			return h
		}
	}
	var recipe struct {
		Recipe struct {
			Request struct {
				URL string `json:"url"`
			} `json:"request"`
		} `json:"recipe"`
	}
	if json.Unmarshal(repro, &recipe) == nil {
		if h := hostOf(recipe.Recipe.Request.URL); h != "" {
			return h
		}
	}
	var t struct {
		URL  string `json:"url"`
		Host string `json:"host"`
	}
	if json.Unmarshal(target, &t) == nil {
		if h := hostOf(t.URL); h != "" {
			return h
		}
		if t.Host != "" && t.Host != "unknown" {
			return t.Host
		}
	}
	return ""
}

func hostOf(raw string) string {
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return ""
}
