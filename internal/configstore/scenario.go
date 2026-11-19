package configstore

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/V3teran/liusha/internal/builtin"
	"github.com/V3teran/liusha/internal/builtinsync"
	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/override"
	"github.com/V3teran/liusha/internal/scenariostore"
)

// 场景表的缓存键带出厂内容摘要（同技能，见 keySkills）。
var keyScenarios = "configstore:scenarios:" + builtinsync.Digest(scenariostore.Seeds())

func (s *Store) scenarioRecords(ctx context.Context) ([]scenariostore.Record, error) {
	return cachestore.ReadThrough(ctx, s.cache, keyScenarios,
		func([]scenariostore.Record) []string { return []string{keyScenarios} },
		s.scenarios.List)
}

// ScenarioDetail 是场景页需要的全部信息。内置场景附出厂内容与各字段相对出厂的状态。
type ScenarioDetail struct {
	Scenario  scenariostore.Scenario    `json:"scenario"`
	Defaults  *scenariostore.Content    `json:"defaults"`
	Overrides map[string]override.State `json:"overrides"`
	// Shadows 为真表示这个自建场景与某内置场景同名，内置版本被遮蔽。
	Shadows bool `json:"shadows_builtin"`
}

// liveScenarios 返回未被删除的场景行（内置在前，再按 code 升序）。
func (s *Store) liveScenarios(ctx context.Context) ([]scenariostore.Record, error) {
	recs, err := s.scenarioRecords(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(slices.Clone(recs), func(r scenariostore.Record) bool { return r.Deleted }), nil
}

// DeletedScenarios 返回被用户删除、可恢复的内置场景（内容取当前出厂，恢复即得到它）。
func (s *Store) DeletedScenarios(ctx context.Context) ([]scenariostore.Scenario, error) {
	recs, err := s.scenarioRecords(ctx)
	if err != nil {
		return nil, err
	}
	var out []scenariostore.Scenario
	for _, r := range recs {
		if b, ok := builtin.ScenarioByCode(r.Code); ok && r.Deleted {
			out = append(out, scenariostore.Scenario{
				Code: b.Code, Name: b.Name, Description: b.Description, Body: b.Body, Enabled: true, IsBuiltin: true,
			})
		}
	}
	return out, nil
}

// RestoreScenario 把用户删除的内置场景恢复成当前出厂内容。不是已删除的内置场景返回 ErrNotFound。
func (s *Store) RestoreScenario(ctx context.Context, code string) (scenariostore.Scenario, error) {
	if err := s.scenarios.Restore(ctx, code); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return scenariostore.Scenario{}, fmt.Errorf("已删除的内置场景 %q %w", code, ErrNotFound)
		}
		return scenariostore.Scenario{}, err
	}
	if err := s.cache.Invalidate(ctx, keyScenarios); err != nil {
		return scenariostore.Scenario{}, err
	}
	return s.Scenario(ctx, code)
}

// Scenarios 返回全部场景（不含已删除的内置场景；内置在前，再按 code 升序）。
func (s *Store) Scenarios(ctx context.Context) ([]scenariostore.Scenario, error) {
	recs, err := s.liveScenarios(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]scenariostore.Scenario, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.Scenario)
	}
	return out, nil
}

// Scenario 按 code 返回场景。
func (s *Store) Scenario(ctx context.Context, code string) (scenariostore.Scenario, error) {
	all, err := s.Scenarios(ctx)
	if err != nil {
		return scenariostore.Scenario{}, err
	}
	for _, sc := range all {
		if sc.Code == code {
			return sc, nil
		}
	}
	return scenariostore.Scenario{}, fmt.Errorf("场景 %q %w", code, ErrNotFound)
}

// UsableScenario 返回建任务可用的场景：存在且启用；否则返回 scenariostore.ErrUnavailable（400）——
// 请求点名的场景用不了时明确拒绝，不悄悄换成别的场景。
func (s *Store) UsableScenario(ctx context.Context, code string) (scenariostore.Scenario, error) {
	sc, err := s.Scenario(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return scenariostore.Scenario{}, scenariostore.NotFound(code)
	}
	if err != nil {
		return scenariostore.Scenario{}, err
	}
	return sc, scenariostore.Usable(sc)
}

// DefaultScenario 返回缺省场景（请求不指定场景、代理捕获的任务用它）；它被停用或删除时返回
// nil——缺省即无场景，由用户的操作决定，不另找兜底。
func (s *Store) DefaultScenario(ctx context.Context) (*scenariostore.Scenario, error) {
	sc, err := s.UsableScenario(ctx, scenariostore.DefaultCode)
	if errors.Is(err, scenariostore.ErrUnavailable) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sc, nil
}

