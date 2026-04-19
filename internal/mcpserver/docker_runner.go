package mcpserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
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

// Run 在容器内执行 cmd，返回 stdout+stderr 合并输出。
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

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, attach.Reader); err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("读取 exec 输出失败: %w", err)
	}
	return buf.String(), nil
}
