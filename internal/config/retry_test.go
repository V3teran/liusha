package config

import (
	"reflect"
	"testing"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// 省略 llm.retry 的部署拿到的退避策略必须就是框架的内置表：默认值只有一处来源，
// 配置层不得另持一份（两份曾漂移成 3 次 [1,4,16]s 与 4 次 [2,8,30,60]s）。
func TestRetryDefaultsMatchFrameworkDefaults(t *testing.T) {
	var c AppConfig
	c.ApplyDefaults()
	if got, want := c.LLM.Retry.Options(), llm.DefaultRetryOptions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("缺省退避策略与框架内置表不一致:\n got  %+v\n want %+v", got, want)
	}
}
