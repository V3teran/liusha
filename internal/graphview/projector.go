// Package graphview 是图视图投影器：从 finding + finding_relation + flow + engagement
// 几张事实表实时拼出图视图（origin / endpoint / parameter / finding / goal
// 节点 + has_param / vulnerable_to / enables / contributes_to 边），
// **不独立存储**——所有节点边都是查询时派生，避免数据漂移。
//
// 设计理念：图的节点种类 = Fact 的最小表达，原图本身就够用。
// 我们的投影器只是把已有事实换个视角呈现。
package graphview

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/activescan"
	"github.com/V3teran/liusha/internal/passivesession"
	"github.com/V3teran/liusha/internal/finding"
)

// Node kind 枚举（节点 kind 白名单——避免 LLM 自由起 kind 翻车的历史）。
const (
	KindOrigin    = "origin"    // 扫描起点：host 信息
	KindGoal      = "goal"      // 扫描目标：枚举所有可达漏洞
	KindEndpoint  = "endpoint"  // method+host+path（从 finding/flow 派生）
	KindParameter = "parameter" // endpoint + location + name（从 finding.target.params 派生）
	KindFinding   = "finding"   // 已落库的 finding（直接引用 finding.id）
)

// Edge kind 枚举。
const (
	EdgeDiscovered    = "discovered"     // origin → endpoint（首次在某次扫描里看到）
	EdgeHasParam      = "has_param"      // endpoint → parameter
	EdgeVulnerableTo  = "vulnerable_to"  // parameter/endpoint → finding
	EdgeEnables       = "enables"        // finding → finding（来自 finding_relation）
	EdgeContributesTo = "contributes_to" // finding → goal（每条 finding 都贡献到目标）
)

// Node 是投影出的一个图节点。
type Node struct {
	ID      string         `json:"id"`
	Kind    string         `json:"kind"`
	Label   string         `json:"label"`
	Payload map[string]any `json:"payload,omitempty"`
}

// Edge 是投影出的一条有向边。
type Edge struct {
	From    string         `json:"from"`
	To      string         `json:"to"`
	Kind    string         `json:"kind"`
	Label   string         `json:"label,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`
}

// View 是单次投影的完整图。
type View struct {
	OwnerID     string    `json:"owner_id"`
	Host        string    `json:"host"`
	GeneratedAt  time.Time `json:"generated_at"`
	Nodes        []Node    `json:"nodes"`
	Edges        []Edge    `json:"edges"`
}

// FindingReader 是投影器读 finding 表所需的最小接口。
// *finding.Store 自动满足。
//
// LLM 用 write_relation 工具主动声明 finding 间 enables 关系，projector 渲染成图边。
type FindingReader interface {
	ListByOwner(ctx context.Context, ownerType, ownerID string) ([]finding.VulnFinding, error)
	ListRelationsByEngagement(ctx context.Context, engagementID string) ([]finding.Relation, error)
}

// PassiveReader / ActiveReader 是投影器读 owner 元数据所需的最小接口。
// *passivesession.Store / *activescan.Store 自动满足。
type PassiveReader interface {
	GetByID(ctx context.Context, id string) (passivesession.Session, error)
}

// ActiveReader 同上的 active_scan 表读接口。
type ActiveReader interface {
	GetByID(ctx context.Context, id string) (activescan.Scan, error)
}

// Projector 是无状态投影器；可全局共享一份。
type Projector struct {
	Findings FindingReader
	Passive  PassiveReader
	Active   ActiveReader
}

