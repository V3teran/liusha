# 流量模块与凭证库问题排查

## 问题1：前端流量页面没有数据

### 根本原因
流量表 `proxy_traffic` 为空，不是前端或后端代码问题。

### 诊断步骤

1. **检查后端流量接口是否正常**：
```bash
cd /Users/Xlbula/workspace/programs/go/liusha
API_KEY=$(grep LIUSHA_API_KEY .env | cut -d= -f2)
curl -H "X-API-Key: $API_KEY" 'http://localhost:8090/traffic?size=5'
# 正常应返回: {"items":[], "total":0, ...}
```

2. **检查代理进程是否运行**：
```bash
lsof -i :8888  # 应显示 proxy 进程
ps aux | grep "cmd/proxy" | grep -v grep
```

3. **检查数据库中是否有流量数据**：
```bash
# 如果安装了 psql
psql postgresql://liusha:liusha_dev@localhost:5432/liusha -c "SELECT COUNT(*) FROM proxy_traffic;"

# 或通过 pgAdmin / TablePlus 连接查看
```

### 解决方案

**流量数据来源**：需要通过代理捕获真实 HTTP 流量

1. **配置浏览器使用代理**：
   - 代理地址：`localhost:8888`（external listener）
   - 协议：HTTP/HTTPS
   - 安装 mitmproxy CA 证书（如果抓 HTTPS）

2. **或使用命令行工具通过代理**：
```bash
# curl 通过代理
curl -x http://localhost:8888 http://example.com

# 或配置环境变量
export http_proxy=http://localhost:8888
export https_proxy=http://localhost:8888
curl http://example.com
```

3. **测试流量捕获**：
   - 浏览器访问几个网站
   - 刷新前端流量页面，应该能看到捕获的流量

4. **查看 proxy 日志**：
```bash
tail -f logs/proxy.log
# 应该能看到流量捕获日志
```

### 注意事项

- **双 listener 隔离**：
  - `:8888` - external listener，用于捕获外部真实流量
  - `:8890` - agent listener，用于 AI agent 自身的请求（避免自激）
  
- **流量过滤**：proxy 进程会根据 `system_setting.proxy_filter` 配置过滤流量
  - 默认可能过滤掉静态资源（.css/.js/.png 等）
  - 可在系统配置页面调整过滤规则

## 问题2：前端凭证库功能

### 已完成

1. **创建了 `CredentialsPage.tsx`**：
   - 展示按 host 归属的凭证列表
   - 查看某 host 下的所有身份和凭证
   - 删除某 host 的全部凭证
   - 集成到路由 `/credentials`

2. **对接后端 API**：
   - `GET /credential?host=<host>` - 列出某 host 的凭证
   - `POST /credential/batch` - 批量保存凭证
   - `DELETE /credential?host=<host>` - 删除某 host 凭证

### 局限和改进方向

**当前局限**：后端没有 `GET /credential/hosts` 端点来列出所有已有凭证的 host

**改进方案**：

**方案 A（推荐）**：复用现有端点
```typescript
// 在 CredentialsPage.tsx 中
useEffect(() => {
  // 从流量模块获取 host 列表
  listTrafficHosts().then(hosts => setHosts(hosts))
}, [])
```

**方案 B**：后端补充端点
```go
// internal/httpapi/handlers.go
func listCredentialHostsHandler(api CredentialsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Redis SCAN credentials:* 获取所有 host
		hosts, err := api.ListHosts(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"hosts": hosts})
	}
}

// internal/credential/redis.go
func (r *RedisProvider) ListHosts(ctx context.Context) ([]string, error) {
	var hosts []string
	iter := r.client.Scan(ctx, 0, r.keyPrefix+"*", 0).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		host := strings.TrimPrefix(key, r.keyPrefix)
		hosts = append(hosts, host)
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	return hosts, nil
}
```

### 凭证数据来源

凭证有两种来源：

1. **自动捕获**（passive）：
   - proxy 从流量中提取 Cookie/Authorization header
   - 自动存入 Redis `credentials:<host>`
   - AI agent 在分析流量时可自动获取登录态

2. **手动录入**：
   - 未来可在凭证页面添加「新增凭证」按钮
   - 手动填写 host 和对应的身份凭证
   - 用于手动配置测试账号

## 快速验证完整流程

```bash
# 1. 确保后端运行
cd /Users/Xlbula/workspace/programs/go/liusha
./scripts/dev/run-svc.sh

# 2. 确保前端运行（注意带上环境变量）
cd web
VITE_API_TARGET=http://localhost:8090 pnpm dev

# 3. 配置浏览器代理到 localhost:8888

# 4. 浏览器访问几个网站（如 http://example.com）

# 5. 打开前端 http://localhost:5173/traffic 查看流量

# 6. 打开 http://localhost:5173/credentials 查看凭证
```

## 相关文档

- 架构设计：`docs/architecture/proxy-traffic.md`（如果存在）
- 配置说明：`config/config.yaml` 中的 `proxy:` 和 `credential:` 配置
- Memory: `project_passive_fullread_refactor.md` - 流量重构说明
