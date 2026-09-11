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
		dbURL = "postgresql://postgres:postgres@localhost:5432/liusha?sslmode=disable"
	}

	pool, err := db.NewPgPool(ctx, dbURL, 10, 2, 10, 300)
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	defer pool.Close()

	// 创建 store
	agentStore := agent.NewStore(pool)
	skillStore := skillstore.NewStore(pool)

	// 执行种子加载
	fmt.Println("开始加载种子...")
	if err := seed.Import(ctx, ".", agentStore, skillStore); err != nil {
		log.Fatalf("种子加载失败: %v", err)
	}

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
