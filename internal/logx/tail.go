package logx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ── 日志文件视图（「日志」页数据面）────────────────────────────────────────
// 写入端（New）把每进程日志落为 dir/{process}.log 的 JSON lines；本文件是同一
// 布局的读取端：从文件尾部取窗口、逐行解析、按筛选保留最新 limit 条。
// 游标（字节偏移）支撑增量轮询：客户端带上次响应的 cursors，之后每轮只读增量
// 字节；文件比游标短（lumberjack 轮转/外部截断）时对该进程置 reset 整段重取。
// 不引入第二日志事实源——UI 看到的与文件落地的永远一致。

// ViewProcesses 是日志视图的进程域：三个业务进程；
// 新业务进程加进此表即自动出现在进程枚举与读取结果里。
var ViewProcesses = []string{"api", "proxy", "runner"}

// defaultReadLimit 是 Limit 非法时的兜底值（handler 已 clamp，这里再守一道）。
const defaultReadLimit = 500

// instanceCap 是单进程实例枚举的上限：实例数正常是个位数，超限说明字段被滥用。
const instanceCap = 16

// ProcessInfo 是进程日志文件概况。
type ProcessInfo struct {
	Name      string    `json:"name"`
	Exists    bool      `json:"exists"`
	SizeBytes int64     `json:"size_bytes"`
	ModTime   time.Time `json:"mod_time"`
	// Instances 是文件尾窗口内出现过的 instance（pid@host），按首次出现序。
	Instances []string `json:"instances"`
}

// Entry 是一行日志的解析形态；Offset 是行首在文件中的字节偏移（增量游标事实源）。
// 归不进已知字段的业务键（task_id、observation_id 之类）收进 Fields，详情展开显示。
type Entry struct {
	Offset   int64          `json:"offset"`
	Time     time.Time      `json:"time"`
	Level    string         `json:"level"`
	Service  string         `json:"service"`
	Instance string         `json:"instance"`
	Caller   string         `json:"caller,omitempty"`
	Message  string         `json:"message"`
	Error    string         `json:"error,omitempty"`
	Fields   map[string]any `json:"fields,omitempty"`
}

// ReadQuery 描述一次尾读。Cursors 缺项的进程做尾窗口冷读（首次加载/筛选变化）。
type ReadQuery struct {
	// Processes 是 ViewProcesses 的子集；空 = 全部。
	Processes []string
	Level     string // 等值：debug|info|warn|error；空 = 不筛
	Instance  string // 等值：pid@host；空 = 不筛
	Search    string // 跨 message/error/caller/service/instance/fields 的大小写不敏感子串；空 = 不筛
	Limit     int    // 每进程保留的最新条数
	Cursors   map[string]int64
}

// ReadResult 里 Entries 跨进程按时间升序；Reset/Truncated 列进程名（缺省为空表）。
type ReadResult struct {
	Entries []Entry          `json:"entries"`
	Cursors map[string]int64 `json:"cursors"`
	// Reset：文件比游标短（轮转/截断），该进程数据已在本响应整段重取。
	Reset []string `json:"reset"`
	// Truncated：更早的匹配行可能存在但未返回（只扫到尾窗口，或窗口内匹配超过
	// Limit 丢了旧条）。冷窗口语义的诚实信号，前端据此提示「窗口外未加载」。
	Truncated []string `json:"truncated"`
}

// Reader 读 dir 下的进程日志文件；无状态，可并发复用。
type Reader struct {
	dir string
	// 尾窗口与实例枚举窗口的字节上限（测试收小以驱动窗口逻辑）。
	scanWindow     int64
	instanceWindow int64
}

// NewReader 构造读 dir 目录的 Reader。dir 一律传 Dir()——与写入端同一套
// LIUSHA_LOG_DIR 解析，读到的一定是写出的。
func NewReader(dir string) *Reader {
	return &Reader{dir: dir, scanWindow: 16 << 20, instanceWindow: 512 << 10}
}

// Dir 返回日志文件目录：LIUSHA_LOG_DIR，缺省 logs（相对进程工作目录）。
// 容器部署三个业务进程共用一个日志卷（docker-compose.yml），
// api 进程经它读全体进程文件；同机非容器部署要求各进程同工作目录或同设该变量。
func Dir() string { return logDir() }

