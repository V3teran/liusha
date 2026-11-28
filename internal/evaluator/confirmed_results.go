package evaluator

// confirmed_results.go：判定输入里的既有坐实结果，与判定对「再次确认」的归属。
//
// 主张的语义去重只在复现门里做，分两层。指纹规范键的精确匹配是确定性的一层，但指纹是
// 评估者写的三段自由文本（类别、对象、作用点）：措辞、类别取值或输入项的名字一有出入，
// 同一主张就对不上；既有结果已经演示过的后果（经某处注入读到某个文件）被当成新主张
// 坐实时，指纹更是无从对照——实测一个任务 20 条结果里 7 条是重复或后果的重述。认得出
// 换了说法的同一主张的是判定本身：判定输入列出本任务已坐实的结果，评估者坐实后判定
// 本次是不是其中某一条的再次确认（契约的 duplicate_of，按短号指明），门据此折叠。

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/textx"
)

// ConfirmedResult 是判定输入里列出的一条既有坐实结果。
type ConfirmedResult struct {
	NodeID      string // 结果节点
	Statement   string // 结果的陈述（已按长度钳制）
	Fingerprint string // 主张指纹的规范键；这条结果没带指纹时为空
}

const (
	// confirmedHandlePrefix 是既有结果在判定输入里的短号前缀（R1、R2……）：模型按短号指明
	// 本次坐实是哪一条的再次确认，不必照抄节点 ID。短号就是它在清单里的序号。
	confirmedHandlePrefix = "R"

	// maxConfirmedResults / maxConfirmedResultsBytes 是清单的条数与字节上限：清单进入每次判定的
	// 上下文，两个上限到顶为止都只留最新的——探索有局部性，正在验证的假设多半与近来的结果
	// 相邻；字节上限保证它不会把首条输入顶过单条消息的硬上限（runtime.MaxMessageBytes）。
	maxConfirmedResults      = 40
	maxConfirmedResultsBytes = 24 * 1024

	// confirmedStatementClampRunes / confirmedFingerprintClampRunes 是清单里陈述与指纹规范键的
	// 长度上限（字符）。
	confirmedStatementClampRunes   = 200
	confirmedFingerprintClampRunes = 160

	// confirmedLineOverhead 是清单里每行除正文以外的字节（短号、标记与换行），计预算时计入。
	confirmedLineOverhead = 8
)

// 重复归属的依据（审计链的 duplicate_basis）：是评估者对照既有结果判定的，还是指纹规范键相同。
const (
	duplicateBasisJudge       = "judge"
	duplicateBasisFingerprint = "fingerprint"
)

// confirmedResults 把任务的结果节点整理成判定输入里的既有结果清单：按创建先后，第 i 个的
// 短号是 R{i+1}。陈述与指纹都没有的结果无从对照，不列；超出上限的只留最新的。
func confirmedResults(nodes []explorationgraph.Node) []ConfirmedResult {
	all := make([]ConfirmedResult, 0, len(nodes))
	for _, node := range nodes {
		var content struct {
			Statement string `json:"statement"`
		}
		var meta struct {
			Fingerprint string `json:"claim_fingerprint"`
		}
		_ = json.Unmarshal(node.Content, &content) // 载荷损坏只缺陈述
		_ = json.Unmarshal(node.Metadata, &meta)   // metadata 损坏只缺指纹
		if content.Statement == "" && meta.Fingerprint == "" {
			continue
		}
		all = append(all, ConfirmedResult{
			NodeID:      node.ID,
			Statement:   textx.TruncateRunes(content.Statement, confirmedStatementClampRunes, "…（截断）"),
			Fingerprint: textx.TruncateRunes(meta.Fingerprint, confirmedFingerprintClampRunes, "…"),
		})
	}
	return newestWithinBudget(all)
}

// newestWithinBudget 从最新的一条往前收，条数或字节数到顶为止。
func newestWithinBudget(all []ConfirmedResult) []ConfirmedResult {
	size, n := 0, 0
	for n < len(all) && n < maxConfirmedResults {
		size += len(confirmedLine(all[len(all)-1-n])) + confirmedLineOverhead
		if size > maxConfirmedResultsBytes {
			break
		}
		n++
	}
	return all[len(all)-n:]
}

// confirmedLine 是清单里一条结果的正文：陈述，后跟括在括号里的指纹规范键。
func confirmedLine(r ConfirmedResult) string {
	if r.Fingerprint == "" {
		return r.Statement
	}
	return r.Statement + "（" + r.Fingerprint + "）"
}

// renderConfirmedResults 渲染判定输入的既有结果一节：一行一条，短号在行首。没有结果时返回
// 空串——输入里没有这一节。
func renderConfirmedResults(confirmed []ConfirmedResult) string {
	if len(confirmed) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## 本任务已坐实的结果\n")
	for i, r := range confirmed {
		fmt.Fprintf(&sb, "- [%s%d] %s\n", confirmedHandlePrefix, i+1, confirmedLine(r))
	}
	return sb.String()
}

// settleDuplicate 落定判定里的再次确认归属：坐实的判定把 duplicate_of 从短号换成结果节点 ID。
// 短号不在清单里是失效引用，证伪的判定带上它是输出不合规——都置空、按独立主张处理，判定
// 本身不受影响。
func settleDuplicate(v Verdict, confirmed []ConfirmedResult, logger zerolog.Logger) Verdict {
	given := strings.TrimSpace(v.DuplicateOf)
	v.DuplicateOf = ""
	if given == "" || v.Verdict != VerdictConfirmed {
		return v
	}
	i, ok := confirmedIndex(given, len(confirmed))
	if !ok {
		logger.Warn().Str("duplicate_of", given).Int("confirmed", len(confirmed)).
			Msg("判定指明的既有结果不在清单里，按独立主张处理")
		return v
	}
	v.DuplicateOf = confirmed[i].NodeID
	return v
}

// confirmedIndex 把既有结果的短号解析成清单下标。前缀不分大小写：模型写 r2 与 R2 指同一条。
func confirmedIndex(handle string, size int) (int, bool) {
	if len(handle) <= len(confirmedHandlePrefix) || !strings.EqualFold(handle[:len(confirmedHandlePrefix)], confirmedHandlePrefix) {
		return 0, false
	}
	n, err := strconv.Atoi(handle[len(confirmedHandlePrefix):])
	if err != nil || n < 1 || n > size {
		return 0, false
	}
	return n - 1, true
}
