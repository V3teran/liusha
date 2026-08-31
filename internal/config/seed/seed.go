// Package seed 把磁盘上的 agent/scenario 配置首次导入 DB。
//
// insert-only 首填语义（见 D6）：DB 是事实源，种子只填**空库**，按 code 判存在——
// 已存在的行一律跳过，绝不覆盖前端/运维在 DB 里的改动。导入顺序遵守 FK 依赖：
// agent → scenario（scenario.solo_executor_id 引用 agent）。
//
// 目录约定（dir 为配置根）：
//   - dir/agents/*.md   ：操作员 charter（frontmatter 元信息 + body 方法论正文）
//   - dir/scenarios/*.md ：场景（frontmatter + body 领域侧重 instruction）
package seed

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"

	cfgagent "github.com/V3teran/liusha/internal/config/agent"
	cfgscenario "github.com/V3teran/liusha/internal/config/scenario"
)

// agentFront 是 agents/*.md frontmatter 的解析目标。
// id 用作稳定引用键 code；kind∈{planner,domain}；body 取 markdown 正文。
// function_tools 是内置函数工具（进程内原生函数 code 列表）。
// cli_tools 是外置 CLI 工具集（tools.yaml 名字），严格白名单，空=不装配任何外部工具。
type agentFront struct {
	ID            string   `yaml:"id"`
	Name          string   `yaml:"name"`
	Description   string   `yaml:"description"`
	Kind          string   `yaml:"kind"`
	FunctionTools []string `yaml:"function_tools"`
	CliTools      []string `yaml:"cli_tools"`
	MaxIterations int      `yaml:"max_iterations"`
	Tier          string   `yaml:"tier"` // 能力档 heavy|vision|light（空 → store 落 DEFAULT 'heavy'）
}

// scenarioFront 是 scenarios/*.md frontmatter 的解析目标。
// id 用作 code；body 取 markdown 正文作 instruction。
// solo_executor 仅 solo 引擎需要（引用唯一执行操作员 code）；swarm 引擎留空
// （子代理池=全部 enabled 领域操作员，无需在场景里枚举）。
type scenarioFront struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Engine      string `yaml:"engine"`
	SoloAgent  string `yaml:"solo_executor"`
}

var (
	frontDelim = []byte("---")
	newline    = []byte("\n")
)

// splitFrontmatter 切 markdown 成 (frontmatter, body)。约定同 Jekyll/Hugo：开头 ---\n yaml \n---。
func splitFrontmatter(raw []byte) ([]byte, []byte, error) {
	r := bytes.TrimLeft(raw, "\n\r\t ")
	if !bytes.HasPrefix(r, frontDelim) {
		return nil, nil, errors.New("缺 frontmatter：开头未发现 '---'")
	}
	r = r[len(frontDelim):]
	closeMark := append(append([]byte{}, newline...), frontDelim...)
	idx := bytes.Index(r, closeMark)
	if idx < 0 {
		return nil, nil, errors.New("frontmatter 未闭合：缺 '\\n---'")
	}
	return r[:idx], bytes.TrimLeft(r[idx+len(closeMark):], "\n\r"), nil
}

// Import 把 dir 下的 agent/scenario 配置 insert-only 首填进 DB。
// 顺序遵守 FK：先 agent，后 scenario（scenario.solo_executor_id 引用 agent）。
// 每类按 code 判存在→仅不存在才 Create；已存在跳过（绝不覆盖 DB 事实源）。
func Import(
	ctx context.Context,
	dir string,
	h *cfgagent.Store,
	s *cfgscenario.Store,
) error {
	if err := importExecutors(ctx, filepath.Join(dir, "agents"), h); err != nil {
		return fmt.Errorf("import executors: %w", err)
	}
	if err := importScenarios(ctx, filepath.Join(dir, "scenarios"), s, h); err != nil {
		return fmt.Errorf("import scenarios: %w", err)
	}
	return nil
}

// notFound 判定 GetByCode/GetByID 的「不存在」——store 用 %w 包了 pgx.ErrNoRows。
func notFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// walkFiles 收集 dir 下匹配 ext 的文件路径（字典序），dir 不存在时返回空（种子目录可选）。
func walkFiles(dir, ext string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return fs.SkipDir
			}
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ext) {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	// WalkDir 已按字典序遍历目录项，out 天然有序。
	return out, nil
}

