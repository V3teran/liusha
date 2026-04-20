package runner_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/tool/runner"
)

// dockerFrame 按 docker 多路流协议拼一帧：
// [stream_type][0,0,0][BE uint32 length][payload...]
// stream_type: 1=stdout, 2=stderr。
func dockerFrame(streamType byte, payload []byte) []byte {
	hdr := make([]byte, 8)
	hdr[0] = streamType
	binary.BigEndian.PutUint32(hdr[4:], uint32(len(payload)))
	return append(hdr, payload...)
}

// fakeDocker 抓取 ContainerCreate 的调用参数，便于断言 HostConfig 是否加固。
// 其余方法返回默认值或早退错误——按测试需要逐个覆写。
type fakeDocker struct {
	createCfg  *container.Config
	createHost *container.HostConfig
	createErr  error

	startErr error

	waitStatus int64 // 0 表示正常退出
	waitErr    error

	logsOut []byte
	logsErr error
}

func (f *fakeDocker) ContainerCreate(
	_ context.Context,
	cfg *container.Config,
	host *container.HostConfig,
	_ *network.NetworkingConfig,
	_ *ocispec.Platform,
	_ string,
) (container.CreateResponse, error) {
	f.createCfg = cfg
	f.createHost = host
	if f.createErr != nil {
		return container.CreateResponse{}, f.createErr
	}
	return container.CreateResponse{ID: "fake-id"}, nil
}

func (f *fakeDocker) ContainerStart(_ context.Context, _ string, _ container.StartOptions) error {
	return f.startErr
}

func (f *fakeDocker) ContainerWait(
	_ context.Context,
	_ string,
	_ container.WaitCondition,
) (<-chan container.WaitResponse, <-chan error) {
	ok := make(chan container.WaitResponse, 1)
	errCh := make(chan error, 1)
	if f.waitErr != nil {
		errCh <- f.waitErr
	} else {
		ok <- container.WaitResponse{StatusCode: f.waitStatus}
	}
	return ok, errCh
}

func (f *fakeDocker) ContainerLogs(_ context.Context, _ string, _ container.LogsOptions) (io.ReadCloser, error) {
	if f.logsErr != nil {
		return nil, f.logsErr
	}
	return io.NopCloser(bytes.NewReader(f.logsOut)), nil
}

// 未在白名单内的 image 必须被拒绝——agent-worker 不允许用任意镜像跑命令。
func TestDockerRunner_RunTool_RejectsNonWhitelistedImage(t *testing.T) {
	r := runner.NewDockerRunner(nil) // client 用不上——参数校验在调用 docker 之前就该失败
	_, _, err := r.RunTool(context.Background(), "evil/image", []string{"nuclei", "-u", "https://x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "image")
}

// cmd[0] 不在 nuclei/ffuf/httpx 中必须被拒绝——防止调用方塞 sh/rm/curl 等。
func TestDockerRunner_RunTool_RejectsNonWhitelistedTool(t *testing.T) {
	r := runner.NewDockerRunner(nil)
	_, _, err := r.RunTool(context.Background(), "liusha/vuln-tools", []string{"sh", "-c", "rm -rf /"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tool")
}

// 空 cmd 必须被拒绝——白名单无从校验。
func TestDockerRunner_RunTool_RejectsEmptyCmd(t *testing.T) {
	r := runner.NewDockerRunner(nil)
	_, _, err := r.RunTool(context.Background(), "liusha/vuln-tools", nil)
	require.Error(t, err)
}

// 核心安全契约：ContainerCreate 必须用加固过的 HostConfig + Config 调用，
// 调用方无法绕开。create 后故意返回错误，不跑到 start/wait。
func TestDockerRunner_RunTool_EnforcesHardenedHostConfig(t *testing.T) {
	fake := &fakeDocker{createErr: errors.New("stop after create")}
	r := runner.NewDockerRunner(fake)

	_, _, _ = r.RunTool(context.Background(), "liusha/vuln-tools", []string{"nuclei", "-version"})

	require.NotNil(t, fake.createHost, "应调用 ContainerCreate")
	require.NotNil(t, fake.createCfg)

	assert.True(t, fake.createHost.AutoRemove, "AutoRemove 必须开——容器退出即销毁")
	assert.True(t, fake.createHost.ReadonlyRootfs, "ReadonlyRootfs 必须开")
	assert.False(t, fake.createHost.Privileged, "Privileged 必须关")
	assert.Contains(t, fake.createHost.CapDrop, "ALL", "必须 drop ALL capabilities")
	assert.Contains(t, fake.createHost.SecurityOpt, "no-new-privileges:true")

	assert.Greater(t, fake.createHost.Memory, int64(0), "必须设置内存上限")
	require.NotNil(t, fake.createHost.PidsLimit, "必须设置 PID 上限")
	assert.Greater(t, *fake.createHost.PidsLimit, int64(0))

	assert.Equal(t, "nobody", fake.createCfg.User, "必须以非 root 运行")
	assert.Equal(t, "liusha/vuln-tools", fake.createCfg.Image)
	assert.Equal(t, []string{"nuclei", "-version"}, []string(fake.createCfg.Cmd))

	// 网络隔离：NetworkDisabled 明确关闭容器网络或用默认 bridge 均可——
	// 此处只要求调用方不手贱开 host 网络。
	assert.NotEqual(t, container.NetworkMode("host"), fake.createHost.NetworkMode, "禁止 host 网络")
}

// 完整 happy path：Create→Start→Wait→Logs；stdout/stderr 按多路流正确拆分。
func TestDockerRunner_RunTool_HappyPath(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(dockerFrame(1, []byte("scan started\n")))
	buf.Write(dockerFrame(2, []byte("deprecated flag\n")))
	buf.Write(dockerFrame(1, []byte("found 1 finding\n")))

	fake := &fakeDocker{logsOut: buf.Bytes()} // waitStatus=0、无错

	r := runner.NewDockerRunner(fake)
	stdout, stderr, err := r.RunTool(context.Background(), "liusha/vuln-tools", []string{"nuclei", "-version"})

	require.NoError(t, err)
	assert.Equal(t, "scan started\nfound 1 finding\n", stdout)
	assert.Equal(t, "deprecated flag\n", stderr)
}

// 容器非零退出码：保留输出但返回错误，调用方需要知道工具失败。
func TestDockerRunner_RunTool_NonZeroExit_ReturnsError(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(dockerFrame(2, []byte("bad target\n")))

	fake := &fakeDocker{logsOut: buf.Bytes(), waitStatus: 2}

	r := runner.NewDockerRunner(fake)
	stdout, stderr, err := r.RunTool(context.Background(), "liusha/vuln-tools", []string{"nuclei"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "2")
	assert.Empty(t, stdout)
	assert.Equal(t, "bad target\n", stderr, "即便非零退出，stderr 也要保留便于排障")
}