// ScenarioDetails 返回全部场景及其详情（顺序同 Scenarios）。
func (s *Store) ScenarioDetails(ctx context.Context) ([]ScenarioDetail, error) {
	recs, err := s.liveScenarios(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ScenarioDetail, 0, len(recs))
	for _, r := range recs {
		d := ScenarioDetail{Scenario: r.Scenario, Overrides: map[string]override.State{}}
		b, seeded := builtin.ScenarioByCode(r.Code)
		switch {
		case r.IsBuiltin && seeded:
			def := scenariostore.Defaults(b)
			d.Defaults = &def
			ov, err := describeAgainstSeed(scenariostore.ContentOf(r.Scenario), def, r.SeedHash, scenariostore.SeedHash(b))
			if err != nil {
				return nil, fmt.Errorf("场景 %s: %w", r.Code, err)
			}
			d.Overrides = ov
		case !r.IsBuiltin:
			d.Shadows = seeded
		}
		out = append(out, d)
	}
	return out, nil
}

// ScenarioDetail 返回单个场景的详情。
func (s *Store) ScenarioDetail(ctx context.Context, code string) (ScenarioDetail, error) {
	all, err := s.ScenarioDetails(ctx)
	if err != nil {
		return ScenarioDetail{}, err
	}
	for _, d := range all {
		if d.Scenario.Code == code {
			return d, nil
		}
	}
	return ScenarioDetail{}, fmt.Errorf("场景 %q %w", code, ErrNotFound)
}

// CreateScenario 新建自建场景。与内置或已有场景同名返回 scenariostore.ErrConflict。
func (s *Store) CreateScenario(ctx context.Context, sc scenariostore.Scenario) (scenariostore.Scenario, error) {
	sc.IsBuiltin = false
	if err := scenariostore.ValidateNew(sc); err != nil {
		return scenariostore.Scenario{}, err
	}
	if _, ok := builtin.ScenarioByCode(sc.Code); ok {
		return scenariostore.Scenario{}, fmt.Errorf("%w：%s 是内置场景", scenariostore.ErrConflict, sc.Code)
	}
	created, err := s.scenarios.Create(ctx, sc)
	if err != nil {
		return scenariostore.Scenario{}, err
	}
	return created.Scenario, s.cache.Invalidate(ctx, keyScenarios)
}

// PatchScenario 修改场景的可修改字段（内置与自建同一路径）。
func (s *Store) PatchScenario(ctx context.Context, code string, changes override.Document) (scenariostore.Scenario, error) {
	return s.updateScenario(ctx, code, func(r scenariostore.Record) (scenariostore.Content, error) {
		next, err := mergeFields(scenariostore.Schema(), scenariostore.ContentOf(r.Scenario), changes)
		if err != nil {
			return scenariostore.Content{}, err
		}
		return next, scenariostore.Validate(next)
	})
}

// ResetScenario 把内置场景的指定字段恢复为出厂内容（不传字段则全部恢复）。
func (s *Store) ResetScenario(ctx context.Context, code string, fields ...string) (scenariostore.Scenario, error) {
	return s.updateScenario(ctx, code, func(r scenariostore.Record) (scenariostore.Content, error) {
		b, ok := builtin.ScenarioByCode(code)
		if !r.IsBuiltin || !ok {
			return scenariostore.Content{}, scenariostore.ErrCustom
		}
		next, err := resetFields(scenariostore.Schema(), scenariostore.ContentOf(r.Scenario), scenariostore.Defaults(b), fields)
		if err != nil {
			return scenariostore.Content{}, err
		}
		return next, scenariostore.Validate(next)
	})
}

func (s *Store) updateScenario(ctx context.Context, code string, fn func(scenariostore.Record) (scenariostore.Content, error)) (scenariostore.Scenario, error) {
	updated, err := s.scenarios.Update(ctx, code, fn)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return scenariostore.Scenario{}, fmt.Errorf("场景 %q %w", code, ErrNotFound)
		}
		return scenariostore.Scenario{}, err
	}
	return updated.Scenario, s.cache.Invalidate(ctx, keyScenarios)
}

// DeleteScenario 删除场景（自建删行，内置记删除标记、可恢复；被定时模板引用不可删）。
func (s *Store) DeleteScenario(ctx context.Context, code string) error {
	if err := s.scenarios.Delete(ctx, code); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("场景 %q %w", code, ErrNotFound)
		}
		return err
	}
	return s.cache.Invalidate(ctx, keyScenarios)
}
