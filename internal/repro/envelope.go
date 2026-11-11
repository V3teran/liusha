// Package repro 承载复现配方的域信封契约：归一化、矫正、分域校验。
// 它是纯函数叶子包（零内部依赖），executor 收割侧与 evaluator 裁决门共同依赖。
// 观察节点的唯一生产路径是 executor 引擎收割（ReAct 最终 JSON →
// createObservationNode），归一化在落图前统一执行。
package repro

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MinBodyContainsLen 是 body_contains 子串的最小鉴别长度：过短的子串（如 "1"/"2"/"3"）
// 几乎必然命中任意页面。写入侧与复现门共用此阈值。
const MinBodyContainsLen = 4

// NormalizeReproEnvelope 把 LLM 提交的 repro 归一化成域信封 {domain, recipe, assert} 并做分域校验。
// 容忍 LLM 的常见笔误：request/baseline 写在顶层、漏写 domain 时嗅探并包装成对应域信封；
// assert 误放进 recipe 时自动提升（返回的教学提示随工具输出回给 LLM）。
// 归一化后存储——收割层与复现门只认信封，不感知任何域内形状。
// 返回 (信封, 教学提示（可空）, 错误)。
func NormalizeReproEnvelope(repro json.RawMessage) (json.RawMessage, string, error) {
	repro = convertArrayRepro(repro)
	flex, err := parseReproEnvelope(repro)
	if err != nil {
		return nil, "", err
	}
	note, err := promoteAssertFromRecipe(&flex)
	if err != nil {
		return nil, "", err
	}

	domain := flex.Domain
	if domain == "" {
		domain = sniffReproDomain(flex)
	}

	var recipe, assert json.RawMessage
	switch domain {
	case "web":
		recipe, assert, err = normalizeWebRepro(flex)
	case "generic":
		recipe, assert, err = normalizeGenericRepro(flex)
	case "":
		return nil, "", fmt.Errorf("repro 缺少 domain 信封。\n\n" +
			"web 域：{\"domain\": \"web\", \"recipe\": {\"request\": {\"method\",\"url\",\"headers\",\"body\"}}, \"assert\": {...}}\n" +
			"generic 域（非 HTTP）：{\"domain\": \"generic\", \"recipe\": {\"steps\": \"...\"}, \"assert\": {\"description\": \"...\"}}")
	default:
		return nil, "", fmt.Errorf("未知复现域 %q（已支持: web, generic）", domain)
	}
	if err != nil {
		return nil, "", err
	}

	out, err := json.Marshal(map[string]json.RawMessage{
		"domain": json.RawMessage(`"` + domain + `"`),
		"recipe": recipe,
		"assert": assert,
	})
	if err != nil {
		return nil, "", fmt.Errorf("repro 归一化失败: %w", err)
	}
	return out, note, nil
}

// reproEnvelope 是 repro 信封的自由形状：除信封三字段外，也接住 LLM 笔误写在顶层的
// 域内字段，供归一化前嗅探与包装。
type reproEnvelope struct {
	Domain   string          `json:"domain"`
	Recipe   json.RawMessage `json:"recipe"`
	Request  json.RawMessage `json:"request"`  // web 域笔误：应在 recipe 内
	Baseline json.RawMessage `json:"baseline"` // web 域笔误：应在 recipe 内
	Assert   json.RawMessage `json:"assert"`
	Steps    string          `json:"steps"` // generic 域笔误：应在 recipe 内
}

// parseReproEnvelope 把 repro 解析为自由形状信封。
func parseReproEnvelope(repro json.RawMessage) (reproEnvelope, error) {
	var flex reproEnvelope
	if err := json.Unmarshal(repro, &flex); err != nil {
		return flex, fmt.Errorf("repro 解析失败: %w", err)
	}
	return flex, nil
}

