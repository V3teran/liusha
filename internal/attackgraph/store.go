package attackgraph

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/finding"
)

// messagePageSize 是拉对话消息的翻页大小（思维链要全量历史，循环翻页拉尽）。
const messagePageSize = 500

// MessageLister 是投影器读对话事件流所需的最小接口（*conversation.Store 满足）。
type MessageLister interface {
	ListMessages(ctx context.Context, convID string, afterSeq int64, limit int) ([]conversation.Message, error)
}

// FindingLister 是投影器读漏洞所需的最小接口（*finding.Store 满足）。
type FindingLister interface {
	ListByOwner(ctx context.Context, ownerType, ownerID string) ([]finding.VulnFinding, error)
}

// Projector 是 store 版执行图投影器；无状态，可全局共享一份。
//
// 它把"按 owner / 对话拉源记录"和"纯函数 Project 拼图"接起来。
// owner→conversation 的解析在调用方（API handler）做：传入 convID + (ownerType, ownerID)。
type Projector struct {
	Messages MessageLister
	Findings FindingLister
}

// Project 拉取一次扫描的对话事件流 + 漏洞，投影成执行图。
//
//   - convID：本次扫描绑定的对话 id（思维链来源）。空则思维链为空（纯 passive 自动路径可能无对话）。
//   - ownerType/ownerID：成果链来源（finding.ListByOwner）。
func (p *Projector) Project(ctx context.Context, convID, ownerType, ownerID string) (Graph, error) {
	var msgs []conversation.Message
	if convID != "" {
		var err error
		msgs, err = p.allMessages(ctx, convID)
		if err != nil {
			return Graph{}, fmt.Errorf("拉对话消息: %w", err)
		}
	}

	findings, err := p.Findings.ListByOwner(ctx, ownerType, ownerID)
	if err != nil {
		return Graph{}, fmt.Errorf("拉漏洞: %w", err)
	}

	return Project(ownerID, msgs, findings), nil
}

// allMessages 循环翻页拉全对话消息（按 seq 升序）。
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
