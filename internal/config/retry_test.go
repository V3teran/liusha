package config

import (
	"reflect"
	"testing"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// config.yaml 的 llm.retry 与框架内置退避表一致：框架在未经配置构造 Router 时用内置表，
// 两份曾漂移成 3 次 [1,4,16]s 与 4 次 [2,8,30,60]s。
func TestRetryTemplateMatchesFrameworkDefaults(t *testing.T) {
	c, err := Load(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := c.LLM.Retry.Options(), llm.DefaultRetryOptions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("config.yaml 的退避策略与框架内置表不一致:\n got  %+v\n want %+v", got, want)
	}
}
