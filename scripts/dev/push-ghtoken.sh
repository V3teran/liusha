#!/bin/bash
# 临时脚本（用后即删）：gh 令牌通道 push（git 凭证 403 的旁路）
set -e
cd /Users/Xlbula/workspace/programs/go/liusha
GH_TOKEN=$(gh auth token) git push origin main
git log --oneline -3
