package server

// exec_capture.go：命令输出的收集——有界的内存占用，与完整输出的落盘。
//
// 命令的输出可能很大（一次全端口扫描、一份长报告，也可能是一条失控的命令），调用方只把其中的
// 一段交给模型。沙盒不替调用方裁剪：输出原样返回，上下文的管理贴近调用方；沙盒负责两件只有
// 它做得到的事——
//
//   - 内存占用有界：输出没越过落盘阈值时全部留在内存里（至多一个阈值）；越过之后边到边写进
//     文件，内存里只留响应要带的末尾（maxCaptureBytes）。一条打印几个 GB 的命令不会把沙盒撑爆。
//   - 被调用方省掉的部分不丢：调用方给出落盘阈值（ExecRequest.SaveOutputOverBytes），stdout 与
//     stderr 合计超过它就把两个流完整落盘到任务工作区的 console/ 下——命令可以分段读那个文件，
//     操作员在文件面也看得到。阈值按合计算：调用方的窗口是对整份结果开的，单独看哪一个流都
//     定不了「有没有内容被省掉」。

import (
	"os"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"
)

// consoleDirName 是任务目录下命令输出落盘的子目录（与 work / out / state 并列）。只在输出越过
// 落盘阈值时才会建出来。
const consoleDirName = "console"

// maxCaptureBytes 是响应里一个流至多带的字节数：超过就只带末尾。它不是给调用方的窗口——正常
// 情况下调用方的落盘阈值远小于它，被省掉的部分都在落盘文件里；它只防一条失控的命令把几个 GB
// 塞进一个 JSON 响应。var 便于单测注入小值。
var maxCaptureBytes = 64 << 20

// maxSaveBytes 是一个流的落盘文件至多写入的字节数：再往后的输出不再留档，没写进去的字节数报给
// 调用方。留档不该把工作区所在的盘写满。var 便于单测注入小值。
var maxSaveBytes int64 = 1 << 30

// outputCapture 是一次命令两个流的收集器。
type outputCapture struct {
	mu         sync.Mutex
	limit      int64 // 落盘阈值（两个流合计）；<= 0 从不落盘
	total      int64 // 两个流合计的字节数
	spilled    bool  // 合计已经越过阈值
	consoleDir string

	stdout, stderr *streamCapture
}

// capturedStreams 是收集的结果：两个流各自的文本、完整字节数、落盘路径，以及落盘上的问题。
type capturedStreams struct {
	stdout, stderr               string
	stdoutBytes, stderrBytes     int64
	stdoutFile, stderrFile       string
	stdoutErr, stderrErr         error // 落盘失败的原因；没有失败为 nil
	stdoutUnsaved, stderrUnsaved int64 // 超过落盘文件上限、没写进去的字节数
}

// newOutputCapture 建一次命令的收集器：两个流合计超过 limit 字节就完整落盘到 consoleDir。
func newOutputCapture(limit int, consoleDir string) *outputCapture {
	o := &outputCapture{limit: int64(limit), consoleDir: consoleDir}
	o.stdout = &streamCapture{owner: o, ext: ".stdout", keep: tailBuffer{limit: maxCaptureBytes}}
	o.stderr = &streamCapture{owner: o, ext: ".stderr", keep: tailBuffer{limit: maxCaptureBytes}}
	return o
}

// over 记下合计字节数，报告这次写入之后输出是否已经越过落盘阈值。
func (o *outputCapture) over(n int) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.total += int64(n)
	if o.limit > 0 && o.total > o.limit {
		o.spilled = true
	}
	return o.spilled
}

// hasSpilled 报告合计是否已经越过落盘阈值。
func (o *outputCapture) hasSpilled() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.spilled
}

// finish 结束两个流的收集。
func (o *outputCapture) finish() capturedStreams {
	var s capturedStreams
	s.stdout, s.stdoutBytes, s.stdoutFile, s.stdoutUnsaved, s.stdoutErr = o.stdout.finish()
	s.stderr, s.stderrBytes, s.stderrFile, s.stderrUnsaved, s.stderrErr = o.stderr.finish()
	return s
}

// streamCapture 收集一条输出流（stdout 或 stderr）。实现 io.Writer，直接交给 exec.Cmd。
type streamCapture struct {
	owner *outputCapture
	ext   string // 落盘文件的扩展名
	keep  tailBuffer
	file  *os.File
	path  string // 落盘文件的本机路径
	total int64  // 完整字节数
	saved int64  // 写进文件的字节数
	err   error  // 落盘失败的原因；失败之后不再尝试
}

