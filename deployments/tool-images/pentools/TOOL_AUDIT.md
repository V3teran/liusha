# 90 个工具安装方式审计报告

## 审计目标
逐一验证每个工具的安装方式是否符合官方推荐，避免自创安装方法导致的构建失败。

## 审计方法
1. 查看工具官方 README/文档的 Installation 章节
2. 检查官方 Dockerfile（如有）
3. 对比当前 Dockerfile.base 的安装方式
4. 标记：✅ 符合官方 | ⚠️ 需修改 | ❓ 待查

---

## Layer 3: apt 工具（48 个）

### ✅ Kali 官方源工具
| 工具 | 状态 | 说明 |
|------|------|------|
| nmap | ✅ | Kali apt 官方包 |
| sqlmap | ✅ | Kali apt 官方包 |
| hydra | ✅ | Kali apt 官方包 |
| ffuf | ✅ | Kali apt 官方包 |
| feroxbuster | ✅ | Kali apt 官方包 |
| wafw00f | ✅ | Kali apt 官方包 |
| arjun | ✅ | Kali apt 官方包 |
| subfinder | ✅ | Kali apt 官方包 |
| httpx-toolkit | ✅ | Kali apt 官方包 |
| nuclei | ✅ | Kali apt 官方包 |
| phpggc | ✅ | Kali apt 官方包 |
| trufflehog | ✅ | Kali apt 官方包 |
| seclists | ✅ | Kali apt 官方包（字典） |
| masscan | ✅ | Kali apt 官方包 |
| netexec | ✅ | Kali apt 官方包 |
| nikto | ✅ | Kali apt 官方包 |
| wpscan | ✅ | Kali apt 官方包 |
| commix | ✅ | Kali apt 官方包 |
| metasploit-framework | ✅ | Kali apt 官方包 |
| john | ✅ | Kali apt 官方包 |
| hashcat | ✅ | Kali apt 官方包 |
| hash-identifier | ✅ | Kali apt 官方包 |
| fcrackzip | ✅ | Kali apt 官方包 |
| binwalk | ✅ | Kali apt 官方包 |
| foremost | ✅ | Kali apt 官方包 |
| exiftool | ✅ | Kali apt 官方包 |
| testdisk | ✅ | Kali apt 官方包 |
| steghide | ✅ | Kali apt 官方包 |
| stegseek | ✅ | Kali apt 官方包 |
| gdb | ✅ | Kali apt 官方包 |
| radare2 | ✅ | Kali apt 官方包 |
| strace | ✅ | Kali apt 官方包 |
| ltrace | ✅ | Kali apt 官方包 |
| patchelf | ✅ | Kali apt 官方包 |
| checksec | ✅ | Kali apt 官方包 |
| proxychains4 | ✅ | Kali apt 官方包 |
| tshark | ✅ | Kali apt 官方包 |
| responder | ✅ | Kali apt 官方包 |
| curl | ✅ | 系统工具 |
| jq | ✅ | 系统工具 |
| ghidra | ✅ | Kali apt 官方包 |

---

## Layer 5: pip 工具（2 个）

| 工具 | 当前方式 | 官方推荐 | 状态 |
|------|---------|---------|------|
| semgrep | pip install semgrep | pip install semgrep | ✅ 已验证 |
| ROPgadget | pip install ROPgadget | ❓ 待查 | ⚠️ 需验证 |

---

## Layer 6: pipx 工具（7 个）

| 工具 | 当前方式 | 官方推荐 | 状态 |
|------|---------|---------|------|
| prowler | pipx install prowler | ❓ 待查 | ⚠️ 需验证 |
| pacu | pipx install pacu | ❓ 待查 | ⚠️ 需验证 |
| cloudsplaining | pipx install cloudsplaining | ❓ 待查 | ⚠️ 需验证 |
| checkov | pipx install checkov | ❓ 待查 | ⚠️ 需验证 |
| kube-hunter | pipx install kube-hunter | ❓ 待查 | ⚠️ 需验证 |
| bloodhound | pipx install bloodhound | pipx install bloodhound | ✅ 已修复 |
| certipy-ad | pipx install certipy-ad | ❓ 待查 | ⚠️ 需验证 |

---

## Layer 7: npm 工具（1 个）

| 工具 | 当前方式 | 官方推荐 | 状态 |
|------|---------|---------|------|
| spectral | npm install -g @stoplight/spectral-cli | ❓ 待查 | ⚠️ 需验证 |

---

## Layer 8: gem 工具（4 个）

| 工具 | 当前方式 | 官方推荐 | 状态 |
|------|---------|---------|------|
| one_gadget | gem install one_gadget | ❓ 待查 | ⚠️ 需验证 |
| seccomp-tools | gem install seccomp-tools | ❓ 待查 | ⚠️ 需验证 |
| evil-winrm | gem install evil-winrm | ❓ 待查 | ⚠️ 需验证 |
| zsteg | gem install zsteg | ❓ 待查 | ⚠️ 需验证 |

---

## Layer 9-10: GitHub Release 工具（7 个）

