// Package llmprobe 实现 provider 连接诊断：满足 httpapi.ProviderDiagnoser 窄接口。
//
// 诊断只认两种协议的规范，不认任何具体服务：所有请求都经运行期同一套构造路径
// （framework/llm 的 ClientPool + Generator）发出，Base URL 归一、鉴权方式、输出上限
// 字段的语义与真实调用完全一致；服务之间的差异靠实测协商得出，而不是查名单。
//
// 密钥双路径与运行期一致：请求带明文 key（新建/更换密钥）优先直用；否则据 code 从
// store 取已存 provider 并解密——同 For(role) 一套缓存事实源，不新开直连 DB 读。
package llmprobe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	openai "github.com/sashabaranov/go-openai"

	fwllm "github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/httpapi"
	"github.com/V3teran/liusha/internal/llmconfig"
)

// Store 是诊断对 llmstore 的窄依赖：据 code 取已存 provider（多级缓存）。
type Store interface {
	ProviderByCode(ctx context.Context, code string) (llmconfig.Provider, error)
}

// Diagnoser 实现 httpapi.ProviderDiagnoser。
type Diagnoser struct {
	store Store
	dec   llmconfig.KeyDecrypter // 解密已存 provider 的密文密钥
}

// New 装配诊断器。
func New(store Store, dec llmconfig.KeyDecrypter) *Diagnoser {
	return &Diagnoser{store: store, dec: dec}
}

// diagnosisRun 是一次诊断的执行上下文：自带一个生命周期仅限本次诊断的连接池——池以明文
// 密钥为复用键，诊断处理的是用户临时输入（可能是错的）的凭据，用完即释放，既不在
// 进程里留存这些密钥，也不随诊断次数增长。请求构造路径与运行期完全相同。
type diagnosisRun struct {
	*Diagnoser
	pool *fwllm.ClientPool
}

const (
	callTimeout     = 20 * time.Second // 单次实测请求的超时
	diagnoseTimeout = 90 * time.Second // 整次诊断的超时（最坏情况多次协商）
	pingMaxTokens   = 16               // 对话实测的输出上限：足够任何服务正常应答，又不浪费额度
	toolsMaxTokens  = 64               // 工具实测的输出上限：容得下一次工具调用

	modelsListedFmt = "可选 %d 个模型"
)

// versionSegment 匹配路径中的版本段（v1、v4、v1beta…）：OpenAI 兼容 Base URL 通常以它结尾。
var versionSegment = regexp.MustCompile(`(?i)^v\d+[a-z0-9.]*$`)

