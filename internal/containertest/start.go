// Package containertest 启动集成测试用的容器（PostgreSQL、Redis 等 testcontainers 模块）。
package containertest

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

// 回收容器启动竞态的重试：间隔与次数。
var (
	retryDelay  = 500 * time.Millisecond
	maxAttempts = 10
)

// Start 启动一个测试容器（run 包一层 tcpostgres.Run、tcredis.Run 等）。
//
// testcontainers 的回收容器（reaper，测试进程异常退出时负责清掉容器）在同一次 go test 的多个
// 包进程间共享：一个进程刚创建、尚未运行时，另一个进程找到它并等待端口，看到 created 状态即
// 按「不可重试」失败——库里复用回收容器的等待只接受 running。这个竞态在回收容器转为运行后
// 消失，所以只对它重试，其余错误照常返回。库没有导出可判别的错误类型，只能按它的固定措辞识别。
// 失败的那次若已建出容器，先清掉再重试。
func Start[C testcontainers.Container](ctx context.Context, run func(context.Context) (C, error)) (C, error) {
	for attempt := 1; ; attempt++ {
		c, err := run(ctx)
		if err == nil || !reaperStarting(err) || attempt == maxAttempts {
			return c, err
		}
		_ = testcontainers.TerminateContainer(c)
		select {
		case <-ctx.Done():
			var zero C
			return zero, errors.Join(err, ctx.Err())
		case <-time.After(retryDelay):
		}
	}
}

// reaperStarting 判断错误是否为「复用的回收容器还在 created 状态」。
func reaperStarting(err error) bool {
	s := err.Error()
	return strings.Contains(s, "wait for reaper") && strings.Contains(s, `unexpected container status "created"`)
}
