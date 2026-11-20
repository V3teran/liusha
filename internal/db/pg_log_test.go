package db

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/tracelog"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
)

// SQL 参数不落日志原文（可能是密钥、凭据或整条向量），只记个数。
func TestPgxLogger_OmitsArgValues(t *testing.T) {
	var buf bytes.Buffer
	l := pgxLogger{log: zerolog.New(&buf)}

	l.Log(context.Background(), tracelog.LogLevelError, "Query", map[string]any{
		"sql":  "SELECT 1 WHERE x = $1",
		"args": []any{"secret-token", []float32{0.1, 0.2}},
		"err":  errors.New("boom"),
		"time": 3,
	})

	out := buf.String()
	assert.NotContains(t, out, "secret-token")
	assert.NotContains(t, out, "0.1")
	assert.Contains(t, out, `"arg_count":2`)
	assert.Contains(t, out, `"elapsed_ms":3`)
	assert.Contains(t, out, `"error":"boom"`)
}
