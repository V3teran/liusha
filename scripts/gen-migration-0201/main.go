// gen-migration-0201 生成迁移 0201 的升级数据段：从 git 全部历史收集每个出厂字段曾经
// 发布过的值，写成迁移内的历史对照表。迁移据此判断老库里每个字段是「当年的出厂值」
// （升级后跟随新版）还是「用户改过」（转为自定义保留）。
//
// 一次性工具：迁移落地后删除，可从 git 历史追溯与复现。
//
// 用法（仓库根目录）：
//
//	go run ./scripts/gen-migration-0201 > db/migrations/0201_builtin_defaults_layering.up.sql
package main

import (
	"bytes"
	"crypto/md5" // #nosec G501 -- 内容指纹，非安全用途；与迁移内 Postgres md5() 对齐
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/V3teran/liusha/internal/builtin"
	"github.com/V3teran/liusha/internal/settingstore"
)

// fact 是一个历史出厂值：资源（agent / skill / setting）、资源键、字段、值（JSON）。
type fact struct {
	resource, key, field string
	value                json.RawMessage
}

type collector struct {
	seen  map[string]bool
	facts []fact
}

func (c *collector) add(resource, key, field string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	k := resource + "\x00" + key + "\x00" + field + "\x00" + string(b)
	if c.seen[k] {
		return
	}
	c.seen[k] = true
	c.facts = append(c.facts, fact{resource, key, field, b})
}

// textFingerprint 与迁移里的 md5(btrim(t, E' \t\r\n')) 一致：去掉首尾空白后取 MD5，
// 屏蔽历代解析器在首尾空白上的差异。
func textFingerprint(s string) string {
	sum := md5.Sum([]byte(strings.Trim(s, " \t\r\n"))) // #nosec G401 -- 同上
	return hex.EncodeToString(sum[:])
}

// tierToComplexity 是历史 frontmatter 的 tier 取值到 complexity 的映射（取自当年的迁移）。
var tierToComplexity = map[string]string{
	"light": "simple", "heavy": "medium", "vision": "complex",
	"simple": "simple", "medium": "medium", "complex": "complex",
}

func main() {
	c := &collector{seen: map[string]bool{}}
	collectAgents(c)
	collectSkills(c)
	collectSettings(c)
	writeSQL(c)
}

// ── agent ───────────────────────────────────────────────────────────

func addAgent(c *collector, front map[string]any, body string) {
	code := str(front["code"])
	if code == "" {
		code = str(front["id"])
	}
	if code == "" {
		return
	}
	c.add("agent", code, "system_prompt", textFingerprint(body))
	if d, ok := front["description"]; ok {
		c.add("agent", code, "description", textFingerprint(str(d)))
	}
	for _, f := range []string{"function_tools", "cli_tools", "skills"} {
		c.add("agent", code, f, strList(front[f]))
	}
	if n, ok := front["max_iterations"].(int); ok {
		c.add("agent", code, "max_iterations", n)
	} else {
		c.add("agent", code, "max_iterations", 0)
	}
	cx := str(front["complexity"])
	if cx == "" {
		cx = tierToComplexity[str(front["tier"])]
	}
	if cx != "" {
		c.add("agent", code, "complexity", cx)
	}
}

func collectAgents(c *collector) {
	for _, a := range builtin.Agents() { // 当前版本
		addAgent(c, map[string]any{
			"code": a.Code, "description": a.Description, "max_iterations": a.MaxIterations,
			"complexity": a.Complexity, "function_tools": toAny(a.FunctionTools),
			"cli_tools": toAny(a.CliTools), "skills": toAny(a.Skills),
		}, a.Charter)
	}
	dirs := []string{"agents", "internal/config/seed/agents", "internal/builtin/agents"}
	for _, commit := range commits(dirs...) {
		for _, p := range files(commit, dirs...) {
			if !strings.HasSuffix(p, ".md") {
				continue
			}
			front, body, ok := parseMarkdown(show(commit, p))
			if ok {
				addAgent(c, front, body)
			}
		}
	}
}

// ── 技能 ────────────────────────────────────────────────────────────

func addSkill(c *collector, keys []string, front map[string]any, body string) {
	for _, key := range keys {
		c.add("skill", key, "name", textFingerprint(str(front["name"])))
		c.add("skill", key, "description", textFingerprint(str(front["description"])))
		c.add("skill", key, "body", textFingerprint(body))
	}
}

func collectSkills(c *collector) {
	for _, s := range builtin.Skills() {
		addSkill(c, []string{s.Code}, map[string]any{"name": s.Name, "description": s.Description}, s.Body)
	}
	dirs := []string{"skills", "internal/builtin/skills"}
	for _, commit := range commits(dirs...) {
		for _, p := range files(commit, dirs...) {
			if path.Base(p) != "SKILL.md" {
				continue
			}
			front, body, ok := parseMarkdown(show(commit, p))
			if !ok {
				continue
			}
			dir := path.Dir(p)
			// 历代种子用过「目录名」与「分类/目录名」两种 code 形式，都登记。
			keys := []string{path.Base(dir), path.Base(path.Dir(dir)) + "/" + path.Base(dir)}
			addSkill(c, keys, front, body)
		}
	}
}

// ── 系统设置 ────────────────────────────────────────────────────────

