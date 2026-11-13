// Package logx 提供统一的结构化日志器。
//
// 字段约定：time / level / service / instance / caller / message / error / stack
// 输出形态：stdout（dev=console 彩色；prod=json）+ file（永远 json，lumberjack 轮转）
// 全部行为由环境变量驱动，零配置即可运行。
//
// 输出端进程内唯一：所有 New() 写同一个输出端，它在首条日志写出时才按当时的环境变量构建
// （级别、格式、是否写 stdout / 文件、目录）。包级 logger 在包初始化时就已建好，而入口可能
// 之后才补齐环境（liusha 的 -env-file）——惰性构建让它们与入口建的 logger 同一口径。
//
// 文件聚合：同一进程写一个 logs/{进程名}.log。进程名由入口经 SetProcess 设定（liusha 的
// 子命令名），未设定时取 os.Args[0] 的 basename。
// 测试二进制（testing.Testing()）里文件落盘缺省关闭——否则会在每个被测包目录下留下
// logs/*.test.log；需要时显式设 LIUSHA_LOG_TO_FILE=true。
package logx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"gopkg.in/natefinch/lumberjack.v2"
)

// New 返回带 service / instance / caller / timestamp 的 logger。
// 同进程多次调用安全；所有 logger 共写进程内唯一的输出端。
func New(service string) zerolog.Logger {
	cfg := loadConfig()
	applyGlobals(cfg)
	return zerolog.New(output).With().
		Timestamp().
		Caller().
		Str("service", service).
		Str("instance", cfg.instance).
		Logger()
}

// output 是进程内全部 logger 共用的输出端。
var output = &lazyOutput{}

// lazyOutput 在首次写入时按当时的环境构建真正的输出端。
type lazyOutput struct {
	mu sync.Mutex
	w  atomic.Pointer[io.Writer]
}

func (o *lazyOutput) Write(p []byte) (int, error) {
	if w := o.w.Load(); w != nil {
		return (*w).Write(p)
	}
	o.mu.Lock()
	w := o.w.Load()
	if w == nil {
		cfg := loadConfig()
		applyGlobals(cfg)
		built := buildWriter(cfg)
		w = &built
		o.w.Store(w)
	}
	o.mu.Unlock()
	return (*w).Write(p)
}

// reset 让下一次写入重新构建输出端。
func (o *lazyOutput) reset() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.w.Store(nil)
}

const (
	serviceField  = "service"
	instanceField = "instance"

	ansiCyan  = "\x1b[36m"
	ansiReset = "\x1b[0m"
)

// newConsoleWriter 构造 dev 形态的 ConsoleWriter：
//   - service 提前到 LEVEL 后渲染为 [api] 标签（彩色 cyan）
//   - instance 在 dev console 完全隐藏（多副本/容器再单独打开）
//   - 行尾只剩业务字段
func newConsoleWriter(out io.Writer, noColor bool) zerolog.ConsoleWriter {
	return zerolog.ConsoleWriter{
		Out:        out,
		TimeFormat: "15:04:05.000",
		NoColor:    noColor,
		PartsOrder: []string{
			zerolog.TimestampFieldName,
			zerolog.LevelFieldName,
			serviceField,
			zerolog.CallerFieldName,
			zerolog.MessageFieldName,
		},
		FieldsExclude: []string{serviceField, instanceField},
		FormatPartValueByName: func(i any, name string) string {
			if name != serviceField || i == nil {
				if i == nil {
					return ""
				}
				return fmt.Sprint(i)
			}
			tag := "[" + fmt.Sprint(i) + "]"
			if noColor {
				return tag
			}
			return ansiCyan + tag + ansiReset
		},
	}
}

type config struct {
	level      zerolog.Level
	format     string // "json" | "console"
	toStdout   bool
	toFile     bool
	dir        string
	instance   string
	maxSizeMB  int
	maxBackups int
	maxAgeDays int
	compress   bool
}

// logDir 解析日志文件目录：LIUSHA_LOG_DIR，缺省 ~/data/liusha/logs（与任务工作区同一数据根）。
// 缺省不用相对路径：以服务方式运行时工作目录不可依赖（systemd 为 /，Windows 服务为
// System32）。写入端（buildWriter）与读取端（tail.go 的 Dir）共用这一处解析。
func logDir() string {
	if v := strings.TrimSpace(os.Getenv("LIUSHA_LOG_DIR")); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, "data", "liusha", "logs")
	}
	return "logs"
}

func loadConfig() config {
	env := strings.ToLower(strings.TrimSpace(os.Getenv("LIUSHA_ENV")))
	isDev := env == "" || env == "development" || env == "dev"

	format := strings.ToLower(strings.TrimSpace(os.Getenv("LIUSHA_LOG_FORMAT")))
	if format != "json" && format != "console" {
		if isDev {
			format = "console"
		} else {
			format = "json"
		}
	}

	dir := logDir()

	return config{
		level:      parseLevel(os.Getenv("LIUSHA_LOG_LEVEL")),
		format:     format,
		toStdout:   boolEnv("LIUSHA_LOG_TO_STDOUT", true),
		toFile:     boolEnv("LIUSHA_LOG_TO_FILE", !testing.Testing()),
		dir:        dir,
		instance:   resolveInstance(),
		maxSizeMB:  intEnv("LIUSHA_LOG_MAX_SIZE_MB", 100),
		maxBackups: intEnv("LIUSHA_LOG_MAX_BACKUPS", 7),
		maxAgeDays: intEnv("LIUSHA_LOG_MAX_AGE_DAYS", 14),
		compress:   boolEnv("LIUSHA_LOG_COMPRESS", true),
	}
}

