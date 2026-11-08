// Package attachment 管理会话附件：上传落位、暂存、下发任务时搬家进任务工作区。
//
// 附件生命周期与渗透主流程对齐（先传字典/目标清单 → 再一句话下发扫描）：
//   - 会话已绑任务 → 文件直落 <assignment>/<task>/inbox/（与 work/out 同层，
//     随工作区 bind mount 直接进沙盒，agent 经 prompt 里的绝对路径可读）；
//   - 会话未绑任务（含会话尚未创建的 pending 态）→ 暂存 .staging/ 下（文件面
//     不展示、不进沙盒），任务创建的那一刻整批搬进 inbox；
//   - 从未随消息下发的暂存件随会话删除清理（pending 态另有超期清扫兜底）。
//
// 引用防伪造：前端提交的 Ref 只当作提示，路径必须落在该会话的合法根
// （inbox / 自身暂存区 / pending 区）内且真实存在，Name/Size 一律以盘上
// 实况为准——客户端声明不进事实链。
package attachment

import (
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/workspace"
)

// StagingTop 是工作区根下的暂存顶层目录（点前缀：workspace.Tree 把它排除在
// 文件面外，不与任务工件混排）。
const StagingTop = ".staging"

// SandboxMount 是工作区 bind 进沙盒容器的绝对路径（launcher 契约）：
// prompt 清单里的 agent 可读路径 = 挂载点 + 工作区相对路径。
const SandboxMount = "/root/liusha/workspace"

// MaxInlineBytes 是单附件可内联进对话 prompt 的内容上限（qa/纯聊天回答里
// 直接引用小文本）。qa/chat 对整条用户消息另有 32000 rune 的上下文钳制：
// 24 KiB 留出 brief、清单框架与多字节膨胀的余量，满载内联不再顶掉尾部。
// 超限或二进制只给占位说明，下发扫描后 agent 从工作区读。
const MaxInlineBytes = 24 << 10 // 24 KiB

// ErrRefExpired 报告引用落在合法根内但文件已不存在（最常见：pending 暂存
// 超期被清扫后旧草稿才发送）。与 ErrInvalidRef（伪造/越权路径）分开——
// 前者是人话可恢复操作（重新上传），后者是安全事件须保留原始路径供排查。
var ErrRefExpired = errors.New("附件已过期或不存在，请重新上传")

// Ref 是一条附件引用：随消息 metadata 落库、随指令注入 prompt 的最小描述。
// Path 是工作区相对斜杠路径。
type Ref struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Path string `json:"path"`
}

// Store 是附件操作的入口（复用 workspace.Store 的路径钳制与落盘语义）。
type Store struct {
	ws *workspace.Store
}

// NewStore 用工作区 Store 构造附件 Store。
func NewStore(ws *workspace.Store) *Store { return &Store{ws: ws} }

// ErrInvalidRef 报告附件引用非法：路径不在合法根内、不存在或不是普通文件。
var ErrInvalidRef = errors.New("附件引用非法")

// sanitizeName 把客户端文件名收敛为安全的单段路径：剥目录成分、控制字符、
// 前后空白；空/全剥光时回退 attachment。字符白名单不做——中文/空格文件名
// 是真实需求（与 workspace.isSafeSegment 同口径）。
func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Trim(name, " \t.")
	var b strings.Builder
	for _, r := range name {
		if r >= 0x20 && r != 0x7f {
			b.WriteRune(r)
		}
	}
	name = strings.TrimSpace(b.String())
	if name == "" {
		return "attachment"
	}
	return name
}

// inboxRel 是任务附件目录的工作区相对路径（assignment/task 两级 + inbox，
// 与沙盒自建的 work/out/state 同层）。
func inboxRel(assignmentID, taskID string) string {
	return path.Join(assignmentID, taskID, "inbox")
}

// convStagingRel 是会话暂存目录的工作区相对路径。
func convStagingRel(conversationID string) string {
	return path.Join(StagingTop, "conv", conversationID)
}

// pendingStagingRel 是「会话尚未创建」上传的暂存目录（随机目录名由调用方
// 生成并随响应返回，发送消息时凭 Ref.Path 认领）。
func pendingStagingRel(uploadID string) string {
	return path.Join(StagingTop, "pending", uploadID)
}

// Dest 是一次上传/落位的目标描述，四种域态由非空字段表达：
//   - TaskID 非空：任务在编 → 直落 <AssignmentID>/<TaskID>/inbox；
//   - ConversationID 非空：会话暂存 → .staging/conv/<id>/；
//   - 仅 UploadID 非空：会话尚未创建 → .staging/pending/<id>/。
type Dest struct {
	ConversationID string
	AssignmentID   string
	TaskID         string
	UploadID       string
}

