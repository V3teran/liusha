#!/bin/bash
# 临时脚本（用后即删）：重试 push（认证已验证通畅）
set -e
cd /Users/Xlbula/workspace/programs/go/liusha
git push origin main
git log --oneline -4
