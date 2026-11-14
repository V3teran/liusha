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
	running map[browserKey]*browserService
}

// browserKey 标识一个浏览器服务：身份只在任务内有意义。
type browserKey struct{ taskID, identity string }

type browserService struct {
	key      browserKey
	group    *procGroup
	exited   chan struct{} // 进程退出
	stopping bool          // 停止流程已接手（受 browserServices.mu 保护）
	retired  chan struct{} // 停止流程完成：已退出、端点已清理、已撤销登记
}

func newBrowserServices() *browserServices {
	return &browserServices{running: map[browserKey]*browserService{}}
}

// ensure 确保任务某身份的服务在运行：已有且未退出则什么也不做；正在停止则等它停完；否则
// 启动一个。服务就绪后把端点写进任务的端点目录，客户端轮询它。服务日志写在任务的状态目录
// （browser-<身份>.log）：随任务工作区留存与清扫，不在沙盒运行目录里无限累积。
func (b *browserServices) ensure(s *Server, req browserRequest) error {
	if s.cfg.BrowserService == "" || s.cfg.BrowserDir == "" {
		return fmt.Errorf("沙盒未配置浏览器能力")
	}
	key := browserKey{req.TaskID, req.Identity}
	for {
		b.mu.Lock()
		svc, ok := b.running[key]
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
			delete(b.running, key) // 自行退出、回收协程尚未撤销登记
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
	endpoints := browser.TaskDir(s.cfg.BrowserDir, req.TaskID)
	if err := os.MkdirAll(endpoints, 0o700); err != nil {
		return fmt.Errorf("创建端点目录: %w", err)
	}
	logFile, err := os.OpenFile(filepath.Join(dirs.state, "browser-"+req.Identity+".log"),
		os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600) // #nosec G304 // 身份名已校验
	if err != nil {
		return fmt.Errorf("打开服务日志: %w", err)
	}
	cmd := exec.Command(s.cfg.BrowserPython, s.cfg.BrowserService, req.Identity, endpoints) // #nosec G204 // 解释器来自部署配置，参数已校验
	cmd.Dir = dirs.work
	cmd.Env = append(s.baseEnv(), dirs.env(dirs.out)...)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	group, err := s.procs.start(req.TaskID, cmd)
	if err != nil {
		_ = logFile.Close()
		return err
	}
	svc := &browserService{key: key, group: group, exited: make(chan struct{}), retired: make(chan struct{})}
	b.running[key] = svc
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
		s.procs.finish(req.TaskID, group)
		close(svc.exited)
		b.mu.Lock()
		if b.running[key] == svc && !svc.stopping {
			delete(b.running, key)
		}
		b.mu.Unlock()
	}()
	return nil
}

// testHookBrowserKilled 在强制结束的服务退出之后、清理之前调用（测试固定交错用；生产为空）。
var testHookBrowserKilled func()

// exitWait 是结束服务后等它退出的上限。
const exitWait = 10 * time.Second

// kill 强制结束任务某身份的服务及其浏览器，等它退出、删除端点、撤销登记。不在登记里的（上一
// 沙盒实例启动、仍在运行的）按端点里记录的进程号结束，平台能核验归属时才动手。
func (b *browserServices) kill(s *Server, taskID, identity string) {
	key := browserKey{taskID, identity}
	b.mu.Lock()
	svc, ok := b.running[key]
	stopping := ok && svc.stopping
	if ok {
		svc.stopping = true
	}
	b.mu.Unlock()
	switch {
	case stopping:
		<-svc.retired // 已有停止流程在处理
	case ok:
		_ = svc.group.kill()
		b.retire(s, svc)
	default:
		if pid, found := browser.EndpointPID(s.cfg.BrowserDir, taskID, identity); found {
			s.procs.killLeftover(taskID, pid)
		}
		// 结束期间可能已有新服务登记（它会覆盖端点）：只在仍无人登记时删除。
		b.mu.Lock()
		if _, ok := b.running[key]; !ok {
			browser.RemoveEndpoint(s.cfg.BrowserDir, taskID, identity)
		}
		b.mu.Unlock()
	}
}

// stopTask 接手任务的全部服务（标记停止中），返回它们。任务释放在结束进程之前调用：进程一旦
// 被结束，回收协程就会撤销未被接手的登记，停止流程便看不到它、不会清理它的端点。
func (b *browserServices) stopTask(taskID string) []*browserService {
	b.mu.Lock()
	defer b.mu.Unlock()
	var gone []*browserService
	for key, svc := range b.running {
		if key.taskID == taskID && !svc.stopping {
			svc.stopping = true
			gone = append(gone, svc)
		}
	}
	return gone
}

// finishTask 在任务的进程已全部结束后完成释放：收尾 stopTask 接手的服务（等退出、删除端点、
// 撤销登记），再清掉其余不属于运行中服务的端点（自行退出或崩溃时没删的、上一沙盒实例留下的），
// 端点目录空了即删除。
func (b *browserServices) finishTask(s *Server, taskID string, gone []*browserService) {
	for _, svc := range gone {
		b.retire(s, svc)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	browser.SweepTask(s.cfg.BrowserDir, taskID, func(identity string) bool {
		_, ok := b.running[browserKey{taskID, identity}]
		return ok
	})
}

// retire 完成停止流程：等服务退出（至多 exitWait）、删除端点、撤销登记，最后放行等待中的
// 同身份启动请求。端点在放行之前删除，新服务写的端点不会被误删。
func (b *browserServices) retire(s *Server, svc *browserService) {
	select {
	case <-svc.exited:
	case <-time.After(exitWait):
		releaseLog.Warn().Str("task_id", svc.key.taskID).Str("identity", svc.key.identity).Msg("浏览器服务结束后未在时限内退出")
	}
	if testHookBrowserKilled != nil {
		testHookBrowserKilled()
	}
	browser.RemoveEndpoint(s.cfg.BrowserDir, svc.key.taskID, svc.key.identity)
	b.mu.Lock()
	if b.running[svc.key] == svc {
		delete(b.running, svc.key)
	}
	b.mu.Unlock()
	close(svc.retired)
}
