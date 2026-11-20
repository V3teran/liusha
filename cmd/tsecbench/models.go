package main

import "encoding/json"

// ===== TSecBench 答题 API wire 结构（见 guide_1.md §5） =====

// challenge 是 GET /openapi/v1/challenges 的单题形状。
type challenge struct {
	UniqueCode       string   `json:"unique_code"`
	Description      string   `json:"description"`
	Difficulty       string   `json:"difficulty"` // easy/medium/hard
	Level            int      `json:"level"`
	TotalScore       int      `json:"total_score"`
	FlagCount        int      `json:"flag_count"`
	CorrectFlagCount int      `json:"correct_flag_count"`
	IsCompleted      bool     `json:"is_completed"`
	ContainerStatus  string   `json:"container_status"` // pending/available/stop_pending/stopped
	ContainerAddr    []string `json:"container_addr"`   // 仅 available 时非空
}

// startResp 是 POST /challenges/start 的响应。
type startResp struct {
	UniqueCode    string   `json:"unique_code"`
	ContainerAddr []string `json:"container_addr"`
}

// submitResp 是 POST /challenges/submit 的响应。
type submitResp struct {
	Correct          bool `json:"correct"`
	Awarded          int  `json:"awarded"`
	CumulativeScore  int  `json:"cumulative_score"`
	CorrectFlagCount int  `json:"correct_flag_count"`
	TotalFlagCount   int  `json:"total_flag_count"`
	MatchedFlagIndex *int `json:"matched_flag_index"`
}

// closeResp 是 POST /challenges/close 的响应。
type closeResp struct {
	UniqueCode string `json:"unique_code"`
	Closed     bool   `json:"closed"`
}

// vpnPrecheckResp 是 VPN 预检 GET http://10.0.100.58 的响应。
type vpnPrecheckResp struct {
	ClientIP string `json:"client_ip"`
	Status   string `json:"status"` // "ok" 才算连通
	Time     string `json:"time"`
}

// ===== liusha API wire 结构 =====

// dispatchResp 是 POST /api/tasks 的响应（briefs 建任务，一个 brief 一个 task）。
type dispatchResp struct {
	Tasks []struct {
		TaskID         string `json:"task_id"`
		ConversationID string `json:"conversation_id"`
	} `json:"tasks"`
}

// taskStatusResp 是 GET /api/tasks/:id 的部分字段（只取调度所需）。
type taskStatusResp struct {
	Status string `json:"status"` // active/completed/aborted
	Paused bool   `json:"paused"`
}

// graphResp 是 GET /api/tasks/:id/exploration/graph 的响应。
type graphResp struct {
	Nodes []graphNode `json:"nodes"`
}

// graphNode 只取 flag 提取所需字段；result 的 content 是 {statement, impact, suggestion}，
// 原样透传后由 resultStatement 取陈述。
type graphNode struct {
	Kind    string          `json:"kind"` // objective/action/observation/result
	Content json.RawMessage `json:"content"`
}

// usageResp 是 GET /api/tasks/:id/usage 的部分字段（活性信号）。
type usageResp struct {
	LLMInvocations  int `json:"llm_calls"`
	ToolInvocations int `json:"tool_calls"`
}
