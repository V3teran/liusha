package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// Launcher 管理 sandbox 容器生命周期。
//
// 容器粒度 = 一次 agent run：
//
//	Spawn          启动容器 + 等 healthz → 返回 Client
//	Destroy        停止 + 删除容器
//	CleanupOrphans 启动时一次性清理上次进程崩前残留的容器
//
// 见 docs/superpowers/specs/2026-05-16-sandbox-server-design.md
type Launcher interface {
	Spawn(ctx context.Context, runID, ownerID string) (Client, error)
	Destroy(ctx context.Context, runID string) error
	CleanupOrphans(ctx context.Context) error
}

const (
	// containerNamePrefix 是容器命名前缀，CleanupOrphans 模糊匹配用。
	containerNamePrefix = "liusha-sandbox-"

	// healthzMaxWait 是 spawn 后等待 sandbox-server ready 的最长时间。
	// 容器启动 + sandbox-server bind 完成通常 < 5s，30s 安全冗余。
	healthzMaxWait = 30 * time.Second

	// healthzInterval 是 healthz 轮询间隔。
	healthzInterval = 300 * time.Millisecond

	// 容器资源默认上限——和 pentest 工具实际需求匹配（sqlmap / nuclei 等内存
	// 占用通常 < 1GB，给 2GB 余量；chrome + browser-use 也在这个范围内）。
	defaultMemLimit = "2g"
	defaultCPULimit = "2"

	// 容器内 sandbox-server 监听端口（与 cmd/sandbox-server/main.go 一致）。
	containerSandboxPort = "8080"
)

// DockerLauncher 是 Launcher 的 docker CLI 实现。
//
// 部署假设（A1 方案）：scanner 主进程跑在 host，sandbox 容器在 host docker；
// 端口映射 127.0.0.1:0:8080（绑 localhost 任意端口，不暴露 0.0.0.0 减少攻击面），
// Spawn 时通过 `docker port` 拿到 host 端口拼 baseURL。
type DockerLauncher struct {
	// Image 是 sandbox 镜像 tag，必填。
	Image string

	// DockerBin 是 docker CLI 可执行路径，空走默认 "docker"。
	DockerBin string

	// ViewportWidth/Height 是 chromium 视口尺寸，通过 docker run -e 注入到容器 env，
	// 容器内 browser-use wrapper 读 LIUSHA_VIEWPORT_WIDTH/HEIGHT 透传给 browser-use-cli
	// 的 --window-width/--window-height 全局 flag。零值时容器内 wrapper 走自己默认（1280×720）。
	ViewportWidth  int
	ViewportHeight int

	// AgentProxyAddr 是宿主机 cmd/proxy agent listener 地址（如 "host.docker.internal:8890"）。
	// Spawn 时注入到容器 HTTP_PROXY / HTTPS_PROXY env，sandbox 内所有 CLI 工具流量自动
	// 经此代理 → liusha proxy 字典（source=internal），无需 LLM 显式 --proxy 参数。
	// URL 中嵌入 hunter_<runID>:_ 作为 Proxy-Authorization basic auth user，让 proxy
	// 端解析出 hunter_id 关联到 owner（cmd/proxy server.go parseProxyAuthHunterID）。
	// 空字符串时不注入 proxy env（向后兼容 / 单测场景）。
	AgentProxyAddr string
}

// NewDockerLauncher 构造 launcher。Image 必填，DockerBin 空走默认。
func NewDockerLauncher(image string) *DockerLauncher {
	return &DockerLauncher{Image: image}
}

