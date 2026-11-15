//go:build windows

package server

import (
	"errors"
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

// backgroundSleepPID 让命令在后台留一个长命进程并输出它的 Windows 进程号（sh 的 $! 是 shell
// 运行时自己的进程号，Windows 进程号在 /proc/<pid>/winpid）。
const backgroundSleepPID = `sleep 300 >/dev/null 2>&1 & p=$!; sleep 0.2; cat /proc/$p/winpid`

// processAlive 报告进程是否仍在运行。
func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

func killProcess(pid int) {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return
	}
	_ = windows.TerminateProcess(h, 1)
	_ = windows.CloseHandle(h)
}

// 进程组收尾（组长退出、句柄关闭）之后再结束它：不得再用已关闭的句柄，返回已收尾。
// （任务作业是否在此刻回收不作断言：作业的活动进程计数比 Wait 返回略晚更新，未及回收的
// 任务作业随任务释放回收。）
func TestProcGroup_KillAfterFinishDoesNotUseClosedHandle(t *testing.T) {
	p := newTaskProcesses()
	cmd := exec.Command(testShell, "-c", "exit 0")
	g, err := p.start("task-x", cmd)
	if err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	p.finish("task-x", g)
	if err := g.kill(); !errors.Is(err, errGroupClosed) {
		t.Fatalf("收尾后结束应返回 errGroupClosed, got %v", err)
	}
	p.killTask("task-x")
}

// Windows 上只隔离 HOME，不改写 USERPROFILE：系统组件与原生程序（如浏览器）靠它定位真实的
// 用户目录，改写会让它们工作异常（实测 Chrome 在改写后的 USERPROFILE 下无法完成启动）。
func TestExec_KeepsUserProfile(t *testing.T) {
	h := newHarness(t)
	res := h.exec(t, `printf '%s' "$USERPROFILE"`)
	if want := os.Getenv("USERPROFILE"); res.Stdout != want {
		t.Fatalf("USERPROFILE = %q，期望保持 %q", res.Stdout, want)
	}
}
