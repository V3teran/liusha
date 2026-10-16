package skill

import (
	"context"
	"fmt"
	"strings"
)

// Reader 是 skill 渐进式加载的消费接口（文件 Set 与 DB StoreReader 均实现）：
//
//	Metas(ctx) 拉本视图可见的 frontmatter 列表（Tier 1 索引，不读正文）
//	Load(ctx)  懒加载单个 skill 完整正文（Tier 2，后端缓存命中时 0 文件 IO / 0 DB 查询）
//
// ctx 贯穿：DB 后端的每次读写都要带超时/取消。运行时事实源是 DB（cfgstore
// 多级缓存），文件 Set 只服务种子导入与测试。
type Reader interface {
	Metas(ctx context.Context) ([]*Card, error)
	Load(ctx context.Context, name string) (*Card, error)
}

// Set 把多个分类目录（skills/tooling、skills/vuln …）的 Loader 聚合成单一裸名命名空间。
//
// 各 root 内的 skill 名本就裸（tooling 下 "browser-use"、vuln 下 "dom-xss"），
// 跨目录重名时启动期报错；agent 的 skills 声明因此可以统一写成
// View 是 Reader 的白名单视图：agent.skills 声明哪些 skill，本视图就只暴露哪些。
// 语义与 function_tools/cli_tools 白名单一致：nil=全部（未配置视图），空=空集。
// 装配层（cognition）按各 agent 配置分别建视图——executor 与 evaluator 可各自独立。
type View struct {
	src   Reader
	allow map[string]struct{} // nil=全部
}

// Allow 给 src 建白名单视图。names=nil 表示不过滤（全量）；空 slice=空集。
// 声明里不存在的名字在 Metas 过滤时自然消失（配置漂移不致命，索引里看不到）。
func Allow(src Reader, names []string) *View {
	v := &View{src: src}
	if names == nil {
		return v
	}
	v.allow = make(map[string]struct{}, len(names))
	for _, n := range names {
		v.allow[n] = struct{}{}
	}
	return v
}

// Metas 返回白名单内的 frontmatter（Tier 1 索引；源序，禁用/不存在的声明项自然缺席）。
func (v *View) Metas(ctx context.Context) ([]*Card, error) {
	all, err := v.src.Metas(ctx)
	if err != nil {
		return nil, err
	}
	if v.allow == nil {
		return all, nil
	}
	out := make([]*Card, 0, len(all))
	for _, c := range all {
		if _, ok := v.allow[c.Key]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

// Load 仅放行白名单内的名字（Tier 2 正文）。
func (v *View) Load(ctx context.Context, name string) (*Card, error) {
	if v.allow != nil {
		if _, ok := v.allow[name]; !ok {
			return nil, fmt.Errorf("skill %q 不在本 agent 的 skills 白名单内", name)
		}
	}
	return v.src.Load(ctx, name)
}

// RenderIndex 把 Tier 1 索引（frontmatter：寻址名 + 显示名 + description）渲染成
// system prompt 段落。executor engine 与 evaluator judge 共用本渲染——两侧 agent
// 看到的 skill 目录口径完全一致。寻址一律用 Key（目录名/DB code）；显示名/描述只作选题线索。
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
