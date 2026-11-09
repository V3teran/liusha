package skill

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/skillstore"
)

// Source 是 StoreReader 依赖的最小后端能力（*configstore.Store 满足——生效技能 =
// 内置技能叠加用户自定义 + 自建技能，用户层走三级缓存，前端改动广播失效后下次读即最新）。
type Source interface {
	EnabledSkills(ctx context.Context) ([]skillstore.Skill, error)
	Skill(ctx context.Context, code string) (skillstore.Skill, error)
}

// StoreReader 把生效技能适配成渐进式加载 Reader，是运行时（runner 认知循环）的后端：
//
//	Tier 1 Metas：EnabledSkills 列表 → 索引卡（name/description）
//	Tier 2 Load ：Skill 单条 → 全文卡（read_skill 正文）
//
// 前端对技能的自定义无需重启 runner 即生效。
type StoreReader struct {
	src Source
}

// NewStoreReader 构造 Reader。
func NewStoreReader(src Source) *StoreReader { return &StoreReader{src: src} }

// skillToCard 生效技能 → 加载卡。Key = code（裸名寻址）。
func skillToCard(sk skillstore.Skill) *Card {
	return &Card{
		Key:         sk.Code,
		Name:        sk.Name,
		Description: sk.Description,
		Category:    sk.Category,
		Body:        sk.Body,
	}
}

// Metas 全部启用技能的索引卡（Tier 1）。正文显式清空——正文只走 Load（Tier 2），
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

// Load 按裸名读单个技能全文（Tier 2）。已停用的拒载——Tier 1 列表已过滤，
// 此处是对 agent 技能白名单声明了但被停用的兜底拦截，给 LLM 可读错误。
func (r *StoreReader) Load(ctx context.Context, name string) (*Card, error) {
	sk, err := r.src.Skill(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("skill %q 不存在: %w", name, err)
	}
	if !sk.Enabled {
		return nil, fmt.Errorf("skill %q 已停用，本任务不可读", name)
	}
	return skillToCard(sk), nil
}
