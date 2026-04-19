package mcpserver_test

import (
	"bytes"
	"testing"

	"github.com/V3teran/liusha/internal/mcpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDockerRunner_EmptyContainerErrors(t *testing.T) {
	_, err := mcpserver.NewDockerRunner("")
	require.Error(t, err)
}

func TestNewDockerRunner_OK(t *testing.T) {
	r, err := mcpserver.NewDockerRunner("liusha-vuln-tools")
	require.NoError(t, err)
	require.NotNil(t, r)
}

// TestDockerRunner_DecodesMultiplexedStream 验证 docker exec 的多路复用帧
// （每 8 字节一个帧头）被正确解析为 stdout 和 stderr，而非带脏数据的裸字节。
func TestDockerRunner_DecodesMultiplexedStream(t *testing.T) {
	// 帧格式：[stream_type(1) padding(3) size(4 big-endian)][payload]
	// stream_type 1=stdout, 2=stderr。
	frame := func(stream byte, payload string) []byte {
		buf := make([]byte, 8+len(payload))
		buf[0] = stream
		// size big-endian
		n := uint32(len(payload))
		buf[4] = byte(n >> 24)
		buf[5] = byte(n >> 16)
		buf[6] = byte(n >> 8)
		buf[7] = byte(n)
		copy(buf[8:], payload)
		return buf
	}

	var raw bytes.Buffer
	raw.Write(frame(1, "out1"))
	raw.Write(frame(2, "err1"))
	raw.Write(frame(1, "out2"))

	stdout, stderr, err := mcpserver.DecodeDockerStream(&raw)
	require.NoError(t, err)
	assert.Equal(t, "out1out2", stdout)
	assert.Equal(t, "err1", stderr)
}
