// Package scenario 把任务的场景快照渲染成拼在各 agent 系统提示词末尾的块。
//
// 场景是任务所处情境的说明（领域内容在此，见 internal/scenariostore）；这里只陈述数据——带
// 名称的边界标签包住正文，不附任何指导语。放在系统提示词末尾：章程、契约、工具目录跨任务
// 不变，场景随任务变，稳定内容在前利于前缀缓存；放系统提示词而非消息历史，压缩不会裁掉它。
package scenario

import "strings"

// Render 返回场景块；正文为空（无场景）返回空串。
func Render(name, body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	name = strings.NewReplacer(`"`, "'", "\n", " ").Replace(strings.TrimSpace(name))
	return `<scenario name="` + name + "\">\n" + body + "\n</scenario>"
}

// Append 把场景块追加到系统提示词末尾；块为空或提示词为空（章程缺失，由调用方报错）时原样返回。
func Append(prompt, block string) string {
	if block == "" || prompt == "" {
		return prompt
	}
	return prompt + "\n\n" + block
}
