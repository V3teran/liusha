"""browser-use 薄客户端：把子命令转发给每身份常驻的浏览器服务（service.py）。

由沙盒编进程序、启动时释放到本地目录，经同目录的 browser-use 包装脚本以 sandbox.browser_python
运行。

模型：
  IDENTITY = 身份（cookie jar 边界 = 独立 chromium）。缺省 "default"。
  AGENT_ID = 同身份下的并行执行单元（工具层注入 action id），各占一个 tab（页面/截图/并发隔离）。
  LIUSHA_AGENT_RUN_ID = 本条命令所属的 run（沙盒注入命令环境），浏览器抓到的流量据此归属。

连接：服务监听回环地址的随机端口，把 {port, key, pid} 写进端点目录（沙盒注入的
LIUSHA_BROWSER_DIR，只对本用户开放）下的 <身份>.json；客户端读端点文件、连接、每个请求带上
口令。连不上或口令被拒即视为服务不在，请沙盒经本机接口（LIUSHA_SANDBOX_LOCAL_URL）启动它——
服务由沙盒启动、归属任务，活过单条命令（命令超时被结束时登录态不丢），并发的启动请求由沙盒
去重。约定与沙盒的 browser.go / browsers.go 一致。

管理类子命令（install/doctor/setup/...）不需浏览器，直接透传 browser_use 自带的命令行。
kill 不经服务：服务已无响应时请沙盒强制结束它及其浏览器（自愈的最后一级）。
"""
import json
import os
import socket
import subprocess
import sys
import time
import urllib.parse
import urllib.request

IDENTITY = os.environ.get("IDENTITY", "default")
AGENT_ID = os.environ.get("AGENT_ID", "default")
AGENT_RUN_ID = os.environ.get("LIUSHA_AGENT_RUN_ID", "")
ASSIGNMENT_ID = os.environ.get("LIUSHA_ASSIGNMENT_ID", "")
TASK_ID = os.environ.get("LIUSHA_TASK_ID", "")
OUTPUT_DIR = os.environ.get("OUTPUT_DIR", "")
BROWSER_DIR = os.environ.get("LIUSHA_BROWSER_DIR", "")
# 本机接口随每个请求交给服务（服务经它回灌流量）：服务可能比启动它的沙盒活得久，沙盒重启后
# 地址与口令都会变。
LOCAL = {
    "url": os.environ.get("LIUSHA_SANDBOX_LOCAL_URL", ""),
    "key": os.environ.get("LIUSHA_SANDBOX_LOCAL_KEY", ""),
}

# 不需浏览器的管理类子命令 → 透传 browser_use 自带的命令行（install 等连 chrome 都不用）。
PASSTHROUGH = {"", "--help", "-h", "install", "init", "setup", "doctor",
               "python", "tunnel", "sessions", "cloud", "profile"}

CONNECT_TIMEOUT = 2.0
# 单个请求的上限须长于服务端最长的动作上限（open 150s、wait 按参数），这里只防服务失联。
REQUEST_TIMEOUT = 900.0
# 冷启动最多等 60s（首启含 chromium 起动）。
START_TIMEOUT = 60.0


def emit(stream, text: str):
    """按 UTF-8 字节原样写出：文本模式在 Windows 上会把 \\n 改成 \\r\\n、按控制台代码页编码
    （中文等字符直接报错），而服务回放的输出要逐字节交给沙盒。"""
    stream.buffer.write(text.encode("utf-8"))
    stream.flush()


class Stale(Exception):
    """端点指向的服务不在（连不上 / 口令被拒）。"""


def read_endpoint():
    try:
        with open(os.path.join(BROWSER_DIR, IDENTITY + ".json"), encoding="utf-8") as f:
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


def local(method: str, path: str, body=None):
    """调用沙盒的本机接口。"""
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(LOCAL["url"] + path, data=data, method=method, headers={
        "Authorization": "Bearer " + LOCAL["key"], "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=30):
        pass


def call(payload: dict) -> dict:
    """把请求交给服务；服务不在则请沙盒启动它，等它就绪后再交。"""
    ep = read_endpoint()
    if ep is not None:
        try:
            return request(ep, payload)
        except Stale:
            pass
    local("POST", "/browsers", {"assignment_id": ASSIGNMENT_ID, "task_id": TASK_ID, "identity": IDENTITY})
    deadline = time.monotonic() + START_TIMEOUT
    while time.monotonic() < deadline:
        ep = read_endpoint()
        if ep is not None:
            try:
                return request(ep, payload)
            except Stale:
                pass  # 残留端点：新服务就绪时会覆盖它
        time.sleep(0.1)
    emit(sys.stderr, "browser-use: 服务启动超时（日志见 $LIUSHA_STATE/browser-%s.log）\n" % IDENTITY)
    sys.exit(1)


def main() -> int:
    sub = sys.argv[1] if len(sys.argv) > 1 else ""
    args = sys.argv[2:]

    if sub in PASSTHROUGH:
        # 子进程而不是 exec：Windows 上的 exec 会让本进程先行退出，调用方拿不到退出码。
        return subprocess.call([sys.executable, "-m", "browser_use", sub, *args])

    if not (BROWSER_DIR and LOCAL["url"] and TASK_ID):
        emit(sys.stderr, "browser-use: 缺少沙盒注入的环境变量（应在沙盒命令中运行）\n")
        return 1

    if sub == "kill":
        local("DELETE", "/browsers/" + urllib.parse.quote(IDENTITY) + "?task_id=" + urllib.parse.quote(TASK_ID))
        emit(sys.stdout, "ok\n")
        return 0

    payload = {"sub": sub, "args": args, "agent_id": AGENT_ID,
               "agent_run_id": AGENT_RUN_ID, "output_dir": OUTPUT_DIR, "local": LOCAL}
    if sub in ("reset", "release-tab"):
        # 没有运行中的服务 → 无可重置/释放，直接成功返回（不白白冷启一个再杀）。
        ep = read_endpoint()
        try:
            resp = request(ep, payload) if ep is not None else None
        except Stale:
            resp = None
        if resp is None:
            if sub == "reset":
                emit(sys.stdout, "ok\n")
            return 0
    else:
        resp = call(payload)

    if resp.get("stdout"):
        emit(sys.stdout, resp["stdout"])
    if resp.get("stderr"):
        emit(sys.stderr, resp["stderr"])
    return resp.get("code", 0)


if __name__ == "__main__":
    sys.exit(main())
