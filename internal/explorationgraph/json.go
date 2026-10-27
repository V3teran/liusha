package explorationgraph

import "encoding/json"

// MustJSON 把节点/边的内存 content 序列化为 jsonb 载荷。
//
// 单一权威：此前 evaluator 与 cognition 各持一份私有实现（一份失败回 `{}`、
// 一份裸 `_ =`），语义分叉。调用方写入的是确定性的内存 map/struct（键均为
// string，无 channel/func/环），json.Marshal 对其不会失败；失败即编程错误，
// 回空对象保节点写入不阻塞主流程。读侧对 content 的 json.Unmarshal 相应地
// 以「解析失败 = 字段零值 = 跳过」防御即可，无需逐点上抛。
func MustJSON(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
