package runtime

// compactor.go 实现上下文压缩：token 预算 + 被裁前缀的蒸馏纪要。
//
// 设计约束（优先级从高到低）：
//  1. 压缩永远不能弄死 ReAct 循环——蒸馏失败/超时一律降级为纯截断，Modify 自身
//     不返回错误（链上唯一 fail-fast 的仍是长度校验哨兵）。
//  2. 协议自洽——tool 结果与其父 assistant(tool_calls) 是不可再分单位，裁切点
//     不得落进组内；压缩后首条非 system 消息必须是 user（纪要/占位即 user）。
//  3. 纪要可折叠——连续压缩时旧纪要随前缀被裁，抽出折叠进新纪要，长任务多轮
//     压缩不丢早期结论。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// tailKeepRatio 以整数分数表达（1/2）：压缩触发时分配给尾部窗口的预算占比。
// 历史越近价值越高（工具现场、当前指令都在尾部），截断只从最旧的一端发生。
const (
	tailKeepNum = 1
	tailKeepDen = 2
)

// defaultCompactionBudget 是预算未知时的兜底（token）。正常链路由各引擎按
// 比例 × provider.context_window 解析；本值只兜配置读取失败的路径。
const defaultCompactionBudget = 32768

// summaryReserveTokens 是压缩纪要的预算预留基准（蒸馏产出 ≤400 字纪要的
// 输入侧留位）；预算本身很小时按 1/16 缩放让位。
const summaryReserveTokens = 1024

func summaryReserve(budget int) int {
	if r := budget / 16; r < summaryReserveTokens {
		return r
	}
	return summaryReserveTokens
}

// distillCallTimeout 是单次纪要蒸馏的时限。先于修改链的 30s 总超时到期，
// 保证蒸馏慢时走降级路径而不是耗尽链超时弄死整轮。
const distillCallTimeout = 20 * time.Second

// SummaryPrefix 是前情纪要消息的正文前缀。作用有二：向模型显式声明这段内容
// 的性质（蒸馏事实，非对话现场）；连续压缩时据此识别旧纪要并折叠。
const SummaryPrefix = "【前情纪要】"

// truncationNotice 是蒸馏不可用时的降级占位声明（承接既有语义：被截必须
// 留痕，不许静默丢前缀）。
const truncationNotice = "（更早的对话历史已按上下文预算压缩，以下为最近的执行上下文）"

// Distiller 把被裁掉的历史前缀蒸馏成一段纪要。
//
// 输出是给模型读的纯文本纪要（进展/关键观察/已试死路径/待办），不是给人看的
// 散文。prior 非空时是上一轮纪要，实现应把它折叠进新纪要而非丢弃。
type Distiller interface {
	Distill(ctx context.Context, role string, evicted []llm.Message, prior string) (string, error)
}

// ContextCompactor 按 token 预算压缩消息历史：超预算才触发；尾部保留
// tailKeep 比例的预算，被裁前缀蒸馏成纪要顶替占位声明。
//
// 无状态可并发：每次 ReAct Run 重建一份（链随 Run 重建），纪要缓存在包级
// summaryCache——同一前缀的重试直接命中，不重复烧蒸馏调用。
type ContextCompactor struct {
	budgetTokens int
	role         string
	distiller    Distiller
}

// NewContextCompactor 构造压缩器。budgetTokens<=0 用内置兜底预算。
func NewContextCompactor(budgetTokens int, role string, d Distiller) *ContextCompactor {
	if budgetTokens <= 0 {
		budgetTokens = defaultCompactionBudget
	}
	return &ContextCompactor{budgetTokens: budgetTokens, role: role, distiller: d}
}