// importExecutors 扫 dir/*.md，按 code(=frontmatter id) insert-only 建操作员。
func importExecutors(ctx context.Context, dir string, h *cfgagent.Store) error {
	files, err := walkFiles(dir, ".md")
	if err != nil {
		return err
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读取 %s: %w", path, err)
		}
		front, body, err := splitFrontmatter(raw)
		if err != nil {
			return fmt.Errorf("解析 %s: %w", path, err)
		}
		var f agentFront
		if err := yaml.Unmarshal(front, &f); err != nil {
			return fmt.Errorf("解析 %s frontmatter: %w", path, err)
		}
		code := strings.TrimSpace(f.ID)
		if code == "" {
			return fmt.Errorf("%s: 缺 id", path)
		}
		if _, err := h.GetByCode(ctx, code); err == nil {
			continue // 已存在→跳过（insert-only）
		} else if !notFound(err) {
			return fmt.Errorf("查操作员 %q: %w", code, err)
		}
		systemPrompt := string(body)
		if _, err := h.Update(ctx, code, cfgagent.UpdateParams{
			SystemPrompt:  &systemPrompt,
			FunctionTools: &f.FunctionTools,
			CliTools:      &f.CliTools,
			MaxIterations: &f.MaxIterations,
			Complexity:    strPtr(strings.TrimSpace(f.Tier)),
		}); err != nil {
			return fmt.Errorf("建操作员 %q: %w", code, err)
		}
	}
	return nil
}

// importScenarios 扫 dir/*.md，按 code(=frontmatter id) insert-only 建场景。
// solo 引擎：solo_executor 字段（操作员 code）解析成 solo_executor_id FK；
// swarm 引擎：solo_executor 必须留空（子代理池=全部 enabled 领域操作员，运行期动态构成）。
func importScenarios(ctx context.Context, dir string, s *cfgscenario.Store, h *cfgagent.Store) error {
	files, err := walkFiles(dir, ".md")
	if err != nil {
		return err
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读取 %s: %w", path, err)
		}
		front, body, err := splitFrontmatter(raw)
		if err != nil {
			return fmt.Errorf("解析 %s: %w", path, err)
		}
		var f scenarioFront
		if err := yaml.Unmarshal(front, &f); err != nil {
			return fmt.Errorf("解析 %s frontmatter: %w", path, err)
		}
		code := strings.TrimSpace(f.ID)
		if code == "" {
			return fmt.Errorf("%s: 缺 id", path)
		}
		if _, err := s.GetByCode(ctx, code); err == nil {
			continue // 已存在→跳过（insert-only）
		} else if !notFound(err) {
			return fmt.Errorf("查场景 %q: %w", code, err)
		}
		engine := strings.TrimSpace(f.Engine)
		soloCode := strings.TrimSpace(f.SoloAgent)
		// solo 引擎解析 solo_executor code → agent id；swarm 引擎不接受 solo_executor。
		var soloExecutorID *string
		if engine == cfgscenario.EngineSolo {
			if soloCode == "" {
				return fmt.Errorf("%s: solo 引擎缺 solo_executor", path)
			}
			op, err := h.GetByCode(ctx, soloCode)
			if err != nil {
				return fmt.Errorf("场景 %q 引用操作员 %q: %w", code, soloCode, err)
			}
			soloExecutorID = &op.ID
		} else if soloCode != "" {
			return fmt.Errorf("%s: swarm 引擎不接受 solo_executor（子代理池=全部启用领域操作员）", path)
		}
		if _, err := s.Create(ctx, cfgscenario.NewParams{
			Code:         code,
			Name:         strings.TrimSpace(f.Name),
			Description:  strings.TrimSpace(f.Description),
			Instruction:  string(body),
			Engine:       engine,
			SoloExecutorID: soloExecutorID,
			Enabled:      true,
		}); err != nil {
			return fmt.Errorf("建场景 %q: %w", code, err)
		}
	}
	return nil
}
func strPtr(s string) *string { return &s }
