// Package httpapi: provider 实测 handler（前端「模型」页的「测试连接」与「拉取模型列表」）。
//
// 两个操作都按表单当前值实测、不落库，各管一件事：
//   - POST /models/providers/diagnose（测试连接）：以默认模型发对话请求，逐项给出端点、鉴权、
//     模型、工具调用的结果与实际请求地址；协商出可用的请求画像（鉴权方式、输出上限字段、
//     是否支持工具调用）；配置不通但换个写法能通时（补版本段、换协议）给出建议。结果由前端
//     回填表单，用户保存时才生效。
//   - POST /models/providers/models（拉取模型列表）：只取上游的可选模型，供选择默认模型；
//     不改写表单里的任何字段。
//
// 密钥来源双路径：请求体带 api_key（新建/更换密钥的明文，优先）；否则据 code 取已存
// provider——经 llmstore 多级缓存读并解密，与运行期同一套缓存事实源。

package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/llmconfig"
)

// ProviderProbeSpec 是实测的入参规格（表单当前值）。
// APIKey 非空 → 用它（新建或更换密钥）；为空 → 据 Code 取已存 provider 的密钥。
// AuthScheme / OutputLimitParam 是当前记录值，作为协商的首选。DefaultModel 与
// OutputLimitParam 只有测试连接用得到（拉取模型列表不发对话请求）。
type ProviderProbeSpec struct {
	Code             string
	Type             string
	BaseURL          string
	DefaultModel     string
	APIKey           string
	AuthScheme       string
	OutputLimitParam string
}

// 测试连接的检查项与各项状态。清单按依赖顺序排列（前端按返回顺序渲染）：工具调用依赖
// 模型可用。状态取值同时用作模型列表拉取结果的状态（ok / warn / fail）。
const (
	StepEndpoint = "endpoint" // 端点可达（请求打到了一个讲所选协议的服务）
	StepAuth     = "auth"     // 鉴权通过（并识别出鉴权方式）
	StepModel    = "model"    // 模型可用（对话请求成功）
	StepTools    = "tools"    // 工具调用（携带工具定义的请求被接受）

	StepOK   = "ok"
	StepFail = "fail"
	StepWarn = "warn" // 非阻断的提示（如工具调用未能判定、端点不提供模型列表）
	StepSkip = "skip" // 前置未满足而未执行
)

// DiagnosisStep 是测试连接的一项检查结果。
type DiagnosisStep struct {
	Key       string `json:"key"`
	Status    string `json:"status"`
	Detail    string `json:"detail"`
	Request   string `json:"request,omitempty"` // 实际请求，如 "POST https://…/chat/completions"
	LatencyMS int64  `json:"latency_ms,omitempty"`
}

// DiagnosisProfile 是实测得到的请求画像（前端回填表单，保存后运行期按此发请求）。
type DiagnosisProfile struct {
	AuthScheme       string `json:"auth_scheme"`
	OutputLimitParam string `json:"output_limit_param"`
	SupportsTools    *bool  `json:"supports_tools,omitempty"` // 未测出结论时为空
}

// DiagnosisSuggestion 是「换个写法能通」的建议：与表单当前值不同的协议或 Base URL，
// 连同按该写法实测通过的鉴权方式（采纳时一并回填，免去再协商一轮）。
type DiagnosisSuggestion struct {
	Type       string `json:"type"`
	BaseURL    string `json:"base_url"`
	AuthScheme string `json:"auth_scheme"`
	Reason     string `json:"reason"`
}

// Diagnosis 是一次测试连接的完整结果。
type Diagnosis struct {
	OK         bool                 `json:"ok"`      // 对话请求成功（配置可用）
	Steps      []DiagnosisStep      `json:"steps"`   // 按 endpoint/auth/model/tools 顺序
	Profile    *DiagnosisProfile    `json:"profile"` // 打通后的请求画像；未打通为空
	Suggestion *DiagnosisSuggestion `json:"suggestion,omitempty"`
	BaseURL    string               `json:"base_url"` // 归一后的 Base URL（保存时同样归一）
}

