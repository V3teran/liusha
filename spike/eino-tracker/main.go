// spike/eino-tracker —— eino 迁移最小验证（throwaway，独立 module，不碰 liusha 主代码）。
//
// 验证 4 件事，趟通 = eino 在 liusha 走得通：
//   ① eino-ext openai adapter 能接小米 mimo（OpenAI 兼容）+ tool calling 正常
//   ② liusha 的工具能包成 eino tool 被正确调用（这里用 write_finding 桩，不接真 DB）
//   ③ ChatModelAgent 的 ReAct 循环跑通（reason → 调工具 → observe → 收尾）
//   ④ InferTool 从 Go struct 自动推 JSON schema（省掉手写 schema 字符串）
//
// 跑：XIAOMI_API_KEY=tp-xxx go run .
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// writeFindingArgs —— 工具入参，jsonschema tag 由 InferTool 自动转成 JSON schema
// （对比 liusha 现在手写 ParametersJSON 字符串，这里只写 struct）。
type writeFindingArgs struct {
	Type     string `json:"type"     jsonschema:"required,description=漏洞类型，如 SQLi/XSS/BAC"`
	Path     string `json:"path"     jsonschema:"required,description=漏洞所在 URL path"`
	Evidence string `json:"evidence" jsonschema:"required,description=证据/复现关键片段"`
}

func main() {
	ctx := context.Background()
	apiKey := os.Getenv("XIAOMI_API_KEY")
	if apiKey == "" {
		fmt.Println("✗ 请设置 XIAOMI_API_KEY")
		os.Exit(1)
	}

	// ① ChatModel：eino-ext openai 接小米 mimo-v2.5（OpenAI 兼容端点）
	cm, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		APIKey:  apiKey,
		BaseURL: "https://token-plan-cn.xiaomimimo.com/v1",
		Model:   "mimo-v2.5",
	})
	if err != nil {
		fmt.Println("✗ ChatModel 构造失败:", err)
		os.Exit(1)
	}

	// ② + ④ 工具：InferTool 从 struct 自动推 schema；命中漏洞时 LLM 会调它
	writeFinding, err := utils.InferTool(
		"write_finding", "把挖到的漏洞落库（type/path/evidence 三字段）",
		func(_ context.Context, in writeFindingArgs) (string, error) {
			fmt.Printf("\n  >>> 【工具被调用】write_finding  type=%s  path=%s\n      evidence=%q\n\n",
				in.Type, in.Path, in.Evidence)
			return `{"saved":true,"id":"spike-001"}`, nil
		})
	if err != nil {
		fmt.Println("✗ InferTool 失败:", err)
		os.Exit(1)
	}

	// ③ ChatModelAgent —— 对应 liusha 的 tracker（单 agent + 工具 + ReAct 循环）
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "tracker",
		Description: "passive 侦察兵：分析一条流量挖漏洞",
		Instruction: "你是渗透测试侦察兵。分析给你的一条 HTTP 流量（先看响应再回看请求），" +
			"判断涉及的漏洞类型；命中就调 write_finding 落库；确实无洞就直接文字说明。" +
			"每步先 reason 一句话再行动。",
		Model: cm,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{writeFinding},
			},
		},
		MaxIterations: 10,
	})
	if err != nil {
		fmt.Println("✗ NewChatModelAgent 失败:", err)
		os.Exit(1)
	}

	// 跑一条带 SQLi 信号的流量（响应 500 + SQL syntax error）
	flow := "## 流量请求\nGET /user?id=1%27%20OR%20%271%27%3D%271 HTTP/1.1\nHost: target.example\n\n" +
		"## 流量响应\nHTTP/1.1 500 Internal Server Error\n\nbody: You have an error in your SQL syntax near \"OR '1'='1\" at line 1"

	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})
	iter := runner.Run(ctx, []adk.Message{schema.UserMessage(flow)})

	fmt.Println("=== eino tracker 运行（小米 mimo-v2.5）===")
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
			if mv.Message.Content != "" {
				fmt.Printf("[%s 思考/输出] %s\n", ev.AgentName, mv.Message.Content)
			}
			for _, tc := range mv.Message.ToolCalls {
				fmt.Printf("[%s 决定调工具] %s  args=%s\n", ev.AgentName, tc.Function.Name, tc.Function.Arguments)
			}
		case schema.Tool:
			fmt.Printf("[工具返回 %s] %s\n", mv.ToolName, mv.Message.Content)
		}
	}
	fmt.Println("\n=== 完成 ===")
}
