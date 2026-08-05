// Package seed 把磁盘上的 hunter/scenario 配置首次导入 DB。
//
// insert-only 首填语义（见 D6）：DB 是事实源，种子只填**空库**，按 code 判存在——
// 已存在的行一律跳过，绝不覆盖前端/运维在 DB 里的改动。导入顺序遵守 FK 依赖：
// hunter → scenario（scenario.solo_hunter_id 引用 hunter）。
//
// 目录约定（dir 为配置根）：
//   - dir/hunters/*.md   ：猎手 charter（frontmatter 元信息 + body 方法论正文）
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

	cfghunter "github.com/V3teran/liusha/internal/config/hunter"
	cfgscenario "github.com/V3teran/liusha/internal/config/scenario"
)

// hunterFront 是 hunters/*.md frontmatter 的解析目标。
// id 用作稳定引用键 code；kind∈{orchestrator,domain}；body 取 markdown 正文。
// cli_tools 是外置 CLI 工具白名单（tools.yaml 名字），空=交战域内全部可见。
type hunterFront struct {
	ID            string   `yaml:"id"`
	Name          string   `yaml:"name"`
	Description   string   `yaml:"description"`
	Kind          string   `yaml:"kind"`
	Tools         []string `yaml:"tools"`
	CliTools      []string `yaml:"cli_tools"`
	MaxIterations int      `yaml:"max_iterations"`
}

// scenarioFront 是 scenarios/*.md frontmatter 的解析目标。
// id 用作 code；body 取 markdown 正文作 instruction。
// solo_hunter 仅 solo 引擎需要（引用唯一执行猎手 code）；swarm 引擎留空
// （子代理池=全部 enabled 领域猎手，无需在场景里枚举）。
type scenarioFront struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Engine      string `yaml:"engine"`
	SoloHunter  string `yaml:"solo_hunter"`
	Domain      string `yaml:"domain"`
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

// Import 把 dir 下的 hunter/scenario 配置 insert-only 首填进 DB。
// 顺序遵守 FK：先 hunter，后 scenario（scenario.solo_hunter_id 引用 hunter）。
// 每类按 code 判存在→仅不存在才 Create；已存在跳过（绝不覆盖 DB 事实源）。
func Import(
	ctx context.Context,
	dir string,
	h *cfghunter.Store,
	s *cfgscenario.Store,
) error {
	if err := importHunters(ctx, filepath.Join(dir, "hunters"), h); err != nil {
		return fmt.Errorf("import hunters: %w", err)
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

// importHunters 扫 dir/*.md，按 code(=frontmatter id) insert-only 建猎手。
func importHunters(ctx context.Context, dir string, h *cfghunter.Store) error {
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
		var f hunterFront
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
			return fmt.Errorf("查猎手 %q: %w", code, err)
		}
		if _, err := h.Create(ctx, cfghunter.NewParams{
			Code:          code,
			Kind:          cfghunter.Kind(strings.TrimSpace(f.Kind)),
			Name:          strings.TrimSpace(f.Name),
			Description:   strings.TrimSpace(f.Description),
			Body:          string(body),
			Tools:         f.Tools,
			CliTools:      f.CliTools,
			MaxIterations: f.MaxIterations,
			Enabled:       true,
		}); err != nil {
			return fmt.Errorf("建猎手 %q: %w", code, err)
		}
	}
	return nil
}

// importScenarios 扫 dir/*.md，按 code(=frontmatter id) insert-only 建场景。
// solo 引擎：solo_hunter 字段（猎手 code）解析成 solo_hunter_id FK；
// swarm 引擎：solo_hunter 必须留空（子代理池=全部 enabled 领域猎手，运行期动态构成）。
func importScenarios(ctx context.Context, dir string, s *cfgscenario.Store, h *cfghunter.Store) error {
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
		soloCode := strings.TrimSpace(f.SoloHunter)
		// solo 引擎解析 solo_hunter code → hunter id；swarm 引擎不接受 solo_hunter。
		var soloHunterID *string
		if engine == cfgscenario.EngineSolo {
			if soloCode == "" {
				return fmt.Errorf("%s: solo 引擎缺 solo_hunter", path)
			}
			hunter, err := h.GetByCode(ctx, soloCode)
			if err != nil {
				return fmt.Errorf("场景 %q 引用猎手 %q: %w", code, soloCode, err)
			}
			soloHunterID = &hunter.ID
		} else if soloCode != "" {
			return fmt.Errorf("%s: swarm 引擎不接受 solo_hunter（子代理池=全部启用领域猎手）", path)
		}
		if _, err := s.Create(ctx, cfgscenario.NewParams{
			Code:         code,
			Name:         strings.TrimSpace(f.Name),
			Description:  strings.TrimSpace(f.Description),
			Instruction:  string(body),
			Domain:       strings.TrimSpace(f.Domain),
			Engine:       engine,
			SoloHunterID: soloHunterID,
			Enabled:      true,
		}); err != nil {
			return fmt.Errorf("建场景 %q: %w", code, err)
		}
	}
	return nil
}
