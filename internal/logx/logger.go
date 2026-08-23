// Package logx 提供统一的结构化日志器。
//
// 字段约定：time / level / service / instance / caller / message / error / stack
// 输出形态：stdout（dev=console 彩色；prod=json）+ file（永远 json，lumberjack 轮转）
// 全部行为由环境变量驱动，零配置即可运行。
//
// 文件聚合：同一进程内所有 New() 共写一个 logs/{processName}.log。
// processName 取自 LIUSHA_LOG_PROCESS env，fallback 到 os.Args[0] basename
// （cmd 二进制名）。多 logger 实例共享同一个 *lumberjack.Logger，
// 由 lumberjack 内部 mutex 保证并发安全。
package logx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"gopkg.in/natefinch/lumberjack.v2"
)

// New 返回带 service / instance / caller / timestamp 的 logger。
// 同进程多次调用安全；每次返回新副本，但共享底层 writer。
func New(service string) zerolog.Logger {
	cfg := loadConfig()
	applyGlobals(cfg)
	w := buildWriter(cfg)
	return zerolog.New(w).With().
		Timestamp().
		Caller().
		Str("service", service).
		Str("instance", cfg.instance).
		Logger()
}

// newWith 仅供单测注入 writer 与显式形态使用，不读 LIUSHA_LOG_DIR/TO_FILE。
func newWith(w io.Writer, env string) zerolog.Logger {
	applyGlobals(loadConfig())
	if env == "development" {
		return zerolog.New(newConsoleWriter(w, true)).With().Timestamp().Logger()
	}
	return zerolog.New(w).With().Timestamp().Logger()
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
	level       zerolog.Level
	format      string // "json" | "console"
	toStdout    bool
	toFile      bool
	dir         string
	instance    string
	processName string
	maxSizeMB   int
	maxBackups  int
	maxAgeDays  int
	compress    bool
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

	dir := strings.TrimSpace(os.Getenv("LIUSHA_LOG_DIR"))
	if dir == "" {
		dir = "logs"
	}

	return config{
		level:       parseLevel(os.Getenv("LIUSHA_LOG_LEVEL")),
		format:      format,
		toStdout:    boolEnv("LIUSHA_LOG_TO_STDOUT", true),
		toFile:      boolEnv("LIUSHA_LOG_TO_FILE", true),
		dir:         dir,
		instance:    resolveInstance(),
		processName: resolveProcessName(),
		maxSizeMB:   intEnv("LIUSHA_LOG_MAX_SIZE_MB", 100),
		maxBackups:  intEnv("LIUSHA_LOG_MAX_BACKUPS", 7),
		maxAgeDays:  intEnv("LIUSHA_LOG_MAX_AGE_DAYS", 14),
		compress:    boolEnv("LIUSHA_LOG_COMPRESS", true),
	}
}

// resolveProcessName 决定文件名（同进程所有 logger 共写此文件）。
// 优先 LIUSHA_LOG_PROCESS env，fallback 到 os.Args[0] 的 basename
// （即 cmd 二进制名，如 ./bin/runner → "runner"）。
func resolveProcessName() string {
	if v := strings.TrimSpace(os.Getenv("LIUSHA_LOG_PROCESS")); v != "" {
		return v
	}
	if len(os.Args) == 0 || os.Args[0] == "" {
		return "app"
	}
	name := filepath.Base(os.Args[0])
	// go test 时 os.Args[0] 是 *.test 形式（如 logx.test）；保留 basename，
	// 让单测产物落到 ${pkg}.test.log，不污染 logs/{cmd}.log 的命名。
	return name
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

func applyGlobals(cfg config) {
	zerolog.SetGlobalLevel(cfg.level)
	zerolog.MessageFieldName = "message"
	zerolog.ErrorFieldName = "error"
	zerolog.TimeFieldFormat = time.RFC3339Nano
	zerolog.CallerMarshalFunc = shortCaller
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
		if err := os.MkdirAll(cfg.dir, 0o755); err == nil {
			path := filepath.Join(cfg.dir, cfg.processName+".log")
			ws = append(ws, sharedFileWriter(path, cfg))
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