// promoteAssertFromRecipe 处理 assert 误放进 recipe 的常见笔误（e2e 实测 12 连犯）：
// 自动提升到顶层并继续，返回教学提示让 LLM 下次写对位置。
func promoteAssertFromRecipe(flex *reproEnvelope) (string, error) {
	var recipeProbe struct {
		Assert json.RawMessage `json:"assert"`
	}
	if len(flex.Assert) > 0 || len(flex.Recipe) == 0 ||
		json.Unmarshal(flex.Recipe, &recipeProbe) != nil || len(recipeProbe.Assert) == 0 {
		return "", nil //nolint:nilerr // 宽容探测：recipe 不是对象即无可提升的 assert，交由后续分域校验报错
	}
	flex.Assert = recipeProbe.Assert
	// 从 recipe 中剔除已提升的 assert，避免双份。
	var rm map[string]json.RawMessage
	if json.Unmarshal(flex.Recipe, &rm) == nil {
		delete(rm, "assert")
		if b, mErr := json.Marshal(rm); mErr == nil {
			flex.Recipe = b
		}
	}
	return "\n\nℹ️ 注意：assert 应写在 repro 顶层（与 domain/recipe 平级），本次已自动从 recipe 中提升——下次请直接写对位置。", nil
}

// sniffReproDomain 对漏写 domain 的输入做嗅探：顶层/recipe 内 request → web，
// steps → generic（LLM 常见笔误）。
func sniffReproDomain(flex reproEnvelope) string {
	var probe struct {
		Steps   string          `json:"steps"`
		Request json.RawMessage `json:"request"`
	}
	_ = json.Unmarshal(flex.Recipe, &probe)
	switch {
	case len(flex.Request) > 0 || len(probe.Request) > 0:
		return "web"
	case flex.Steps != "" || probe.Steps != "":
		return "generic"
	default:
		return ""
	}
}

// normalizeWebRepro 归一化 web 域 repro：扁平形状包装 + assert/request 校验。
func normalizeWebRepro(flex reproEnvelope) (recipe, assert json.RawMessage, err error) {
	switch {
	case len(flex.Recipe) > 0:
		recipe = flex.Recipe
	case len(flex.Request) > 0:
		// 扁平形状（LLM 常见笔误：request/baseline 写在顶层未嵌入 recipe）→ 提进 recipe。
		wrapped := map[string]json.RawMessage{"request": flex.Request}
		if len(flex.Baseline) > 0 {
			wrapped["baseline"] = flex.Baseline
		}
		b, mErr := json.Marshal(wrapped)
		if mErr != nil {
			return nil, nil, fmt.Errorf("repro 归一化失败: %w", mErr)
		}
		recipe = b
	default:
		return nil, nil, fmt.Errorf("web 域 repro 缺少 recipe.request。\n\n" +
			"正确格式：{\"domain\": \"web\", \"recipe\": {\"request\": {\"method\": \"GET\", \"url\": \"http://host/api?id=payload\", \"headers\": {}, \"body\": \"\"}}, \"assert\": {\"body_contains\": [...]}}")
	}
	assert = flex.Assert
	if len(assert) == 0 {
		return nil, nil, fmt.Errorf("repro 缺少 assert 字段。\n\n" +
			"web 域可用字段: status_code, body_contains, body_not_contains, header_contains, min_duration_ms")
	}
	assert = coerceWebAssertShape(assert)
	if err := validateWebRequestRecipe(recipe); err != nil {
		return nil, nil, err
	}
	if err := teachWebAssertDiscriminative(assert); err != nil {
		return nil, nil, err
	}
	return recipe, assert, nil
}