// LogProcesses 枚举视图域内各进程文件的存在性/大小/近期实例。
// 文件缺失的进程照常返回（exists=false），前端下拉保持稳定并提示无文件。
func (r *Reader) LogProcesses() []ProcessInfo {
	out := make([]ProcessInfo, 0, len(ViewProcesses))
	for _, name := range ViewProcesses {
		info := ProcessInfo{Name: name}
		if fi, err := os.Stat(r.filePath(name)); err == nil && fi.Mode().IsRegular() {
			info.Exists, info.SizeBytes, info.ModTime = true, fi.Size(), fi.ModTime()
			info.Instances = r.recentInstances(name)
		}
		out = append(out, info)
	}
	return out
}

// ReadLogs 按 query 读若干进程并归并。单进程读取出错即整体出错（调用方 500）。
func (r *Reader) ReadLogs(q ReadQuery) (ReadResult, error) {
	names := q.Processes
	if len(names) == 0 {
		names = ViewProcesses
	}
	res := ReadResult{
		Entries:   []Entry{},
		Cursors:   make(map[string]int64, len(names)),
		Reset:     []string{},
		Truncated: []string{},
	}
	for _, name := range names {
		entries, next, reset, truncated, err := r.readProcess(name, q)
		if err != nil {
			return ReadResult{}, err
		}
		res.Cursors[name] = next
		if reset {
			res.Reset = append(res.Reset, name)
		}
		if truncated {
			res.Truncated = append(res.Truncated, name)
		}
		res.Entries = append(res.Entries, entries...)
	}
	sort.SliceStable(res.Entries, func(i, j int) bool {
		return res.Entries[i].Time.Before(res.Entries[j].Time)
	})
	return res, nil
}

