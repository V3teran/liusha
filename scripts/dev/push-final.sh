#!/bin/bash
# 临时脚本（用后即删）：push 已知存量项（沙箱本职×7/浏览器svc×2/SQL列名误报/穿越防护测试）
set -e
cd /Users/Xlbula/workspace/programs/go/liusha
git push origin main
git log --oneline -3
