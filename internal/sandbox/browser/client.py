"""browser-use 薄客户端：把子命令转发给每身份常驻的浏览器服务（service.py）。

由沙盒编进程序、启动时释放到本地目录，经同目录的 browser-use 包装脚本以 sandbox.browser_python
运行。本客户端用 sys.executable 拉起 service.py，二者同一解释器 → import browser_use 命中同一环境
（沙盒镜像里是独立 venv，与随发行版滚动的系统 Python 隔离）。

模型：
  IDENTITY = 身份（cookie jar 边界 = 独立 chromium）。缺省 "default"。
  AGENT_ID = 同身份下的并行执行单元（工具层注入 action id），各占一个 tab（页面/截图/并发隔离）。
  LIUSHA_AGENT_RUN_ID = 本条命令所属的 run（沙盒注入命令环境），浏览器抓到的流量据此归属。

连接：服务监听回环地址的随机端口，把 {port, key} 写进端点目录（沙盒注入的 LIUSHA_BROWSER_DIR，
只对本用户开放）下的 <身份>.json；客户端读端点文件、连接、每个请求带上口令。连不上或口令被拒
即视为残留端点，抢冷启动锁后拉起新服务。约定与沙盒的 browser.go 一致。

管理类子命令（install/doctor/setup/...）不需浏览器，直接透传 browser_use 自带的命令行。
"""
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
BROWSER_DIR = os.environ.get("LIUSHA_BROWSER_DIR", "")
# 回灌中继随每个请求下发：服务可能比拉起它的沙盒活得久（沙盒重启后中继地址与口令都会变）。
INGEST = {
    "url": os.environ.get("LIUSHA_INGEST_RELAY_URL", ""),
    "key": os.environ.get("LIUSHA_INGEST_RELAY_KEY", ""),
}
SVC = os.path.join(os.path.dirname(os.path.abspath(__file__)), "service.py")

# 不需浏览器的管理类子命令 → 透传 browser_use 自带的命令行（install 等连 chrome 都不用）。
PASSTHROUGH = {"", "--help", "-h", "install", "init", "setup", "doctor",
               "python", "tunnel", "sessions", "cloud", "profile"}

CONNECT_TIMEOUT = 2.0
# 单个请求的上限须长于服务端最长的动作上限（open 150s、wait 按参数），这里只防服务失联。
REQUEST_TIMEOUT = 900.0


class Stale(Exception):
    """端点指向的服务不在（连不上 / 口令被拒）。"""


def endpoint_path() -> str:
    return os.path.join(BROWSER_DIR, IDENTITY + ".json")


def read_endpoint():
    try:
        with open(endpoint_path(), encoding="utf-8") as f:
            ep = json.load(f)
        return int(ep["port"]), str(ep["key"])
    except (OSError, ValueError, KeyError, TypeError):
        return None


def request(ep, payload: dict) -> dict:
    """向服务发一个请求并读完回包。连不上、口令被拒抛 Stale；其余错误原样抛出。"""
    port, key = ep
    try:
        s = socket.create_connection(("127.0.0.1", port), timeout=CONNECT_TIMEOUT)
    except OSError as e:
        raise Stale() from e
    with s:
        s.settimeout(REQUEST_TIMEOUT)
        s.sendall(json.dumps({**payload, "key": key}).encode() + b"\n")
        buf = b""
        while True:
            chunk = s.recv(65536)
            if not chunk:
                break
            buf += chunk
    if not buf:
        raise Stale()  # 端口已被别的进程占用，或服务在回包前退出
    try:
        resp = json.loads(buf.decode())
    except ValueError as e:
        raise Stale() from e
    if resp.get("unauthorized"):
        raise Stale()
    return resp