// Diagnose 诊断一个 provider 规格。见 httpapi.ProviderDiagnoser。
func (d *Diagnoser) Diagnose(ctx context.Context, spec httpapi.ProviderProbeSpec) (httpapi.Diagnosis, error) {
	key, err := d.resolveKey(ctx, spec)
	if err != nil {
		return httpapi.Diagnosis{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, diagnoseTimeout)
	defer cancel()
	run := &diagnosisRun{Diagnoser: d, pool: fwllm.NewClientPool()}
	defer run.pool.Close()
	report := run.diagnose(ctx, spec, key)
	if spec.DefaultModel != "" || len(report.Models) == 0 {
		return report, nil
	}
	// 未填模型而拿到了可选模型：自动选用列表第一个，按它再做一次完整实测——一次诊断就给出
	// 配置是否可用的结论，不必先选模型再诊断。
	spec.DefaultModel = report.Models[0]
	report = run.diagnose(ctx, spec, key)
	report.AutoModel = spec.DefaultModel
	report.Steps = withAutoModel(report.Steps, spec.DefaultModel)
	return report, nil
}

// withAutoModel 在模型项注明模型是自动选用的：可用时提示可改选，不可用时引导从列表另选。
func withAutoModel(steps []httpapi.DiagnosisStep, model string) []httpapi.DiagnosisStep {
	out := slices.Clone(steps)
	for i := range out {
		if out[i].Key != httpapi.StepModel {
			continue
		}
		switch out[i].Status {
		case httpapi.StepOK:
			out[i].Detail = model + "（未填写模型，已自动选用列表第一个，可改选）"
		case httpapi.StepFail:
			out[i].Detail = fmt.Sprintf("自动选用的 %s 不可用：%s——请从模型列表另选", model, out[i].Detail)
		}
	}
	return out
}

// diagnose 是一次诊断的主流程（见 Diagnose）。
func (d *diagnosisRun) diagnose(ctx context.Context, spec httpapi.ProviderProbeSpec, key string) httpapi.Diagnosis {

	target := profileTarget{
		protocol: spec.Type,
		baseURL:  llmconfig.NormalizeBaseURL(spec.Type, spec.BaseURL),
		key:      key,
		model:    spec.DefaultModel,
		limit:    fwllm.OutputLimitParam(spec.OutputLimitParam),
	}
	report := &httpapi.Diagnosis{BaseURL: target.baseURL, Models: []string{}}

	// 1. 所选协议下协商「候选地址 × 鉴权方式」
	reach, attempts := d.negotiate(ctx, target, baseCandidates(target), schemeCandidates(spec.Type, spec.AuthScheme))
	if reach == nil {
		d.reportFailure(ctx, report, target, spec.BaseURL, attempts)
		return *report
	}
	if reach.baseURL != target.baseURL {
		report.Suggestion = &httpapi.DiagnosisSuggestion{
			Type: target.protocol, BaseURL: reach.baseURL, AuthScheme: string(reach.scheme),
			Reason: "当前地址请求 404，补上版本段后可用",
		}
	}
	// 2. 打通之后：模型列表、工具调用
	d.reportReach(ctx, report, reach)
	return *report
}

// reportReach 把打通结果展开为检查清单并补齐画像。清单顺序即依赖顺序：端点 → 鉴权 →
// 模型列表 → 模型 → 工具调用——选模型依赖列表，工具调用依赖选定的模型。
func (d *diagnosisRun) reportReach(ctx context.Context, report *httpapi.Diagnosis, r *reachResult) {
	report.Profile = &httpapi.DiagnosisProfile{AuthScheme: string(r.scheme)}
	endpoint := httpapi.DiagnosisStep{Key: httpapi.StepEndpoint, Status: httpapi.StepOK, Request: r.request, LatencyMS: r.latency}
	auth := httpapi.DiagnosisStep{Key: httpapi.StepAuth, Status: httpapi.StepOK, Detail: schemeLabel(r.scheme)}
	if !r.chat {
		// 以模型列表打通：列表就是这次实测的结果，不再重复请求
		report.Models, report.ModelsSupported = r.models, true
		report.Steps = []httpapi.DiagnosisStep{
			endpoint, auth,
			{Key: httpapi.StepModels, Status: httpapi.StepOK, Detail: fmt.Sprintf(modelsListedFmt, len(r.models)), Request: r.request, LatencyMS: r.latency},
			{Key: httpapi.StepModel, Status: httpapi.StepSkip, Detail: "未填写模型——从模型列表选择或手填后再诊断"},
			{Key: httpapi.StepTools, Status: httpapi.StepSkip, Detail: "需先填写模型并通过对话实测"},
		}
		return
	}
	modelsStep, models, supported := d.checkModels(ctx, r.profileTarget, r.scheme)
	report.Models, report.ModelsSupported = models, supported
	report.OK = true
	report.Profile.OutputLimitParam = string(r.limit)
	toolStep, supports := d.checkTools(ctx, r)
	report.Profile.SupportsTools = supports
	model := httpapi.DiagnosisStep{Key: httpapi.StepModel, Status: httpapi.StepOK, Detail: r.model}
	report.Steps = []httpapi.DiagnosisStep{endpoint, auth, modelsStep, model, toolStep}
}

// reportFailure 把未打通的协商展开为检查清单（见 failureSteps），并补上仍能给出的信息：
// 根因是「地址不讲这个协议」时换协议实测给建议；鉴权已通过时照常拉模型列表——模型名
// 填错时用户能直接从列表改选；未填模型而端点不提供列表时，尝试同主机的旁路来源。
func (d *diagnosisRun) reportFailure(ctx context.Context, report *httpapi.Diagnosis, t profileTarget, rawBase string, attempts []attempt) {
	last := attempts[len(attempts)-1]
	if last.offProtocol() {
		report.Suggestion = d.otherProtocol(ctx, t, rawBase)
	}
	report.Steps = failureSteps(t, attempts)
	switch classifyFailure(t, last) {
	case failInconclusive:
		if ids, step, ok := d.siblingModels(ctx, t); ok {
			report.Models = ids
			report.Steps = withStep(report.Steps, step)
		}
	case failAfterAuth:
		if t.model == "" {
			return // 未填模型时实测的就是模型列表，它的失败已在清单里
		}
		at := t
		at.baseURL = last.baseURL
		step, models, supported := d.checkModels(ctx, at, last.scheme)
		report.Steps = withStep(report.Steps, step)
		report.Models, report.ModelsSupported = models, supported
	}
}

// profileTarget 是一次实测的目标配置。
type profileTarget struct {
	protocol string
	baseURL  string
	key      string
	model    string
	limit    fwllm.OutputLimitParam
}

// reachResult 是打通时的配置与证据。
type reachResult struct {
	profileTarget
	scheme  fwllm.AuthScheme
	chat    bool     // 以对话请求打通（填了模型）；否则以模型列表打通
	models  []string // 以模型列表打通时拉到的模型
	request string
	latency int64
}

// attempt 是协商中的一次实测记录。
type attempt struct {
	baseURL string
	scheme  fwllm.AuthScheme
	request string
	status  int // 上游 HTTP 状态码；0 = 成功或未拿到 HTTP 响应（网络/超时）
	err     error
	latency int64
}

func isPathMissing(status int) bool {
	return status == http.StatusNotFound || status == http.StatusMethodNotAllowed
}

// offProtocol 判定实测失败是否属于「这个地址不讲这个协议」：路径不存在（404/405），或
// 2xx 但响应不符合协议（网关常把错误页包成 200 返回）。这类失败值得换地址、换协议再试。
func (a attempt) offProtocol() bool {
	return isPathMissing(a.status) || errors.Is(a.err, fwllm.ErrUnexpectedResponse)
}

// baseCandidates 返回所选协议下的候选地址：原地址；OpenAI 兼容且路径没有版本段时
// 追加补 /v1 的候选（多数网关的 OpenAI 兼容接口挂在 /v1 下）。
//
// 地址发现只在有对话实测时进行：两种协议的模型列表响应形状相近（补出的 …/v1/models
// 可能恰好是另一种协议的列表端点），模型列表证明不了地址讲的是所选协议，只有对话
// 请求才是可靠证据。
func baseCandidates(t profileTarget) []string {
	out := []string{t.baseURL}
	if t.protocol != llmconfig.ProviderTypeOpenAICompat || t.model == "" {
		return out
	}
	u, err := url.Parse(t.baseURL)
	if err != nil {
		return out
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if versionSegment.MatchString(seg) {
			return out
		}
	}
	return append(out, strings.TrimRight(t.baseURL, "/")+"/v1")
}

// schemeCandidates 返回鉴权方式的尝试顺序：当前记录值（或协议标准方式）优先，另一种其次。
func schemeCandidates(protocol, current string) []fwllm.AuthScheme {
	first := fwllm.AuthScheme(current)
	if first != fwllm.AuthBearer && first != fwllm.AuthXAPIKey {
		first = fwllm.DefaultAuthScheme(protocol)
	}
	if first == fwllm.AuthBearer {
		return []fwllm.AuthScheme{fwllm.AuthBearer, fwllm.AuthXAPIKey}
	}
	return []fwllm.AuthScheme{fwllm.AuthXAPIKey, fwllm.AuthBearer}
}

// negotiate 逐个候选实测：401/403 换鉴权方式，地址不讲这个协议（404/405、响应不符合
// 协议）换地址，其余错误（模型不可用、限流、网络等）已能定性，停止协商。返回打通结果（nil = 未打通）与全部实测记录。
func (d *diagnosisRun) negotiate(ctx context.Context, t profileTarget, bases []string, schemes []fwllm.AuthScheme) (*reachResult, []attempt) {
	var attempts []attempt
	for _, base := range bases {
		candidate := t
		candidate.baseURL = base
		for _, scheme := range schemes {
			res, a := d.reach(ctx, candidate, scheme)
			attempts = append(attempts, a)
			if res != nil {
				return res, attempts
			}
			if a.status == http.StatusUnauthorized || a.status == http.StatusForbidden {
				continue // 换鉴权方式
			}
			if !a.offProtocol() {
				return nil, attempts // 已能定性的失败
			}
			break // 地址不讲这个协议：换地址
		}
	}
	return nil, attempts
}

// reach 以一种鉴权方式实测：填了模型发最小对话请求，否则拉模型列表。
func (d *diagnosisRun) reach(ctx context.Context, t profileTarget, scheme fwllm.AuthScheme) (*reachResult, attempt) {
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	start := time.Now()
	if t.model != "" {
		a := attempt{baseURL: t.baseURL, scheme: scheme, request: "POST " + fwllm.OperationURL(t.protocol, t.baseURL, fwllm.OpChat)}
		gen, err := d.generator(cctx, t, scheme, pingMaxTokens)
		if err == nil {
			_, err = gen.Generate(cctx, []fwllm.Message{{Role: fwllm.RoleUser, Content: "ping"}}, nil)
		}
		a.latency, a.err, a.status = time.Since(start).Milliseconds(), err, httpStatus(err)
		if err != nil {
			return nil, a
		}
		res := &reachResult{profileTarget: t, scheme: scheme, chat: true, request: a.request, latency: a.latency}
		res.limit = outputLimitOf(gen, t.limit)
		return res, a
	}
	a := attempt{baseURL: t.baseURL, scheme: scheme, request: "GET " + fwllm.OperationURL(t.protocol, t.baseURL, fwllm.OpModels)}
	ids, err := d.listModels(cctx, t, scheme)
	if err == nil && len(ids) == 0 {
		// 空列表证明不了端点讲这个协议（网关错误页按协议解码同样是空列表）
		err = fmt.Errorf("模型列表为空，无法确认端点: %w", fwllm.ErrUnexpectedResponse)
	}
	a.latency, a.err, a.status = time.Since(start).Milliseconds(), err, httpStatus(err)
	if err != nil {
		return nil, a
	}
	return &reachResult{profileTarget: t, scheme: scheme, models: ids, request: a.request, latency: a.latency}, a
}

// otherProtocol 用另一种协议实测同一地址（按该协议的规则归一），能通则返回建议。
func (d *diagnosisRun) otherProtocol(ctx context.Context, t profileTarget, rawBase string) *httpapi.DiagnosisSuggestion {
	other := llmconfig.ProviderTypeAnthropic
	if t.protocol == llmconfig.ProviderTypeAnthropic {
		other = llmconfig.ProviderTypeOpenAICompat
	}
	alt := t
	alt.protocol = other
	alt.baseURL = llmconfig.NormalizeBaseURL(other, rawBase)
	alt.limit = ""
	reach, _ := d.negotiate(ctx, alt, baseCandidates(alt), schemeCandidates(other, ""))
	if reach == nil {
		return nil
	}
	return &httpapi.DiagnosisSuggestion{
		Type: other, BaseURL: reach.baseURL, AuthScheme: string(reach.scheme),
		Reason: fmt.Sprintf("该地址按 %s 协议可用", protocolLabel(other)),
	}
}

// checkTools 携带一个工具定义发对话请求：被接受即支持工具调用；上游 400 拒绝即不支持；
// 其他失败（限流、网络）给不出结论，不改写声明。
func (d *diagnosisRun) checkTools(ctx context.Context, r *reachResult) (httpapi.DiagnosisStep, *bool) {
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	step := httpapi.DiagnosisStep{Key: httpapi.StepTools, Request: r.request}
	t := r.profileTarget
	start := time.Now()
	gen, err := d.generator(cctx, t, r.scheme, toolsMaxTokens)
	if err == nil {
		_, err = gen.Generate(cctx, []fwllm.Message{{Role: fwllm.RoleUser, Content: "Call the ping tool."}}, []fwllm.ToolSchema{{
			Name: "ping", Description: "Connectivity check. Takes no arguments.",
			Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
		}})
	}
	step.LatencyMS = time.Since(start).Milliseconds()
	switch {
	case err == nil:
		step.Status, step.Detail = httpapi.StepOK, "支持"
		return step, boolPtr(true)
	case httpStatus(err) == http.StatusBadRequest:
		step.Status, step.Detail = httpapi.StepFail, "不支持："+humanizeProbeErr(err)
		return step, boolPtr(false)
	default:
		step.Status, step.Detail = httpapi.StepWarn, "未能判定："+humanizeProbeErr(err)
		return step, nil
	}
}

// checkModels 以已通过的鉴权方式拉模型列表：可选能力，端点不提供（404/405）只是提示。
func (d *diagnosisRun) checkModels(ctx context.Context, t profileTarget, scheme fwllm.AuthScheme) (httpapi.DiagnosisStep, []string, bool) {
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	step := httpapi.DiagnosisStep{Key: httpapi.StepModels, Request: "GET " + fwllm.OperationURL(t.protocol, t.baseURL, fwllm.OpModels)}
	start := time.Now()
	ids, err := d.listModels(cctx, t, scheme)
	step.LatencyMS = time.Since(start).Milliseconds()
	switch {
	case err == nil && len(ids) > 0:
		step.Status, step.Detail = httpapi.StepOK, fmt.Sprintf(modelsListedFmt, len(ids))
		return step, ids, true
	case err == nil:
		step.Status, step.Detail = httpapi.StepWarn, "返回了空列表，请直接填写模型名"
		return step, []string{}, true
	case isPathMissing(httpStatus(err)):
		if sibling, siblingStep, ok := d.siblingModels(ctx, t); ok {
			return siblingStep, sibling, false
		}
		step.Status, step.Detail = httpapi.StepWarn, "该端点不提供模型列表，请直接填写模型名"
	default:
		step.Status, step.Detail = httpapi.StepWarn, "拉取失败："+humanizeProbeErr(err)
	}
	return step, []string{}, false
}

// siblingModels 在 Anthropic 兼容端点不提供模型列表时，从同主机的 OpenAI 兼容端点取可选
// 模型（见 siblingListBases）。只用于填充模型下拉：两个端点认的模型名未必完全一致，
// 选定的模型是否可用仍以对话实测为准。取到非空列表才算成功。
func (d *diagnosisRun) siblingModels(ctx context.Context, t profileTarget) ([]string, httpapi.DiagnosisStep, bool) {
	for _, base := range siblingListBases(t.protocol, t.baseURL) {
		cctx, cancel := context.WithTimeout(ctx, callTimeout)
		start := time.Now()
		sibling := profileTarget{protocol: llmconfig.ProviderTypeOpenAICompat, baseURL: base, key: t.key}
		ids, err := d.listModels(cctx, sibling, fwllm.AuthBearer)
		cancel()
		if err != nil || len(ids) == 0 {
			continue
		}
		return ids, httpapi.DiagnosisStep{
			Key: httpapi.StepModels, Status: httpapi.StepOK,
			Detail:    fmt.Sprintf("该端点不提供模型列表；已从同主机的 OpenAI 兼容端点获取 %d 个模型（仅供选择，可用性以对话诊断为准）", len(ids)),
			Request:   "GET " + fwllm.OperationURL(llmconfig.ProviderTypeOpenAICompat, base, fwllm.OpModels),
			LatencyMS: time.Since(start).Milliseconds(),
		}, true
	}
	return nil, httpapi.DiagnosisStep{}, false
}

// siblingListBases 返回 Anthropic 兼容端点的模型列表旁路来源。常见的部署惯例是：
// Anthropic 兼容接口挂在某个以 anthropic 命名的子路径下，同一主机的上一级路径提供
// OpenAI 兼容接口。规则只依据这一惯例（地址最后一段含 anthropic），不认具体服务：
// 去掉该段得到上一级路径，候选 …/v1/models 与 …/models。其余情况没有旁路来源。
func siblingListBases(protocol, baseURL string) []string {
	if protocol != llmconfig.ProviderTypeAnthropic {
		return nil
	}
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || u.Host == "" {
		return nil
	}
	cut := strings.LastIndex(u.Path, "/")
	if cut < 0 || !strings.Contains(strings.ToLower(u.Path[cut+1:]), "anthropic") {
		return nil
	}
	u.Path = u.Path[:cut]
	root := strings.TrimRight(u.String(), "/")
	return []string{root + "/v1", root}
}

// withStep 返回把同 key 的一项换成 step 的检查清单副本。
func withStep(steps []httpapi.DiagnosisStep, step httpapi.DiagnosisStep) []httpapi.DiagnosisStep {
	out := slices.Clone(steps)
	for i := range out {
		if out[i].Key == step.Key {
			out[i] = step
		}
	}
	return out
}

// generator 按目标配置构造运行期同款生成器（输出上限按 maxTokens 覆盖，省额度）。
func (d *diagnosisRun) generator(ctx context.Context, t profileTarget, scheme fwllm.AuthScheme, maxTokens int) (fwllm.Generator, error) {
	return fwllm.BuildGeneratorWithKey(ctx, fwllm.ProviderSpec{
		Code: "diagnose", Type: t.protocol, BaseURL: t.baseURL, Model: t.model, APIKey: t.key,
		AuthScheme: scheme, OutputLimitParam: t.limit, MaxTokens: maxTokens,
	}, d.pool)
}

// listModels 经运行期同一个 SDK client 调协议原生的模型列表接口（诊断不重试）。
func (d *diagnosisRun) listModels(ctx context.Context, t profileTarget, scheme fwllm.AuthScheme) ([]string, error) {
	var ids []string
	if t.protocol == llmconfig.ProviderTypeAnthropic {
		cli, err := d.pool.GetOrCreateAnthropic(t.baseURL, t.key, scheme)
		if err != nil {
			return nil, err
		}
		pager := cli.Models.ListAutoPaging(ctx, anthropic.ModelListParams{Limit: anthropic.Int(1000)}, option.WithMaxRetries(0))
		for pager.Next() {
			if id := pager.Current().ID; id != "" {
				ids = append(ids, id)
			}
		}
		if err := pager.Err(); err != nil {
			return nil, err
		}
	} else {
		cli, err := d.pool.GetOrCreateOpenAI(t.baseURL, t.key, scheme)
		if err != nil {
			return nil, err
		}
		list, err := cli.ListModels(ctx)
		if err != nil {
			return nil, err
		}
		for _, m := range list.Models {
			if m.ID != "" {
				ids = append(ids, m.ID)
			}
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

// resolveKey 决定诊断用哪把密钥：请求明文优先，否则据 code 取已存并解密。
// 已存 provider 不存在时透传 pgx.ErrNoRows（HTTP 404）；取不到密钥是可修复的配置问题，
// 包装 fwllm.ErrNotConfigured（HTTP 400）。
func (d *Diagnoser) resolveKey(ctx context.Context, spec httpapi.ProviderProbeSpec) (string, error) {
	if spec.APIKey != "" {
		return spec.APIKey, nil
	}
	stored, err := d.store.ProviderByCode(ctx, spec.Code)
	if err != nil {
		return "", fmt.Errorf("取已存 provider %q 失败: %w", spec.Code, err)
	}
	key, err := llmconfig.ResolveAPIKey(stored, d.dec)
	if err != nil {
		return "", fmt.Errorf("%w: %w", fwllm.ErrNotConfigured, err)
	}
	return key, nil
}

// failureKind 是未打通时失败的性质，决定失败落在检查清单的哪一项。
type failureKind int

const (
	failInconclusive failureKind = iota // 未填模型、列表接口 404/405：判定不了端点（可能只是不提供列表）
	failEndpoint                        // 没拿到 HTTP 响应，或地址不讲这个协议
	failAuth                            // 鉴权被拒（各鉴权方式都试过）
	failAfterAuth                       // 已过鉴权：模型不可用、限流、上游错误等
)

func classifyFailure(t profileTarget, a attempt) failureKind {
	switch {
	case t.model == "" && isPathMissing(a.status):
		return failInconclusive
	case a.status == 0 || a.offProtocol():
		return failEndpoint
	case a.status == http.StatusUnauthorized || a.status == http.StatusForbidden:
		return failAuth
	default:
		return failAfterAuth
	}
}

// failureSteps 依最后一次实测的失败性质，把未打通的结果展开为检查清单（顺序同 reportReach）：
// 失败落在哪一项，其后依赖它的各项跳过。填了模型而鉴权已通过时，模型列表项先占位跳过，
// 由 reportFailure 实测后替换。
func failureSteps(t profileTarget, attempts []attempt) []httpapi.DiagnosisStep {
	last := attempts[len(attempts)-1]
	detail := humanizeProbeErr(last.err)
	if n := len(attempts); n > 1 {
		detail = fmt.Sprintf("%s（已尝试 %d 种地址/鉴权组合）", detail, n)
	}
	skip := func(keys ...string) []httpapi.DiagnosisStep {
		out := make([]httpapi.DiagnosisStep, 0, len(keys))
		for _, k := range keys {
			out = append(out, httpapi.DiagnosisStep{Key: k, Status: httpapi.StepSkip})
		}
		return out
	}
	endpoint := httpapi.DiagnosisStep{Key: httpapi.StepEndpoint, Status: httpapi.StepOK, Request: last.request, LatencyMS: last.latency}
	auth := httpapi.DiagnosisStep{Key: httpapi.StepAuth, Status: httpapi.StepOK, Detail: schemeLabel(last.scheme)}
	switch classifyFailure(t, last) {
	case failInconclusive:
		// 未填模型时只能经模型列表验证，而列表接口 404/405 判定不了任何事：端点可能只是
		// 不提供模型列表（很多兼容端点只实现对话接口），也可能地址有误。只有对话请求能给出
		// 结论——如实说「无法判定」，并引导填写模型名，不把它报成端点错误。
		endpoint.Status = httpapi.StepWarn
		endpoint.Detail = fmt.Sprintf("无法判定：模型列表接口返回 %d——该端点可能不提供模型列表，也可能 Base URL 有误", last.status)
		return []httpapi.DiagnosisStep{
			endpoint,
			{Key: httpapi.StepAuth, Status: httpapi.StepSkip},
			{Key: httpapi.StepModels, Status: httpapi.StepWarn, Detail: "该端点可能不提供模型列表"},
			{Key: httpapi.StepModel, Status: httpapi.StepFail, Detail: "请填写模型名后再诊断：对话请求能确认端点、鉴权与模型"},
			{Key: httpapi.StepTools, Status: httpapi.StepSkip},
		}
	case failEndpoint:
		endpoint.Status, endpoint.Detail = httpapi.StepFail, detail
		return append([]httpapi.DiagnosisStep{endpoint}, skip(httpapi.StepAuth, httpapi.StepModels, httpapi.StepModel, httpapi.StepTools)...)
	case failAuth:
		auth.Status, auth.Detail = httpapi.StepFail, detail
		return append([]httpapi.DiagnosisStep{endpoint, auth}, skip(httpapi.StepModels, httpapi.StepModel, httpapi.StepTools)...)
	}
	if t.model == "" {
		// 未填模型时实测的就是模型列表：失败落在模型列表项
		return []httpapi.DiagnosisStep{
			endpoint, auth,
			{Key: httpapi.StepModels, Status: httpapi.StepFail, Detail: "拉取失败：" + detail, Request: last.request, LatencyMS: last.latency},
			{Key: httpapi.StepModel, Status: httpapi.StepSkip, Detail: "未填写模型——从模型列表选择或手填后再诊断"},
			{Key: httpapi.StepTools, Status: httpapi.StepSkip},
		}
	}
	model := httpapi.DiagnosisStep{Key: httpapi.StepModel, Status: httpapi.StepFail, Detail: detail}
	return []httpapi.DiagnosisStep{endpoint, auth, {Key: httpapi.StepModels, Status: httpapi.StepSkip}, model, {Key: httpapi.StepTools, Status: httpapi.StepSkip}}
}

// outputLimitOf 取生成器自适应后的输出上限字段（anthropic 协议没有这一概念，恒为 max_tokens）。
func outputLimitOf(gen fwllm.Generator, fallback fwllm.OutputLimitParam) fwllm.OutputLimitParam {
	if g, ok := gen.(interface{ OutputLimitParam() fwllm.OutputLimitParam }); ok {
		return g.OutputLimitParam()
	}
	if fallback == "" {
		return fwllm.OutputLimitMaxTokens
	}
	return fallback
}

// httpStatus 取错误携带的 HTTP 状态码（不是 HTTP 层错误时为 0）。
func httpStatus(err error) int {
	if err == nil {
		return 0
	}
	var he *fwllm.HTTPError // 运行期 LLM 层对 SDK 错误的统一包装
	if errors.As(err, &he) {
		return he.Code
	}
	var ae *anthropic.Error
	if errors.As(err, &ae) {
		return ae.StatusCode
	}
	var oe *openai.APIError
	if errors.As(err, &oe) {
		return oe.HTTPStatusCode
	}
	var re *openai.RequestError
	if errors.As(err, &re) {
		return re.HTTPStatusCode
	}
	return 0
}

// humanizeProbeErr 把底层 error 归类成中文原因，并附上上游给出的原文（如「余额不足」
// 「模型不存在」——比状态码更能直接定位问题）。优先按状态码归类；拿不到状态码时按
// 协议不符、网络/超时特征归类，其余带上原文。
func humanizeProbeErr(err error) string {
	if err == nil {
		return ""
	}
	reason := classify(err)
	if msg := upstreamMessage(err); msg != "" {
		return reason + "——上游：" + msg
	}
	return reason
}

func classify(err error) string {
	switch httpStatus(err) {
	case http.StatusUnauthorized:
		return "鉴权失败：API Key 无效、无权限，或鉴权方式不被接受（401）"
	case http.StatusForbidden:
		return "拒绝访问：Key 无该操作权限（403）"
	case http.StatusNotFound:
		return "找不到资源：Base URL 路径、协议或模型名有误（404）"
	case http.StatusMethodNotAllowed:
		return "请求方法不被接受：Base URL 路径或协议有误（405）"
	case http.StatusTooManyRequests:
		return "触发限流或额度不足（429）"
	case http.StatusBadRequest:
		return "请求被拒绝（400）：多为模型名不受该端点支持"
	}
	low := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, fwllm.ErrUnexpectedResponse):
		return "响应不符合所选协议：Base URL 指向了别的协议，或网关返回了错误页"
	case strings.Contains(low, "timeout") || strings.Contains(low, "deadline exceeded"):
		return "连接超时：Base URL 不可达或响应过慢"
	case strings.Contains(low, "connection refused") || strings.Contains(low, "connection reset") ||
		strings.Contains(low, "no such host") || strings.Contains(low, "dial"):
		return "网络错误：无法连接到 Base URL（域名解析/端口/证书）"
	default:
		return "请求失败：" + err.Error()
	}
}

// upstreamMessage 取上游错误体里的说明文字（两种协议的错误体形状各异，网关也常自定义：
// 依次取 error.message、message、msg）；取不到返回空串。截断过长内容。
func upstreamMessage(err error) string {
	raw := ""
	var oe *openai.APIError
	var re *openai.RequestError
	var ae *anthropic.Error
	switch {
	case errors.As(err, &oe):
		if oe.Message != "" {
			return truncate(oe.Message)
		}
	case errors.As(err, &re):
		raw = string(re.Body)
	case errors.As(err, &ae):
		raw = ae.RawJSON()
	}
	if raw == "" {
		return ""
	}
	var body struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Msg     string          `json:"msg"`
	}
	if json.Unmarshal([]byte(raw), &body) != nil {
		return ""
	}
	var nested struct {
		Message string `json:"message"`
	}
	if len(body.Error) > 0 && json.Unmarshal(body.Error, &nested) == nil && nested.Message != "" {
		return truncate(nested.Message)
	}
	if body.Message != "" {
		return truncate(body.Message)
	}
	return truncate(body.Msg)
}

func truncate(s string) string {
	const limit = 200
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return s
}

func schemeLabel(s fwllm.AuthScheme) string {
	if s == fwllm.AuthXAPIKey {
		return "x-api-key 头"
	}
	return "Authorization: Bearer"
}

func protocolLabel(protocol string) string {
	if protocol == llmconfig.ProviderTypeAnthropic {
		return "Anthropic"
	}
	return "OpenAI 兼容"
}

func boolPtr(v bool) *bool { return &v }
