# vuln-tools：一次性沙箱镜像，基于 Kali rolling。
#
# 运行模式：agent-worker 通过 docker API 新建 --rm 容器，
# 指定 cmd = [<tool>, <args...>]，跑完立即销毁。
# 不设 CMD —— 由调用方显式提供；ENTRYPOINT 仅接 tini 做 SIGTERM 传递。
#
# 工具清单需与 internal/tool/runner/docker.go 的 allowedTools 白名单对齐：
# 新增工具时，这里装 + 白名单加 + tools-usage 写说明，缺一不可。
FROM kalilinux/kali-rolling:latest

ENV DEBIAN_FRONTEND=noninteractive

# 把 apt source 固定到清华 TUNA 镜像。kali 官方 CDN (kali.download / http.kali.org)
# 当前亚太节点持续 502；TUNA 是官方列出的 China mirror，稳定且速度快。
# 走 HTTP + [trusted=yes]：基础镜像此时还没装 ca-certificates，HTTPS 握手会失败。
RUN echo "deb [trusted=yes] http://mirrors.tuna.tsinghua.edu.cn/kali kali-rolling main contrib non-free non-free-firmware" \
      > /etc/apt/sources.list

# - sqlmap / nuclei / ffuf：Kali 官方仓库直装，版本由 kali-rolling 兜底。
# - httpx-toolkit：projectdiscovery 的 httpx；Debian 的 python3-httpx 是同名另一物。
# - tini：init 进程，短命容器下接住 SIGTERM 避免僵尸。
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      sqlmap \
      nuclei \
      ffuf \
      httpx-toolkit \
      tini \
      ca-certificates \
 && apt-get clean \
 && rm -rf /var/lib/apt/lists/*

# httpx-toolkit 的二进制叫 httpx-toolkit；agent-worker 白名单里是 "httpx"，
# 这里软链一刀对齐，避免调用方还得感知包名差异。
RUN ln -sf /usr/bin/httpx-toolkit /usr/local/bin/httpx

# 调用方会在 ContainerCreate 里强制 User=nobody + CapDrop=ALL + ReadonlyRootfs，
# Dockerfile 内提前声明做冗余加固，防止有人以默认方式起容器时跑成 root。
USER nobody

ENTRYPOINT ["/usr/bin/tini", "--"]