func (c *streamCapture) Write(p []byte) (int, error) {
	c.total += int64(len(p))
	if c.owner.over(len(p)) && c.file == nil && c.err == nil {
		c.open()
	}
	if c.file != nil {
		c.save(p)
	}
	c.keep.Write(p)
	return len(p), nil
}

// open 开始落盘：建文件，把此前的内容（还在内存里的那一段）先写进去。
func (c *streamCapture) open() {
	if err := os.MkdirAll(c.owner.consoleDir, 0o750); err != nil {
		c.fail(err)
		return
	}
	file, err := os.CreateTemp(c.owner.consoleDir, time.Now().UTC().Format("20060102-150405")+"-*"+c.ext)
	if err != nil {
		c.fail(err)
		return
	}
	c.file, c.path = file, file.Name()
	c.save(c.keep.bytes())
}

// save 把 p 追加进落盘文件，至多写到 maxSaveBytes。
func (c *streamCapture) save(p []byte) {
	room := maxSaveBytes - c.saved
	if room <= 0 {
		return
	}
	if int64(len(p)) > room {
		p = p[:room]
	}
	n, err := c.file.Write(p)
	c.saved += int64(n)
	if err != nil {
		c.fail(err)
	}
}

// fail 记下落盘失败并放弃这个文件：输出照常保留，响应里不带文件路径，原因随提示带出。
func (c *streamCapture) fail(err error) {
	if c.file != nil {
		_ = c.file.Close()
		_ = os.Remove(c.path)
		c.file, c.path = nil, ""
	}
	if c.err == nil {
		c.err = err
	}
}

// finish 结束这一条的收集，返回交给响应的文本、完整字节数、落盘路径（交给 agent 的形式；没有
// 落盘时为空）、没写进文件的字节数与落盘失败的原因。只留了尾部时，开头可能落在一个多字节字符
// 的中间，残字节丢掉。
func (c *streamCapture) finish() (text string, size int64, file string, unsaved int64, err error) {
	// 两个流各由一个协程送达，先后与命令写它们的顺序无关：这个流可能在合计越过阈值之前就写完了，
	// 它的 Write 没赶上落盘。内容还完整留在内存里，现在补上——越过阈值之后两个流都要有完整的留档。
	if c.owner.hasSpilled() && c.file == nil && c.err == nil && len(c.keep.buf) > 0 && !c.keep.truncated() {
		c.open()
	}
	if c.file != nil {
		if cerr := c.file.Close(); cerr != nil {
			c.fail(cerr)
		}
		c.file = nil
	}
	buf := c.keep.bytes()
	if c.keep.truncated() {
		for len(buf) > 0 && buf[0]&0xC0 == 0x80 { // 续字节（10xxxxxx）= 残字符的开头
			buf = buf[1:]
		}
	}
	if c.err != nil {
		return string(buf), c.total, "", 0, c.err
	}
	return string(buf), c.total, sandbox.AgentPath(c.path), c.total - c.saved, nil
}

// tailBuffer 保留最后写入的 limit 字节。内容没有超过 limit 时就是全部内容；一旦超过就转入一个
// 容量恰为 limit 的环形缓冲，内存占用不随输出增长、也不随单次写入的大小波动（追加后再切头的
// 写法做不到这一点：切片底层数组的容量会跟着单次写入走）。
type tailBuffer struct {
	limit int
	buf   []byte // 没超过 limit 时是全部内容；之后是容量 limit 的环形缓冲
	ring  bool
	pos   int // 环形缓冲的写入位置
}

func (b *tailBuffer) Write(p []byte) {
	if b.limit <= 0 {
		return // 不保留任何内容
	}
	if !b.ring {
		if len(b.buf)+len(p) <= b.limit {
			b.buf = append(b.buf, p...)
			return
		}
		// 转入环形缓冲：留已有内容与本次写入合起来的最后 limit 字节。
		kept := make([]byte, b.limit)
		if len(p) >= b.limit {
			copy(kept, p[len(p)-b.limit:])
		} else {
			n := copy(kept, b.buf[len(b.buf)+len(p)-b.limit:])
			copy(kept[n:], p)
		}
		b.buf, b.ring, b.pos = kept, true, 0
		return
	}
	for len(p) > 0 {
		n := copy(b.buf[b.pos:], p)
		b.pos = (b.pos + n) % b.limit
		p = p[n:]
	}
}

// bytes 按写入顺序返回保留的内容。
func (b *tailBuffer) bytes() []byte {
	if !b.ring {
		return b.buf
	}
	out := make([]byte, b.limit)
	n := copy(out, b.buf[b.pos:])
	copy(out[n:], b.buf[:b.pos])
	return out
}

// truncated 报告是否已经有更早的内容被挤掉。
func (b *tailBuffer) truncated() bool { return b.ring }
