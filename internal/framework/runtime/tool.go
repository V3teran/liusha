package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/registry"
)

func (r *reactRuntime) parseLLMResponse(response llm.Response) (*llmResponse, error) {
	toolCalls, malformed := normalizeToolCalls(response.ToolCalls)
	return &llmResponse{
		Content:       response.Content,
		ToolCalls:     toolCalls,
		MalformedArgs: malformed,
		Thinking:      response.Thinking,
		Usage:         response.Usage,
		FinishReason:  response.FinishReason,
	}, nil
}

// normalizeToolCalls 规整模型生成的工具参数，返回新切片（不改入参）。参数既要交给工具
// 执行，也要随 assistant 消息进历史、下一轮原样回传给 provider——非法 JSON 回传会让
// 整个请求体非法，provider 以 400 拒绝，整段 ReAct 失败。因此：
//   - 能确定性修复的（转义噪声、尾逗号）修复后执行，历史里存修复后的参数；
//   - 修不好的（如流式截断）不执行，历史里以 {} 占位，错误经工具结果回给模型重试。
func normalizeToolCalls(calls []llm.ToolCall) ([]llm.ToolCall, map[string]error) {
	if len(calls) == 0 {
		return calls, nil
	}
	out := make([]llm.ToolCall, len(calls))
	var malformed map[string]error
	for i, tc := range calls {
		args := tc.Arguments
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		args = llm.SanitizeJSON(args)
		var probe any
		if err := json.Unmarshal(args, &probe); err != nil {
			if malformed == nil {
				malformed = make(map[string]error)
			}
			malformed[tc.ID] = err
			args = json.RawMessage(`{}`)
		}
		out[i] = llm.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: args}
	}
	return out, malformed
}

// thinkingText nil-safe 取思考原文。
func thinkingText(t *llm.ThinkingBlock) string {
	if t == nil {
		return ""
	}
	return t.Text
}

// hasTools 报告本次运行有没有注册任何工具：一个都没有时，模型的工具调用无从执行。
func (r *reactRuntime) hasTools() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools) > 0
}

// executeToolCalls 执行这一轮的工具调用，把结果逐条写进历史。返回 true 表示循环到此为止：
// 外部中止（每次执行前都探测——浏览器 / 长命令执行期间的外部中止即刻生效），或本次运行一个
// 工具也没有注册。
//
// 这一步的结果先收齐、过尺寸再写进历史：进上下文之前只有这一道窗口——单条结果不超过
// config.ToolOutputWindowBytes、一步的合计有上限（见 fitStepOutputs），说明省掉了多少。
// 工具自己不裁剪：裁剪一分散到各处，同一份输出就会被不同的地方按不同的方向反复截。
//
// 没有工具的运行里，模型的工具调用无从执行：逐条回「没有提供工具」之后结束循环、请模型
// 收尾。把失败回给模型再等它重试换不来东西——它要么再调一次（实测规划者被追问线索交代时
// 连着两次去读探索图），要么直接写终稿，而终稿正是收尾请求要的，还带着契约与放大的输出预算。
func (r *reactRuntime) executeToolCalls(
	ctx context.Context,
	config *ReActConfig,
	iteration int,
	result *ReActResult,
	trace *IterationTrace,
	response *llmResponse,
) bool {
	toolless := !r.hasTools()
	type executed struct {
		toolCall    llm.ToolCall
		observation string // 工具交出的完整结果
		failed      bool
	}
	var batch []executed
	aborted := false
	for _, toolCall := range response.ToolCalls {
		if config.AbortCheck != nil && config.AbortCheck() {
			aborted = true
			break
		}
		var (
			observation string
			failed      bool
			err         error
		)
		if toolless {
			observation, failed = fmt.Sprintf("工具 %s 未执行：本次运行没有提供工具。", toolCall.Name), true
		} else {
			observation, failed, err = r.runToolCall(ctx, config, response, toolCall)
		}
		if err != nil {
			observation = fmt.Sprintf("工具执行失败: %s", err.Error())
			failed = true
		}
		batch = append(batch, executed{toolCall: toolCall, observation: observation, failed: failed})
	}

	observations := make([]string, len(batch))
	for i := range batch {
		observations[i] = batch[i].observation
	}
	observations = fitStepOutputs(observations, outputWindow(config))
	for i, e := range batch {
		action := &Action{
			ToolCall:  e.toolCall,
			Thought:   response.Content,
			Timestamp: time.Now().UnixMilli(),
		}
		trace.Actions = append(trace.Actions, action)

		// 回调：动作
		if config.OnAction != nil {
			config.OnAction(action)
		}

		if e.failed {
			r.consecutiveToolFailures++
		} else {
			r.consecutiveToolFailures = 0
		}

		// 回调：观察——模型看到的就是这一份
		trace.Observations = append(trace.Observations, observations[i])
		if config.OnObservation != nil {
			config.OnObservation(observations[i])
		}

		// 添加工具结果消息
		result.AddToolMessage(e.toolCall.ID, observations[i])
	}

	if aborted {
		result.AddUserMessage("（外部中止：本轮执行已被验证终止，跳过剩余工具调用）")
		trace.Status = IterationStatusCompleted
		trace.EndTime = time.Now().UnixMilli()
		return true
	}
	if toolless {
		trace.Status = IterationStatusCompleted
		trace.EndTime = time.Now().UnixMilli()
		r.finishIteration(ctx, config, iteration, result, trace)
	}
	return toolless
}