// coerceWebAssertShape 对 assert 做宽容形状矫正（LLM 输出形状不可靠，写入侧
// 统一归一到复现门期望的类型）：
//   - body_contains/body_not_contains 传单个字符串时自动包装成单元素数组；
//   - header_contains 传数组（对象数组/字符串数组）时合并成 key→value map；
//   - status_code/min_duration_ms 传数字字符串时转成数字。
//
// 矫正不了的形状原样透传（门侧报错兜底）。
func coerceWebAssertShape(assert json.RawMessage) json.RawMessage {
	var raw map[string]json.RawMessage
	if json.Unmarshal(assert, &raw) != nil {
		return assert
	}
	changed := false

	// 标量 → 单元素数组（body_contains: "x" → ["x"]）
	for _, field := range []string{"body_contains", "body_not_contains"} {
		v, ok := raw[field]
		if !ok || len(v) == 0 {
			continue
		}
		trimmed := strings.TrimSpace(string(v))
		if strings.HasPrefix(trimmed, `"`) && json.Valid(v) {
			raw[field] = json.RawMessage("[" + trimmed + "]")
			changed = true
		}
	}

	// header_contains 数组 → map（复现门 Assertion 期望 key→value；
	// 形状放任会在门侧解析失败，该观察永远无法裁决——e2e 实测连烧 3 次
	// 验证预算后 invalid_recipe 出圈）
	if v, ok := raw["header_contains"]; ok && len(v) > 0 {
		if strings.HasPrefix(strings.TrimSpace(string(v)), "[") {
			if m := mergeHeaderArray(v); m != nil {
				if b, err := json.Marshal(m); err == nil {
					raw["header_contains"] = b
					changed = true
				}
			}
		}
	}

	// 数字字符串 → 数字（status_code: "200" → 200）
	for _, field := range []string{"status_code", "min_duration_ms"} {
		v, ok := raw[field]
		if !ok || len(v) == 0 {
			continue
		}
		trimmed := strings.TrimSpace(string(v))
		if len(trimmed) > 1 && trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"' {
			inner := trimmed[1 : len(trimmed)-1]
			if isJSONInt(inner) {
				raw[field] = json.RawMessage(inner)
				changed = true
			}
		}
	}

	if !changed {
		return assert
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return assert
	}
	return b
}

