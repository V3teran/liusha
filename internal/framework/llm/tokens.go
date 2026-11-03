// tokens.go 提供与模型无关的 token 估算。
//
// 消费方是各类预算场景（上下文压缩、prompt 分区渲染），它们要的不是精确
// tokenizer——引入每家 provider 的分词器既重又随模型漂移——而是一个稳定、
// 偏保守（宁可高估）的上界，保证按估算装进预算的内容在真实计费时不会超。
// 偏高的代价只是压缩略早触发；偏低的代价是 wire 请求反超模型窗口（provider
// 400、迭代失败）——代价不对称决定了必须向上侧取。估算是溢出保护的唯一
// 事实源（长度哨兵只量单条消息），口径不得放宽。
package llm

// asciiTokensPerRune 是 ASCII 文本的 token 摊销口径：现代 BPE 分词器对
// 英文/代码/JSON 的典型密度约 3~4 字符 1 token，取 3（高估侧），让预算
// 天然留有余量。
const asciiTokensPerRune = 3

// wideTokensPerRuneNum/Den 以整数分数（3/2）表达非 ASCII 文本的 token 密度
// 上界：CJK 在各家分词器的密度谱系约 0.6~1.5 token/字——provider 可配置
// 意味着分词器不可假设，取谱系上侧（×1.5）。此前按 ×1 计是中位估计而非
// 上界：中文密集历史 × 高预算占比（比例上限 0.9）时，估算贴线而真实消耗
// 可达 1.35 × 窗口，必然溢出。
const (
	wideTokensPerRuneNum = 3
	wideTokensPerRuneDen = 2
)

// EstimateTokens 估算一段文本的 token 数：非 ASCII 按 1.5 token/字（向上
// 取整，见 wideTokensPerRune 常量注释），ASCII 按 asciiTokensPerRune 摊销。
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	ascii, wide := 0, 0
	for _, r := range text {
		if r < 0x80 {
			ascii++
		} else {
			wide++
		}
	}
	return (wide*wideTokensPerRuneNum+wideTokensPerRuneDen-1)/wideTokensPerRuneDen +
		(ascii+asciiTokensPerRune-1)/asciiTokensPerRune
}

// EstimateMessageTokens 估算单条消息的 token 数：正文 + 扩展思考 + 工具调用
// 名与参数的合计。思考块随多轮 tool_use 规范回传（thinking_budget 是既有
// 配置，非边角），真实消耗上下文——不计入会让预算系统性低估、压缩触发
// 过晚，wire 请求反超模型窗口。参数是模型产出/消费的 wire 内容，计费时
// 同样真实存在。
func EstimateMessageTokens(m Message) int {
	n := EstimateTokens(m.Content)
	if m.Thinking != nil {
		n += EstimateTokens(m.Thinking.Text)
	}
	for _, tc := range m.ToolCalls {
		n += EstimateTokens(tc.Name) + EstimateTokens(string(tc.Arguments))
	}
	return n
}

// EstimateMessagesTokens 估算整段消息历史的 token 数。
func EstimateMessagesTokens(messages []Message) int {
	total := 0
	for _, m := range messages {
		total += EstimateMessageTokens(m)
	}
	return total
}
