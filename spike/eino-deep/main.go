// spike/eino-deep —— 验证 eino deep prebuilt 对应 liusha 的 orchestrator+exploitation。
//
// 验证 3 件事，趟通 = active 多代理在 eino 走得通：
//
//	① deep（orchestrator）能把活委派给多个 sub-agent（exploitation）—— 对应 spawn_exploitation
//	② orchestrator 一轮派多个 exploitation → 并发执行（工具里 sleep+时间戳证明 window 重叠）
//	③ 子代理各自跑 ReAct（reason→调 write_finding）+ orchestrator 收集结果
//
// 跑：XIAOMI_API_KEY=tp-xxx go run .
package main

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/deep"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

type findingArgs struct {
	Type     string `json:"type"     jsonschema:"required,description=漏洞类型"`
	Path     string `json:"path"     jsonschema:"required,description=漏洞路径"`
	Evidence string `json:"evidence" jsonschema:"required,description=证据"`
}

var t0 = time.Now()

func ts() string { return fmt.Sprintf("+%.1fs", time.Since(t0).Seconds()) }

func main() {
	ctx := context.Background()
	apiKey := os.Getenv("XIAOMI_API_KEY")
	if apiKey == "" {
		fmt.Println("✗ 请设置 XIAOMI_API_KEY")
		os.Exit(1)
	}

	// 每个 agent 一个独立 ChatModel 实例——eino BindTools 会改 model 内部状态，
	// 共享一个实例跨 goroutine 并发不安全（会被串行化）。独立实例才能真并发。
	newModel := func() *openai.ChatModel {
		m, e := openai.NewChatModel(ctx, &openai.ChatModelConfig{
			APIKey:  apiKey,
			BaseURL: "https://token-plan-cn.xiaomimimo.com/v1",
			Model:   "mimo-v2.5",
		})
		if e != nil {
			fmt.Println("✗ ChatModel:", e)
			os.Exit(1)
		}
		return m
	}

	var seq int32

	// exploitation 工厂：单 agent + write_finding 桩（桩里 sleep 3s + 时间戳，用于看两 exploitation 是否并发）
	mkExploitation := func(name, desc, instr string) adk.Agent {
		wf, _ := utils.InferTool("write_finding", "把挖到的漏洞落库",
			func(_ context.Context, in findingArgs) (string, error) {
				id := atomic.AddInt32(&seq, 1)
				fmt.Printf("  >>> 【%s 工具进入】%s (id=%d type=%s path=%s)\n", name, ts(), id, in.Type, in.Path)
				time.Sleep(3 * time.Second)
				fmt.Printf("  <<< 【%s 工具退出】%s (id=%d)\n", name, ts(), id)
				return `{"saved":true}`, nil
			})
		ag, e := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
			Name: name, Description: desc, Instruction: instr, Model: newModel(),
			ToolsConfig:   adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{wf}}},
			MaxIterations: 8,
		})
		if e != nil {
			fmt.Println("✗ exploitation", name, e)
			os.Exit(1)
		}
		return ag
	}

	exploitationBAC := mkExploitation("exploitation_bac",
		"测访问控制/越权的突击手",
		"你是越权测试突击手。对分配给你的攻击面判断是否存在访问控制缺陷，确认就调 write_finding 落库。")
	exploitationXSS := mkExploitation("exploitation_xss",
		"测 XSS 的突击手",
		"你是 XSS 测试突击手。对分配给你的攻击面判断是否存在 XSS，确认就调 write_finding 落库。")

	// orchestrator = deep，挂两个 exploitation 作 sub-agent
	orchestrator, err := deep.New(ctx, &deep.Config{
		Name:        "orchestrator",
		Description: "渗透指挥官，recon 后把攻击面拆给 exploitation 子代理并发挖",
		ChatModel:   newModel(),
		Instruction: "你是渗透指挥官。你**不亲自挖洞**，而是把攻击面派给合适的 exploitation 子代理（用 task 工具）。" +
			"有多个独立攻击面时，**在同一轮里并发派多个 task**，别一个个串行。等子代理结果汇总后收尾。",
		SubAgents:              []adk.Agent{exploitationBAC, exploitationXSS},
		WithoutGeneralSubAgent: true, // 只用我这两个 exploitation
		MaxIteration:           12,
	})
	if err != nil {
		fmt.Println("✗ deep orchestrator:", err)
		os.Exit(1)
	}

	brief := "目标 http://t.example 有两个独立攻击面，请**并发**派 exploitation 各挖一个：\n" +
		"1) /admin/users —— 疑似越权（无鉴权能访问），派 exploitation_bac\n" +
		"2) /search?q= —— 参数反射，疑似 XSS，派 exploitation_xss\n" +
		"两个互不依赖，请在同一轮同时派出。"

	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: orchestrator})
	iter := runner.Run(ctx, []adk.Message{schema.UserMessage(brief)})

	fmt.Printf("=== eino deep（orchestrator+2 exploitation，小米 mimo）%s 开始 ===\n", ts())
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev.Err != nil {
			fmt.Println("✗ 运行出错:", ev.Err)
			os.Exit(1)
		}
		if ev.Output == nil || ev.Output.MessageOutput == nil {
			continue
		}
		mv := ev.Output.MessageOutput
		if mv.Message == nil {
			continue
		}
		switch mv.Role {
		case schema.Assistant:
			if c := mv.Message.Content; c != "" {
				if len(c) > 160 {
					c = c[:160] + "…"
				}
				fmt.Printf("[%s %s] %s\n", ev.AgentName, ts(), c)
			}
			for _, tc := range mv.Message.ToolCalls {
				args := tc.Function.Arguments
				if len(args) > 120 {
					args = args[:120] + "…"
				}
				fmt.Printf("[%s %s 调工具] %s  %s\n", ev.AgentName, ts(), tc.Function.Name, args)
			}
		case schema.Tool:
			fmt.Printf("[工具返回 %s %s] %s\n", mv.ToolName, ts(), mv.Message.Content)
		}
	}
	fmt.Printf("\n=== 完成 %s ===\n", ts())
	fmt.Println("（看两个 exploitation 的【工具进入/退出】时间戳：window 重叠 = 并发执行）")
}
