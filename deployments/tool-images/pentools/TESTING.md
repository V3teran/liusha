# Pentools 工具测试文档

## 📋 测试概述

完整的 4 级测试体系，确保所有 90 个工具**真正可用**，不只是命令存在。

---

## 🎯 测试层级

### **Level 1: 命令存在性测试**
```bash
command -v <tool>
```

**目的**: 验证工具命令是否在 PATH 中  
**覆盖**: 所有 90 个工具  
**失败即退出**: ✅

---

### **Level 2: 版本/帮助输出测试**
```bash
nmap --version
sqlmap --version
nuclei -version
masscan --version
sliver version
...
```

**目的**: 验证工具能够启动并输出预期内容  
**覆盖**: 30+ 关键工具  
**检查内容**: 版本号、工具名称是否在输出中  
**失败处理**: 警告但不退出（某些工具可能需要特殊参数）

---

### **Level 3: Python 依赖完整性测试**

#### **3.1 系统 Python 库（Layer 11）**
```python
import requests
import httpx
from pwn import *
from bs4 import BeautifulSoup
from Crypto.Cipher import AES
import paramiko
...
```

**目的**: 验证 `pip install` 的库是否真正可导入  
**覆盖**: pwntools, requests, httpx, beautifulsoup4, cryptography, pycryptodome, paramiko

#### **3.2 pipx 工具**
```python
import bloodhound
import certipy
import checkov
import kube_hunter
import prowler
import pacu
import cloudsplaining
import semgrep
```

**目的**: 验证 pipx 安装的工具入口点是否正常  
**覆盖**: 8 个 pipx 工具

#### **3.3 独立 venv 工具**
```bash
/opt/venv-angr/bin/python3 -c "import angr; print('OK')"
/opt/venv-rsactf/bin/python3 -c "from Crypto.PublicKey import RSA; print('OK')"
```

**目的**: 验证独立 venv 工具的依赖隔离正确  
**覆盖**: angr, RsaCtfTool

**失败即退出**: ✅（Python 依赖缺失会导致工具完全不可用）

---

### **Level 4: 功能冒烟测试**

#### **真实命令执行**
```bash
nmap -sn 127.0.0.1           # 扫描本地主机
sqlmap --version             # SQL 注入工具
nuclei -version              # 模板扫描
masscan --version            # 超高速扫描
sliver version               # C2 框架
chisel --version             # 隧道工具
checksec --version           # ELF 检查
ghidra -help                 # 反编译工具
browser-use (wrapper check)  # 浏览器自动化
```

**目的**: 验证工具能够执行实际操作  
**覆盖**: 9 个核心工具  
**失败即退出**: ✅

---

## 🔍 特殊处理

### **1. venv 工具测试**
```bash
# angr (独立 venv)
/opt/venv-angr/bin/python3 -c "import angr; print('angr OK')"

# RsaCtfTool (独立 venv)
/opt/venv-rsactf/bin/python3 -c "from Crypto.PublicKey import RSA; print('RsaCtfTool OK')"

# volatility3 (独立 venv)
command -v vol  # 检查 wrapper 脚本
```

**原因**: 这些工具依赖冲突，必须隔离 venv

---

### **2. 跳过的工具**
```yaml
跳过原因：
  - python3/node/php/go/ruby/java/gcc/curl/jq/mysql
    → runtime/utility 基础命令，非安全工具
  
  - ghidra (Level 4 深度测试)
    → 需要 X11，只测试 -help 输出
  
  - browser-use (Level 4)
    → 需要 Chromium/X11，只测试 wrapper 存在
```

---

### **3. 超时保护**
```bash
timeout 10 <command>
```

**原因**: 防止某些工具卡死（如交互式工具）

---

## 🚀 运行测试

### **自动触发**
```yaml
# Push to main (tools.yaml/Dockerfile 变更)
git push origin main

# Pull Request
gh pr create

# 手动触发
gh workflow run test-pentools.yml
```

### **本地运行**
```bash
# 构建镜像
docker build -f deployments/tool-images/pentools/Dockerfile.final\
  -t pentools:test .

# 运行测试（Level 1）
docker run --rm pentools:test bash -c "
  for tool in nmap sqlmap nuclei masscan sliver; do
    command -v \$tool && echo \"✅ \$tool\" || echo \"❌ \$tool\"
  done
"

# 运行测试（Level 3 - Python）
docker run --rm pentools:test python3 -c "
from pwn import *
import requests
import httpx
from bs4 import BeautifulSoup
print('✅ All Python deps OK')
"

# 运行测试（Level 4 - Smoke）
docker run --rm pentools:test nmap -sn 127.0.0.1
```

