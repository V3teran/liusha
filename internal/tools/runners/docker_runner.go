// Package runners 提供"在容器里跑外部工具"的通用底座。
//
// 当前唯一实现：DockerRunner（用 docker CLI exec，避开 docker SDK 依赖）。
// 上层（如 internal/tools/external.RunCommand）只关心 RunSpec/RunResult，
// 不需要懂 docker flag 细节。
package runners

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// RunSpec 描述一次容器运行需求。所有字段都是声明式：底层 runner 把它翻译成 docker run flags。
//
// 字段语义：
//   - Image：镜像 tag，如 "liusha/pentools:v1"
//   - Cmd：容器启动命令（含可执行 + 参数），如 ["sqlmap", "-r", "/work/req.raw", ...]
//   - Workdir：host 侧工作目录，会以 /work 路径 mount 进容器（read-write）
//   - Network：docker network 名；空 → 默认 bridge（可访问公网）
//   - Env：注入容器的环境变量
//   - MemLimit：内存上限（字节，0 不限）
//   - CPULimit：CPU 限额（小数，0 不限，1.0 = 1 核）
//   - Timeout：整次运行的硬超时；0 不限（不推荐）
//   - AutoRemove：true → docker run --rm（容器退出立即删，避免堆积）
type RunSpec struct {
	Image string
	Cmd   []string
	// ContainerName 是 docker --name 值（如 "liusha-shell-default-a1b2c3d4"），
	// 让运维 docker ps / docker logs 能直接定位某次扫描；空值由 docker 自动生成。
	// caller 必须保证唯一性（docker --name 重复会报错）。
	ContainerName string
	Workdir       string
	Network       string
	Env           map[string]string
	MemLimit      int64
	CPULimit      float64
	Timeout       time.Duration
	AutoRemove    bool
}

// RunResult 是一次容器运行的结果。
//
//   - ExitCode：容器进程退出码（0 = 成功；docker run 自身错误时为 -1）
//   - Stdout/Stderr：容器输出（已 buffer 整段返回；上限受 docker 默认行为约束）
//   - TimedOut：true 表示因 Timeout 触发被 SIGKILL
type RunResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	TimedOut bool
}

// fallbackConcurrency 是同时跑容器数的默认上限——避免 host CPU/mem 被多并发 sqlmap
// 等长任务打满。可由 NewDockerRunner(WithConcurrency(n)) 覆盖。
const fallbackConcurrency = 5

// DockerRunner 是 docker CLI 实现。除并发 semaphore 外无内部状态，可全局共享单例。
type DockerRunner struct {
	// DockerBin 默认 "docker"；需要时可改 podman 等兼容 CLI 路径。
	DockerBin string

	// sem 全局并发限流：buffered channel，token 数 = 并发上限。
	// RunAndWait 入口先 <-sem 占 token，结束后归还。
	sem chan struct{}
}

// Option 是 NewDockerRunner 的函数式选项。
type Option func(*DockerRunner)

// WithConcurrency 覆盖默认并发上限（n <= 0 时退化为 fallbackConcurrency）。
func WithConcurrency(n int) Option {
	return func(r *DockerRunner) {
		if n <= 0 {
			n = fallbackConcurrency
		}
		r.sem = make(chan struct{}, n)
	}
}

// NewDockerRunner 构造默认 runner（并发上限 fallbackConcurrency=5）。
// 可选传 Option 覆盖默认值。
func NewDockerRunner(opts ...Option) *DockerRunner {
	r := &DockerRunner{
		DockerBin: "docker",
		sem:       make(chan struct{}, fallbackConcurrency),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// RunAndWait 启容器、阻塞至退出（或 Timeout）、回收输出。
//
// 错误语义：
//   - 容器正常退出（包括非 0 退出码）→ 返回 RunResult，err=nil；caller 看 ExitCode 判定
//   - docker CLI 自身报错（镜像不存在 / daemon 不可达 / mount 失败）→ err 非 nil
//   - Timeout 命中 → RunResult.TimedOut=true，err=nil（caller 看 TimedOut 判定）
func (r *DockerRunner) RunAndWait(ctx context.Context, spec RunSpec) (RunResult, error) {
	if spec.Image == "" {
		return RunResult{}, fmt.Errorf("RunSpec.Image 必填")
	}
	bin := r.DockerBin
	if bin == "" {
		bin = "docker"
	}

	// 全局并发限流：阻塞直到拿到 token（受 ctx 取消尊重）。
	// sem 为 nil（外部直接 &DockerRunner{...} 实例化的旧路径）时跳过限流。
	if r.sem != nil {
		select {
		case r.sem <- struct{}{}:
			defer func() { <-r.sem }()
		case <-ctx.Done():
			return RunResult{}, ctx.Err()
		}
	}

	args := buildDockerArgs(spec)

	runCtx := ctx
	var cancel context.CancelFunc
	if spec.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(runCtx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	res := RunResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if runCtx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
		res.ExitCode = -1
		return res, nil
	}

	if err != nil {
		// 区分"容器非 0 退出"vs"docker CLI 调用失败"。
		// exec.ExitError → 容器拿到了，按非 0 退出处理（不算 runner 错误）。
		var exitErr *exec.ExitError
		if asExitError(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res, nil
		}
		// 其他 → docker CLI 自身错误（daemon down / image pull failed / etc.）
		res.ExitCode = -1
		return res, fmt.Errorf("docker run failed: %w (stderr=%s)", err, strings.TrimSpace(stderr.String()))
	}

	res.ExitCode = 0
	return res, nil
}

// buildDockerArgs 把 RunSpec 翻译成 docker CLI args。
//
// 顺序：run [flags...] image [cmd...]
func buildDockerArgs(spec RunSpec) []string {
	args := []string{"run", "-i"} // -i 让容器 stdin 可读（多数工具不需要，但避免某些工具 abort）
	// 让容器内 host.docker.internal 解析到宿主网关——sqlmap 等工具打 host 上的 :8001/:4280 必需。
	// macOS Docker Desktop 默认已加；Linux 必须显式注入。--network=host 时 docker 会自动忽略此 flag。
	args = append(args, "--add-host=host.docker.internal:host-gateway")
	if spec.AutoRemove {
		args = append(args, "--rm")
	}
	if spec.ContainerName != "" {
		args = append(args, "--name", spec.ContainerName)
	}
	if spec.Workdir != "" {
		args = append(args, "-v", spec.Workdir+":/work")
	}
	if spec.Network != "" {
		args = append(args, "--network", spec.Network)
	}
	for k, v := range spec.Env {
		args = append(args, "-e", k+"="+v)
	}
	if spec.MemLimit > 0 {
		args = append(args, "--memory", strconv.FormatInt(spec.MemLimit, 10))
	}
	if spec.CPULimit > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(spec.CPULimit, 'f', 2, 64))
	}
	args = append(args, spec.Image)
	args = append(args, spec.Cmd...)
	return args
}

// asExitError 跨包等价 errors.As 的薄壳，避免引入 errors 包冲突。
func asExitError(err error, target **exec.ExitError) bool {
	for e := err; e != nil; {
		if ee, ok := e.(*exec.ExitError); ok {
			*target = ee
			return true
		}
		// 兼容 wrap 链
		type unwrapper interface{ Unwrap() error }
		u, ok := e.(unwrapper)
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}