// Project 投影 (engagementID, host) 范围的图。
//
// engagement 可挂多 host，host 参数语义：
//   - 非空：按 finding.host 过滤，只投影该 host 下的图
//   - 空：列本 engagement 跨 host 的全部 finding（多 host 时图可能较杂）
//
// 步骤：
//  1. 拉 engagement 元数据（created_at / mode / status 等做 origin 节点 payload）
//  2. 拉本 engagement 全部 finding（已 dedup）
//  3. 拉本 engagement 全部 finding_relation（enables 边）
//  4. 按 finding.target 派生 endpoint / parameter 节点（dedup_key 由 Go 端规范化）
//  5. 拼出 origin → endpoint → parameter → finding → goal 主链
//  6. 加 finding_relation 提供的 enables 边
func (p *Projector) Project(ctx context.Context, engagementID, host string) (View, error) {
	if engagementID == "" {
		return View{}, fmt.Errorf("engagement_id 不能为空")
	}

	// 双轨切读：engagementID 参数实际语义改为 ownerID（passive_session.id / active_scan.id）。
	// 双试两表确定 ownerType + 拉元数据（created_at / mode / status）。
	var ownerType, modeStr, statusStr string
	var createdAt time.Time
	if sess, err := p.Passive.GetByID(ctx, engagementID); err == nil {
		ownerType, modeStr, statusStr, createdAt = "passive_session", "passive", string(sess.Status), sess.CreatedAt
	} else if sc, aerr := p.Active.GetByID(ctx, engagementID); aerr == nil {
		ownerType, modeStr, statusStr, createdAt = "active_scan", "active", string(sc.Status), sc.CreatedAt
	} else {
		return View{}, fmt.Errorf("owner %s not found in passive_session or active_scan", engagementID)
	}

	// host 为空表示「列本 owner 跨 host 的全部 finding」，由下方 host 过滤分支跳过。
	effectiveHost := host

	findings, err := p.Findings.ListByOwner(ctx, ownerType, engagementID)
	if err != nil {
		return View{}, fmt.Errorf("finding.ListByOwner: %w", err)
	}
	// relation 表 owner 列暂未加，仍按 engagement_id 查；过渡期 active relation 可能查不到
	// （新 finding 的 engagement_id 与旧 engagement 关联，relation 写入仍走旧路径，此处兼容）。
	relations, err := p.Findings.ListRelationsByEngagement(ctx, engagementID)
	if err != nil {
		return View{}, fmt.Errorf("finding.ListRelationsByEngagement: %w", err)
	}

	// host 过滤：finding.host 可能跨多个值（虽然 ListByEngagement 已过滤一次）。
	if effectiveHost != "" {
		filtered := findings[:0]
		for _, f := range findings {
			if f.Host == effectiveHost {
				filtered = append(filtered, f)
			}
		}
		findings = filtered
	}

	v := View{
		OwnerID:     engagementID,
		Host:        effectiveHost,
		GeneratedAt: time.Now().UTC(),
	}

	// origin / goal 永远存在。
	originID := "origin"
	goalID := "goal"
	v.Nodes = append(v.Nodes,
		Node{
			ID:    originID,
			Kind:  KindOrigin,
			Label: effectiveHost,
			Payload: map[string]any{
				"target_host":   effectiveHost,
				"engagement_id": engagementID, // 字段名保持兼容前端 viewer；值是 owner_id
				"owner_type":    ownerType,
				"started_at":    createdAt,
				"mode":          modeStr,
				"status":        statusStr,
			},
		},
		Node{
			ID:    goalID,
			Kind:  KindGoal,
			Label: "枚举所有可达漏洞",
		},
	)

	// 用 map 去重 endpoint / parameter 节点。
	endpointSeen := map[string]bool{}
	paramSeen := map[string]bool{}

	for _, f := range findings {
		epID, epLabel, epOK := buildEndpointFromFinding(f)
		if !epOK {
			// finding.target 缺 method/path——直接挂 origin → finding，跳过 endpoint/parameter。
			fid := "finding:" + f.ID
			v.Nodes = append(v.Nodes, findingNode(fid, f))
			v.Edges = append(v.Edges,
				Edge{From: originID, To: fid, Kind: EdgeDiscovered},
				Edge{From: fid, To: goalID, Kind: EdgeContributesTo},
			)
			continue
		}

		if !endpointSeen[epID] {
			endpointSeen[epID] = true
			v.Nodes = append(v.Nodes, Node{
				ID: epID, Kind: KindEndpoint, Label: epLabel,
				Payload: map[string]any{
					"method": payloadString(f.Target, "method"),
					"path":   payloadString(f.Target, "path"),
					"host":   f.Host,
				},
			})
			v.Edges = append(v.Edges, Edge{From: originID, To: epID, Kind: EdgeDiscovered})
		}

		// parameter 节点——只有 SQLi 等带 param 的 finding 才有
		params := extractParams(f.Target)
		fid := "finding:" + f.ID
		v.Nodes = append(v.Nodes, findingNode(fid, f))

		if len(params) == 0 {
			// 无 param：直接 endpoint → finding
			v.Edges = append(v.Edges, Edge{From: epID, To: fid, Kind: EdgeVulnerableTo})
		} else {
			for _, pp := range params {
				pID := epID + "#" + pp.Location + ":" + pp.Name
				if !paramSeen[pID] {
					paramSeen[pID] = true
					v.Nodes = append(v.Nodes, Node{
						ID: pID, Kind: KindParameter,
						Label: pp.Location + ":" + pp.Name,
						Payload: map[string]any{
							"location": pp.Location,
							"name":     pp.Name,
							"endpoint": epID,
						},
					})
					v.Edges = append(v.Edges, Edge{From: epID, To: pID, Kind: EdgeHasParam})
				}
				v.Edges = append(v.Edges, Edge{From: pID, To: fid, Kind: EdgeVulnerableTo})
			}
		}

		v.Edges = append(v.Edges, Edge{From: fid, To: goalID, Kind: EdgeContributesTo})
	}

	// finding_relation 提供的 enables 边（v0026 重建：write_relation 工具写入）
	for _, r := range relations {
		v.Edges = append(v.Edges, Edge{
			From:  "finding:" + r.FromFindingID,
			To:    "finding:" + r.ToFindingID,
			Kind:  EdgeEnables,
			Label: payloadString(r.Payload, "reason"),
		})
	}

	// 排序保证输出稳定（前端 hash 缓存友好）
	sort.SliceStable(v.Nodes, func(i, j int) bool { return v.Nodes[i].ID < v.Nodes[j].ID })
	sort.SliceStable(v.Edges, func(i, j int) bool {
		if v.Edges[i].From != v.Edges[j].From {
			return v.Edges[i].From < v.Edges[j].From
		}
		if v.Edges[i].To != v.Edges[j].To {
			return v.Edges[i].To < v.Edges[j].To
		}
		return v.Edges[i].Kind < v.Edges[j].Kind
	})

	return v, nil
}

