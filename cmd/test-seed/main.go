// Package main 把 skills/agents 种子离线灌入指定 DB（调试用，常规路径走 api 启动时自动导入）。
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/V3teran/liusha/internal/agent"
	"github.com/V3teran/liusha/internal/config/seed"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/skillstore"
)

func main() {
	ctx := context.Background()

	// 连接数据库
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgresql://postgres:postgres@localhost:5432/liusha?sslmode=disable" // #nosec G101 // 本地 dev 默认值，可被 LIUSHA_POSTGRES_DSN 覆盖
	}

	pool, err := db.NewPgPool(ctx, dbURL, 10, 2, 10, 300)
	if err != nil {
		pool.Close()
		log.Fatalf("连接数据库失败: %v", err)
	}

	// 创建 store
	agentStore := agent.NewStore(pool)
	skillStore := skillstore.NewStore(pool)

	// 执行种子加载
	fmt.Println("开始加载种子...")
	res, err := seed.Import(ctx, ".", agentStore, skillStore)
	if err != nil {
		fmt.Fprintf(os.Stderr, "种子加载失败: %v\n", err)
		pool.Close()
		os.Exit(1)
	}
	fmt.Printf("种子写入: agents=%v skills插入=%v skills清理=%v\n",
		res.Agents, res.Skills.Inserted, res.Skills.Pruned)

	// 验证 Agent
	fmt.Println("\n=== 验证 Agent ===")
	agents, err := agentStore.List(ctx, false) // false = 包含禁用的
	if err != nil {
		log.Fatalf("查询 Agent 失败: %v", err)
	}
	fmt.Printf("Agent 总数: %d\n", len(agents))
	for _, a := range agents {
		fmt.Printf("  - %s (%s): %s\n", a.Code, a.Kind, a.Name)
		fmt.Printf("    Skills: %v\n", a.Skills)
	}

	// 验证 Skill
	fmt.Println("\n=== 验证 Skill ===")
	skills, err := skillStore.List(ctx, skillstore.ListParams{})
	if err != nil {
		log.Fatalf("查询 Skill 失败: %v", err)
	}
	fmt.Printf("Skill 总数: %d\n", len(skills))
	for _, s := range skills {
		fmt.Printf("  - %s (%s): %s\n", s.Code, s.Category, s.Name)
	}

	fmt.Println("\n✅ 种子加载验证完成！")
}
