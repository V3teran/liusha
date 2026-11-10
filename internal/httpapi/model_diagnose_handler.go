// Package httpapi: provider 连接诊断 handler（前端「LLM 配置」页的「运行诊断」）。
//
// POST /models/providers/diagnose：按表单当前值实测一个 provider 规格——端点、鉴权、
// 模型列表、模型、工具调用逐项给出结果与实际请求地址；协商出可用的请求画像（鉴权方式、
// 输出上限字段、是否支持工具调用）；配置不通但换个写法能通时（补版本段、换协议）给出
// 建议。不落库：诊断结果由前端回填表单，用户保存时才生效。
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

// ProviderProbeSpec 是诊断的入参规格（表单当前值）。
// APIKey 非空 → 用它（新建或更换密钥）；为空 → 据 Code 取已存 provider 的密钥。
// AuthScheme / OutputLimitParam 是当前记录值，作为协商的首选。
type ProviderProbeSpec struct {
	Code             string
	Type             string
	BaseURL          string
	DefaultModel     string
	APIKey           string
	AuthScheme       string
	OutputLimitParam string
}

// 诊断步骤与状态。清单按依赖顺序排列（前端按返回顺序渲染）：选模型依赖模型列表，
// 工具调用依赖选定的模型。
const (
	StepEndpoint = "endpoint" // 端点可达（请求打到了一个会回应的服务）
	StepAuth     = "auth"     // 鉴权通过（并识别出鉴权方式）
	StepModels   = "models"   // 模型列表（可选能力，供选择模型）
	StepModel    = "model"    // 模型可用（对话请求成功）
	StepTools    = "tools"    // 工具调用（携带工具定义的请求被接受）

	StepOK   = "ok"
	StepFail = "fail"
	StepWarn = "warn" // 非阻断的提示（如端点不提供模型列表）
	StepSkip = "skip" // 前置未满足而未执行
)

// DiagnosisStep 是诊断的一项检查结果。
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
	OutputLimitParam string `json:"output_limit_param,omitempty"` // 未发对话请求时为空（无从判断）
	SupportsTools    *bool  `json:"supports_tools,omitempty"`     // 未测出结论时为空
}

// DiagnosisSuggestion 是「换个写法能通」的建议：与表单当前值不同的协议或 Base URL，
// 连同按该写法实测通过的鉴权方式（采纳时一并回填，免去再协商一轮）。
type DiagnosisSuggestion struct {
	Type       string `json:"type"`
	BaseURL    string `json:"base_url"`
	AuthScheme string `json:"auth_scheme"`
	Reason     string `json:"reason"`
}

// Diagnosis 是一次诊断的完整结果。
type Diagnosis struct {
	OK              bool                 `json:"ok"`      // 对话请求成功（配置可用）
	Steps           []DiagnosisStep      `json:"steps"`   // 按 endpoint/auth/models/model/tools 顺序
	Profile         *DiagnosisProfile    `json:"profile"` // 打通后的请求画像；未打通为空
	Suggestion      *DiagnosisSuggestion `json:"suggestion,omitempty"`
	BaseURL         string               `json:"base_url"` // 归一后的 Base URL（保存时同样归一）
	Models          []string             `json:"models"`
	ModelsSupported bool                 `json:"models_supported"`
	// AutoModel 是未填模型时诊断自动选用的模型（可选模型列表的第一个）：诊断按它做完整
	// 实测；配置可用时前端把它回填为默认模型。
	AutoModel string `json:"auto_model,omitempty"`
}

// ProviderDiagnoser 对一个 provider 规格做连接诊断。只有无法发起（已存 provider 不存在、
// 密钥未配置）才返回 error；上游的任何失败都体现在 Diagnosis.Steps 里。
// cmd/api 注入闭合 llmstore + cryptx + framework/llm 的实现；为 nil 时路由不注册。
type ProviderDiagnoser interface {
	Diagnose(ctx context.Context, spec ProviderProbeSpec) (Diagnosis, error)
}

// diagnoseBody 是诊断请求体。
type diagnoseBody struct {
	Code             string `json:"code"`
	Type             string `json:"type"`
	BaseURL          string `json:"base_url"`
	DefaultModel     string `json:"default_model"`
	APIKey           string `json:"api_key"`
	AuthScheme       string `json:"auth_scheme"`
	OutputLimitParam string `json:"output_limit_param"`
}

// diagnoseProviderHandler 处理 POST /models/providers/diagnose。
func diagnoseProviderHandler(d ProviderDiagnoser) gin.HandlerFunc {
	return func(c *gin.Context) {
		var b diagnoseBody
		if err := c.ShouldBindJSON(&b); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}
		if b.Type != llmconfig.ProviderTypeOpenAICompat && b.Type != llmconfig.ProviderTypeAnthropic {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请先选择协议（OpenAI 兼容或 Anthropic）"})
			return
		}
		if err := llmconfig.ValidateBaseURL(b.BaseURL); err != nil {
			respondErr(c, http.StatusBadRequest, err)
			return
		}
		if b.APIKey == "" && b.Code == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缺少密钥：请填入 API Key"})
			return
		}
		res, err := d.Diagnose(c.Request.Context(), ProviderProbeSpec{
			Code: b.Code, Type: b.Type, BaseURL: b.BaseURL, DefaultModel: strings.TrimSpace(b.DefaultModel),
			APIKey: b.APIKey, AuthScheme: b.AuthScheme, OutputLimitParam: b.OutputLimitParam,
		})
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, res)
	}
}