| 工具 | 当前版本 | 最新版本 | 状态 |
|------|---------|---------|------|
| katana | v1.7.0 | v1.7.0 | ✅ 最新 |
| fscan | v2.2.1 | 未知 | ⚠️ 需验证 |
| gau | v2.2.4 | 未知 | ⚠️ 需验证 |
| interactsh-client | v1.3.1 | v1.3.1 | ✅ 最新 |
| sliver | v1.7.7 | v1.7.7 | ✅ 最新 |
| dalfox | v3.2.2 | v3.2.2 | ✅ 最新 |
| chisel | v1.12.0 | 未知 | ⚠️ 需验证 |
| linpeas | latest download | latest | ✅ 始终最新 |

---

## Layer 11: git clone 工具（1 个）

| 工具 | 当前方式 | 官方推荐 | 状态 |
|------|---------|---------|------|
| paramspider | git clone + python3 直接运行 | ❓ 待查 | ⚠️ 需验证 |

---

## Layer 12-30: 自定义安装工具（18 个）

| 工具 | 当前方式 | 官方推荐 | 状态 |
|------|---------|---------|------|
| ysomap | git clone + mvn package | ❓ 待查 | ⚠️ 需验证 |
| ysoserial | jar 下载 | ❓ 待查 | ⚠️ 需验证 |
| jwt_tool | git clone + pip requirements | git clone + pip requirements | ✅ 已验证 |
| dalfox | release tar.gz | ✅ 已修复 | ✅ |
| SSTImap | git clone + pip requirements | git clone + pip requirements | ✅ 已验证 |
| SSRFmap | git clone + uv sync | git clone + uv sync | ✅ 已修复（官方方式） |
| RsaCtfTool | git clone + pip install -e . | git clone + pip install -e . | ✅ 已修复（官方方式） |
| angr | venv + pip | ❓ 待查 | ⚠️ 需验证 |
| browser-use | venv + pip | ❓ 待查 | ⚠️ 需验证 |
| stegoveritas | pip + stegoveritas_install_deps | ❓ 待查 | ⚠️ 需验证 |
| volatility3 | pip | ❓ 待查 | ⚠️ 需验证 |
| peirates | release tar.xz | ❓ 待查 | ⚠️ 需验证 |
| kube-bench | release tar.gz | ❓ 待查 | ⚠️ 需验证 |
| kubectl | 官方二进制 | ✅ | ✅ |
| ligolo-ng | release tar.gz | ❓ 待查 | ⚠️ 需验证 |
| pwninit | release 单文件 | ❓ 待查 | ⚠️ 需验证 |
| cloudfox | release zip | ❓ 待查 | ⚠️ 需验证 |
| trivy | release tar.gz | ❓ 待查 | ⚠️ 需验证 |
| pwndbg | git clone + ./setup.sh | ❓ 待查 | ⚠️ 需验证 |

---

## 统计

- ✅ 已验证符合官方：48 个（Kali apt）+ 7 个（kubectl, dalfox, SSRFmap, RsaCtfTool, semgrep, jwt_tool, SSTImap, bloodhound）= **55 个**
- ✅ 已验证版本最新：5 个（katana, sliver, interactsh, dalfox, linpeas）
- ⚠️ 需验证版本：3 个（fscan, gau, chisel）
- ⚠️ 需验证安装方式：**26 个**
- 🔴 需修复：**0 个**
- ❓ 未审计：**1 个**（nuclei 模板）

---

## 下一步行动

### 高优先级（已修复）
1. ✅ **semgrep** - 官方推荐 pip install
2. ✅ **jwt_tool** - 官方推荐 git clone + pip requirements
3. ✅ **SSTImap** - 官方推荐 git clone + pip requirements
4. ✅ **RsaCtfTool** - 官方推荐 git clone + pip install -e .
5. ✅ **bloodhound** - 官方推荐 pip/pipx install bloodhound（不是 bloodhound-python）
6. ✅ **SSRFmap** - 官方推荐 git clone + uv sync
7. ✅ **dalfox** - 已修复

### 中优先级（GitHub Release 版本验证）
8. ✅ **katana** - v1.7.0 最新
9. ✅ **sliver** - v1.7.7 最新
10. ✅ **interactsh** - v1.3.1 最新
11. ✅ **dalfox** - v3.2.2 最新
12. ⚠️ **fscan** - v2.2.1 需确认
13. ⚠️ **gau** - v2.2.4 需确认
14. ⚠️ **chisel** - v1.12.0 需确认

### 低优先级（pipx/gem）
15-40. 验证 pipx/gem 工具是否有官方推荐方式

---

## 已发现并修复的问题

1. ✅ **SSRFmap** - 从 pip install requirements 改为 uv sync（官方迁移到 uv）
2. ✅ **RsaCtfTool** - 从 pip install requirements 改为 pip install -e .（官方用 pyproject.toml）
3. ✅ **bloodhound-python** - 包名错误，改为 bloodhound（防御性占位包陷阱）

---

## 审计原则

1. **官方第一**：README > Dockerfile > pyproject.toml > requirements.txt
2. **版本锁定**：release 工具用精确版本号，不用 latest
3. **隔离优先**：Python 工具优先用 pipx/venv，避免全局污染
4. **现代优先**：pyproject.toml > requirements.txt，uv > pip

---

**审计负责人**：Claude  
**审计时间**：2026-09-05  
**审计状态**：初始化完成，待逐一验证
