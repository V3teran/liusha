// Package seed 把出厂配置首次导入 DB。
//
// insert-only 首填语义：DB 是事实源，种子只填**空库**，按 code 判存在——
// 已存在的行一律跳过，绝不覆盖前端/运维在 DB 里的改动。
// agent 与内置技能的出厂内容来自 internal/builtin（编译进程序）。
package seed

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	agent "github.com/V3teran/liusha/internal/agent"
	"github.com/V3teran/liusha/internal/builtin"
	skill "github.com/V3teran/liusha/internal/skillstore"
)

// SkillsSeed 是一次 skill 种子导入的结果，供调用方（api 启动 / reseed）做缓存失效。
type SkillsSeed struct {
	Inserted []string // 新插入的 skill code
	Upserted []string // 强制覆盖写入的 skill code（仅 force 语义）
	Pruned   []string // 清理的死行 code（出厂内容里已不存在的内置 skill）
}

// Touched 汇总全部被写动过的 code（insert/upsert/prune 并集）。
func (s SkillsSeed) Touched() []string {
	out := make([]string, 0, len(s.Inserted)+len(s.Upserted)+len(s.Pruned))
	out = append(out, s.Inserted...)
	out = append(out, s.Upserted...)
	out = append(out, s.Pruned...)
	return out
}

// Result 是一次 Import 的整体结果。
type Result struct {
	Agents []string // 写入的 agent code（insert 或 force）
	Skills SkillsSeed
}

// Import 把出厂 agent 与内置技能 insert-only 首填进 DB，并清理出厂内容里已删除的
// 内置 skill 死行。返回写动清单供调用方失效多级缓存。
func Import(ctx context.Context, h *agent.Store, s *skill.Store) (Result, error) {
	var res Result
	agents, err := importAgents(ctx, h, false)
	if err != nil {
		return res, fmt.Errorf("import agents: %w", err)
	}
	res.Agents = agents
	skillsRes, err := importSkills(ctx, s, false)
	if err != nil {
		return res, fmt.Errorf("import skills: %w", err)
	}
	res.Skills = skillsRes
	return res, nil
}

// ImportAgentsForce 把出厂 agent 强制覆盖写入 DB（reset 语义）。
func ImportAgentsForce(ctx context.Context, h *agent.Store) ([]string, error) {
	return importAgents(ctx, h, true)
}

// ImportSkillsForce 把内置技能以强制覆盖语义写入内置行（reset 语义），用户自建不动。
func ImportSkillsForce(ctx context.Context, s *skill.Store) (SkillsSeed, error) {
	return importSkills(ctx, s, true)
}

func notFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func importAgents(ctx context.Context, h *agent.Store, force bool) ([]string, error) {
	updated := make([]string, 0)
	for _, a := range builtin.Agents() {
		if _, err := h.GetByCode(ctx, a.Code); err == nil && !force {
			continue
		} else if err != nil && !notFound(err) {
			return nil, fmt.Errorf("查 agent %q: %w", a.Code, err)
		}
		complexity := strings.TrimSpace(a.Complexity)
		if _, err := h.Upsert(ctx, a.Code, a.Name, a.Description, a.Charter, agent.UpdateParams{
			FunctionTools: &a.FunctionTools,
			CliTools:      &a.CliTools,
			Skills:        &a.Skills,
			MaxIterations: &a.MaxIterations,
			Complexity:    &complexity,
		}); err != nil {
			return nil, fmt.Errorf("写 agent %q: %w", a.Code, err)
		}
		updated = append(updated, a.Code)
	}
	return updated, nil
}

func importSkills(ctx context.Context, s *skill.Store, force bool) (SkillsSeed, error) {
	var res SkillsSeed
	builtins := builtin.Skills()
	keepCodes := make([]string, 0, len(builtins))
	for _, b := range builtins {
		keepCodes = append(keepCodes, b.Code)
		sk := skill.Skill{
			Code: b.Code, Category: b.Category, Name: b.Name,
			Description: b.Description, Body: b.Body, IsBuiltin: true, Enabled: true,
		}
		if force {
			if _, err := s.UpsertBuiltin(ctx, sk); err != nil {
				return res, fmt.Errorf("强制覆盖 skill %q: %w", b.Code, err)
			}
			res.Upserted = append(res.Upserted, b.Code)
			continue
		}
		if _, err := s.GetByCode(ctx, b.Code); err == nil {
			continue
		} else if !notFound(err) {
			return res, fmt.Errorf("查 skill %q: %w", b.Code, err)
		}
		if _, err := s.Create(ctx, sk); err != nil {
			return res, fmt.Errorf("建 skill %q: %w", b.Code, err)
		}
		res.Inserted = append(res.Inserted, b.Code)
	}
	pruned, err := s.PruneBuiltinNotIn(ctx, keepCodes)
	if err != nil {
		return res, fmt.Errorf("清理内置 skill 死行: %w", err)
	}
	res.Pruned = pruned
	return res, nil
}
