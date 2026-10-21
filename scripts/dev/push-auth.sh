#!/bin/bash
# 临时脚本（用后即删）：gh 凭证通道配置完成后 push
set -e
cd /Users/Xlbula/workspace/programs/go/liusha
git push origin main
git log --oneline -4
