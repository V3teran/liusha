# Pentools 语言环境完整清单

## 📊 语言环境总览

pentools 镜像包含 **7 种主流语言环境** + **1 种编译工具链**，覆盖所有渗透测试和 CTF 场景。

---

## 🔧 Runtime 分类工具（6个）

### **1. Python 3.13**
```yaml
- name: python3
  category: runtime
  description: 自定义脚本兜底（GraphQL/NoSQL 注入、复杂登录链、并发 fuzz、二阶段 payload、编码绕过）。已装 pwntools（pwn 交互）+ pycryptodome/sympy/primefac/fpylll（crypto）+ pymysql。
  install: { method: apt }
```

**版本**: Python 3.13（Kali rolling）  
**来源**: Kali apt  
**预装库**:
- **PWN**: pwntools
- **Crypto**: pycryptodome, cryptography, pyjwt, sympy, primefac, fpylll
- **Web**: requests, httpx, aiohttp, beautifulsoup4, lxml, urllib3
- **WebSocket**: websockets, websocket-client
- **Network**: scapy, dnspython, pysocks
- **Data**: pyyaml, xmltodict
- **SSH/MySQL**: paramiko, pymysql
- **Other**: colorama

**特殊 Python 环境**:
- **Python 3.12 venv** (`/opt/browser-use-venv`): browser-use 专用
- **独立 venv**:
  - `/opt/venv-angr` - angr 符号执行
  - `/opt/venv-vol` - volatility3 内存取证
  - `/opt/venv-rsactf` - RsaCtfTool

---

### **2. Java（双 JDK）**
```yaml
- name: java
  category: runtime
  description: Java 运行/编译（反序列化 gadget、JNDI、RCE payload、CTF Java 题）。双 JDK 可切换——默认 `java`/`javac`=JDK8（配合 ysomap/ysoserial）；现代 Spring Boot 3.x 用 `java17`/`javac17`。
```

**版本**: 
- **JDK 8u352-b08**（默认）← ysoserial/ysomap 需要
- **JDK 17.0.19+10** ← Ghidra/现代 Java 应用

**来源**: Adoptium Temurin（手动安装）  
**切换命令**:
```bash
java -version      # JDK 8（默认）
java17 -version    # JDK 17
javac -version     # JDK 8 编译器
javac17 -version   # JDK 17 编译器
```

**用途**:
- JDK 8: Java 反序列化漏洞利用（ysoserial/ysomap）
- JDK 17: Ghidra 反编译、Spring Boot 3.x 应用

---

### **3. PHP 8.4**
```yaml
- name: php
  category: runtime
  description: PHP 解释器（php 8.4）——写/跑 PHP payload、验证反序列化（配合 phpggc）、LFI 链、webshell、magic hash。
  install: { method: apt, pkg: php-cli }
```

**版本**: PHP 8.4（Kali rolling）  
**来源**: Kali apt  
**用途**:
- PHP 反序列化利用（配合 phpggc）
- LFI/RFI 链测试
- Webshell 测试
- Magic hash 验证

---

### **4. Node.js 24**
```yaml
- name: node
  category: runtime
  description: Node.js（node 24）——JS payload 与利用：原型链污染、客户端 SSTI、JWT 弱密钥/算法混淆脚本、前端逻辑复现。
  install: { method: apt, pkg: nodejs }
```

**版本**: Node.js 24（Kali rolling）  
**来源**: Kali apt  
**预装 npm 全局包**:
- `@stoplight/spectral-cli` - API 安全扫描

**用途**:
- 原型链污染攻击
- 客户端 SSTI
- JWT 弱密钥/算法混淆
- 前端逻辑复现

---

### **5. Ruby 3.3**
```yaml
- name: ruby
  category: runtime
  description: Ruby 解释器（ruby 3.3，ruby-full）——跑 Ruby payload/PoC，也是 zsteg/one_gadget/seccomp-tools/evil-winrm 等 gem 工具的运行时。
  install: { method: apt, pkg: ruby-full }
```

**版本**: Ruby 3.3（Kali rolling）  
**来源**: Kali apt  
**预装 gem 包**:
- evil-winrm - WinRM shell
- one_gadget - libc one-gadget 查找
- seccomp-tools - seccomp 分析

**用途**:
- 运行 Ruby payload/PoC
- gem 工具运行时

---

### **6. Go 1.26**
```yaml
- name: go
  category: runtime
  description: Go 编译器——现场编译 Go payload 或小工具（go run x.go / go build）。
  install: { method: apt, pkg: golang }
```

**版本**: Go 1.26（Kali rolling）  
**来源**: Kali apt  
**环境变量**:
- `GOBIN=/usr/local/bin`
- `GOPROXY=https://proxy.golang.org,direct`

**用途**:
- 现场编译 Go payload
- 编译小工具（如自定义扫描器）
- go install 安装工具（gau 等）

---

## 🔨 编译工具链

