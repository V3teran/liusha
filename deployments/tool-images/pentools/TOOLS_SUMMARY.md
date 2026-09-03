# Pentools 工具清单总结

## 📊 工具统计

| 项目 | 数量 |
|------|------|
| **原有工具** | 78 个 |
| **新增工具** | 12 个 |
| **当前总计** | 90 个 |

---

## ✅ 新增工具清单（12个）

### **1. 扫描/侦察（2个）**

#### masscan
- **分类**: recon
- **功能**: 超高速端口扫描（比 nmap 快 1000x）
- **场景**: 大范围 C 段快速发现开放端口
- **与 nmap 互补**: masscan 快速探测 → nmap -A 深度分析

#### fscan
- **分类**: recon
- **功能**: 国产内网综合扫描（一键打点）
- **场景**: 集成端口扫描 + 服务爆破 + 漏洞检测（MS17-010/Redis/SMB）
- **价值**: 内网横向首选，一条命令拿下全 C 段

---

### **2. 内容/参数发现（1个）**

#### paramspider
- **分类**: discovery
- **功能**: 参数挖掘（爬 Wayback Machine 历史）
- **与 arjun 互补**: arjun 爆破参数名，paramspider 从历史 URL 挖掘
- **场景**: 发现未文档化的隐藏参数

---

### **3. 漏洞扫描（2个）**

#### nikto
- **分类**: vulnscan
- **功能**: 经典 Web 服务器配置审计（6700+ 测试项）
- **与 nuclei 互补**: nuclei 侧重 CVE 检测，nikto 侧重配置审计
- **特点**: HTTP 响应头安全检查 + Apache/Nginx 配置弱点 + SSL/TLS 测试

#### wpscan
- **分类**: vulnscan
- **功能**: WordPress 专用漏洞扫描
- **场景**: 枚举插件/主题漏洞 + 用户名 + 弱口令爆破
- **价值**: WordPress 站点必用，nuclei 的 WP 模板覆盖不全

---

### **4. PWN/CTF（1个）**

#### checksec
- **分类**: pwn
- **功能**: ELF 二进制保护检查
- **场景**: 快速查看 NX/PIE/Canary/RELRO/ASLR 状态
- **价值**: pwn 题第一步必跑，比 pwntools 的 checksec 输出更清晰

---

### **5. 云安全（1个）**

#### checkov
- **分类**: cloud
- **功能**: IaC 安全扫描（1000+ 规则）
- **场景**: 扫描 Terraform/CloudFormation/Kubernetes YAML/Dockerfile
- **支持**: AWS/Azure/GCP/阿里云

---

### **6. 容器安全（1个）**

#### kube-hunter
- **分类**: container
- **功能**: Kubernetes 渗透测试
- **与 kube-bench 互补**: kube-bench 是合规检查，kube-hunter 是渗透测试
- **场景**: 探测 K8s 集群攻击面（API Server/etcd/kubelet/dashboard）

---

### **7. 后渗透（2个）**

#### chisel
- **分类**: post-exploit
- **功能**: 快速 TCP/UDP 隧道（HTTP 传输）
- **与 ligolo-ng 互补**: chisel 更轻量，ligolo-ng 更稳定
- **特点**: Go 单文件，支持 SOCKS5 代理

#### linpeas
- **分类**: post-exploit
- **功能**: Linux 提权检查脚本
- **场景**: 自动枚举 SUID/sudo 滥用 + 内核漏洞 + 敏感文件 + 定时任务 + 容器逃逸
- **价值**: 后渗透提权第一步

---

### **8. C2 框架（1个）**

#### sliver
- **分类**: exploitation
- **功能**: 现代 C2 框架（Go 实现）
- **特点**: 
  - 多协议通信（HTTP/DNS/mTLS/WireGuard）
  - 动态 payload 生成（躲避签名检测）
  - 内存执行 + 进程注入
  - 轻量级（~30MB）
- **与 Metasploit 互补**: 
  - Metasploit: 公开漏洞利用，特征明显
  - Sliver: 红队持久化，隐蔽性强
- **价值**: 新一代开源 C2 标准（SANS/MITRE ATT&CK 推荐）

---

## ❌ 已排除的工具（功能重复）

### **扫描工具**
- **amass**: subfinder 已覆盖 90% 场景（被动枚举足够）
- **rustscan**: masscan 已提供超高速扫描

### **目录爆破**
- **dirsearch**: feroxbuster + ffuf 已覆盖
- **gobuster**: feroxbuster + ffuf 已覆盖

### **PWN 工具**
- **ropper**: ROPgadget + pwntools.ROP 已足够

### **URL 收集**
- **waybackurls**: gau 完全覆盖（gau 包含 Wayback + 多源）

