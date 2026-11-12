package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/sandbox"
)

// validateBrowserArgs 验证浏览器命令参数
func validateBrowserArgs(a *browserCmdArgs) *string {
	if a.Action == "" {
		msg := "drive_browser: action 必填（open/state/click/input/type/select/hover/dblclick/rightclick/scroll/back/keys/wait/eval/get/screenshot）。" +
			"浏览器驱动循环：open URL → state（带编号 DOM）→ click/input 编号 → state 复查"
		return &msg
	}
	if a.TimeoutSeconds <= 0 {
		a.TimeoutSeconds = defaultCommandTimeout
	}
	return nil
}

// buildBrowserCommand 构建浏览器命令
func (t *driveBrowserTool) buildBrowserCommand(ctx context.Context, a *browserCmdArgs) (string, error) {
	// 按子命令拼接参数
	argParts, errMsg := a.buildArgParts()
	if errMsg != "" {
		return "", fmt.Errorf("%s", errMsg)
	}

	// 构建基础命令
	command := "browser-use " + a.Action
	if argParts != "" {
		command += " " + argParts
	}

	// 注入身份和动作 ID
	command = fmt.Sprintf("IDENTITY=%q %s", browserIdentity(t.deps.TaskID, a.Identity), command)
	if actionID := ActionIDFromContext(ctx); actionID != "" {
		command = fmt.Sprintf("AGENT_ID=%q %s", actionID, command)
	}

	return command, nil
}

// executeBrowserCommand 执行浏览器命令
func (t *driveBrowserTool) executeBrowserCommand(ctx context.Context, command string, timeoutSeconds int) (sandbox.ExecResult, error) {
	return t.deps.Sandbox.Exec(ctx, sandbox.ExecRequest{
		AssignmentID:   t.deps.AssignmentID,
		TaskID:         t.deps.TaskID,
		AgentRunID:     t.deps.AgentRunID,
		Command:        command,
		TimeoutSeconds: timeoutSeconds,
		Tag:            "drive_browser",
	})
}

// tryHealBrowserDaemon 尝试修复退化的浏览器 daemon。返回修复后的结果与 unrecovered：
// 检测到退化且两级修复后仍退化时为 true（调用方据此把本次调用记为失败）。
// 修复与重试请求复用正常执行的批次/任务/身份参数（executeBrowserCommand）。
func (t *driveBrowserTool) tryHealBrowserDaemon(ctx context.Context, command string, res sandbox.ExecResult) (sandbox.ExecResult, bool) {
	if !browserDegraded(res) {
		return res, false
	}

	var log strings.Builder
	log.WriteString("[自愈] 检测到 browser daemon 退化态\n")

	// 两级恢复策略
	healSteps := []struct {
		name string
		cmd  string
	}{
		{"L1 reset", "browser-use reset"},
		{"L2 杀 daemon+chromium 冷启动", "pkill -f browser-svc.py; pkill -f chromium; true"},
	}

	recovered := false
	for _, heal := range healSteps {
		// 执行修复命令（失败原因进日志，供 LLM 与审计判断）
		h, hErr := t.executeBrowserCommand(ctx, heal.cmd, defaultCommandTimeout)
		if hErr != nil {
			fmt.Fprintf(&log, "--- %s 执行失败: %v ---\n", heal.name, hErr)
		} else {
			fmt.Fprintf(&log, "--- %s ---\n%s\n", heal.name, orDash(h.Stdout))
		}

		// 重试原命令
		retry, rErr := t.executeBrowserCommand(ctx, command, defaultCommandTimeout)
		if rErr != nil {
			fmt.Fprintf(&log, "--- %s 后重试执行失败: %v ---\n", heal.name, rErr)
		}

		if rErr == nil && !browserDegraded(retry) {
			res = retry
			recovered = true
			break
		}
		fmt.Fprintf(&log, "--- %s 后重试仍退化 ---\n", heal.name)
	}

	// 更新输出
	if recovered {
		fmt.Fprintf(&log, "--- 已恢复，重试输出 ---\n%s\n", res.Stdout)
		res.Stdout = log.String() + res.Stdout
		res.Stderr = ""
	} else {
		res.Stdout = log.String() + res.Stdout
	}

	return res, !recovered
}

// formatBrowserOutput 格式化浏览器输出
func formatBrowserOutput(res sandbox.ExecResult, timeoutSeconds int) string {
	var sb strings.Builder
	if res.Stdout != "" {
		sb.WriteString(res.Stdout)
	}
	if res.Stderr != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n--- stderr ---\n")
		}
		sb.WriteString(res.Stderr)
	}
	if res.TimedOut {
		fmt.Fprintf(&sb, "\n[超时: %ds]", timeoutSeconds)
	}
	fmt.Fprintf(&sb, "\n[exit_code: %d]", res.ExitCode)

	output := sb.String()
	// 元素编号失效是浏览器驱动的常态错误
	if strings.Contains(output, "not found - page may have changed") {
		output += "\n提示: 元素编号基于最近一次 state 快照，页面变化后即失效。请先执行 {\"action\":\"state\"} 获取最新带编号 DOM，再按新编号重试。"
	}
	return output
}

// buildBrowserResult 构建浏览器工具结果。failed（超时 / 自愈耗尽）时输出同时作为
// ToolResult.Error：运行时据 Error 识别结果性失败、累计连续失败止损——只填 Output
// 会让持续超时被计为成功。非零退出码不单独算失败（与 run_command 同口径：退出码
// 的含义由输出内容说明）。
func buildBrowserResult(output string, failed bool) registry.ToolResult {
	res := registry.ToolResult{
		Output: output,
		Signal: &registry.Signal{
			Kind:     registry.SignalCmdOutput,
			ToolName: "drive_browser",
			Content:  truncateOutput(output, 2048),
			Detail:   output,
		},
	}
	if failed {
		res.Error = output
	}
	return res
}
