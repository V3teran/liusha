// Package corpus 是跨任务长期知识库：持久化、检索与写入。
//
// 检索有两种模式，由是否配置了向量模型服务决定：
//   - 语义：向量近邻与词元两路召回，合并后经重排模型打分，按相关度门槛过滤；
//   - 字面：按词元匹配，以 IDF 加权的查询词覆盖率打分，按覆盖率门槛过滤。
//
// 语义检索运行期出故障时退回字面检索，并在结果里标明（Result.Fallback）。两种模式都宁可
// 返回空，也不拿不相关的条目凑数。
//
// 每条知识归属一个场景（空 = 通用）：任务只看得到本场景与通用的知识。source 记录当前内容的
// 作者——任务沉淀（agent）或人工（expert）；人工内容只能被印证，不被收尾整合改写。
package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Source 取值：当前内容的作者。
const (
	SourceAgent  = "agent"  // 任务收尾沉淀
	SourceExpert = "expert" // 人工编写：录入、导入，或人工改写过
)

// 条目体量上限。正文上限对齐重排模型单篇文档的有效长度，超出的导入内容按段落切分。
const (
	MaxTitleRunes   = 80
	MaxContentRunes = 1000
)

var (
	// ErrNotFound 表示条目不存在（包装 pgx.ErrNoRows，HTTP 层收敛为 404）。
	ErrNotFound = errors.New("知识条目不存在")
	// ErrDuplicate 表示已有正文相同的条目（errors.As 取 *DuplicateError 拿到已有条目）。
	ErrDuplicate = errors.New("已有正文相同的知识条目")
	// ErrInvalid 表示提交的条目不合法。
	ErrInvalid = errors.New("知识条目不合法")
)

// Entry 是一条知识。
type Entry struct {
	ID       string
	Title    string
	Content  string
	Scenario *string // 所属场景标识；nil = 通用
	Source   string
	// Indexed 表示已有向量（语义召回可见）；向量化失败的条目由 IndexPending 补齐。
	Indexed bool
	// Confirmations 是印证过它的任务数。
	Confirmations int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Text 是条目参与向量化与重排的文本：标题 + 正文（与词元生成列 corpus_terms(title || ' ' || content) 同一范围）。
func (e Entry) Text() string { return Text(e.Title, e.Content) }

// Text 拼出标题与正文的检索文本。
func Text(title, content string) string { return title + "\n" + content }

// Confirmation 是一次印证：哪个任务、何时。
type Confirmation struct {
	TaskID      string
	TaskGoal    string
	ConfirmedAt time.Time
}

// Draft 是待写入的新条目。
type Draft struct {
	Title    string
	Content  string
	Scenario *string
	Source   string
}

// Normalize 去掉首尾空白并校验，返回规范化后的草稿。
func (d Draft) Normalize() (Draft, error) {
	d.Title = strings.TrimSpace(d.Title)
	d.Content = strings.TrimSpace(d.Content)
	if d.Scenario != nil && strings.TrimSpace(*d.Scenario) == "" {
		d.Scenario = nil
	}
	if err := validate(d.Title, d.Content); err != nil {
		return Draft{}, err
	}
	if d.Source != SourceAgent && d.Source != SourceExpert {
		return Draft{}, fmt.Errorf("%w：来源 %q 不合法", ErrInvalid, d.Source)
	}
	return d, nil
}

func validate(title, content string) error {
	switch {
	case title == "":
		return fmt.Errorf("%w：标题不能为空", ErrInvalid)
	case utf8.RuneCountInString(title) > MaxTitleRunes:
		return fmt.Errorf("%w：标题不超过 %d 字", ErrInvalid, MaxTitleRunes)
	case content == "":
		return fmt.Errorf("%w：正文不能为空", ErrInvalid)
	case utf8.RuneCountInString(content) > MaxContentRunes:
		return fmt.Errorf("%w：正文不超过 %d 字", ErrInvalid, MaxContentRunes)
	}
	return nil
}

// contentHash 是正文去重键（去掉首尾空白后的 SHA-256）。
func contentHash(content string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(content)))
	return hex.EncodeToString(sum[:])
}

// DuplicateError 是写入撞上已有正文时的错误，带出已有条目的 ID。
type DuplicateError struct{ ExistingID string }

func (e *DuplicateError) Error() string { return ErrDuplicate.Error() }

// Unwrap 让 errors.Is(err, ErrDuplicate) 成立。
func (e *DuplicateError) Unwrap() error { return ErrDuplicate }
