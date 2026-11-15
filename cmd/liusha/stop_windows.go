//go:build windows

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/windows/svc"

	"github.com/V3teran/liusha/internal/logx"
)

// runUntilStopped 运行 fn 直到被要求停止。由服务控制管理器启动时按 Windows 服务运行：停止
// 与关机请求取消 fn 的 ctx，等 fn 收尾期间持续汇报停止进度；否则按控制台程序运行，Ctrl+C、
// 关闭控制台窗口时取消。
func runUntilStopped(name string, fn func(context.Context) error) error {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return fn(ctx)
	}
	h := &service{fn: fn}
	if err := svc.Run(name, h); err != nil {
		return err
	}
	if h.err != nil {
		// 服务没有控制台，错误只能进日志。
		logger := logx.New(name)
		logger.Error().Err(h.err).Msg("运行失败")
	}
	return h.err
}

// stopProgressInterval 是停止过程中向服务控制管理器汇报进度的间隔：每次汇报承诺在 2 倍间隔内
// 再有进展，收尾（runner 中断在途任务、沙盒退出）多久都不会被判为无响应。
const stopProgressInterval = 5 * time.Second

// service 把 fn 适配为 Windows 服务。
type service struct {
	fn  func(context.Context) error
	err error
}

func (s *service) Execute(_ []string, reqs <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.fn(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case err := <-done:
			return s.exit(err)
		case req := <-reqs:
			switch req.Cmd {
			case svc.Interrogate:
				status <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				cancel()
				return s.exit(awaitStop(done, status))
			}
		}
	}
}

// awaitStop 等 fn 收尾返回，期间按间隔汇报停止进度。
func awaitStop(done <-chan error, status chan<- svc.Status) error {
	ticker := time.NewTicker(stopProgressInterval)
	defer ticker.Stop()
	checkpoint := uint32(1)
	status <- svc.Status{State: svc.StopPending, CheckPoint: checkpoint, WaitHint: uint32(2 * stopProgressInterval / time.Millisecond)}
	for {
		select {
		case err := <-done:
			return err
		case <-ticker.C:
			checkpoint++
			status <- svc.Status{State: svc.StopPending, CheckPoint: checkpoint, WaitHint: uint32(2 * stopProgressInterval / time.Millisecond)}
		}
	}
}

// exit 记下 fn 的结果并给出服务退出码：出错时以服务自定义退出码 1 结束，服务控制管理器据此
// 按恢复策略处理（如自动重启）。
func (s *service) exit(err error) (bool, uint32) {
	s.err = err
	if err != nil {
		return true, 1
	}
	return false, 0
}