// candidateRel 返回 dir 下 name 的第 i 个候选落点：i=0 直名，之后「名-i.ext」。
func candidateRel(dir, name string, i int) string {
	if i == 0 {
		return path.Join(dir, name)
	}
	ext := path.Ext(name)
	return path.Join(dir, fmt.Sprintf("%s-%d%s", strings.TrimSuffix(name, ext), i, ext))
}

// placeUnique 依次尝试 dir 下 name 的候选落点，直到 place 不再报 workspace.ErrExists。
// 占名与创建/搬入是同一个原子操作（O_EXCL / 存在即拒），并发同名不会互相覆盖。
func placeUnique(dir, name string, place func(rel string) error) (string, error) {
	for i := 0; ; i++ {
		rel := candidateRel(dir, name, i)
		err := place(rel)
		if errors.Is(err, workspace.ErrExists) {
			continue
		}
		return rel, err
	}
}

// SaveUpload 把一个上传流按 dest 落位（域态语义见 Dest）。返回的 Ref 以落盘
// 实况为准。
func (s *Store) SaveUpload(dest Dest, r io.Reader, filename string, sizeCap int64) (Ref, error) {
	name := sanitizeName(filename)
	var dir string
	switch {
	case dest.TaskID != "":
		dir = inboxRel(dest.AssignmentID, dest.TaskID)
	case dest.ConversationID != "":
		dir = convStagingRel(dest.ConversationID)
	default:
		dir = pendingStagingRel(dest.UploadID)
	}
	var n int64
	rel, err := placeUnique(dir, name, func(rel string) error {
		var werr error
		n, werr = s.ws.WriteFrom(rel, r, sizeCap)
		return werr
	})
	if err != nil {
		// 暂存是中转层：失败不留垃圾（空目录一并移走；非空说明有并发上传，no-op）。
		if dest.TaskID == "" {
			_ = s.ws.RemoveIfEmptyDir(dir)
		}
		return Ref{}, fmt.Errorf("attachment: 写入 %s: %w", rel, err)
	}
	return Ref{Name: name, Size: n, Path: rel}, nil
}

// Materialize 把随消息提交的附件引用整批落位到 dest 的任务 inbox（任务创建/
// 续接的收口点）：inbox 内的直接沿用；暂存区的搬入（重名自动 -N 后缀）。引用
// 必须逐条通过合法根校验（本任务 inbox / 本会话暂存 / pending 区），任何一条
// 非法即拒绝整批——半落位的批次比全拒更难收拾。返回落位后的引用
// （Name/Size 以盘上实况重取，Path 更新为 inbox 路径）。
func (s *Store) Materialize(dest Dest, refs []Ref) ([]Ref, error) {
	inbox := inboxRel(dest.AssignmentID, dest.TaskID)

	// 阶段一：整批校验，任何一条非法 / 已过期都在动盘之前拒绝整批。
	type planned struct {
		src  string
		size int64
	}
	plan := make([]planned, 0, len(refs))
	for _, ref := range refs {
		src, err := s.validateRef(ref.Path, dest.ConversationID, inbox)
		if err != nil {
			return nil, err
		}
		info, err := s.ws.Stat(src)
		if err != nil {
			return nil, fmt.Errorf("attachment: %s: %w", ref.Name, ErrRefExpired)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("attachment: %w: %s 是目录", ErrInvalidRef, ref.Path)
		}
		plan = append(plan, planned{src: src, size: info.Size()})
	}

	// 阶段二：搬入 inbox。中途失败把已搬的件搬回原处，保持「要么全落位、要么全不动」，
	// 用户重试时引用仍然有效。
	type moved struct{ from, to string }
	var done []moved
	rollback := func() {
		for i := len(done) - 1; i >= 0; i-- {
			_ = s.ws.MoveFile(done[i].to, done[i].from)
		}
	}
	out := make([]Ref, 0, len(plan))
	for _, p := range plan {
		dstRel := path.Join(inbox, path.Base(p.src))
		if p.src != dstRel {
			rel, err := placeUnique(inbox, path.Base(p.src), func(rel string) error {
				return s.ws.MoveFile(p.src, rel)
			})
			if err != nil {
				rollback()
				return nil, fmt.Errorf("attachment: 搬家 %s → %s: %w", p.src, rel, err)
			}
			done = append(done, moved{from: p.src, to: rel})
			dstRel = rel
		}
		out = append(out, Ref{Name: path.Base(dstRel), Size: p.size, Path: dstRel})
	}
	return out, nil
}

