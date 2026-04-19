package mcpserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// DockerRunner 在指定容器内执行命令并收集输出。
type DockerRunner struct {
	cli           *client.Client
	containerName string
}

// NewDockerRunner 创建 DockerRunner；containerName 不能为空。
func NewDockerRunner(containerName string) (*DockerRunner, error) {
	if containerName == "" {
		return nil, errors.New("containerName 不能为空")
	}
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("初始化 Docker client 失败: %w", err)
	}
	return &DockerRunner{cli: cli, containerName: containerName}, nil
}

// DecodeDockerStream 解析 docker exec attach 返回的多路复用流，
// 每 8 字节帧头 + 帧体；stream_type 1=stdout、2=stderr。
// 返回分离的 stdout/stderr 字符串。
func DecodeDockerStream(r io.Reader) (string, string, error) {
	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, r); err != nil && !errors.Is(err, io.EOF) {
		return "", "", fmt.Errorf("解析 docker exec 多路流失败: %w", err)
	}
	return stdout.String(), stderr.String(), nil
}

// Run 在容器内执行 cmd，返回 stdout+stderr 合并输出（stderr 前缀标注）。
func (r *DockerRunner) Run(ctx context.Context, cmd []string) (string, error) {
	resp, err := r.cli.ContainerExecCreate(ctx, r.containerName, container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return "", fmt.Errorf("exec create 失败: %w", err)
	}
	attach, err := r.cli.ContainerExecAttach(ctx, resp.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", fmt.Errorf("exec attach 失败: %w", err)
	}
	defer attach.Close()

	stdout, stderr, err := DecodeDockerStream(attach.Reader)
	if err != nil {
		return "", err
	}
	if stderr != "" {
		return stdout + "\n[stderr]\n" + stderr, nil
	}
	return stdout, nil
}