// readProcess 读单个进程文件：取 [max(cursor, size-scanWindow), size) 窗口内
// 最新 limit 条匹配行。返回匹配行（升序）、续读偏移、是否轮转、是否截窗。
//
// 性能关键：先只做行边界字节扫描（GB/s 级），再从尾部倒序解析、凑满 limit
// 即停——大文件的窗口内可能有几十万行，全量 json 解析只为了取最新几百条
// 是数量级浪费；只有罕见的「筛选极少命中」才退化为整窗解析（与旧实现同界）。
func (r *Reader) readProcess(name string, q ReadQuery) (entries []Entry, next int64, reset, truncated bool, err error) {
	f, err := os.Open(r.filePath(name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, false, false, nil
		}
		return nil, 0, false, false, err
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		return nil, 0, false, false, err
	}
	size := fi.Size()

	cursor := q.Cursors[name]
	if cursor > size {
		// 文件比上次续读点短：已轮转/被截断，老游标作废、整段重取。
		reset = true
		cursor = 0
	}
	start := cursor
	if size-start > r.scanWindow {
		start = size - r.scanWindow
		truncated = start > cursor
	}
	if start >= size {
		return []Entry{}, size, reset, truncated, nil
	}

	buf := make([]byte, size-start)
	n, err := f.ReadAt(buf, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, false, false, err
	}
	buf = buf[:n] // stat 与 read 之间文件可能变短（轮转竞态），按实读截断

	// 行边界扫描：记录完整行行尾（\n）的位置
	newlines := make([]int32, 0, len(buf)/128+1)
	for i := 0; i < len(buf); i++ {
		if buf[i] == '\n' {
			newlines = append(newlines, int32(i))
		}
	}

	match := compileMatcher(q)
	keep := q.Limit
	if keep <= 0 {
		keep = defaultReadLimit
	}

	// 从尾行倒序解析，凑满 keep 即停
	for i := len(newlines) - 1; i >= 0; i-- {
		nl := int(newlines[i])
		lineStart := 0
		if i > 0 {
			lineStart = int(newlines[i-1]) + 1
		}
		if e := parseEntry(buf[lineStart:nl], start+int64(lineStart), name); e != nil && match(e) {
			entries = append(entries, *e)
			if len(entries) == keep {
				// 停下时窗口内还有未扫的行 → 更早的匹配可能存在
				if lineStart > 0 {
					truncated = true
				}
				break
			}
		}
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 { // 倒序收集 → 反转成升序
		entries[i], entries[j] = entries[j], entries[i]
	}

	// 续读点 = 最后一个完整行的行尾。尾行没有 \n 说明正在被写入，本轮不取，
	// 游标停在它行首，写完整后下一轮读回；窗口内一个完整行都没有则游标不动。
	if len(newlines) > 0 {
		next = start + int64(newlines[len(newlines)-1]) + 1
	} else {
		next = start
	}
	return entries, next, reset, truncated, nil
}

// recentInstances 扫文件尾 instanceWindow 窗口，按首次出现序收集去重实例。
func (r *Reader) recentInstances(name string) []string {
	f, err := os.Open(r.filePath(name))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	start := int64(0)
	if fi.Size() > r.instanceWindow {
		start = fi.Size() - r.instanceWindow
	}
	buf := make([]byte, fi.Size()-start)
	n, err := f.ReadAt(buf, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil
	}
	buf = buf[:n]

	var out []string
	seen := make(map[string]bool)
	lineOff := 0
	for i := 0; i <= len(buf); i++ {
		if i != len(buf) && buf[i] != '\n' {
			continue
		}
		if i == len(buf) && lineOff == i {
			break
		}
		if e := parseEntry(buf[lineOff:i], 0, name); e != nil && e.Instance != "" && !seen[e.Instance] {
			seen[e.Instance] = true
			out = append(out, e.Instance)
			if len(out) >= instanceCap {
				break
			}
		}
		lineOff = i + 1
	}
	return out
}

// filePath 是文件布局的唯一权威：dir/{process}.log，与写入端 sharedFileWriter 一致。
func (r *Reader) filePath(name string) string {
	return filepath.Join(r.dir, name+".log")
}

// entryJSONKeys 是 Entry 的已知字段；其余键全部收进 Fields。
var entryJSONKeys = []string{"time", "level", "service", "instance", "caller", "message", "error"}

// parseEntry 解析一行 JSON 日志；非 JSON/空行返回 nil（文件只应有 JSON lines，
// 半截尾行由调用方按「游标停在行首」处理，不走这里）。
func parseEntry(line []byte, offset int64, fallbackService string) *Entry {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return nil
	}

	e := &Entry{Offset: offset, Service: fallbackService}
	if v, ok := raw["time"].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			e.Time = t
		}
	}
	if v, ok := raw["level"].(string); ok {
		e.Level = v
	}
	if v, ok := raw["service"].(string); ok && v != "" {
		e.Service = v
	}
	if v, ok := raw["instance"].(string); ok {
		e.Instance = v
	}
	if v, ok := raw["caller"].(string); ok {
		e.Caller = v
	}
	if v, ok := raw["message"].(string); ok {
		e.Message = v
	}
	if v, ok := raw["error"].(string); ok {
		e.Error = v
	}
	for _, k := range entryJSONKeys {
		delete(raw, k)
	}
	if len(raw) > 0 {
		e.Fields = raw
	}
	return e
}

// compileMatcher 把筛选编译成谓词，避免逐行重复解析筛选参数。
func compileMatcher(q ReadQuery) func(*Entry) bool {
	needle := strings.ToLower(strings.TrimSpace(q.Search))
	return func(e *Entry) bool {
		if q.Level != "" && e.Level != q.Level {
			return false
		}
		if q.Instance != "" && e.Instance != q.Instance {
			return false
		}
		if needle != "" {
			hay := strings.ToLower(e.Message + "\n" + e.Error + "\n" + e.Caller + "\n" + e.Service + "\n" + e.Instance)
			if len(e.Fields) > 0 {
				// 业务键（task_id 等）参与搜索：详情里看得到的就搜得到
				if fb, err := json.Marshal(e.Fields); err == nil {
					hay += "\n" + strings.ToLower(string(fb))
				}
			}
			if !strings.Contains(hay, needle) {
				return false
			}
		}
		return true
	}
}
