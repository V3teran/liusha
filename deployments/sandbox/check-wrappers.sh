#!/bin/sh
# check-wrappers：校验 /usr/local/bin 下包装脚本的 exec 目标都存在（沙盒构建最后一步执行）。
#
# 包装脚本把工具名转到实际入口（java -jar /opt/x.jar、venv 里的 python 脚本等）。上游改了
# 目录布局时脚本照样存在，工具页按命令名判定为已安装，运行时却找不到入口——构建时就拦下。
set -u
bad=0
for f in /usr/local/bin/*; do
  [ -f "$f" ] && [ ! -L "$f" ] || continue
  head -c 2 "$f" | grep -q '#!' || continue
  grep -E '^exec ' "$f" | while IFS= read -r line; do
    # exec 后的第一个词是程序，其后形如绝对路径的参数是它要打开的文件
    set -f
    # shellcheck disable=SC2086
    set -- $line
    set +f
    shift
    prog=$1
    shift
    case $prog in
      /*) [ -x "$prog" ] || echo "$(basename "$f")：程序不存在或不可执行 $prog" ;;
      *) command -v "$prog" >/dev/null 2>&1 || echo "$(basename "$f")：找不到程序 $prog" ;;
    esac
    for arg in "$@"; do
      case $arg in
        /opt/* | /usr/*) [ -e "$arg" ] || echo "$(basename "$f")：入口文件不存在 $arg" ;;
      esac
    done
  done
done | tee /tmp/check-wrappers.out
if [ -s /tmp/check-wrappers.out ]; then
  echo "包装脚本校验失败：$(wc -l < /tmp/check-wrappers.out) 处" >&2
  rm -f /tmp/check-wrappers.out
  exit 1
fi
rm -f /tmp/check-wrappers.out
echo "包装脚本校验通过"
