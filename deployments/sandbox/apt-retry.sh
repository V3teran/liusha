#!/bin/sh
# apt-retry：apt-get 失败时退避重试，参数原样交给 apt-get（如 apt-retry install -y nmap）。
#
# 镜像源偶发 5xx（实测 kali.download 返回 502）时 apt 自身的 Acquire::Retries 不会重试，
# 一个包下载失败整层构建就失败。重试时已下载的包留在缓存里，只补下失败的。
set -u
attempt=1
until apt-get "$@"; do
  if [ "$attempt" -ge 4 ]; then
    echo "apt-get $* 连续失败 $attempt 次，放弃" >&2
    exit 1
  fi
  wait=$((attempt * 15))
  echo "apt-get $* 失败（第 $attempt 次），${wait} 秒后重试" >&2
  sleep "$wait"
  attempt=$((attempt + 1))
done
