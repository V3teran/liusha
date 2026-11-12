"""browser-use 薄客户端：把子命令经 unix socket 转发给每身份常驻的浏览器服务（service.py）。

由沙盒编进程序、启动时释放到本地目录，经同目录的 browser-use 包装脚本以 sandbox.browser_python
运行。本客户端用 sys.executable 拉起 service.py，二者同一解释器 → import browser_use 命中同一环境
（沙盒镜像里是独立 venv，与随发行版滚动的系统 Python 隔离）。

模型不变：
  IDENTITY = 身份（cookie jar 边界 = 独立 chromium）。缺省 "default"。
  AGENT_ID = 同身份下的并行执行单元（工具层注入 action id），各占一个 tab（页面/截图/并发隔离）。
  LIUSHA_AGENT_RUN_ID = 本条命令所属的 run（沙盒注入命令环境），浏览器抓到的流量据此归属。

热路径不再有全局 active tab，也不再需要 flock 串行化 —— 服务用 actor.Page 按 target_id 寻址，
同身份多 agent 并发各操作自己的 tab。本客户端只做：选身份 socket → 确保服务起（flock 守冷启）→
转发 {sub,args,agent_id,agent_run_id,output_dir} → 回放 stdout/stderr + exit code。

管理类子命令（install/doctor/setup/...）不需浏览器，直接透传 browser_use 自带的命令行。
"""
import fcntl
import json
import os
import socket
import subprocess
import sys
import time

IDENTITY = os.environ.get("IDENTITY", "default")
AGENT_ID = os.environ.get("AGENT_ID", "default")
AGENT_RUN_ID = os.environ.get("LIUSHA_AGENT_RUN_ID", "")
OUTPUT_DIR = os.environ.get("OUTPUT_DIR", "")
# 套接字路径约定（/tmp/browser-svc-<身份>.sock）由沙盒按任务释放时共用，改动须同步 browser.go。
SOCK = f"/tmp/browser-svc-{IDENTITY}.sock"
START_LOCK = f"/tmp/browser-svc-{IDENTITY}.start.lock"
LOG = f"/tmp/browser-svc-{IDENTITY}.log"
SVC = os.path.join(os.path.dirname(os.path.abspath(__file__)), "service.py")

# 不需浏览器的管理类子命令 → 透传 browser_use 自带的命令行（install 等连 chrome 都不用）。
PASSTHROUGH = {"", "--help", "-h", "install", "init", "setup", "doctor",
               "python", "tunnel", "sessions", "cloud", "profile"}


def connect():
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    try:
        s.connect(SOCK)
        return s
    except OSError:
        s.close()
        return None


def ensure_service():
    """连服务；连不上则抢锁冷启 service.py 再连。"""
    s = connect()
    if s is not None:
        return s
    with open(START_LOCK, "w") as lf:
        fcntl.flock(lf, fcntl.LOCK_EX)
        s = connect()
        if s is not None:
            return s
        # 残留死 socket 清掉再拉新服务。
        try:
            if os.path.exists(SOCK):
                os.remove(SOCK)
        except OSError:
            pass
        with open(LOG, "ab") as logf:
            subprocess.Popen(
                [sys.executable, SVC, IDENTITY, SOCK],
                stdout=logf, stderr=subprocess.STDOUT, start_new_session=True,
            )
        for _ in range(600):  # 冷启最多等 60s（首启含 chromium 起动）
            s = connect()
            if s is not None:
                return s
            time.sleep(0.1)
    sys.stderr.write("browser-use: 服务启动超时\n")
    sys.exit(1)


def main() -> int:
    sub = sys.argv[1] if len(sys.argv) > 1 else ""
    args = sys.argv[2:]

    if sub in PASSTHROUGH:
        os.execv(sys.executable, [sys.executable, "-m", "browser_use", sub, *args])

    if sub in ("reset", "release-tab"):
        # 没有运行中的服务 → 无可重置/释放，直接成功返回（不白白冷启一个再杀）。
        s = connect()
        if s is None:
            if sub == "reset":
                sys.stdout.write("ok\n")
            return 0
    else:
        s = ensure_service()

    req = json.dumps({"sub": sub, "args": args, "agent_id": AGENT_ID,
                      "agent_run_id": AGENT_RUN_ID, "output_dir": OUTPUT_DIR})
    s.sendall(req.encode() + b"\n")
    buf = b""
    while True:
        chunk = s.recv(65536)
        if not chunk:
            break
        buf += chunk
    s.close()

    if not buf:
        sys.stderr.write("browser-use: 服务无响应\n")
        return 1
    resp = json.loads(buf.decode())
    if resp.get("stdout"):
        sys.stdout.write(resp["stdout"])
    if resp.get("stderr"):
        sys.stderr.write(resp["stderr"])
    return resp.get("code", 0)


if __name__ == "__main__":
    sys.exit(main())