var (
	processMu   sync.Mutex
	processName string
)

// SetProcess 设定本进程的日志文件名（logs/<name>.log）。入口在补齐环境之后、写出任何日志
// 之前调用一次：之后的首条日志按此时的环境构建输出端。
func SetProcess(name string) {
	processMu.Lock()
	processName = name
	processMu.Unlock()
	output.reset()
}

// currentProcess 返回日志文件名：SetProcess 设定的值，未设定时取 os.Args[0] 的 basename。
func currentProcess() string {
	processMu.Lock()
	defer processMu.Unlock()
	if processName != "" {
		return processName
	}
	if len(os.Args) == 0 || os.Args[0] == "" {
		return "app"
	}
	return filepath.Base(os.Args[0])
}

func resolveInstance() string {
	if v := strings.TrimSpace(os.Getenv("LIUSHA_INSTANCE")); v != "" {
		return v
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("%d@%s", os.Getpid(), host)
}

// zerolog 的字段名、时间格式、caller 格式是普通包级变量，每条日志都会读：只能在任何日志写出
// 之前设定一次。它们不随环境变化，放在包初始化里。
func init() {
	zerolog.MessageFieldName = "message"
	zerolog.ErrorFieldName = "error"
	zerolog.TimeFieldFormat = time.RFC3339Nano
	zerolog.CallerMarshalFunc = shortCaller
}

// applyGlobals 应用随环境变化的全局设置（级别；zerolog 以原子操作读写，可随时调用）。
func applyGlobals(cfg config) {
	zerolog.SetGlobalLevel(cfg.level)
}

// shortCaller 把 /abs/path/to/pkg/file.go 缩成 pkg/file.go:line。
// 规则：去掉前导 /，保留最后两段（不足两段就保留全部）。
func shortCaller(_ uintptr, file string, line int) string {
	s := strings.TrimLeft(file, "/")
	count := 0
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			count++
			if count == 2 {
				s = s[i+1:]
				break
			}
		}
	}
	return s + ":" + strconv.Itoa(line)
}

// fileWriters 进程内共享 *lumberjack.Logger 缓存，键为绝对文件路径。
// 所有 logx.New 拿到同一个实例，避免多 fd 同写一个文件交错 + 轮转踩踏。
var (
	fileWritersMu sync.Mutex
	fileWriters   = map[string]*lumberjack.Logger{}
)

func sharedFileWriter(path string, cfg config) *lumberjack.Logger {
	fileWritersMu.Lock()
	defer fileWritersMu.Unlock()
	if w, ok := fileWriters[path]; ok {
		return w
	}
	w := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    cfg.maxSizeMB,
		MaxBackups: cfg.maxBackups,
		MaxAge:     cfg.maxAgeDays,
		Compress:   cfg.compress,
	}
	fileWriters[path] = w
	return w
}

// closeFiles 关闭并忘掉全部日志文件，下一条日志重新打开（测试用：Windows 上打开着的文件删不掉）。
func closeFiles() {
	fileWritersMu.Lock()
	defer fileWritersMu.Unlock()
	for path, w := range fileWriters {
		_ = w.Close()
		delete(fileWriters, path)
	}
	output.reset()
}

func buildWriter(cfg config) io.Writer {
	var ws []io.Writer
	if cfg.toStdout {
		if cfg.format == "console" {
			ws = append(ws, newConsoleWriter(os.Stdout, false))
		} else {
			ws = append(ws, os.Stdout)
		}
	}
	if cfg.toFile {
		if err := os.MkdirAll(cfg.dir, 0o750); err == nil {
			ws = append(ws, sharedFileWriter(filepath.Join(cfg.dir, currentProcess()+".log"), cfg))
		} else {
			fmt.Fprintf(os.Stderr, "logx: 创建日志目录 %s 失败: %v\n", cfg.dir, err)
		}
	}
	switch len(ws) {
	case 0:
		return io.Discard
	case 1:
		return ws[0]
	default:
		return zerolog.MultiLevelWriter(ws...)
	}
}

func parseLevel(s string) zerolog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return zerolog.DebugLevel
	case "warn", "warning":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	default:
		return zerolog.InfoLevel
	}
}

func boolEnv(key string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

func intEnv(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	return n
}

// Recover 返回 defer 用的 recover 处理：panic 转错误日志。
func Recover(service, msg string) {
	if r := recover(); r != nil {
		logger := New(service)
		logger.Error().
			Interface("panic", r).
			Msg(msg)
	}
}

// Go 以带 recover 的独立 goroutine 启动 fn：panic 转错误日志，避免单个后台
// 协程 panic 拖垮整个进程（runner 是长驻服务，agent 循环/持久化循环/事件
// 总线等均经此启动）。
func Go(logger zerolog.Logger, name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error().
					Interface("panic", r).
					Str("goroutine", name).
					Msg("后台协程 panic 已捕获")
			}
		}()
		fn()
	}()
}
