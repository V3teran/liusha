package skill

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/skillstore"
)

// SkillSource 是 StoreReader 依赖的最小后端能力（*cache.Store 满足——
// EnabledSkills / SkillByCode 均为 L1 内存 → L2 redis → DB 的多级缓存读，
// 前端写经失效总线广播，本进程 L1 被动清，下次读即最新）。
type SkillSource interface {
	EnabledSkills(ctx context.Context) ([]skillstore.Skill, error)
	SkillByCode(ctx context.Context, code string) (skillstore.Skill, error)
}

// StoreReader 把 DB 事实源的 skill 配置适配成渐进式加载 Reader——文件 Set 的
// DB 对位物，运行时（runner 认知循环）的唯一后端：
//
//	Tier 1 Metas：EnabledSkills 列表 → frontmatter 卡（name/description 进索引）
//	Tier 2 Load ：SkillByCode 单条 → 全文卡（read_skill 正文）
//
// 文件（skills/*.md）自此只是种子：api 启动 insert-only 导入 + 清死行，
// 前端对 skill 的修改以 DB 为准，无需重启 runner 即生效。
type StoreReader struct {
	src SkillSource
}

// NewStoreReader 构造 DB 后端 Reader。
func NewStoreReader(src SkillSource) *StoreReader { return &StoreReader{src: src} }

// skillToCard DB 行 → 渐进式加载卡。Key=code（=目录名，裸名寻址口径）。
func skillToCard(sk skillstore.Skill) *Card {
	return &Card{
		Key:         sk.Code,
		Name:        sk.Name,
		Description: sk.Description,
		Category:    sk.Category,
		Body:        sk.Body,
	}
}

// Metas 全部启用 skill 的 frontmatter（Tier 1）。
// 目录不带正文：DB 行虽含 body，此处显式清空——正文只走 Load（Tier 2），
// 防止上层误用以及白名单视图被绕过时正文随索引扩散。
func (r *StoreReader) Metas(ctx context.Context) ([]*Card, error) {
	skills, err := r.src.EnabledSkills(ctx)
	if err != nil {
		return nil, fmt.Errorf("读启用 skill 列表: %w", err)
	}
	out := make([]*Card, 0, len(skills))
	for i := range skills {
		c := skillToCard(skills[i])
		c.Body = ""
		out = append(out, c)
	}
	return out, nil
}

// Load 按裸名读单个 skill 全文（Tier 2）。禁用行拒载——Tier 1 列表已过滤，
// 此处是对 agent.skills 声明了但被运维禁用的兜底拦截，给 LLM 可读错误。
func (r *StoreReader) Load(ctx context.Context, name string) (*Card, error) {
	sk, err := r.src.SkillByCode(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("skill %q 不存在: %w", name, err)
	}
	if !sk.Enabled {
		return nil, fmt.Errorf("skill %q 已被禁用（运维下线），本任务不可读", name)
	}
	return skillToCard(sk), nil
}
