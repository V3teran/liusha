package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/traffic"
)

// 请求与响应体按文本给出：直接序列化 []byte 会变成 base64，读者按明文核对出处时会误判
// 「记录里没有」（实测一条 flag 因此被判不可复现）。
func TestAgentTrafficView_BodiesAreText(t *testing.T) {
	rec := traffic.AgentTraffic{
		ID: 7, Method: "GET", URL: "http://t/api/status?debug=1", StatusCode: 200,
		RequestHeaders:  json.RawMessage(`{"Accept":"*/*"}`),
		ResponseHeaders: json.RawMessage(`{"Content-Type":"application/json"}`),
		ResponseBody:    []byte(`{"debug":{"flag":"flag{abc}"}}`),
	}

	out, err := json.Marshal(agentTrafficView(rec))
	require.NoError(t, err)

	assert.Contains(t, string(out), `flag{abc}`)
	assert.Contains(t, string(out), `"id":"agent:7"`)
	assert.Contains(t, string(out), `"request_body":""`)
}

// 非 UTF-8 的二进制响应体不原样塞进文本：注明字节数，不让乱码进入推理。
func TestAgentTrafficView_BinaryBody(t *testing.T) {
	v := agentTrafficView(traffic.AgentTraffic{ID: 1, ResponseBody: []byte{0xff, 0xd8, 0xff, 0xe0}})
	assert.Equal(t, "（二进制内容，4 字节）", v.ResponseBody)
}