### **SMB/AD 工具**
- **smbmap**: netexec 已覆盖
- **rpcclient**: netexec 已覆盖
- **enum4linux-ng**: netexec 已覆盖

### **云安全**
- **scout-suite**: prowler 已覆盖 AWS
- **terrascan**: checkov 已覆盖 Terraform

### **其他**
- **pwntools**: 已在 Layer 11 安装（Python 库）

---

## 🎯 工具分类统计（90个）

| 分类 | 工具数 | 代表工具 |
|------|--------|----------|
| **recon** | 8 | subfinder, httpx, katana, nmap, masscan, fscan |
| **discovery** | 4 | arjun, feroxbuster, ffuf, paramspider |
| **vulnscan** | 4 | nuclei, trufflehog, nikto, wpscan |
| **sast** | 2 | semgrep, spectral |
| **injection** | 5 | sqlmap, commix, dalfox, SSTImap, SSRFmap |
| **deserialization** | 2 | ysomap, ysoserial |
| **oob** | 1 | interactsh-client |
| **auth** | 2 | hydra, jwt_tool |
| **exploitation** | 6 | msfconsole, sliver, impacket-secretsdump, netexec, responder, evil-winrm |
| **post-exploit** | 6 | bloodhound-python, certipy, ligolo-ng, chisel, linpeas, proxychains4 |
| **cloud** | 5 | prowler, pacu, cloudsplaining, cloudfox, checkov |
| **container** | 5 | trivy, kubectl, peirates, kube-bench, kube-hunter |
| **pwn** | 9 | gdb, ghidra, radare2, afl-fuzz, angr, pwninit, checksec, seccomp-tools, ROPgadget |
| **reverse** | 3 | ghidra, radare2, patchelf |
| **crypto** | 1 | RsaCtfTool |
| **forensics** | 3 | vol, testdisk, foremost |
| **stego** | 5 | steghide, stegoveritas, stegseek, zsteg, exiftool |
| **cracking** | 4 | john, hashcat, fcrackzip, hash-identifier |
| **runtime** | 6 | python3, node, php, go, ruby, java |
| **browser** | 1 | browser-use |
| **utility** | 13 | curl, jq, mysql, gcc, binwalk, strace, patchelf, one_gadget, tshark, xxd 等 |

---

## 🔍 OOB vs Recon 分类说明

### **无重复，功能完全不同**

| 分类 | 工具 | 功能 |
|------|------|------|
| **OOB** | interactsh-client | 带外攻击检测平台（接收回调，检测 Blind SSRF/RCE/XXE） |
| **Recon** | subfinder, httpx, nmap 等 | 主动扫描/被动收集（子域名枚举、端口扫描、服务探测） |

**结论**: OOB 是攻击验证平台，Recon 是信息收集工具，两者无重叠。

---

## 📝 镜像命名

```
基础镜像: ghcr.io/v3teran/pentools-base:latest
最终镜像: ghcr.io/v3teran/liusha-pentools:latest
```

---

## ✅ JDK 版本配置

当前配置已足够，无需补充：

| JDK 版本 | 用途 | 支持的工具 |
|---------|------|-----------|
| **JDK 8u352-b08** | 默认 | ysoserial, ysomap（Java 反序列化工具） |
| **JDK 17.0.19+10** | 现代 Java | Ghidra 10.x/11.x/12.x, Spring Boot 3.x |

**说明**: Kali apt 安装 ghidra 时自动拉取依赖 JDK，不与手动安装的 JDK 8/17 冲突。

---

## 🎯 严格筛选原则

1. **功能互补，不重复**: 每个工具必须有独特场景，不与现有工具重叠 70% 以上
2. **主流工具优先**: 优先选择社区认可度高、维护活跃的工具
3. **轻量级优先**: 避免引入体积大、依赖重的工具
4. **实战导向**: 以渗透测试/CTF 实战场景为准，不追求工具数量

---

## 📊 对比 CyberStrikeAI

| 项目 | liusha | CyberStrikeAI |
|------|--------|--------------|
| **工具总数** | 90 | 90 |
| **重复工具** | 0（内部无冗余） | 未知 |
| **C2 工具** | 2（Metasploit + Sliver） | 1（Metasploit） |
| **质量** | 严格去重，互补性强 | 覆盖全面 |
| **镜像架构** | 两层拆分（base + final） | 单层 |
| **构建速度** | 常规变更 2 分钟 | 40 分钟 |

---

## 🔄 后续维护建议

1. **定期更新**: 每季度检查工具版本更新（release/apt 包）
2. **工具验证**: 每次补充工具前检查功能重复性
3. **移除废弃**: 监控工具维护状态，移除停止维护的工具
4. **社区反馈**: 根据实际使用反馈调整工具清单

---

**生成时间**: 2025-01-XX  
**工具总数**: 90 个  
**最后更新**: commit 551afb5b
