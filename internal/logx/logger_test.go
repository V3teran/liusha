package logx

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNew_JSONIncludesComponent(t *testing.T) {
	var buf bytes.Buffer
	l := newWith(&buf, "json").With().Str("component", "test_pkg").Logger()
	l.Info().Str("k", "v").Msg("hello")

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("expect JSON, got %q: %v", buf.String(), err)
	}
	if got["component"] != "test_pkg" || got["message"] != "hello" || got["k"] != "v" {
		t.Fatalf("missing fields: %v", got)
	}
}

func TestNew_DevelopmentIsConsole(t *testing.T) {
	var buf bytes.Buffer
	l := newWith(&buf, "development")
	l.Info().Msg("plain")
	if !strings.Contains(buf.String(), "plain") {
		t.Fatalf("console writer should contain message verbatim, got %q", buf.String())
	}
	if json.Valid(buf.Bytes()) {
		t.Fatalf("development writer should not be JSON, got %q", buf.String())
	}
}
