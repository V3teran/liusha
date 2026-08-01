package attackgraph

import (
	"context"
	"errors"
	"fmt"

	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/finding"
)

// sentinel error：ProjectMilestones 的两种业务性失败，handler 用 errors.Is 判定映射 HTTP 状态码，
// 不靠错误文案字符串匹配（文案改动不该牵动状态码）。
var (
	// ErrNoSummarizer：Projector 未注入 Summary（服务未配置 LLM）。
	ErrNoSummarizer = errors.New("attackgraph: 未配置 LLM summarizer，里程碑不可用")
	// ErrNoConversation：显式传入与自解析后 convID 均为空（纯 passive 无绑定会话）。
	ErrNoConversation = errors.New("attackgraph: 无会话（conv 为空），里程碑不可用")
)

// messagePageSize 是拉会话消息的翻页大小（思维链要全量历史，循环翻页拉尽）。
const messagePageSize = 500

// MessageLister 是投影器读会话事件流所需的最小接口（*conversation.Store 满足）。
type MessageLister interface {
	ListMessages(ctx context.Context, convID string, afterSeq int64, limit int) ([]conversation.Message, error)
}

// FindingLister 是投影器读漏洞所需的最小接口（*finding.Store 满足）。
type FindingLister interface {
	ListByTask(ctx context.Context, taskID string) ([]finding.VulnFinding, error)
}

// ConvResolver 由 *conversation.Store 满足：按 task 反解绑定的会话 id + 查扫描运行态。
// 投影器据此自解析 conv（思维链来源），前端不再需要 join 会话列表拿 conv。
type ConvResolver interface {
	ResolveConvByTask(ctx context.Context, taskID string) (string, error)
	IsRunActive(ctx context.Context, convID string) (bool, error)
}

// Projector 是 store 版执行图投影器；无状态，可全局共享一份。
//
// 它把"按 owner / 会话拉源记录"和"纯函数 Project 拼图"接起来。
// owner→conversation 的解析由 Conv（注入的 ConvResolver）在 Project 内部完成：
// 调用方只传 taskID，convID 传空即触发自解析（也可显式传 convID 覆盖）。
type Projector struct {
	Messages MessageLister
	Findings FindingLister
	// Conv 可选：注入后 Project/ProjectMilestones 在 convID 为空时按 taskID 自解析会话。
	// nil 时退化为旧行为（convID 必须由调用方传入）。
	Conv ConvResolver
	// Summary 可选：注入后支持 ProjectMilestones（LLM 里程碑摘要）。nil 时该方法报错。
	Summary Summarizer
}

// Project 拉取一次扫描的会话事件流 + 漏洞，投影成执行图。
//
//   - convID：本次扫描绑定的会话 id（思维链来源）。传空且注入了 Conv 时按 taskID 自解析；
//     解析后仍为空（纯 passive 自动路径无会话）则思维链为空，只出成果链。
//   - taskID：成果链来源（finding.ListByTask）。
func (p *Projector) Project(ctx context.Context, convID, taskID string) (Graph, error) {
	if convID == "" && p.Conv != nil {
		resolved, err := p.Conv.ResolveConvByTask(ctx, taskID)
		if err != nil {
			return Graph{}, fmt.Errorf("解析会话: %w", err)
		}
		convID = resolved
	}

	var msgs []conversation.Message
	if convID != "" {
		var err error
		msgs, err = p.allMessages(ctx, convID)
		if err != nil {
			return Graph{}, fmt.Errorf("拉会话消息: %w", err)
		}
	}

	findings, err := p.Findings.ListByTask(ctx, taskID)
	if err != nil {
		return Graph{}, fmt.Errorf("拉漏洞: %w", err)
	}

	// 运行态先查：决定投影趟数。有会话则查 task 终态；无会话（纯 passive）默认非运行（图不再增长）。
	running := false
	if convID != "" && p.Conv != nil {
		running, err = p.Conv.IsRunActive(ctx, convID)
		if err != nil {
			return Graph{}, fmt.Errorf("查运行态: %w", err)
		}
	}

	// 实时（running）出骨架，流畅不烧钱；扫描结束补 ① LLM 提炼出完整语义图（Enriched=true）。
	// 前端在 running=false 后停止轮询，故提炼只在有限次请求上触发（首屏/手动刷新）。
	var g Graph
	if running || p.Summary == nil {
		g = Project(taskID, msgs, findings)
	} else {
		g = ProjectEnriched(ctx, taskID, msgs, findings, p.Summary)
	}
	g.ConversationID = convID
	g.Running = running
	return g, nil
}

// ProjectMilestones 拉会话事件流，按子代理聚合 reasoning，调 LLM 总结成里程碑列表。
// convID 为空时按 taskID 自解析（同 Project）；解析后仍空或 Summary 未注入则返回错误
// （ErrNoSummarizer / ErrNoConversation，handler 用 errors.Is 判定，不靠错误文案字符串匹配）。
func (p *Projector) ProjectMilestones(ctx context.Context, convID, taskID string) ([]Milestone, error) {
	if p.Summary == nil {
		return nil, ErrNoSummarizer
	}
	if convID == "" && p.Conv != nil {
		resolved, err := p.Conv.ResolveConvByTask(ctx, taskID)
		if err != nil {
			return nil, fmt.Errorf("解析会话: %w", err)
		}
		convID = resolved
	}
	if convID == "" {
		return nil, ErrNoConversation
	}
	msgs, err := p.allMessages(ctx, convID)
	if err != nil {
		return nil, fmt.Errorf("拉会话消息: %w", err)
	}
	return Milestones(ctx, msgs, p.Summary)
}

// allMessages 循环翻页拉全会话消息（按 seq 升序）。
func (p *Projector) allMessages(ctx context.Context, convID string) ([]conversation.Message, error) {
	var all []conversation.Message
	var after int64
	for {
		batch, err := p.Messages.ListMessages(ctx, convID, after, messagePageSize)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < messagePageSize {
			break
		}
		after = batch[len(batch)-1].Seq
	}
	return all, nil
}