// Modify 实现 MessageModifier。永不返回 error——一切失败都降级。
func (c *ContextCompactor) Modify(ctx context.Context, messages []llm.Message) ([]llm.Message, error) {
	if llm.EstimateMessagesTokens(messages) <= c.budgetTokens {
		return messages, nil
	}

	var system, others []llm.Message
	for _, m := range messages {
		if m.Role == llm.RoleSystem {
			system = append(system, m)
		} else {
			others = append(others, m)
		}
	}

	// 尾部预算 =（总预算 − system 开销 − 纪要预留）× tailKeep 比例：system 不
	// 参与裁切必须先行扣减，纪要预留给压缩后注入的纪要消息留位；尾部再乘
	// tailKeep 比例制造压缩落差——检查点语义的摊销收益来自「压缩后总量显著
	// 低于预算」，贴线压缩会让下一条追加消息立即再度触发（被裁前缀随之变化、
	// 纪要指纹次次 miss），蒸馏退化为每步一次。落差即摊销空间，不可省。
	// 预留随预算本身缩放（小预算让位），尾部设下限防 system 过肥时归零。
	tailBudget := (c.budgetTokens - llm.EstimateMessagesTokens(system) - summaryReserve(c.budgetTokens)) * tailKeepNum / tailKeepDen
	if minTail := c.budgetTokens / 8; tailBudget < minTail {
		tailBudget = minTail
	}
	// 从尾部向前保留：预算装不下整条历史时，最旧的消息先出窗。
	n := len(others)
	cut := n
	used := 0
	for i := n - 1; i >= 0; i-- {
		t := llm.EstimateMessageTokens(others[i])
		// 至少保留最后一条（i==n-1 无条件保留）——窗口全空的压缩没有收益。
		if used+t > tailBudget && i < n-1 {
			break
		}
		used += t
		cut = i
	}
	// 边界对齐一：裁切点不得落在 tool 消息上——其父 assistant(tool_calls) 已在
	// 被裁侧，悬空的 tool 结果会被 provider 整包拒收。整组丢弃是不可再分单位。
	for cut < n && others[cut].Role == llm.RoleTool {
		cut++
	}
	if cut >= n {
		// 预算小到连最后一条消息都装不下（且它是 tool 结果）：保住尾部完整组
		// 直通——空窗口的压缩没有收益，悬空 tool 结果会被拒收，有效性优先于
		// 预算精确。回退到尾组 leader（assistant）。
		cut = n - 1
		for cut > 0 && others[cut].Role == llm.RoleTool {
			cut--
		}
	}

	if cut == 0 {
		// 无可裁（system 本体超预算）：原样直通，provider 侧超限是明确的失败，
		// 插占位反而掩盖真因。
		return messages, nil
	}
	tail, evicted := others[cut:], others[:cut]

	// 边界对齐二：压缩发生后窗口必须从 user 轮开始——部分严格实现要求首条
	// 非 system 消息是 user。纪要/占位声明即承担该职责。
	summary := c.summarize(ctx, evicted)
	result := make([]llm.Message, 0, len(system)+len(tail)+1)
	result = append(result, system...)
	if summary != "" {
		result = append(result, llm.Message{Role: llm.RoleUser, Content: SummaryPrefix + "\n" + summary})
	} else {
		result = append(result, llm.Message{Role: llm.RoleUser, Content: truncationNotice})
	}
	result = append(result, tail...)
	return result, nil
}

// summarize 产出被裁前缀的纪要：命中缓存直取；未命中且蒸馏器在场才现蒸馏；
// 其余情况（无蒸馏器/失败/超时/空产出）返回空串 → 调用方降级占位声明。
func (c *ContextCompactor) summarize(ctx context.Context, evicted []llm.Message) string {
	prior := priorSummary(evicted)
	key := summaryCacheKey(c.role, prior, evicted)
	if s, ok := summaryCache.get(key); ok {
		return s
	}
	if c.distiller == nil {
		return ""
	}
	dctx, cancel := context.WithTimeout(ctx, distillCallTimeout)
	defer cancel()
	s, err := c.distiller.Distill(dctx, c.role, evicted, prior)
	if err != nil || strings.TrimSpace(s) == "" {
		return ""
	}
	summaryCache.put(key, s)
	return s
}

// priorSummary 抽出被裁前缀里的旧纪要（连续压缩时前一轮注入的纪要消息随
// 前缀一起出窗）：返回其正文（去前缀标记），供蒸馏器折叠成新纪要。
func priorSummary(evicted []llm.Message) string {
	for _, m := range evicted {
		if m.Role == llm.RoleUser && strings.HasPrefix(m.Content, SummaryPrefix) {
			return strings.TrimPrefix(m.Content, SummaryPrefix+"\n")
		}
	}
	return ""
}

// summaryCacheKey 以「角色 + 旧纪要 + 被裁前缀全文」为指纹——同前缀的重试与
// 相邻轮次各自成键，命中不串味。
func summaryCacheKey(role, prior string, evicted []llm.Message) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\x00%s\x00", role, prior)
	for _, m := range evicted {
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00", m.Role, m.Content)
		for _, tc := range m.ToolCalls {
			_, _ = fmt.Fprintf(h, "tc:%s:%s\x00", tc.Name, string(tc.Arguments))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// summaryCache 是纪要的进程内 LRU。压缩器与修改链按 Run 重建，缓存必须活得
// 比它们长——包级单例。容量按并发任务数 × 角色数取整：128 条足够，溢出淘汰
// 的代价只是一次重复蒸馏。
var summaryCache = newSummaryLRU(128)

// summaryLRU 是容量受限的进程内 KV（访问序淘汰，最旧者出）。容量百级，
// O(n) 触碰序足够，不值得上双向链表。
type summaryLRU struct {
	mu    sync.Mutex
	cap   int
	ents  map[string]string
	order []string // 访问序，最旧在前
}

func newSummaryLRU(capacity int) *summaryLRU {
	return &summaryLRU{cap: capacity, ents: make(map[string]string, capacity)}
}

func (l *summaryLRU) get(key string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	v, ok := l.ents[key]
	if ok {
		l.touch(key)
	}
	return v, ok
}

func (l *summaryLRU) put(key, val string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.ents[key]; !exists && len(l.ents) >= l.cap {
		delete(l.ents, l.order[0])
		l.order = l.order[1:]
	}
	l.ents[key] = val
	l.touch(key)
}

func (l *summaryLRU) touch(key string) {
	for i, k := range l.order {
		if k == key {
			l.order = append(l.order[:i], l.order[i+1:]...)
			break
		}
	}
	l.order = append(l.order, key)
}
