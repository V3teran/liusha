package skill

import (
	"context"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/registry"
)

// Reader 是 skill 渐进式加载的消费接口（StoreReader 实现，测试可替换）：
//
//	Metas(ctx) 拉本视图可见的索引卡（Tier 1，不读正文）
//	Load(ctx)  懒加载单个 skill 完整正文（Tier 2，后端缓存命中时 0 文件 IO / 0 DB 查询）
//
// ctx 贯穿：后端读取要带超时/取消。
type Reader interface {
	Metas(ctx context.Context) ([]*Card, error)
	Load(ctx context.Context, name string) (*Card, error)
}

// View 是 Reader 的白名单视图：agent.skills 声明哪些 skill，本视图就只暴露哪些。
// 名单判定与 function_tools / cli_tools 共用 registry.Allows（nil=拒绝，空=空集，含 "*"=全部）。
// 装配层（cognition）按各 agent 配置分别建视图——executor 与 evaluator 可各自独立。
type View struct {
	src   Reader
	names []string
}

// Allow 给 src 建白名单视图。声明里不存在的名字在 Metas 过滤时自然消失（配置漂移不致命）。
func Allow(src Reader, names []string) *View {
	return &View{src: src, names: names}
}

// Metas 返回白名单内的索引卡（Tier 1；源序，停用/不存在的声明项自然缺席）。
func (v *View) Metas(ctx context.Context) ([]*Card, error) {
	all, err := v.src.Metas(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*Card, 0, len(all))
	for _, c := range all {
		if registry.Allows(v.names, c.Key) {
			out = append(out, c)
		}
	}
	return out, nil
}

// Load 仅放行白名单内的名字（Tier 2 正文）。
func (v *View) Load(ctx context.Context, name string) (*Card, error) {
	if !registry.Allows(v.names, name) {
		return nil, fmt.Errorf("skill %q 不在本 agent 的 skills 白名单内", name)
	}
	return v.src.Load(ctx, name)
}

// RenderIndex 把 Tier 1 索引（寻址名 + 显示名 + description）渲染成 system prompt
// 段落。executor engine 与 evaluator judge 共用本渲染——两侧 agent 看到的 skill
// 目录口径完全一致。寻址一律用 Key（目录名/DB code）；显示名/描述只作选题线索。
// 刻意不含 category：业界（Agent Skills 规范等）索引即 name+description，领域语义
// 由 description 承载；category 属页面组织层，进索引在目录规模小时是纯噪声。
func RenderIndex(cards []*Card) string {
	if len(cards) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("**可用技能（skill）索引**——先用 read_skill(name) 拉全文再动手，别凭记忆猜打法：\n")
	for _, c := range cards {
		key := c.Key
		if key == "" {
			key = c.Name
		}
		if c.Name != "" && c.Name != key {
			fmt.Fprintf(&sb, "- %s（%s）: %s\n", key, c.Name, c.Description)
		} else {
			fmt.Fprintf(&sb, "- %s: %s\n", key, c.Description)
		}
	}
	return sb.String()
}
