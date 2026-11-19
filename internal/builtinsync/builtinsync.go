// Package builtinsync 是「出厂种子 + 未改才覆盖」的判定内核：技能与场景的出厂内容编译在
// internal/builtin 里，迁移后同步进各自的表（skill / scenario）。表里一行就是一个资源，内置与
// 自建同表（is_builtin 区分）；seed_hash 记最近一次写入的出厂内容的哈希。
//
//   - 当前内容的哈希 == seed_hash：用户没改过，升级时跟随新版；
//   - 不等（或 seed_hash 为 NULL，基线未知）：用户改过，升级时不动；
//   - 用户删除的内置行留删除标记（deleted_at）：不补回、不更新，用户决定是否恢复。
//
// 本包只做纯计算（哈希与决策），各表按决策执行自己的 SQL。
package builtinsync

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// sep 是哈希输入里字段之间的分隔符（U+001F 单元分隔符：正文里不会出现，PostgreSQL text 可存）。
// 迁移 SQL 用同一算式：encode(sha256(convert_to(name || chr(31) || description || chr(31) || body, 'UTF8')), 'hex')。
const sep = "\x1f"

// Hash 返回内容哈希（sha256 十六进制）。只覆盖用户可编辑的内容字段；启停是状态，不参与。
func Hash(name, description, body string) string {
	sum := sha256.Sum256([]byte(name + sep + description + sep + body))
	return hex.EncodeToString(sum[:])
}

// Row 是表里一行的同步视图。
type Row struct {
	Code      string
	IsBuiltin bool
	Deleted   bool    // 内置行被用户删除（留删除标记）
	Hash      string  // 当前内容的哈希
	SeedHash  *string // nil = NULL（自建，或迁移来的已改内置行：基线未知）
}

// Modified 报告内置行的内容是否被用户改过。
func (r Row) Modified() bool {
	return r.SeedHash == nil || *r.SeedHash != r.Hash
}

// Seed 是一份出厂内容的同步视图。
type Seed struct {
	Code string
	Hash string
}

// Op 是对一个 code 的同步动作。
type Op int

// 同步动作。没有列出的情况（自建行、已是最新的未改行、改过的内置行）什么都不做。
const (
	OpInsert   Op = iota + 1 // 库里没有：插入种子
	OpUpdate                 // 内置、未改、出厂有新版：覆盖内容（保留启停）
	OpBaseline               // 内置、基线未知、内容恰与当前出厂相同：补记 seed_hash
	OpRetire                 // 内置、未改（或已被用户删除）、出厂已下线：删行
	OpDetach                 // 内置、改过、出厂已下线：转为自建，保留用户的内容
)

// Action 是一个 code 的同步动作。
type Action struct {
	Code string
	Op   Op
}

// Plan 计算把表同步到出厂种子需要的动作（按种子顺序，其后是下线的行，按行顺序）。幂等：
// 对同步后的状态再算一次得到空计划。
func Plan(rows []Row, seeds []Seed) []Action {
	byCode := make(map[string]Row, len(rows))
	for _, r := range rows {
		byCode[r.Code] = r
	}
	seeded := make(map[string]bool, len(seeds))
	var out []Action
	for _, s := range seeds {
		seeded[s.Code] = true
		r, ok := byCode[s.Code]
		switch {
		case !ok:
			out = append(out, Action{s.Code, OpInsert})
		case !r.IsBuiltin:
			// 自建与出厂同 code：自建优先，内置版本被遮蔽。
		case r.Deleted:
			// 用户删除了它：不补回、不更新。
		case r.SeedHash == nil && r.Hash == s.Hash:
			out = append(out, Action{s.Code, OpBaseline})
		case r.Modified():
			// 用户改过：不动。
		case *r.SeedHash != s.Hash:
			out = append(out, Action{s.Code, OpUpdate})
		}
	}
	for _, r := range rows {
		if !r.IsBuiltin || seeded[r.Code] {
			continue
		}
		if r.Modified() && !r.Deleted {
			out = append(out, Action{r.Code, OpDetach})
		} else {
			out = append(out, Action{r.Code, OpRetire})
		}
	}
	return out
}

// Digest 返回一组出厂内容的摘要，覆盖同步会写进表的全部字段（含附加列）：缓存键带上它，出厂
// 内容随版本变化后，新版本的进程不会读到旧版本留在 L2 的副本（同一版本的进程之间照常共享）。
func Digest(seeds []SeedRow) string {
	h := sha256.New()
	for _, s := range seeds {
		_, _ = fmt.Fprintf(h, "%q %q %q %q %q\n", s.Code, s.Name, s.Description, s.Body, fmt.Sprint(s.Extra...))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// AfterEdit 返回用户编辑内置行后应存的 seed_hash：编辑后的内容恰与当前出厂相同，就记为当前
// 出厂的哈希（与出厂相同的内容不算自定义，此后照常跟随新版）；否则保留原基线。
// seeded 为假（出厂已下线）时原样返回。
func AfterEdit(prev *string, contentHash, seedHash string, seeded bool) *string {
	if seeded && contentHash == seedHash {
		return &seedHash
	}
	return prev
}