class StartLock:
    """冷启动锁：同一身份同时只有一个客户端拉起服务。POSIX 用 flock，Windows 用 msvcrt。"""

    def __init__(self, path: str):
        self.f = open(path, "a+b")

    def __enter__(self):
        if os.name == "nt":
            import msvcrt
            self.f.seek(0)
            while True:
                try:
                    msvcrt.locking(self.f.fileno(), msvcrt.LK_LOCK, 1)
                    return self
                except OSError:
                    continue  # LK_LOCK 约 10 秒仍拿不到即报错，继续等
        import fcntl
        fcntl.flock(self.f, fcntl.LOCK_EX)
        return self

    def __exit__(self, *exc):
        if os.name == "nt":
            import msvcrt
            self.f.seek(0)
            msvcrt.locking(self.f.fileno(), msvcrt.LK_UNLCK, 1)
        self.f.close()


def spawn_service():
    """拉起服务：脱离本条命令的进程集合（命令超时被结束时服务不受牵连），仍归属任务——
    POSIX 以新会话启动；Windows 脱离命令的作业、留在任务的作业（沙盒为命令设的作业允许脱离，
    为任务设的不允许）。"""
    log = open(os.path.join(BROWSER_DIR, IDENTITY + ".log"), "ab")
    args = [sys.executable, SVC, IDENTITY, BROWSER_DIR]
    with log:
        if os.name != "nt":
            subprocess.Popen(args, stdout=log, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL,
                             start_new_session=True)
            return
        flags = subprocess.CREATE_NEW_PROCESS_GROUP | subprocess.CREATE_NO_WINDOW
        try:
            subprocess.Popen(args, stdout=log, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL,
                             creationflags=flags | subprocess.CREATE_BREAKAWAY_FROM_JOB)
        except OSError:
            # 不在沙盒管理的作业里（手工运行）且所在作业不允许脱离：留在当前作业即可。
            subprocess.Popen(args, stdout=log, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL,
                             creationflags=flags)


def call(payload: dict) -> dict:
    """把请求交给服务；服务不在则抢锁冷启后再交。"""
    ep = read_endpoint()
    if ep is not None:
        try:
            return request(ep, payload)
        except Stale:
            pass
    with StartLock(os.path.join(BROWSER_DIR, IDENTITY + ".lock")):
        ep = read_endpoint()  # 等锁期间可能已有别的客户端拉起了服务
        if ep is not None:
            try:
                return request(ep, payload)
            except Stale:
                pass
        try:
            os.remove(endpoint_path())
        except OSError:
            pass
        spawn_service()
        deadline = time.monotonic() + 60  # 冷启最多等 60s（首启含 chromium 起动）
        while time.monotonic() < deadline:
            ep = read_endpoint()
            if ep is not None:
                try:
                    return request(ep, payload)
                except Stale:
                    pass
            time.sleep(0.1)
    sys.stderr.write("browser-use: 服务启动超时\n")
    sys.exit(1)


def main() -> int:
    sub = sys.argv[1] if len(sys.argv) > 1 else ""
    args = sys.argv[2:]

    if sub in PASSTHROUGH:
        # 子进程而不是 exec：Windows 上的 exec 会让本进程先行退出，调用方拿不到退出码。
        return subprocess.call([sys.executable, "-m", "browser_use", sub, *args])

    if not BROWSER_DIR:
        sys.stderr.write("browser-use: 未设置 LIUSHA_BROWSER_DIR（应在沙盒命令中运行）\n")
        return 1

    payload = {"sub": sub, "args": args, "agent_id": AGENT_ID,
               "agent_run_id": AGENT_RUN_ID, "output_dir": OUTPUT_DIR, "ingest": INGEST}
    if sub in ("reset", "release-tab"):
        # 没有运行中的服务 → 无可重置/释放，直接成功返回（不白白冷启一个再杀）。
        ep = read_endpoint()
        try:
            resp = request(ep, payload) if ep is not None else None
        except Stale:
            resp = None
        if resp is None:
            if sub == "reset":
                sys.stdout.write("ok\n")
            return 0
    else:
        resp = call(payload)

    if resp.get("stdout"):
        sys.stdout.write(resp["stdout"])
    if resp.get("stderr"):
        sys.stderr.write(resp["stderr"])
    return resp.get("code", 0)


if __name__ == "__main__":
    sys.exit(main())
