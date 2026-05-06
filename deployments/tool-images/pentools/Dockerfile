# liusha pentools 镜像 v1：把渗透常用 CLI 工具集成到单一镜像，
# 让 docker_runner 通过统一 image 跑 sqlmap / nuclei / ffuf / ... 不同工具。
#
# 设计要点：
#   - 单镜像而非"一工具一镜像"：apt 共享底层、layer 复用，磁盘占用小
#   - WORKDIR 留给 docker_runner 用 -v workdir:/work mount
#   - 不内置 ENTRYPOINT：让 docker_runner 直接 `docker run image <tool> <args>`，
#     避免 ENTRYPOINT/CMD 与 RunSpec.Cmd 干扰
#
# 构建：
#   docker build -t liusha/pentools:v1 -f deployments/tool-images/pentools/Dockerfile .
#
# 当前包含：sqlmap（SQL 注入）+ python3 / jq / curl（脚本辅助）。
# 后续 Step 4+ 可加 nuclei / ffuf / nikto / whatweb 等。

FROM ubuntu:24.04

# 让 apt 不交互、不缓存索引（减少层大小）
ENV DEBIAN_FRONTEND=noninteractive

# 用清华镜像源（修复本地网络对 ports.ubuntu.com 502 问题）。
# ubuntu:24.04 用 deb822 格式 /etc/apt/sources.list.d/ubuntu.sources。
RUN sed -i \
        -e 's|http://archive.ubuntu.com|http://mirrors.tuna.tsinghua.edu.cn|g' \
        -e 's|http://ports.ubuntu.com|http://mirrors.tuna.tsinghua.edu.cn|g' \
        -e 's|http://security.ubuntu.com|http://mirrors.tuna.tsinghua.edu.cn|g' \
        /etc/apt/sources.list.d/ubuntu.sources \
 && apt-get update \
 && apt-get install -y --no-install-recommends \
    sqlmap \
    python3 \
    python3-pip \
    jq \
    curl \
    ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && sqlmap --version

WORKDIR /work