// mergeHeaderArray 把 header_contains 的数组形状合并成 map：
// [{"X-Foo":"a"},{"X-Bar":"b"}] / ["X-Foo: a","X-Bar=b"] → {"x-foo":"a","x-bar":"b"}。
// 键统一小写（与复现门 HTTP 头匹配口径一致）；无法辨认的元素跳过，
// 一个都拆不出时返回 nil（原样透传，让门侧报出真实形状）。
func mergeHeaderArray(v json.RawMessage) map[string]string {
	var objs []map[string]json.RawMessage
	var strs []string
	merged := map[string]string{}
	if json.Unmarshal(v, &objs) == nil {
		for _, o := range objs {
			for k, rv := range o {
				var s string
				if json.Unmarshal(rv, &s) == nil {
					merged[strings.ToLower(strings.TrimSpace(k))] = s
				}
			}
		}
	} else if json.Unmarshal(v, &strs) == nil {
		for _, s := range strs {
			if k, val, ok := splitHeaderPair(s); ok {
				merged[strings.ToLower(k)] = val
			}
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// splitHeaderPair 拆 "Key: value" / "Key=value" 头对。
func splitHeaderPair(s string) (key, val string, ok bool) {
	for _, sep := range []string{": ", ":", "="} {
		if idx := strings.Index(s, sep); idx > 0 {
			k := strings.TrimSpace(s[:idx])
			v := strings.TrimSpace(s[idx+len(sep):])
			if k != "" && v != "" {
				return k, v, true
			}
		}
	}
	return "", "", false
}

// isJSONInt 判断字符串是否为纯整数形态（status_code/min_duration_ms 用）。
func isJSONInt(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// normalizeGenericRepro 归一化 generic 域 repro：steps 非空 + assert.description 判据强制。
func normalizeGenericRepro(flex reproEnvelope) (recipe, assert json.RawMessage, err error) {
	switch {
	case len(flex.Recipe) > 0:
		recipe = flex.Recipe
	case flex.Steps != "":
		b, mErr := json.Marshal(map[string]string{"steps": flex.Steps})
		if mErr != nil {
			return nil, nil, fmt.Errorf("repro 归一化失败: %w", mErr)
		}
		recipe = b
	}
	if len(recipe) == 0 {
		return nil, nil, fmt.Errorf("generic 域 repro 缺少 recipe.steps。\n\n" +
			"正确格式：{\"domain\": \"generic\", \"recipe\": {\"steps\": \"1. 登录后台 2. 执行 ... 3. 观察 ...\"}, \"assert\": {\"description\": \"看到 X 即坐实\"}}")
	}
	var rc struct {
		Steps string `json:"steps"`
	}
	if err := json.Unmarshal(recipe, &rc); err == nil && strings.TrimSpace(rc.Steps) == "" {
		return nil, nil, fmt.Errorf("generic 域 recipe.steps 不能为空——裁决官据此自主执行，无步骤即无可复现")
	}
	assert = flex.Assert
	if len(assert) == 0 {
		return nil, nil, fmt.Errorf("repro 缺少 assert 字段。\n\n" +
			"generic 域判据：{\"description\": \"执行 steps 后观察到什么才算坐实\"}")
	}
	var ac struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(assert, &ac); err != nil || strings.TrimSpace(ac.Description) == "" {
		return nil, nil, fmt.Errorf("generic 域 assert.description 不能为空——判据缺失即橡皮图章。\n\n" +
			"正确格式：{\"description\": \"执行 steps 后观察到 X 且正常路径无法出现，即坐实\"}")
	}
	return recipe, assert, nil
}

// validateWebRequestRecipe 校验 web 域 recipe：request 四字段齐全 + URL 绝对。
func validateWebRequestRecipe(recipe json.RawMessage) error {
	var rc struct {
		Request struct {
			Method  string                 `json:"method"`
			URL     string                 `json:"url"`
			Headers map[string]interface{} `json:"headers"`
			Body    *string                `json:"body"` // 指针区分未提供与空串
		} `json:"request"`
	}
	if err := json.Unmarshal(recipe, &rc); err != nil {
		return fmt.Errorf("web 域 recipe 解析失败: %w", err)
	}
	req := rc.Request
	var missing []string
	if req.Method == "" {
		missing = append(missing, "method")
	}
	if req.URL == "" {
		missing = append(missing, "url")
	}
	if req.Headers == nil {
		missing = append(missing, "headers")
	}
	if req.Body == nil {
		missing = append(missing, "body")
	}
	if len(missing) > 0 {
		return fmt.Errorf("recipe.request 缺少必需字段: %v\n\n"+
			"request 必须四字段齐全（headers 可 {}、body 可 \"\"），url 须含 http:// 或 https://", missing)
	}
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		return fmt.Errorf("url 必须是完整 URL（包含 http:// 或 https://），当前 %q", req.URL)
	}
	return nil
}

// teachWebAssertDiscriminative 断言鉴别力的早期反馈，与复现门 assertDiscriminative
// 同一口径：只拒 body_contains 短子串（几乎必命中任意页面）。常规状态码等弱断言
// 不在此拦——鉴别力由 LLM 裁决官结合 baseline 差分与完整响应终判；在此报错只会让
// 收割侧保留未矫正的原始配方，复现门因形状错误直接判死，裁决官反而看不到。
func teachWebAssertDiscriminative(assert json.RawMessage) error {
	var ac struct {
		BodyContains []string `json:"body_contains"`
	}
	if err := json.Unmarshal(assert, &ac); err != nil {
		return nil //nolint:nilerr // 结构留给闸门裁决，写入侧不做硬失败
	}
	for _, want := range ac.BodyContains {
		if len(strings.TrimSpace(want)) < MinBodyContainsLen {
			return fmt.Errorf("无鉴别力断言——body_contains 子串 %q 过短（几乎必命中任意页面）。\n\n"+
				"断言应捕捉利用响应独有特征：报错回显/泄露数据的完整子串（如 \"SQL syntax error\"、dump 出的字段值）", want)
		}
	}
	return nil
}

// convertArrayRepro 把数组形状的历史配方（["step1", "step2", ...]）转换成
// generic 域信封——LLM 偶尔把复现步骤写成字符串列表，直接交给结构体解析
// 会失败且该观察永远无法裁决。数组内非字符串元素时原样返回（由分域校验
// 给出可行动的报错）。
func convertArrayRepro(repro json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(repro))
	if !strings.HasPrefix(trimmed, "[") {
		return repro
	}
	var steps []string
	if json.Unmarshal(repro, &steps) != nil || len(steps) == 0 {
		return repro
	}
	envelope := map[string]interface{}{
		"domain": "generic",
		"recipe": map[string]string{"steps": strings.Join(steps, "\n")},
		"assert": map[string]string{"description": "按步骤执行后观察到配方声称的漏洞特征"},
	}
	b, err := json.Marshal(envelope)
	if err != nil {
		return repro
	}
	return b
}
