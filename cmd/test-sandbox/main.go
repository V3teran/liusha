package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/rs/zerolog"
)

func main() {
	ctx := context.Background()

	// 创建 logger
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()

	// 创建 WarmPoolManager
	launcher := &sandbox.DockerLauncher{
		Image: "ghcr.io/v3teran/liusha-pentools:latest",
	}

	config := sandbox.WarmPoolConfig{
		IdleTimeout:       30 * time.Minute,
		HealthCheckPeriod: 30 * time.Second,
	}

	mgr := sandbox.NewWarmPoolManager(launcher, logger, config)

	fmt.Println("🚀 测试开始: Warm Pool Sandbox")
	fmt.Println("====================================================")

	// 启动预热容器
	start := time.Now()
	fmt.Printf("\n[%s] 1. 启动预热容器...\n", time.Now().Format("15:04:05"))
	if err := mgr.EnsureRunning(ctx); err != nil {
		fmt.Printf("❌ 启动失败: %v\n", err)
		return
	}
	fmt.Printf("✅ 预热容器启动成功 (耗时: %v)\n", time.Since(start))

	// 第一次 Acquire（应该立即返回）
	start = time.Now()
	fmt.Printf("\n[%s] 2. 第一次 Acquire (应该 0 延迟)...\n", time.Now().Format("15:04:05"))
	sb1, err := mgr.Acquire(ctx, sandbox.AcquireRequest{TaskID: "task-001"})
	if err != nil {
		fmt.Printf("❌ Acquire 失败: %v\n", err)
		return
	}
	acquireTime1 := time.Since(start)
	fmt.Printf("✅ Acquire 成功 (耗时: %v)\n", acquireTime1)
	fmt.Printf("   Container ID: %s\n", sb1.ID)
	fmt.Printf("   Task ID: %s\n", sb1.TaskID)

	// 执行命令测试
	fmt.Printf("\n[%s] 3. 执行测试命令...\n", time.Now().Format("15:04:05"))
	result, err := sb1.Client.Exec(ctx, sandbox.ExecRequest{
		TaskID:         "task-001",
		AgentID:        "agent-001",
		Command:        "echo 'Hello from warm pool!' && uname -a",
		TimeoutSeconds: 10,
	})
	if err != nil {
		fmt.Printf("❌ 命令执行失败: %v\n", err)
	} else {
		fmt.Printf("✅ 命令执行成功\n")
		fmt.Printf("   Exit Code: %d\n", result.ExitCode)
		fmt.Printf("   Output: %s\n", result.Stdout)
	}

	// Release
	fmt.Printf("\n[%s] 4. Release sandbox...\n", time.Now().Format("15:04:05"))
	if err := mgr.Release(ctx, sb1); err != nil {
		fmt.Printf("❌ Release 失败: %v\n", err)
	} else {
		fmt.Printf("✅ Release 成功\n")
	}

	// 第二次 Acquire（测试复用）
	start = time.Now()
	fmt.Printf("\n[%s] 5. 第二次 Acquire (测试复用)...\n", time.Now().Format("15:04:05"))
	sb2, err := mgr.Acquire(ctx, sandbox.AcquireRequest{TaskID: "task-002"})
	if err != nil {
		fmt.Printf("❌ Acquire 失败: %v\n", err)
		return
	}
	acquireTime2 := time.Since(start)
	fmt.Printf("✅ Acquire 成功 (耗时: %v)\n", acquireTime2)

	// 执行第二个命令
	fmt.Printf("\n[%s] 6. 执行第二个测试命令...\n", time.Now().Format("15:04:05"))
	result2, err := sb2.Client.Exec(ctx, sandbox.ExecRequest{
		TaskID:         "task-002",
		AgentID:        "agent-002",
		Command:        "ls /liusha/ 2>/dev/null || echo 'liusha dir not found'",
		TimeoutSeconds: 10,
	})
	if err != nil {
		fmt.Printf("❌ 命令执行失败: %v\n", err)
	} else {
		fmt.Printf("✅ 命令执行成功\n")
		fmt.Printf("   Output: %s\n", result2.Stdout)
	}

	// Release
	fmt.Printf("\n[%s] 7. Release sandbox...\n", time.Now().Format("15:04:05"))
	if err := mgr.Release(ctx, sb2); err != nil {
		fmt.Printf("❌ Release 失败: %v\n", err)
	} else {
		fmt.Printf("✅ Release 成功\n")
	}

	// 输出总结
	fmt.Println("\n====================================================")
	fmt.Println("📊 测试总结:")
	fmt.Printf("   首次 Acquire 延迟: %v\n", acquireTime1)
	fmt.Printf("   第二次 Acquire 延迟: %v (复用)\n", acquireTime2)

	if acquireTime1 < 500*time.Millisecond && acquireTime2 < 500*time.Millisecond {
		fmt.Println("\n🎉 热池模式工作正常！启动延迟接近 0！")
	} else {
		fmt.Println("\n⚠️  延迟较高，可能存在问题")
	}
}
