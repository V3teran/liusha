package db

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/tracelog"
	"github.com/rs/zerolog"
)

// pgxLogger 桥接归一：err 抬到顶层 error 字段、耗时键 time 改名
// elapsed_ms（避免与时间戳键重复导致 JSON 解析吞字段）、其余键原样透传。
func TestPgxLoggerNormalizesReservedKeys(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	bridge := pgxLogger{log: logger}

	dialErr := errors.New("dial tcp: network is unreachable")
	bridge.Log(context.Background(), tracelog.LogLevelError, "Connect", map[string]any{
		"err":      dialErr,
		"time":     2389.66,
		"database": "liusha",
		"host":     "postgres",
	})

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("解析日志行: %v", err)
	}
	if entry["level"] != "error" {
		t.Errorf("level = %v, want error", entry["level"])
	}
	if entry["error"] != dialErr.Error() {
		t.Errorf("error = %v, want %q", entry["error"], dialErr.Error())
	}
	if _, dup := entry["err"]; dup {
		t.Error("原始 err 键应被移除，不应与 error 并存")
	}
	if entry["elapsed_ms"] != 2389.66 {
		t.Errorf("elapsed_ms = %v, want 2389.66", entry["elapsed_ms"])
	}
	if _, dup := entry["time"]; dup {
		t.Error("time 键必须改名，避免与时间戳键重复")
	}
	if entry["database"] != "liusha" || entry["host"] != "postgres" {
		t.Errorf("其余业务键应原样透传，got database=%v host=%v", entry["database"], entry["host"])
	}
}

func TestPgxLoggerWarnLevelKeepsMessage(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	bridge := pgxLogger{log: logger}

	bridge.Log(context.Background(), tracelog.LogLevelWarn, "Prepare", map[string]any{"pid": 7})

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("解析日志行: %v", err)
	}
	if entry["level"] != "warn" || entry["message"] != "Prepare" {
		t.Errorf("level/message = %v/%v, want warn/Prepare", entry["level"], entry["message"])
	}
	if entry["pid"] != float64(7) {
		t.Errorf("pid = %v, want 7", entry["pid"])
	}
}

// 调用方上下文取消导致的查询失败（任务终止时在途查询）是正常收尾：降为 debug，
// 不与真实的数据库故障混在 error 里。
func TestPgxLoggerCanceledIsDebug(t *testing.T) {
	var buf bytes.Buffer
	bridge := pgxLogger{log: zerolog.New(&buf).Level(zerolog.DebugLevel)}

	bridge.Log(context.Background(), tracelog.LogLevelError, "Acquire", map[string]any{"err": context.Canceled})

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("解析日志行: %v", err)
	}
	if entry["level"] != "debug" {
		t.Errorf("level = %v, want debug", entry["level"])
	}
}
