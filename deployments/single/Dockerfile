#（无 syntax= 指令：仅用基础语法，内置 BuildKit 前端足够）
# liusha 单镜像：一个容器运行全部——PostgreSQL 18（pgvector）+ Redis + liusha serve（含沙盒）。
# 以沙盒镜像为底（工具都在其中），先构建沙盒镜像：
#
#   docker build -t liusha-sandbox:local -f deployments/sandbox/Dockerfile .
#   docker build -t liusha-single:local -f deployments/single/Dockerfile .
#
# 进程托管见 entrypoint.sh；数据（库、队列、工作区、日志、证书、密钥）都在 /data。
ARG SANDBOX_IMAGE=liusha-sandbox:local
FROM ${SANDBOX_IMAGE}

# PostgreSQL 主版本显式钉住：数据目录跨主版本不兼容，发行版升级主版本时宁可构建失败也不
# 静默换版本。
RUN apt-get update \
 && apt-get install -y --no-install-recommends postgresql-18 postgresql-18-pgvector redis-server tini \
 && rm -rf /var/lib/apt/lists/*

COPY deployments/single/entrypoint.sh /usr/local/bin/liusha-single
RUN chmod +x /usr/local/bin/liusha-single

# 沙盒只供同容器的 runner 调用：收回到回环（沙盒镜像默认监听全部网卡）。
ENV LIUSHA_SANDBOX_LISTEN_ADDR=127.0.0.1:8591 \
    LIUSHA_ENV=production

VOLUME ["/data"]
# 8580 控制台与 API；8588 MITM 代理；对外监听段（缺省 21000-21099）按需 -p 发布
EXPOSE 8580 8588
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/liusha-single"]
