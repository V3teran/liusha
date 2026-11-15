package runner

import (
	"context"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"
)

// sandboxWait 是任务开始时等待沙盒可达的上限：吸收沙盒启动、重启的短暂空窗（serve 同进程
// 启动时 runner 可能先于沙盒领到积压任务；compose 里沙盒容器重启需要几秒）。
const sandboxWait = 2 * time.Minute

// sandboxRetryMax 是两次探测之间退避的上限。
const sandboxRetryMax = 15 * time.Second

// awaitInventory 查询沙盒安装状态。沙盒暂不可达（连不上、无响应、5xx）时按指数退避重试，
// 至多 wait；沙盒拒绝请求（4xx）是配置问题、不会自愈，立即返回；ctx 结束立即返回。
func awaitInventory(ctx context.Context, c sandbox.Client, wait, firstDelay time.Duration) (sandbox.Inventory, error) {
	deadline := time.Now().Add(wait)
	delay := firstDelay
	for {
		inv, err := c.Inventory(ctx)
		if err == nil || !sandbox.Unavailable(err) || ctx.Err() != nil {
			return inv, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return inv, err
		}
		timer := time.NewTimer(min(delay, remaining))
		select {
		case <-ctx.Done():
			timer.Stop()
			return inv, err
		case <-timer.C:
		}
		delay = min(delay*2, sandboxRetryMax)
	}
}
