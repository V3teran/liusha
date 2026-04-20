// Package runner 在隔离容器内执行外部安全工具。
//
// DockerRunner 约束：
//   - image 必须在白名单（当前仅 liusha/vuln-tools）。
//   - cmd[0] 必须在工具白名单（nuclei/ffuf/httpx）。
//   - 容器创建时强制硬编码 HostConfig：AutoRemove、ReadonlyRootfs、
//     CapDrop=ALL、SecurityOpt=no-new-privileges、非 root 用户、
//     内存/PID/CPU 限额。调用方无法覆写这些安全字段。
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/pkg/stdcopy"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// allowedImages 是允许启动的镜像白名单。
// agent-worker 不应跑任意镜像——全部流量只走内置沙箱镜像。
var allowedImages = map[string]struct{}{
	"liusha/vuln-tools": {},
}

// allowedTools 是允许作为 cmd[0] 的工具名白名单。
// 阻止调用方塞 sh/bash/curl/rm 等非预期进程。
var allowedTools = map[string]struct{}{
	"nuclei": {},
	"ffuf":   {},
	"httpx":  {},
}

// dockerClient 把 DockerRunner 需要的 docker SDK 能力抽成小接口，
// 便于在单测里用 fake 代替真实 docker.sock 连接。
type dockerClient interface {
	ContainerCreate(
		ctx context.Context,
		config *container.Config,
		hostConfig *container.HostConfig,
		networkingConfig *network.NetworkingConfig,
		platform *ocispec.Platform,
		containerName string,
	) (container.CreateResponse, error)
	ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error
	ContainerWait(
		ctx context.Context,
		containerID string,
		condition container.WaitCondition,
	) (<-chan container.WaitResponse, <-chan error)
	ContainerLogs(ctx context.Context, containerID string, options container.LogsOptions) (io.ReadCloser, error)
}

// DockerRunner 在一次性沙箱容器内执行工具命令，容器随命令结束被销毁。
type DockerRunner struct {
	cli dockerClient
}

// NewDockerRunner 构造一个 DockerRunner。传 nil 仅允许在不触达 docker 的路径上使用
// （例如参数校验失败）——生产路径必须传入已连通的 client。
func NewDockerRunner(cli dockerClient) *DockerRunner {
	return &DockerRunner{cli: cli}
}

// 容器资源与安全限额——硬编码、调用方不可覆写。
const (
	runnerUser       = "nobody"
	runnerMemoryByte = 512 * 1024 * 1024 // 512 MiB
	runnerPidsLimit  = int64(128)
	runnerNanoCPUs   = int64(1_000_000_000) // 1 核
)

// hardenedHostConfig 基于调用方参数构造一份只读、无特权、受限的 HostConfig。
// 这是 docker.sock 直挂场景下的 ACL 唯一落点——任何新增安全字段也只加这里。
func hardenedHostConfig() *container.HostConfig {
	pids := runnerPidsLimit
	return &container.HostConfig{
		AutoRemove:     true,
		ReadonlyRootfs: true,
		Privileged:     false,
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges:true"},
		Resources: container.Resources{
			Memory:    runnerMemoryByte,
			PidsLimit: &pids,
			NanoCPUs:  runnerNanoCPUs,
		},
	}
}

// RunTool 校验 image 与 cmd，在一次性容器内运行后返回 stdout/stderr。
// 容器 AutoRemove=true，退出后由 docker 自动销毁。
func (r *DockerRunner) RunTool(ctx context.Context, image string, cmd []string) (string, string, error) {
	if _, ok := allowedImages[image]; !ok {
		return "", "", fmt.Errorf("image %q 不在白名单内", image)
	}
	if len(cmd) == 0 {
		return "", "", fmt.Errorf("cmd 不能为空")
	}
	if _, ok := allowedTools[cmd[0]]; !ok {
		return "", "", fmt.Errorf("tool %q 不在白名单内", cmd[0])
	}
	cfg := &container.Config{
		Image: image,
		Cmd:   cmd,
		User:  runnerUser,
	}
	created, err := r.cli.ContainerCreate(ctx, cfg, hardenedHostConfig(), nil, nil, "")
	if err != nil {
		return "", "", fmt.Errorf("ContainerCreate 失败: %w", err)
	}
	if err := r.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return "", "", fmt.Errorf("ContainerStart 失败: %w", err)
	}

	// Wait 必须在 Logs 之前 select——按 docker SDK 惯例先订阅再启动同步读。
	okCh, errCh := r.cli.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	var exitCode int64
	select {
	case werr := <-errCh:
		if werr != nil && !errors.Is(werr, io.EOF) {
			return "", "", fmt.Errorf("ContainerWait 失败: %w", werr)
		}
	case resp := <-okCh:
		exitCode = resp.StatusCode
	case <-ctx.Done():
		return "", "", ctx.Err()
	}

	logs, err := r.cli.ContainerLogs(ctx, created.ID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		return "", "", fmt.Errorf("ContainerLogs 失败: %w", err)
	}
	defer logs.Close()

	var stdoutBuf, stderrBuf bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdoutBuf, &stderrBuf, logs); err != nil && !errors.Is(err, io.EOF) {
		return stdoutBuf.String(), stderrBuf.String(), fmt.Errorf("解析 docker 多路流失败: %w", err)
	}

	if exitCode != 0 {
		return stdoutBuf.String(), stderrBuf.String(), fmt.Errorf("容器以非零退出码结束: %d", exitCode)
	}
	return stdoutBuf.String(), stderrBuf.String(), nil
}