// ModelList 是一次拉取模型列表的结果。
type ModelList struct {
	Models []string `json:"models"` // 排序去重；端点不提供或拉取失败时为空
	// Status：ok 取到了列表；warn 端点不提供列表或返回空列表（可直接填写模型名，不是错误）；
	// fail 拉取失败（鉴权被拒、限流、网络等）。
	Status  string `json:"status"`
	Detail  string `json:"detail,omitempty"` // 说明：列表来自旁路来源，或为何没有列表
	Request string `json:"request"`          // 产出该结果的实际请求，如 "GET https://…/models"
}

// ProviderProber 按表单当前值实测一个 provider 规格。只有无法发起（已存 provider 不存在、
// 密钥未配置）才返回 error；上游的任何失败都体现在结果里。
// api 组件注入闭合 llmstore + cryptx + framework/llm 的实现；为 nil 时两条路由都不注册。
type ProviderProber interface {
	Diagnose(ctx context.Context, spec ProviderProbeSpec) (Diagnosis, error)
	ListModels(ctx context.Context, spec ProviderProbeSpec) (ModelList, error)
}

// probeTargetBody 是两个实测请求共有的部分：往哪个地址、按什么协议、拿哪把密钥。
type probeTargetBody struct {
	Code       string `json:"code"`
	Type       string `json:"type"`
	BaseURL    string `json:"base_url"`
	APIKey     string `json:"api_key"`
	AuthScheme string `json:"auth_scheme"`
}

// diagnoseBody 是测试连接的请求体。
type diagnoseBody struct {
	probeTargetBody
	DefaultModel     string `json:"default_model"`
	OutputLimitParam string `json:"output_limit_param"`
}

// validProbeTarget 在边界校验实测目标；不合法时写好 400 并返回 false。
func validProbeTarget(c *gin.Context, b probeTargetBody) bool {
	if b.Type != llmconfig.ProviderTypeOpenAICompat && b.Type != llmconfig.ProviderTypeAnthropic {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请先选择协议（OpenAI 兼容或 Anthropic）"})
		return false
	}
	if err := llmconfig.ValidateBaseURL(b.BaseURL); err != nil {
		respondErr(c, http.StatusBadRequest, err)
		return false
	}
	if b.APIKey == "" && b.Code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少密钥：请填入 API Key"})
		return false
	}
	return true
}

func (b probeTargetBody) spec() ProviderProbeSpec {
	return ProviderProbeSpec{Code: b.Code, Type: b.Type, BaseURL: b.BaseURL, APIKey: b.APIKey, AuthScheme: b.AuthScheme}
}

// diagnoseProviderHandler 处理 POST /models/providers/diagnose（测试连接）。
func diagnoseProviderHandler(p ProviderProber) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b diagnoseBody
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		if !validProbeTarget(c, b.probeTargetBody) {
			return
		}
		model := strings.TrimSpace(b.DefaultModel)
		if model == "" {
			// 可用性只能由对话请求证明：没有模型就没有结论可给
			c.JSON(http.StatusBadRequest, gin.H{"error": "请先填写默认模型（可从模型列表拉取）"})
			return
		}
		spec := b.spec()
		spec.DefaultModel, spec.OutputLimitParam = model, b.OutputLimitParam
		res, err := p.Diagnose(c.Request.Context(), spec)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, res)
	}
}

// listProviderModelsHandler 处理 POST /models/providers/models（拉取模型列表）。
// 用 POST：请求体携带表单里的明文密钥，不能进 URL。
func listProviderModelsHandler(p ProviderProber) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b probeTargetBody
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		if !validProbeTarget(c, b) {
			return
		}
		res, err := p.ListModels(c.Request.Context(), b.spec())
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, res)
	}
}