// findingNode 把 finding 行渲染成节点；payload 含 severity / confidence / kind / summary 等
// 让前端能着色 + 显示 tooltip。
//
// Label 取 f.Summary 第一行（≤72 chars），参考 git commit message convention——
// 第一行充当短标题，完整 summary 通过 payload 给前端。
func findingNode(id string, f finding.VulnFinding) Node {
	return Node{
		ID:    id,
		Kind:  KindFinding,
		Label: firstLine(f.Summary, 72),
		Payload: map[string]any{
			"finding_id": f.ID,
			"severity":   f.Severity,
			"summary":    f.Summary,
			"created_at": f.CreatedAt,
		},
	}
}

// firstLine 截取字符串第一行（按 \n 分割），并按 max char 上限再截。
// 用于把 summary 自由文本压成 UI Label 友好的短标题。
func firstLine(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if max > 0 && len(s) > max {
		s = s[:max]
	}
	return s
}

// buildEndpointFromFinding 从 finding.target 拼 endpoint 节点 ID + label。
// dedup_key 形式：`endpoint:<METHOD>:<host>:<path_template>`。
// path 模板化：URL 里数字 → :id、UUID → :uuid（避免每个 /user/1, /user/2 都成独立节点）。
func buildEndpointFromFinding(f finding.VulnFinding) (id, label string, ok bool) {
	method := strings.ToUpper(payloadString(f.Target, "method"))
	path := payloadString(f.Target, "path")
	if path == "" {
		// 部分老 finding 把完整 URL 放在 url 字段
		if u := payloadString(f.Target, "url"); u != "" {
			parsed, err := url.Parse(u)
			if err == nil {
				path = parsed.Path
			}
		}
	}
	if method == "" || path == "" {
		return "", "", false
	}
	tpl := templatizePath(path)
	id = "endpoint:" + method + ":" + f.Host + ":" + tpl
	label = method + " " + tpl
	return id, label, true
}

// templatizePath 把 path 中的数字段、UUID、长 hex 替换成占位符——
// 避免 /user/1, /user/2 在图里分裂成两个 endpoint 节点。
func templatizePath(p string) string {
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		if seg == "" {
			continue
		}
		switch {
		case isAllDigits(seg):
			parts[i] = ":id"
		case isUUID(seg):
			parts[i] = ":uuid"
		case len(seg) >= 16 && isHex(seg):
			parts[i] = ":hex"
		}
	}
	return strings.Join(parts, "/")
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !isHexRune(r) {
				return false
			}
		}
	}
	return true
}

func isHex(s string) bool {
	for _, r := range s {
		if !isHexRune(r) {
			return false
		}
	}
	return s != ""
}

func isHexRune(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// extractedParam 从 finding.target.params 提取出来的单个参数。
type extractedParam struct {
	Location string
	Name     string
}

// extractParams 解析 finding.target.params 字段（多种历史 schema 兼容）。
//
// 已知 schema：
//
//   - {"params":[{"location":"query","name":"id"}, ...]}    新版
//   - {"params":[{"in":"query","name":"id"}]}               兼容 OpenAPI 风格
//   - {"params":{"query":["id","filter"], "body":["x"]}}    旧版 map 风格
func extractParams(target json.RawMessage) []extractedParam {
	if len(target) == 0 {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(target, &raw); err != nil {
		return nil
	}
	pRaw, ok := raw["params"]
	if !ok || len(pRaw) == 0 {
		return nil
	}

	// 先试数组
	var arr []map[string]string
	if err := json.Unmarshal(pRaw, &arr); err == nil {
		out := make([]extractedParam, 0, len(arr))
		for _, m := range arr {
			loc := m["location"]
			if loc == "" {
				loc = m["in"]
			}
			if loc == "" || m["name"] == "" {
				continue
			}
			out = append(out, extractedParam{Location: loc, Name: m["name"]})
		}
		return out
	}

	// 再试 map
	var m map[string][]string
	if err := json.Unmarshal(pRaw, &m); err == nil {
		var out []extractedParam
		// 排序 location key 让输出稳定
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, loc := range keys {
			for _, name := range m[loc] {
				out = append(out, extractedParam{Location: loc, Name: name})
			}
		}
		return out
	}
	return nil
}

// payloadString 从 jsonb 取顶层 string 字段；解析失败/类型不对返回 ""。
func payloadString(raw json.RawMessage, key string) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
