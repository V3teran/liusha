//go:build windows

package server

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// procGroup 是一个作业对象，嵌套在所属任务的任务作业里。结束与关闭句柄互斥：句柄关闭后
// 其值可能被系统复用给别的作业，关闭之后再用它结束进程可能误伤别的任务。
type procGroup struct {
	mu     sync.Mutex
	job    windows.Handle
	closed bool
}

// errGroupClosed 表示进程组已收尾（组长退出、句柄已关闭）：其余进程仍在任务作业里，随任务释放结束。
var errGroupClosed = errors.New("进程组已收尾")

// kill 结束作业里的全部进程。
func (g *procGroup) kill() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return errGroupClosed
	}
	return windows.TerminateJobObject(g.job, 1)
}

// close 关闭作业句柄（可重复调用）。
func (g *procGroup) close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.closed {
		_ = windows.CloseHandle(g.job)
		g.closed = true
	}
}

// cancelUnpublished 结束组对象发布之前的进程：此时进程仍处于挂起状态（入作业后才恢复），
// 没有派生任何子进程，结束它本身即可。
func cancelUnpublished(cmd *exec.Cmd) error { return cmd.Process.Kill() }

// taskProcesses 用作业对象管理任务的进程。
//
// 每个任务一个任务作业，任务的每个进程组是嵌套在其中的子作业。两层都不允许脱离：Windows 上
// 的 sh 运行时在所属作业允许脱离时会让每个子进程自动脱离，允许脱离就等于管不住命令派生的
// 进程。任务作业设了「句柄关闭即结束」：沙盒进程退出（含崩溃）时任务进程随之结束，不留无人
// 认领的进程。
//
// 进程以挂起状态创建，进入两层作业后才恢复运行：恢复之前它派生不了任何子进程，不存在逃出
// 作业的窗口。
type taskProcesses struct {
	mu   sync.Mutex
	jobs map[string]windows.Handle // 任务 → 任务作业
}

func newTaskProcesses() *taskProcesses {
	return &taskProcesses{jobs: map[string]windows.Handle{}}
}

// start 挂起创建 cmd → 进入任务作业与它自己的子作业 → 恢复。
func (p *taskProcesses) start(taskID string, cmd *exec.Cmd) (*procGroup, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// 新进程组：控制台的 Ctrl+C 不波及它；无窗口：服务方式运行时不弹控制台。
		CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	job, err := p.adopt(taskID, uint32(cmd.Process.Pid)) // #nosec G115 // Windows 进程号本就是 DWORD
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		p.releaseIfIdle(taskID) // 可能刚为它新建了任务作业
		return nil, fmt.Errorf("进程进入任务作业: %w", err)
	}
	return &procGroup{job: job}, nil
}

// adopt 把挂起的进程放进任务作业（没有则新建）与新建的子作业，然后恢复它。返回子作业。
func (p *taskProcesses) adopt(taskID string, pid uint32) (windows.Handle, error) {
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return 0, fmt.Errorf("打开进程: %w", err)
	}
	defer func() { _ = windows.CloseHandle(proc) }()

	// 先入父作业再入子作业，嵌套关系才成立；与 finish 关闭任务作业互斥。
	p.mu.Lock()
	taskJob, ok := p.jobs[taskID]
	if !ok {
		taskJob, err = newJob(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE)
		if err != nil {
			p.mu.Unlock()
			return 0, err
		}
		p.jobs[taskID] = taskJob
	}
	err = windows.AssignProcessToJobObject(taskJob, proc)
	p.mu.Unlock()
	if err != nil {
		return 0, fmt.Errorf("进入任务作业: %w", err)
	}

	job, err := newJob(0)
	if err != nil {
		return 0, err
	}
	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("进入进程组作业: %w", err)
	}
	if err := resume(pid); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

// finish 在组长退出后调用：关闭子作业的句柄（留在后台的进程仍在任务作业里，保留到任务释放）；
// 任务作业已无进程时一并关闭（没有遗留进程的任务不占句柄）。
func (p *taskProcesses) finish(taskID string, g *procGroup) {
	g.close()
	p.releaseIfIdle(taskID)
}

// releaseIfIdle 在任务作业已无进程时关闭并忘掉它。
func (p *taskProcesses) releaseIfIdle(taskID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	job, ok := p.jobs[taskID]
	if !ok || activeProcesses(job) > 0 {
		return
	}
	_ = windows.CloseHandle(job)
	delete(p.jobs, taskID)
}

// killTask 结束任务作业里的全部进程（含嵌套的子作业），返回结束的进程数。
func (p *taskProcesses) killTask(taskID string) int {
	p.mu.Lock()
	job, ok := p.jobs[taskID]
	delete(p.jobs, taskID)
	p.mu.Unlock()
	if !ok {
		return 0
	}
	defer func() { _ = windows.CloseHandle(job) }()
	n := activeProcesses(job)
	if err := windows.TerminateJobObject(job, 1); err != nil {
		releaseLog.Warn().Err(err).Str("task_id", taskID).Msg("结束任务作业失败")
		return 0
	}
	return n
}

// killLeftover：Windows 上不存在上一沙盒实例遗留的进程（任务作业随沙盒退出而结束）。
func (*taskProcesses) killLeftover(string, int) bool { return false }

// newJob 新建作业；limitFlags 非 0 时设置这些限制。
func newJob(limitFlags uint32) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("新建作业: %w", err)
	}
	if limitFlags == 0 {
		return job, nil
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: limitFlags},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil { // #nosec G103 // Win32 API 以指针 + 长度接收结构体
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("设置作业限制: %w", err)
	}
	return job, nil
}

// jobAccounting 对应 JOBOBJECT_BASIC_ACCOUNTING_INFORMATION。
type jobAccounting struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

// activeProcesses 返回作业（含嵌套子作业）里正在运行的进程数；查询失败按 0 计。
func activeProcesses(job windows.Handle) int {
	var info jobAccounting
	if err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil { // #nosec G103 // 同上
		return 0
	}
	return int(info.ActiveProcesses)
}

// resume 恢复挂起创建的进程（恢复其全部线程；新建的进程只有主线程）。
func resume(pid uint32) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("枚举线程: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	resumed := 0
	for err = windows.Thread32First(snap, &entry); err == nil; err = windows.Thread32Next(snap, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return fmt.Errorf("打开线程: %w", err)
		}
		_, err = windows.ResumeThread(thread)
		_ = windows.CloseHandle(thread)
		if err != nil {
			return fmt.Errorf("恢复线程: %w", err)
		}
		resumed++
	}
	if resumed == 0 {
		return fmt.Errorf("进程 %d 没有可恢复的线程", pid)
	}
	return nil
}