// runToolCall 执行一次工具调用。参数无法解析的调用不执行，把错误回给模型重新调用。
func (r *reactRuntime) runToolCall(ctx context.Context, config *ReActConfig, response *llmResponse, toolCall llm.ToolCall) (observation string, failed bool, err error) {
	if argErr, bad := response.MalformedArgs[toolCall.ID]; bad {
		return fmt.Sprintf("工具 %s 未执行：参数不是合法 JSON（%v）。请重新调用并给出完整的 JSON 参数。", toolCall.Name, argErr), true, nil
	}
	return r.executeTool(ctx, config, toolCall)
}

// executeTool 执行工具。failed 区分「结果性失败」（ErrorMaskInterceptor 语义：错误经
// ToolResult.Error 传递、err 为 nil——连续失败止损的计数依据，此前只看 err
// 导致生产链路永不计数、止损恒死）与成功。交出工具的完整输出，尺寸由这一步收齐后统一收
// （见 fitStepOutputs）。
func (r *reactRuntime) executeTool(ctx context.Context, config *ReActConfig, toolCall llm.ToolCall) (observation string, failed bool, err error) {
	r.mu.RLock()
	tool, exists := r.tools[toolCall.Name]
	r.mu.RUnlock()

	if !exists {
		return "", true, fmt.Errorf("工具 %s 未注册", toolCall.Name)
	}

	// 获取工具超时时间
	timeout := tool.Timeout()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// 异步执行工具 + 超时保护
	resultCh := make(chan registry.ToolResult, 1)
	errCh := make(chan error, 1)

	go func() {
		result, err := tool.Execute(ctx, toolCall.Arguments) // 已在 parseLLMResponse 规整为合法 JSON
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	// 等待结果或超时
	var result registry.ToolResult

	select {
	case result = <-resultCh:
		// 成功获取结果
	case err = <-errCh:
		return "", true, err
	case <-ctx.Done():
		return "", true, fmt.Errorf("工具 %s 执行超时（%s）", toolCall.Name, timeout)
	}

	// 结果性失败：错误文本即观察内容（链尾 ErrorMask 把错误装进结果字段）
	if result.Error != "" {
		return result.Error, true, nil
	}
	// 返回工具交出的完整输出（Output 字段已经是字符串）
	return result.Output, false, nil
}

// omittedMarker 是工具结果被窗口省掉中间部分时留在省略处的说明：省掉了多少字节。
const omittedMarker = "…[中间省略 %d 字节]…"

// outputWindow 取单条工具结果的窗口（字节）：配置值；没给（仅测试）或给得超过单条消息的硬上限时
// 取硬上限——窗口之内不再有第二道截断，它自己必须装得进长度哨兵。
func outputWindow(config *ReActConfig) int {
	if w := config.ToolOutputWindowBytes; w > 0 && w <= MaxMessageBytes {
		return w
	}
	return MaxMessageBytes
}

// windowToolOutput 把一条工具结果收进 window 字节：没超过就原样；超过了留开头与结尾各一半，
// 中间换成 omittedMarker。窗口按整条结果计，连说明在内；两处截口都对齐 UTF-8 边界。
//
// 留头尾而不只留尾：命令输出的开头是目标与参数、结尾是汇总与退出码，扫描类工具的结果按序排在
// 中间；图面读取这类结构化结果的字段顺序从头排起——哪一头单独看都不完整。被省掉的中间不丢：
// 超过窗口的命令输出由沙盒完整落盘，结果末尾写着文件路径（见 command_run）。
func windowToolOutput(out string, window int) string {
	if window <= 0 || len(out) <= window {
		return out
	}
	// 说明本身占的字节数随省略量的位数而定：省略量不会多于整条结果的长度，按它留位必然够。
	keep := window - len(fmt.Sprintf(omittedMarker, len(out)))
	if keep <= 0 {
		// 窗口小到连说明都放不下（只有测试才会这样）：只留结尾，不再省掉说明的位置。
		return tailRunes(out, window)
	}
	head, tail := headRunes(out, keep/2), tailRunes(out, keep-keep/2)
	return head + fmt.Sprintf(omittedMarker, len(out)-len(head)-len(tail)) + tail
}

// headRunes 取 s 的开头至多 n 字节，末尾落在字符边界上。
func headRunes(s string, n int) string {
	if n >= len(s) {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// tailRunes 取 s 的末尾至多 n 字节，开头落在字符边界上。
func tailRunes(s string, n int) string {
	if n >= len(s) {
		return s
	}
	start := len(s) - n
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}

// stepOutputWindows 是一步里的工具结果合计至多相当于多少条顶格的结果。
//
// 模型一步可以同时发起多个工具调用，各条结果随下一条 assistant 消息一起回传，压缩动不了这一组
// （见 compactor 的尾组直通）：单条的窗口再合理，条数一多合计仍可能超出模型装得下的范围——
// 实测执行者一步最多发起过 11 个调用。四个窗口（缺省 64 KiB 时共 256 KiB，约 8.7 万估算 token）
// 加上系统提示与目标装得进 131k 的模型，是现用在用的最小一档。窗口调得更大时，要确认模型装得下
// 四条顶格的结果。
const stepOutputWindows = 4

// fitStepOutputs 让这一步的工具结果进上下文：每条先过单条窗口，合计超过 stepOutputWindows 个
// 窗口时均分——小结果原样保留，省下的额度分给大结果（各条至多一个均分额度）。返回与入参同序。
func fitStepOutputs(outputs []string, window int) []string {
	if window <= 0 {
		return outputs
	}
	fitted := make([]string, len(outputs))
	sizes := make([]int, len(outputs))
	total := 0
	for i, out := range outputs {
		fitted[i] = windowToolOutput(out, window)
		sizes[i] = len(fitted[i])
		total += sizes[i]
	}
	if total <= stepOutputWindows*window {
		return fitted
	}
	// 均分额度：找一个额度 L，使各条取「自身长度与 L 的较小者」之后合计不超预算。
	asc := slices.Clone(sizes)
	slices.Sort(asc)
	budget, acc, level := stepOutputWindows*window, 0, 0
	for k, size := range asc {
		if remaining := len(asc) - k; acc+size*remaining >= budget {
			level = (budget - acc) / remaining
			break
		}
		acc += size
	}
	for i := range outputs {
		if sizes[i] > level {
			fitted[i] = windowToolOutput(outputs[i], level)
		}
	}
	return fitted
}
