// Package app 编排 liusha 的组件运行。各组件在 internal/app/<组件> 下暴露
// Run(ctx, cfg) error：阻塞到 ctx 取消后优雅收尾并返回 nil，启动或运行失败返回错误。
package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Component 是一个可运行的组件。
type Component struct {
	Name string
	Run  func(ctx context.Context) error
}

// Serve 在同一进程内运行多层组件，直到 ctx 取消或任一组件退出，然后逆序逐层停止。
//
// 层表达依赖：后面的层依赖前面的层（如 runner 依赖 sandbox）。停止时先停后面的层并等它
// 完全退出，再停前面的层——runner 排空在途任务期间，它依赖的沙盒仍在服务。
//
// 任一组件退出都会让整体停止：组件出错返回其错误；未被要求停止却返回 nil 也视为异常退出。
// 返回所有组件错误的合并。
func Serve(ctx context.Context, layers ...[]Component) error {
	type exit struct {
		name string
		err  error
	}
	total := 0
	for _, layer := range layers {
		total += len(layer)
	}
	exited := make(chan exit, total) // 容量 = 组件数：发送永不阻塞

	cancels := make([]context.CancelFunc, len(layers))
	waits := make([]*sync.WaitGroup, len(layers))
	for i, layer := range layers {
		// 各层独立取消：父 ctx 取消不直接传到组件，由下面的逆序停止逐层传达。
		layerCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		cancels[i] = cancel
		waits[i] = &sync.WaitGroup{}
		for _, c := range layer {
			waits[i].Add(1)
			go func() {
				defer waits[i].Done()
				exited <- exit{name: c.Name, err: c.Run(layerCtx)}
			}()
		}
	}

	var errs []error
	record := func(e exit, stopping bool) {
		switch {
		case e.err != nil:
			errs = append(errs, fmt.Errorf("%s: %w", e.name, e.err))
		case !stopping:
			errs = append(errs, fmt.Errorf("%s 意外退出", e.name))
		}
	}

	select {
	case <-ctx.Done():
	case e := <-exited:
		record(e, false)
	}
	for i := len(layers) - 1; i >= 0; i-- {
		cancels[i]()
		waits[i].Wait()
	}
	close(exited)
	for e := range exited {
		record(e, true)
	}
	return errors.Join(errs...)
}
