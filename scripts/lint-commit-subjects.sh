#!/usr/bin/env bash
# 提交主题门禁：格式为 <type>(<scope>)!: <主题>，显示宽度不超过 72 列（中文等宽字符按 2 列计）。
# feat / fix 的主题原样进 CHANGELOG 与 Release 页面，GitHub 的提交与文件列表也会截断超长主题。
#
# 用法：scripts/lint-commit-subjects.sh <提交范围>   如 origin/main..HEAD
# 合并提交不检查（由 GitHub 生成）。
set -euo pipefail

range="${1:?用法：scripts/lint-commit-subjects.sh <提交范围>}"

git log --no-merges --format='%h%x09%s' "$range" | python3 -c '
import re, sys, unicodedata

MAX_WIDTH = 72
TYPES = "feat|fix|docs|refactor|test|ci|chore|build|perf|revert"
PATTERN = re.compile(r"^(%s)(\([a-z0-9-]+\))?!?: \S" % TYPES)

def width(s):
    return sum(2 if unicodedata.east_asian_width(c) in "WF" else 1 for c in s)

bad = 0
for line in sys.stdin:
    sha, subject = line.rstrip("\n").split("\t", 1)
    problems = []
    if not PATTERN.match(subject):
        problems.append("格式应为 <type>(<scope>): <主题>，type 取 " + TYPES.replace("|", " "))
    if width(subject) > MAX_WIDTH:
        problems.append("显示宽度 %d 列，超过 %d 列" % (width(subject), MAX_WIDTH))
    for p in problems:
        print("%s %s\n    %s" % (sha, subject, p))
        bad += 1
if bad:
    print("\n提交主题不合规 %d 处：主题写用户能感知的变化，原因写进正文（见 .gitmessage）" % bad)
    sys.exit(1)
'