// Spawn 启动 sandbox 容器并等待 healthz。返回绑定到该容器 host 端口的 Client。
//
// runID：用于 docker container name（per-hunter 唯一保证容器名不冲突）。
// ownerID：用于 HTTP_PROXY URL basic auth user=owner_<id>（容器内 agent 工具流量
//   经 liusha proxy 8890 时 sanitizer 解析 owner_<id> → X-Liusha-Owner-Id header
//   → server.go 填 snap.OwnerID）。0061 起统一用 owner_id（之前 hunter_id 删了）。
//
// 失败路径：任一步出错都会尝试 Destroy（best-effort），避免容器残留。
func (l *DockerLauncher) Spawn(ctx context.Context, runID, ownerID string) (Client, error) {
	if l.Image == "" {
		return nil, errors.New("DockerLauncher.Image 必填")
	}
	if runID == "" {
		return nil, errors.New("runID 必填")
	}
	if ownerID == "" {
		return nil, errors.New("ownerID 必填（HTTP_PROXY basic auth user）")
	}
	name := containerNamePrefix + runID
	bin := l.dockerBin()

	// docker run -d -p 127.0.0.1:0:8080 --name=<name> --memory=2g --cpus=2
	//   [-e HTTP_PROXY=...] [-e LIUSHA_VIEWPORT_*] [--add-host host.docker.internal:host-gateway] <image>
	args := []string{"run", "-d",
		"-p", "127.0.0.1:0:" + containerSandboxPort,
		"--name=" + name,
		"--memory=" + defaultMemLimit,
		"--cpus=" + defaultCPULimit,
	}
	// 视口尺寸：注入到容器 env 给 browser-use wrapper 透传。
	// 零值不注入——wrapper 自己有默认（1280×720），保留向后兼容。
	if l.ViewportWidth > 0 {
		args = append(args, "-e", fmt.Sprintf("LIUSHA_VIEWPORT_WIDTH=%d", l.ViewportWidth))
	}
	if l.ViewportHeight > 0 {
		args = append(args, "-e", fmt.Sprintf("LIUSHA_VIEWPORT_HEIGHT=%d", l.ViewportHeight))
	}
	// Agent proxy 注入（0060+ / 0061 简化为 owner_<id>）：容器内 CLI 工具流量自动经
	// liusha proxy 8890 → internal source 字典。URL 嵌入 owner_<ownerID>:_ →
	// Proxy-Authorization basic auth → sanitizer 解析 owner_<id> → X-Liusha-Owner-Id
	// → snap.OwnerID（server.go onResponse 填）→ ingestor 直接入字典（不查 hunter 表）。
	// 空 AgentProxyAddr 时跳过（向后兼容 / 单测）。
	if l.AgentProxyAddr != "" {
		proxyURL := fmt.Sprintf("http://owner_%s:_@%s", ownerID, l.AgentProxyAddr)
		args = append(args,
			"-e", "HTTP_PROXY="+proxyURL,
			"-e", "HTTPS_PROXY="+proxyURL,
			"-e", "http_proxy="+proxyURL,
			"-e", "https_proxy="+proxyURL,
			// 跳过本机 / 容器内 service 避免循环
			"-e", "NO_PROXY=localhost,127.0.0.1,sandbox-server,host.docker.internal",
			"-e", "no_proxy=localhost,127.0.0.1,sandbox-server,host.docker.internal",
			// linux 不支持 host.docker.internal，docker 20.10+ 用 --add-host=host-gateway 等价
			// macOS / Windows desktop 内置该 DNS，加这个也兼容（重复绑定无害）
			"--add-host=host.docker.internal:host-gateway",
		)
	}
	args = append(args, l.Image)
	runOut, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker run %s: %w: %s", name, err, strings.TrimSpace(string(runOut)))
	}

	// 拿 host 端口：docker port <name> 8080 输出形如 "127.0.0.1:54321"（可能两行 IPv4 + IPv6）。
	portOut, err := exec.CommandContext(ctx, bin, "port", name, containerSandboxPort).Output()
	if err != nil {
		_ = l.Destroy(context.Background(), runID)
		return nil, fmt.Errorf("docker port %s: %w", name, err)
	}
	hostAddr := strings.TrimSpace(strings.SplitN(string(portOut), "\n", 2)[0])
	if hostAddr == "" {
		_ = l.Destroy(context.Background(), runID)
		return nil, fmt.Errorf("docker port %s 输出空", name)
	}

	baseURL := "http://" + hostAddr
	client := newHTTPClient(baseURL)

	if err := waitHealthz(ctx, baseURL); err != nil {
		_ = l.Destroy(context.Background(), runID)
		return nil, fmt.Errorf("等待 healthz %s: %w", name, err)
	}
	return client, nil
}

// Destroy 停止 + 删除容器。
//
// docker stop 默认 SIGTERM 后 10s SIGKILL；docker rm -f 强制清理（即使容器没停透）。
// 任一步失败 log 警告不致命——max lifetime 或下次启动 CleanupOrphans 兜底回收。
func (l *DockerLauncher) Destroy(ctx context.Context, runID string) error {
	if runID == "" {
		return errors.New("runID 必填")
	}
	name := containerNamePrefix + runID
	bin := l.dockerBin()

	// docker stop 失败不致命（容器可能已死），继续走 rm -f
	_ = exec.CommandContext(ctx, bin, "stop", name).Run()

	rmOut, err := exec.CommandContext(ctx, bin, "rm", "-f", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker rm -f %s: %w: %s", name, err, strings.TrimSpace(string(rmOut)))
	}
	return nil
}

// CleanupOrphans 列出所有 liusha-sandbox-* 容器并强制删除。
//
// 用途：scanner worker 启动时一次性清理上次进程崩前残留的容器。
// 不是后台 sweeper——只在进程启动时跑一次，正常 Destroy + max lifetime 兜底已经覆盖大部分场景。
func (l *DockerLauncher) CleanupOrphans(ctx context.Context) error {
	bin := l.dockerBin()
	out, err := exec.CommandContext(ctx, bin, "ps", "-a",
		"--filter=name="+containerNamePrefix,
		"--format={{.Names}}",
	).Output()
	if err != nil {
		return fmt.Errorf("docker ps: %w", err)
	}
	names := strings.Fields(strings.TrimSpace(string(out)))
	if len(names) == 0 {
		return nil
	}

	args := append([]string{"rm", "-f"}, names...)
	rmOut, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker rm -f orphans: %w: %s", err, strings.TrimSpace(string(rmOut)))
	}
	return nil
}

// waitHealthz 轮询 GET <baseURL>/healthz 直到 200 或 ctx/timeout 超时。
//
// 单次 HTTP 请求 timeout 2s，避免单次卡住浪费整个 healthzMaxWait 窗口。
func waitHealthz(ctx context.Context, baseURL string) error {
	deadline := time.Now().Add(healthzMaxWait)
	httpc := &http.Client{Timeout: 2 * time.Second}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("healthz 超时 (%v)", healthzMaxWait)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
		if err != nil {
			return fmt.Errorf("build healthz request: %w", err)
		}
		resp, err := httpc.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(healthzInterval)
	}
}

func (l *DockerLauncher) dockerBin() string {
	if l.DockerBin == "" {
		return "docker"
	}
	return l.DockerBin
}