// validateRef 校验一条引用路径落在该会话的合法根内，返回规范化后的路径。
func (s *Store) validateRef(p, conversationID, inbox string) (string, error) {
	clean := path.Clean("/" + strings.ReplaceAll(p, "\\", "/"))
	clean = strings.TrimPrefix(clean, "/")
	if clean == "." || clean == "/" {
		return "", fmt.Errorf("attachment: %w: 空 path", ErrInvalidRef)
	}
	switch {
	case clean == inbox || strings.HasPrefix(clean, inbox+"/"):
	case conversationID != "" && (clean == convStagingRel(conversationID) || strings.HasPrefix(clean, convStagingRel(conversationID)+"/")):
	case clean == pendingStagingRel("") || strings.HasPrefix(clean, pendingStagingRel("")+"/"):
	default:
		return "", fmt.Errorf("attachment: %w: %s", ErrInvalidRef, p)
	}
	return clean, nil
}

// RemoveStaging 删除会话的暂存目录（会话删除时收口；目录不存在为 no-op）。
func (s *Store) RemoveStaging(conversationID string) error {
	if err := s.ws.Delete(convStagingRel(conversationID)); err != nil {
		return fmt.Errorf("attachment: 清理暂存 %s: %w", conversationID, err)
	}
	return nil
}

// SweepStalePending 清扫超期未认领的 pending 暂存目录（每次上传顺手执行，
// 不需要独立定时器）：目录 mtime 早于 maxAge 即整目录删除。
func (s *Store) SweepStalePending(maxAge time.Duration) {
	pendingRoot := pendingStagingRel("")
	dirs, err := s.ws.ReadDir(pendingRoot)
	if err != nil {
		return // 根不存在=从无 pending 上传，静默返回
	}
	for _, d := range dirs {
		if !d.IsDir() || time.Since(d.ModTime()) <= maxAge {
			continue
		}
		_ = s.ws.Delete(pendingRoot + "/" + d.Name())
	}
}

// InlineTexts 渲染附件进对话 prompt 的形态（qa/纯聊天回答用，inspector 无
// 沙盒工具、读盘只有内联这一条通道）。引用与 Materialize 走同一份来源校验
// ——qa 路径不落位不等于免检，工作区任意文件不得经伪造引用流进 prompt。
// ≤MaxInlineBytes 且为纯文本（无二进制标记、无控制字符——NUL 字节串是合法
// UTF-8，utf8 判定放行，必须显式排除）的直接内联原文；其余按失效/过大分别
// 给占位说明。返回值恒非空（refs 为空时返回空串，调用方不拼接）。
func (s *Store) InlineTexts(dest Dest, refs []Ref) (string, error) {
	if len(refs) == 0 {
		return "", nil
	}
	inbox := ""
	if dest.TaskID != "" {
		inbox = inboxRel(dest.AssignmentID, dest.TaskID)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[附件 %d 个]\n", len(refs))
	for i, ref := range refs {
		if i > 0 {
			b.WriteString("\n")
		}
		size := ref.Size
		reason := "过大或为二进制，未内联；下发扫描任务后 agent 可从沙盒工作区直接读取"
		path, verr := s.validateRef(ref.Path, dest.ConversationID, inbox)
		if verr != nil {
			// 引用越权/失效：不进内容、不中断回答——占位行给操作者可见信号
			path, reason = "", "已失效（引用非法），请重新上传"
		} else if _, err := s.ws.Stat(path); err != nil {
			path, reason = "", ErrRefExpired.Error()
		}
		if path != "" {
			if info, err := s.ws.Stat(path); err == nil && !info.IsDir() && info.Size() <= MaxInlineBytes {
				fc, err := s.ws.Read(path)
				if err == nil && !fc.Binary && !fc.Truncated && !hasControlRune(fc.Content) {
					size = info.Size() // 盘上实况为准（与引用防伪造同一纪律）
					fmt.Fprintf(&b, "----- 附件 %d：%s（%s）原文 -----\n%s\n----- 附件 %d 结束 -----", i+1, ref.Name, humanSize(size), fc.Content, i+1)
					continue
				}
			}
		}
		fmt.Fprintf(&b, "附件 %d：%s（%s）——%s", i+1, ref.Name, humanSize(size), reason)
	}
	return b.String(), nil
}

// hasControlRune 报告文本是否含不可打印控制字符（\t \n \r 除外）——
// 含即按二进制占位处理，不进 prompt。
func hasControlRune(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return r < 0x20 && r != '\t' && r != '\n' && r != '\r'
	})
}

// Manifest 渲染注入扫描指令 prompt 的附件清单（沙盒内绝对路径，agent 经
// 工具直接读取）。refs 为空返回空串。
func Manifest(refs []Ref) string {
	if len(refs) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n\n[附件 %d 个，已存入沙盒工作区，可直接读取]\n", len(refs))
	for _, ref := range refs {
		fmt.Fprintf(&b, "- %s（%s）→ %s/%s\n", ref.Name, humanSize(ref.Size), SandboxMount, ref.Path)
	}
	return b.String()
}

// humanSize 是附件尺寸的人类可读形式（清单/占位行共用一套口径）。
func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