### **7. GCC/G++ 15**
```yaml
- name: gcc
  category: runtime
  description: C/C++ 编译器（gcc/g++ 15）——CTF pwn 编译 exploit、编译本地提权 PoC、编译 .so/二进制。
  install: { method: apt, pkg: build-essential }
```

**版本**: GCC 15（Kali rolling）  
**来源**: Kali apt  
**包含**:
- gcc/g++ - C/C++ 编译器
- make - 构建工具
- libc-dev - 开发库
- 其他 build-essential 工具

**用途**:
- PWN exploit 编译
- 本地提权 PoC 编译
- .so 库编译
- 二进制工具编译

---

## 🦀 特殊语言环境

### **Rust（可选）**

**状态**: 当前未安装（注释说明提到可用于 angr 的 rustworkx 依赖）  
**安装方式**:
```dockerfile
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs \
    | sh -s -- -y --default-toolchain stable --profile minimal
```

**用途**: 
- angr 依赖 rustworkx（当前使用预编译 wheel）
- 某些工具的源码编译

---

## 📦 包管理器

### **Python**
- **pip** - 系统 Python 包管理（已设置 `PIP_BREAK_SYSTEM_PACKAGES=1`）
- **pipx** - 隔离 Python CLI 工具（用于 checkov/kube-hunter/prowler 等）
- **uv** - 快速 Python 包管理器（用于 browser-use venv）

### **Node.js**
- **npm** - Node.js 包管理器（全局 spectral-cli）

### **Ruby**
- **gem** - Ruby 包管理器（evil-winrm/one_gadget/seccomp-tools）

### **Java**
- **Maven** - Java 构建工具（用于编译 ysomap）

---

## 🎯 语言版本对比

| 语言 | pentools 版本 | CyberStrikeAI 版本 | 来源 |
|------|--------------|-------------------|------|
| **Python** | 3.13 | 3.x | Kali rolling |
| **Java** | 8 + 17（双版本） | default-jdk | 手动安装 |
| **PHP** | 8.4 | 8.x | Kali apt |
| **Node.js** | 24 | 18+ | Kali apt |
| **Ruby** | 3.3 | 3.x | Kali apt |
| **Go** | 1.26 | 1.23+ | Kali apt |
| **GCC** | 15 | 13+ | Kali apt |

---

## 🔍 验证语言环境

### **测试所有语言**
```bash
docker run --rm pentools:test bash -c "
echo 'Python:' && python3 --version
echo 'Java (default):' && java -version 2>&1 | head -1
echo 'Java 17:' && java17 -version 2>&1 | head -1
echo 'PHP:' && php --version | head -1
echo 'Node.js:' && node --version
echo 'Ruby:' && ruby --version
echo 'Go:' && go version
echo 'GCC:' && gcc --version | head -1
"
```

### **测试 Python 库**
```bash
docker run --rm pentools:test python3 -c "
from pwn import *
import requests
import httpx
from bs4 import BeautifulSoup
from Crypto.Cipher import AES
import paramiko
print('✅ All Python libs OK')
"
```

### **测试 JDK 切换**
```bash
docker run --rm pentools:test bash -c "
echo 'Default JDK:'
java -version 2>&1 | grep version

echo 'JDK 17:'
java17 -version 2>&1 | grep version
"
```

---

## 📚 使用场景

### **Python**
- 自定义漏洞利用脚本
- PWN 交互（pwntools）
- 加密/解密脚本（pycryptodome）
- Web 自动化（requests/httpx）

### **Java**
- Java 反序列化漏洞利用（ysoserial/ysomap）
- JNDI 注入攻击
- Ghidra 反编译
- CTF Java 题

### **PHP**
- PHP 反序列化（phpggc）
- Webshell 测试
- LFI/RFI 利用链

### **Node.js**
- 原型链污染攻击
- JWT 算法混淆
- 前端逻辑复现

### **Ruby**
- WinRM shell（evil-winrm）
- 隐写分析（zsteg）
- libc gadget 查找（one_gadget）

### **Go**
- 现场编译 Go exploit
- 编译轻量级工具

### **GCC**
- PWN exploit 编译
- 本地提权 PoC
- .so 注入库编译

---

## 🎯 总结

| 特性 | 数量/状态 |
|------|----------|
| **语言总数** | 7 种 |
| **JDK 版本** | 2 个（8 + 17） |
| **Python 版本** | 2 个（3.13 系统 + 3.12 venv） |
| **独立 venv** | 4 个（browser-use/angr/vol/rsactf） |
| **包管理器** | 5 个（pip/pipx/npm/gem/maven） |
| **预装 Python 库** | 20+ 个 |
| **预装 gem 包** | 3 个 |
| **预装 npm 包** | 1 个 |

---

**生成时间**: 2025-01-XX  
**工具总数**: 90 个  
**语言环境**: 7 种主流语言 + 双 JDK  
**最后更新**: commit ff360252