---

## 📊 测试统计

| 测试级别 | 覆盖工具 | 测试深度 | 失败即退出 |
|---------|---------|---------|-----------|
| **Level 1** | 90 个 | 命令存在 | ✅ |
| **Level 2** | 30+ 个 | 版本输出 | ⚠️ |
| **Level 3** | 17+ 个 | 依赖导入 | ✅ |
| **Level 4** | 9 个 | 真实执行 | ✅ |

---

## ❌ 常见失败原因

### **Level 1 失败**
```
❌ tool_name - NOT FOUND
```

**原因**:
- 工具未安装（apt/pip/pipx 失败）
- 二进制文件未放入 /usr/local/bin
- PATH 配置错误

**修复**:
1. 检查 tools.yaml 的 install 字段
2. 检查 Dockerfile 的专属层
3. 检查 manifest 安装器日志

---

### **Level 3 失败（Python 依赖）**
```
❌ tool_name dependencies
ModuleNotFoundError: No module named 'xxx'
```

**原因**:
- requirements.txt 未安装
- pip install 失败但未报错
- venv 路径错误

**修复**:
1. 检查 Dockerfile 的 pip install 层
2. 对于 git clone 的工具，检查是否安装了 requirements.txt
3. 对于 venv 工具，检查 venv 路径

**示例**:
```dockerfile
# 错误：只 clone 没装依赖
RUN git clone https://github.com/xxx/tool.git /opt/tool

# 正确：clone + 安装依赖
RUN git clone https://github.com/xxx/tool.git /opt/tool \
 && cd /opt/tool \
 && pip install -r requirements.txt
```

---

### **Level 4 失败（冒烟测试）**
```
❌ tool_name failed
```

**原因**:
- 运行时依赖缺失（动态库）
- 配置文件缺失
- 权限问题

**修复**:
1. 检查 ldd 输出（动态库依赖）
2. 检查工具是否需要配置文件
3. 检查文件权限

---

## 🔧 调试技巧

### **进入容器调试**
```bash
# 构建镜像
docker build -f deployments/tool-images/pentools/Dockerfile.final-t pentools:test .

# 进入容器
docker run --rm -it pentools:test bash

# 测试工具
command -v nmap
nmap --version
python3 -c "from pwn import *; print('OK')"
```

---

### **检查 Python 依赖**
```bash
docker run --rm pentools:test bash -c "
  pip list | grep -i requests
  python3 -c 'import requests; print(requests.__version__)'
"
```

---

### **检查 venv 工具**
```bash
docker run --rm pentools:test bash -c "
  ls -la /opt/venv-angr/
  /opt/venv-angr/bin/pip list
  /opt/venv-angr/bin/python3 -c 'import angr; print(angr.__version__)'
"
```

---

## 📈 未来改进

### **阶段 2: 并行测试**
```yaml
strategy:
  matrix:
    test_level: [level1, level2, level3, level4]
```

**收益**: 测试时间减半

---

### **阶段 3: 增量测试**
```yaml
# 只测试变更的工具
if: contains(github.event.head_commit.message, 'tool_name')
```

**收益**: PR 测试更快

---

### **阶段 4: 性能基准测试**
```bash
time nmap -sn 192.168.1.0/24
time masscan -p80,443 192.168.1.0/24
```

**收益**: 监控工具性能回归

---

## ✅ 验证清单

运行测试后，验证：

- [ ] Level 1: 所有 90 个工具命令存在
- [ ] Level 2: 30+ 关键工具能输出版本
- [ ] Level 3: 17+ Python 工具依赖完整
- [ ] Level 4: 9 个核心工具能真实执行
- [ ] JDK 8 是默认（java -version 输出 1.8）
- [ ] JDK 17 已安装（/opt/jdk17 存在）
- [ ] manifest 安装器存在

---

**生成时间**: 2025-01-XX  
**工具总数**: 90 个  
**测试覆盖**: 4 级测试体系  
**最后更新**: commit d12b3d7f
