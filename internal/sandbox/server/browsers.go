package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/sandbox/browser"
)

// browserServices 是沙盒启动的浏览器服务（每个浏览器身份一个常驻进程）。
//
// 服务由沙盒启动，而不是由驱动它的命令：服务要活过单条命令（命令超时被结束时登录态不能丢），
// 又要随任务释放结束。由沙盒启动就让它自成一个归属任务的进程组，与命令的进程组并列——各平台
// 同一做法，不依赖命令派生的进程「脱离」命令（Windows 上的 sh 运行时会让所有子进程脱离，无从
// 区分）。
//
// 同一身份的启动与停止串行：停止中的服务留在登记里（stopping），同身份的启动请求等它退出、
// 端点清理完（retired）再启动新的。否则停止流程会删掉新服务刚写的端点，客户端从此连不上，
// 又因服务在运行而不会再启动一个。
type browserServices struct {
	mu      sync.Mutex
	running map[string]*browserService // 浏览器身份 → 服务
}

type browserService struct {
	identity, taskID string
	group            *procGroup
	exited           chan struct{} // 进程退出
	stopping         bool          // 停止流程已接手（受 browserServices.mu 保护）
	retired          chan struct{} // 停止流程完成：已退出、端点已清理、已撤销登记
}

func newBrowserServices() *browserServices {
	return &browserServices{running: map[string]*browserService{}}
}

// ensure 确保 req.Identity 的服务在运行：已有且未退出则什么也不做；正在停止则等它停完；否则
// 启动一个。服务就绪后自己写端点文件，客户端轮询它。服务日志写在任务的状态目录
// （browser-<身份>.log）：随任务工作区留存与清扫，不在沙盒运行目录里无限累积。
func (b *browserServices) ensure(s *Server, req browserRequest) error {
	if s.cfg.BrowserService == "" || s.cfg.BrowserDir == "" {
		return fmt.Errorf("沙盒未配置浏览器能力")
	}
	for {
		b.mu.Lock()
		svc, ok := b.running[req.Identity]
		if !ok {
			break // 持锁启动
		}
		if svc.stopping {
			b.mu.Unlock()
			<-svc.retired
			continue
		}
		select {
		case <-svc.exited:
			delete(b.running, req.Identity) // 自行退出、回收协程尚未撤销登记
			b.mu.Unlock()
			continue
		default:
			b.mu.Unlock()
			return nil
		}
	}
	defer b.mu.Unlock()

	dirs, err := s.ensureTaskDirs(req.AssignmentID, req.TaskID)
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(dirs.state, "browser-"+req.Identity+".log"),
		os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600) // #nosec G304 // 身份已校验为路径安全
	if err != nil {
		return fmt.Errorf("打开服务日志: %w", err)
	}
	cmd := exec.Command(s.cfg.BrowserPython, s.cfg.BrowserService, req.Identity, s.cfg.BrowserDir) // #nosec G204 // 解释器来自部署配置，参数已校验
	cmd.Dir = dirs.work
	cmd.Env = append(s.baseEnv(), dirs.env(dirs.out)...)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	group, err := s.procs.start(req.TaskID, cmd)
	if err != nil {
		_ = logFile.Close()
		return err
	}
	svc := &browserService{identity: req.Identity, taskID: req.TaskID, group: group,
		exited: make(chan struct{}), retired: make(chan struct{})}
	b.running[req.Identity] = svc
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
		s.procs.finish(req.TaskID, group)
		close(svc.exited)
		b.mu.Lock()
		if b.running[req.Identity] == svc && !svc.stopping {
			delete(b.running, req.Identity)
		}
		b.mu.Unlock()
	}()
	return nil
}

// testHookBrowserKilled 在强制结束的服务退出之后、清理之前调用（测试固定交错用；生产为空）。
var testHookBrowserKilled func()

// exitWait 是结束服务后等它退出的上限。
const exitWait = 10 * time.Second

// kill 强制结束身份的服务及其浏览器，等它退出、删除端点、撤销登记。不在登记里的（上一沙盒
// 实例启动、仍在运行的）按端点里记录的进程号结束，平台能核验归属时才动手。
func (b *browserServices) kill(s *Server, taskID, identity string) {
	b.mu.Lock()
	svc, ok := b.running[identity]
	owned := ok && svc.taskID == taskID && !svc.stopping
	if owned {
		svc.stopping = true
	}
	b.mu.Unlock()
	switch {
	case owned:
		_ = svc.group.kill()
		b.retire(s, svc)
	case ok && svc.stopping:
		<-svc.retired // 已有停止流程在处理
	default:
		if pid, found := browser.EndpointPID(s.cfg.BrowserDir, identity); found {
			s.procs.killLeftover(taskID, pid)
		}
		browser.RemoveEndpoint(s.cfg.BrowserDir, identity)
	}
}

// forgetTask 在任务释放（任务的进程已全部结束）后收尾该任务的服务：等它们退出、删除端点、
// 撤销登记。
func (b *browserServices) forgetTask(s *Server, taskID string) {
	b.mu.Lock()
	var gone []*browserService
	for _, svc := range b.running {
		if svc.taskID == taskID && !svc.stopping {
			svc.stopping = true
			gone = append(gone, svc)
		}
	}
	b.mu.Unlock()
	for _, svc := range gone {
		b.retire(s, svc)
	}
}

// retire 完成停止流程：等服务退出（至多 exitWait）、删除端点、撤销登记，最后放行等待中的
// 同身份启动请求。端点在放行之前删除，新服务写的端点不会被误删。
func (b *browserServices) retire(s *Server, svc *browserService) {
	select {
	case <-svc.exited:
	case <-time.After(exitWait):
		releaseLog.Warn().Str("task_id", svc.taskID).Msg("浏览器服务结束后未在时限内退出")
	}
	if testHookBrowserKilled != nil {
		testHookBrowserKilled()
	}
	browser.RemoveEndpoint(s.cfg.BrowserDir, svc.identity)
	b.mu.Lock()
	if b.running[svc.identity] == svc {
		delete(b.running, svc.identity)
	}
	b.mu.Unlock()
	close(svc.retired)
}
