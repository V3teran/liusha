package llmstore

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/config"
	fwllm "github.com/V3teran/liusha/internal/framework/llm"
)

// config.yaml 的 llm.retry 与框架内置退避表一致：框架在未经配置构造 Router 时用内置表，
// 两份曾漂移成 3 次 [1,4,16]s 与 4 次 [2,8,30,60]s。
func TestRetryTemplateMatchesFrameworkDefaults(t *testing.T) {
	c, err := config.Load(filepath.Join("..", "config", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := RetryOptions(c.LLM.Retry), fwllm.DefaultRetryOptions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("config.yaml 的退避策略与框架内置表不一致:\n got  %+v\n want %+v", got, want)
	}
}

// 次数原样、退避表按秒换算、529 的间隔按毫秒：单位写错一处，等待就差上千倍。
func TestRetryOptions_KeepsUnits(t *testing.T) {
	got := RetryOptions(config.RetryConfig{
		Max429: 3, Max529: 1, Max5xx: 2, MaxNet: 1,
		Backoff429Seconds: []int{1, 4, 16}, Backoff5xxSeconds: []int{2, 8}, BackoffNetSeconds: []int{5}, Backoff529Ms: 1500,
	})
	want := fwllm.RetryOptions{
		MaxRetries429: 3, MaxRetries529: 1, MaxRetries5xx: 2, MaxRetriesNet: 1,
		Backoff429: []time.Duration{1 * time.Second, 4 * time.Second, 16 * time.Second},
		Backoff5xx: []time.Duration{2 * time.Second, 8 * time.Second},
		BackoffNet: []time.Duration{5 * time.Second},
		Backoff529: 1500 * time.Millisecond,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("换算结果不符:\n got  %+v\n want %+v", got, want)
	}
}
