package llm

import (
	"context"
	"errors"
	"testing"
)

func feed(events ...StreamEvent) <-chan StreamEvent {
	ch := make(chan StreamEvent, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return ch
}

// 通道关闭前没有收尾事件（provider 因 ctx 取消放弃发送错误事件）：部分正文不得被当成完整响应。
func TestCollectStream_ClosedWithoutTerminalEventFails(t *testing.T) {
	_, err := CollectStream(context.Background(), feed(StreamEvent{Kind: StreamText, Content: "半截"}), nil, nil)
	if !errors.Is(err, ErrStreamTruncated) {
		t.Fatalf("缺收尾事件应返回 ErrStreamTruncated，得 %v", err)
	}
}

func TestCollectStream_DoneEventSucceeds(t *testing.T) {
	resp, err := CollectStream(context.Background(), feed(
		StreamEvent{Kind: StreamText, Content: "完整"},
		StreamEvent{Kind: StreamDone, Usage: &Usage{OutTokens: 3}},
	), nil, nil)
	if err != nil || resp.Content != "完整" || resp.Usage.OutTokens != 3 {
		t.Fatalf("正常收尾应成功: resp=%+v err=%v", resp, err)
	}
}
