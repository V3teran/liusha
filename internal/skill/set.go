package skill

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Reader 是 skill 渐进式加载的消费接口（文件 Set 与 DB StoreReader 均实现）：
//
//	Metas(ctx) 拉本视图可见的 frontmatter 列表（Tier 1 索引，不读正文）
//	Load(ctx)  懒加载单个 skill 完整正文（Tier 2，后端缓存命中时 0 文件 IO / 0 DB 查询）
//
// ctx 贯穿：DB 后端的每次读写都要带超时/取消。运行时事实源是 DB（cfgcache
// 多级缓存），文件 Set 只服务种子导入与测试。
type Reader interface {
	Metas(ctx context.Context) ([]*Card, error)
	Load(ctx context.Context, name string) (*Card, error)
}

// Set 把多个分类目录（skills/tooling、skills/vuln …）的 Loader 聚合成单一裸名命名空间。
//
// 各 root 内的 skill 名本就裸（tooling 下 "browser-use"、vuln 下 "dom-xss"），
// 跨目录重名时启动期报错；agent 的 skills 声明因此可以统一写成
// [bac, browser-use, dom-xss]，不必带分类前缀。
type Set struct {
	cats    []string  // 与 loaders 平行：各 Loader 的类别标签（tooling/vuln）
	loaders []*Loader // 与 cats 平行
}

// NewSet 按 categories（类别→root 路径）聚合。按类别名稳定排序遍历 map，
// 保证裸名冲突检测与解析顺序确定（不依赖 map 遍历序）。
func NewSet(categories map[string]string) *Set {
	names := make([]string, 0, len(categories))
	for c := range categories {
		names = append(names, c)
	}
	sort.Strings(names)
	s := &Set{}
	for _, c := range names {
		s.cats = append(s.cats, c)
		s.loaders = append(s.loaders, NewLoader(categories[c]))
	}
	return s
}

// Index 逐目录扫 frontmatter 进缓存。root 不存在的类别跳过（环境缺失→该类降级，
// 不拖垮其余类别）；SKILL.md 畸形则 fail-fast（配置错误必须暴露）。跨目录裸名
// 冲突直接报错——两目录各放同名 skill 会让 agent.skills 声明产生歧义。
// 返回成功索引的裸名列表。
func (s *Set) Index() ([]string, error) {
	var all []string
	seen := make(map[string]string) // 裸名 → 首次出现的类别
	keptLoaders := s.loaders[:0:0]
	keptCats := s.cats[:0:0]
	for i, l := range s.loaders {
		if _, statErr := os.Stat(l.root); os.IsNotExist(statErr) {
			continue // 类别目录缺失：整类跳过
		}
		names, err := l.Index()
		if err != nil {
			return nil, err
		}
		keptLoaders = append(keptLoaders, l)
		keptCats = append(keptCats, s.cats[i])
		for _, n := range names {
			if prev, dup := seen[n]; dup {
				return nil, fmt.Errorf("skill 裸名冲突: %q 同时存在于 %q 与 %q 目录", n, prev, s.cats[i])
			}
			seen[n] = s.cats[i]
			all = append(all, n)
		}
	}
	s.loaders = keptLoaders
	s.cats = keptCats
	return all, nil
}

// Load 按裸名跨目录懒加载完整 Card（Tier 2）。
func (s *Set) Load(_ context.Context, name string) (*Card, error) {
	for _, l := range s.loaders {
		if c, err := l.Load(name); err == nil {
			return c, nil
		}
	}
	return nil, fmt.Errorf("skill %q 不存在", name)
}

// Meta 按裸名跨目录取 frontmatter（不读正文）；找不到返回 ok=false。
func (s *Set) Meta(name string) (*Card, bool) {
	for _, l := range s.loaders {
		if c, ok := l.MetaOnly(name); ok {
			return c, true
		}
	}
	return nil, false
}

// Metas 返回全部已 Index 的 frontmatter（Tier 1 全量索引），按 Key 排序——
// Loader 底层 sync.Map 遍历序随机，不排序会让 Tier 1 索引段在两次启动间漂移。
// （DB 后端 StoreReader 由 SQL ORDER BY 保证，天然稳定。）
func (s *Set) Metas(_ context.Context) ([]*Card, error) {
	out := make([]*Card, 0)
	for _, l := range s.loaders {
		out = append(out, l.List()...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

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

// Allow 是 Set 的便捷封装（文件后端/测试用）。
func (s *Set) Allow(names []string) *View { return Allow(s, names) }

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
			sb.WriteString(fmt.Sprintf("- %s（%s）: %s\n", key, c.Name, c.Description))
		} else {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", key, c.Description))
		}
	}
	return sb.String()
}
