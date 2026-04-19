package flow_test

import (
	"strings"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/flow"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestNewFlow_truncation(t *testing.T) {
	bigBody := []byte(strings.Repeat("x", flow.MaxBodyBytes+1))
	f := flow.New(flow.RawInput{
		EngagementID:   uuid.New(),
		Timestamp:      time.Now().UTC(),
		Method:         "POST",
		URL:            "https://example.com/upload",
		ReqContentType: "application/octet-stream",
		ReqBody:        bigBody,
		RespStatus:     200,
		RespBody:       []byte(`{"ok":true}`),
	})
	require.True(t, f.BodyTruncated)
	require.Equal(t, flow.MaxBodyBytes, len(f.ReqBody))
}

func TestNewFlow_noTruncation(t *testing.T) {
	f := flow.New(flow.RawInput{
		EngagementID: uuid.New(),
		Timestamp:    time.Now().UTC(),
		Method:       "GET",
		URL:          "https://example.com/ping",
		RespStatus:   200,
		RespBody:     []byte("pong"),
	})
	require.False(t, f.BodyTruncated)
	require.Equal(t, []byte("pong"), f.RespBody)
}

func TestNewFlow_respTruncation(t *testing.T) {
	bigResp := []byte(strings.Repeat("y", flow.MaxBodyBytes+100))
	f := flow.New(flow.RawInput{
		EngagementID: uuid.New(),
		Timestamp:    time.Now().UTC(),
		Method:       "GET",
		URL:          "https://example.com/big",
		RespStatus:   200,
		RespBody:     bigResp,
	})
	require.True(t, f.BodyTruncated)
	require.Equal(t, flow.MaxBodyBytes, len(f.RespBody))
}

func TestNewFlow_assignsUUID(t *testing.T) {
	f1 := flow.New(flow.RawInput{EngagementID: uuid.New(), Timestamp: time.Now().UTC()})
	f2 := flow.New(flow.RawInput{EngagementID: uuid.New(), Timestamp: time.Now().UTC()})
	require.NotEqual(t, f1.ID, f2.ID)
}
