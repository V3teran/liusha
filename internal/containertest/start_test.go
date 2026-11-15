package containertest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

// 回收容器的启动竞态：同一次 go test 的另一个包进程刚创建回收容器、尚未运行，本进程复用时
// 看到 created 状态。这种错误等它运行起来即可重试成功；其它启动错误照常返回、不重试。
func TestStart(t *testing.T) {
	reaperRace := errors.New(`run postgres: generic container: create container: reaper: from container "e48a57d7": wait for reaper e48a57d7: detect internal port: check target: retries: 0, last err: unexpected container status "created"`)
	retryDelay = time.Millisecond

	t.Run("回收容器尚未运行时重试", func(t *testing.T) {
		calls := 0
		_, err := Start(context.Background(), func(context.Context) (testcontainers.Container, error) {
			calls++
			if calls < 3 {
				return nil, reaperRace
			}
			return nil, nil
		})
		if err != nil || calls != 3 {
			t.Fatalf("应重试到成功：calls=%d err=%v", calls, err)
		}
	})

	t.Run("其它错误不重试", func(t *testing.T) {
		calls := 0
		other := errors.New("pull image: no space left on device")
		_, err := Start(context.Background(), func(context.Context) (testcontainers.Container, error) {
			calls++
			return nil, other
		})
		if !errors.Is(err, other) || calls != 1 {
			t.Fatalf("其它错误应直接返回：calls=%d err=%v", calls, err)
		}
	})

	t.Run("重试有上限", func(t *testing.T) {
		calls := 0
		_, err := Start(context.Background(), func(context.Context) (testcontainers.Container, error) {
			calls++
			return nil, reaperRace
		})
		if err == nil || calls != maxAttempts {
			t.Fatalf("应在 %d 次后放弃：calls=%d err=%v", maxAttempts, calls, err)
		}
	})
}
