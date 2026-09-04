package sandbox

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"
)

// SafeReleaser 提供 panic-safe 的资源释放包装器
type SafeReleaser struct {
	mgr    ContainerManager
	logger zerolog.Logger
}

// ContainerManager 定义容器管理器接口（PooledManager 和 RefCountManager 都实现）
type ContainerManager interface {
	Acquire(ctx context.Context, assignmentID string) (Client, error)
	Release(ctx context.Context, assignmentID string) error
}

// NewSafeReleaser 创建 panic-safe 包装器
func NewSafeReleaser(mgr ContainerManager, logger zerolog.Logger) *SafeReleaser {
	return &SafeReleaser{
		mgr:    mgr,
		logger: logger.With().Str("component", "safe_releaser").Logger(),
	}
}

// WithContainer 在闭包中使用容器，自动处理 Acquire/Release 和 panic 恢复
func (s *SafeReleaser) WithContainer(
	ctx context.Context,
	assignmentID string,
	fn func(client Client) error,
) (err error) {
	// Acquire 容器
	client, err := s.mgr.Acquire(ctx, assignmentID)
	if err != nil {
		return fmt.Errorf("acquire container: %w", err)
	}

	// 确保 Release（即使 panic 也会执行）
	released := false
	defer func() {
		if !released {
			if recErr := s.mgr.Release(context.Background(), assignmentID); recErr != nil {
				s.logger.Error().
					Err(recErr).
					Str("assignment_id", assignmentID).
					Msg("release container failed in defer")
			}
		}

		// 捕获 panic 并转换为 error
		if r := recover(); r != nil {
			s.logger.Error().
				Interface("panic", r).
				Str("assignment_id", assignmentID).
				Msg("panic recovered in WithContainer")

			err = fmt.Errorf("panic in container operation: %v", r)
		}
	}()

	// 执行用户闭包
	err = fn(client)

	// 提前 Release（正常路径）
	if relErr := s.mgr.Release(context.Background(), assignmentID); relErr != nil {
		s.logger.Warn().
			Err(relErr).
			Str("assignment_id", assignmentID).
			Msg("release container failed")
		// 即使 Release 失败，也返回用户闭包的错误
		if err == nil {
			err = fmt.Errorf("release container: %w", relErr)
		}
	}
	released = true

	return err
}

// AcquireWithRecovery Acquire 容器并返回一个必须调用的 release 函数
// 使用方式：
//
//	client, release, err := s.AcquireWithRecovery(ctx, assignmentID)
//	if err != nil { return err }
//	defer release()
//	// 使用 client...
func (s *SafeReleaser) AcquireWithRecovery(
	ctx context.Context,
	assignmentID string,
) (Client, func(), error) {
	client, err := s.mgr.Acquire(ctx, assignmentID)
	if err != nil {
		return nil, nil, fmt.Errorf("acquire container: %w", err)
	}

	released := false
	release := func() {
		if released {
			return
		}
		released = true

		// 捕获 panic（如果在 defer 中调用）
		if r := recover(); r != nil {
			s.logger.Error().
				Interface("panic", r).
				Str("assignment_id", assignmentID).
				Msg("panic recovered during release")
		}

		if err := s.mgr.Release(context.Background(), assignmentID); err != nil {
			s.logger.Error().
				Err(err).
				Str("assignment_id", assignmentID).
				Msg("release container failed")
		}
	}

	return client, release, nil
}