func collectSettings(c *collector) {
	// 内置默认在全部历史中未变（见提交说明的核查）；当前值即历史值。
	comp := settingstore.DefaultCompaction()
	addStruct(c, settingstore.GroupCompaction, comp)
	rt := settingstore.DefaultRuntime(300)
	addStruct(c, settingstore.GroupRuntime, rt)

	// 部署默认取自 config.yaml：runner.step_llm_timeout_seconds 与 proxy 过滤字段，
	// 缺省时按代码默认补齐（ExcludeUpgradeProtocols=[websocket]、2 MiB、8 MiB）。
	for _, commit := range commits("config/config.yaml") {
		raw := show(commit, "config/config.yaml")
		var doc struct {
			Runner map[string]any `yaml:"runner"`
			Proxy  map[string]any `yaml:"proxy"`
		}
		if yaml.Unmarshal(raw, &doc) != nil {
			continue
		}
		if n, ok := doc.Runner["step_llm_timeout_seconds"].(int); ok && n > 0 {
			c.add("setting", string(settingstore.GroupRuntime), "step_llm_timeout_seconds", n)
		}
		if doc.Proxy == nil {
			continue
		}
		g := string(settingstore.GroupProxyFilter)
		for _, f := range []string{"allow_hosts", "exclude_methods", "exclude_hosts", "exclude_suffixes", "exclude_content_types"} {
			c.add("setting", g, f, strList(doc.Proxy[f]))
		}
		upgrade := strList(doc.Proxy["exclude_upgrade_protocols"])
		if len(upgrade) == 0 {
			upgrade = []string{"websocket"}
		}
		c.add("setting", g, "exclude_upgrade_protocols", upgrade)
		c.add("setting", g, "exclude_status_codes", intList(doc.Proxy["exclude_status_codes"]))
		c.add("setting", g, "max_request_body_size", intOr(doc.Proxy["max_request_body_size"], 2<<20))
		c.add("setting", g, "max_response_body_size", intOr(doc.Proxy["max_response_body_size"], 8<<20))
	}
}

func addStruct(c *collector, g settingstore.Group, v any) {
	b, _ := json.Marshal(v)
	var m map[string]json.RawMessage
	_ = json.Unmarshal(b, &m)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys) // 输出须可复现：map 遍历序不确定
	for _, k := range keys {
		var x any
		_ = json.Unmarshal(m[k], &x)
		c.add("setting", string(g), k, x)
	}
}

// ── git ─────────────────────────────────────────────────────────────

func git(args ...string) []byte {
	out, err := exec.Command("git", args...).Output() // #nosec G204 -- 参数为代码内固定的 git 子命令与仓库路径
	if err != nil {
		fmt.Fprintf(os.Stderr, "git %s: %v\n", strings.Join(args, " "), err)
		os.Exit(1)
	}
	return out
}

// commits 返回触及任一路径的全部提交（全部分支，新到旧）。
func commits(paths ...string) []string {
	args := append([]string{"log", "--all", "--format=%H", "--"}, paths...)
	return strings.Fields(string(git(args...)))
}

func files(commit string, dirs ...string) []string {
	args := append([]string{"ls-tree", "-r", "--name-only", commit, "--"}, dirs...)
	return strings.Fields(string(git(args...)))
}

func show(commit, p string) []byte { return git("show", commit+":"+p) }

func parseMarkdown(raw []byte) (map[string]any, string, bool) {
	front, body, err := builtin.SplitFrontmatter(raw)
	if err != nil {
		return nil, "", false
	}
	var m map[string]any
	if yaml.Unmarshal(front, &m) != nil {
		return nil, "", false
	}
	return m, string(body), true
}

// ── 值转换 ──────────────────────────────────────────────────────────

func str(v any) string {
	s, _ := v.(string)
	return s
}

func strList(v any) []string {
	out := []string{}
	if l, ok := v.([]any); ok {
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func intList(v any) []int {
	out := []int{}
	if l, ok := v.([]any); ok {
		for _, x := range l {
			if n, ok := x.(int); ok {
				out = append(out, n)
			}
		}
	}
	return out
}

func intOr(v any, def int) int {
	if n, ok := v.(int); ok && n > 0 {
		return n
	}
	return def
}

func toAny(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// ── 输出 ────────────────────────────────────────────────────────────

func writeSQL(c *collector) {
	tmpl, err := os.ReadFile("scripts/gen-migration-0201/template.sql")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var rows []string
	for i, f := range c.facts {
		rows = append(rows, fmt.Sprintf("  (%d, %s, %s, %s, %s)", i,
			quote(f.resource), quote(f.key), quote(f.field), quote(string(f.value))+"::jsonb"))
	}
	var builtinSkills []string
	for _, s := range builtin.Skills() {
		builtinSkills = append(builtinSkills, quote(s.Code))
	}
	var settingFields []string
	for _, g := range settingstore.Groups {
		fields := map[settingstore.Group]any{
			settingstore.GroupCompaction:  settingstore.CompactionSettings{},
			settingstore.GroupRuntime:     settingstore.RuntimeSettings{},
			settingstore.GroupProxyFilter: settingstore.ProxyFilterSettings{},
		}[g]
		b, _ := json.Marshal(fields)
		var m map[string]json.RawMessage
		_ = json.Unmarshal(b, &m)
		var names []string
		for k := range m {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			settingFields = append(settingFields, fmt.Sprintf("  (%s, %s)", quote(string(g)), quote(k)))
		}
	}
	slices.Sort(builtinSkills)
	out := bytes.ReplaceAll(tmpl, []byte("-- @HISTORY@"), []byte(strings.Join(rows, ",\n")+";"))
	out = bytes.ReplaceAll(out, []byte("-- @SETTING_FIELDS@"), []byte(strings.Join(settingFields, ",\n")+";"))
	out = bytes.ReplaceAll(out, []byte("'@BUILTIN_SKILLS@'"), []byte("ARRAY["+strings.Join(builtinSkills, ", ")+"]::text[]"))
	_, _ = os.Stdout.Write(out)
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
