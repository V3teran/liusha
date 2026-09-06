# 代码质量分析报告

## 分析范围
- internal/httpapi/ (API handlers)
- internal/task/ (核心领域)
- internal/assignment/ (核心领域)
- internal/finding/ (核心领域)
- internal/traffic/ (核心领域)
- cmd/api/ (入口点)
- cmd/runner/ (入口点)

## 发现的问题

### 1. 代码重复 (Code Duplication)

#### 1.1 工具函数重复定义
**位置**: internal/httpapi/
- `atoiOr` 定义在 tool_handler.go:202
- `rawOrEmpty` 定义在 llm_invocation_handler.go:262
- 这两个函数被多个 handler 文件使用，应该提取到共享的 util 文件

**严重程度**: MEDIUM
**建议**: 创建 internal/httpapi/util.go，统一管理工具函数

#### 1.2 分页参数解析重复
**位置**: internal/httpapi/
- finding_handler.go:49-59 (page/size 解析)
- traffic_handler.go:48-58 (page/size 解析)
- config_handler.go:46-50 (page/size 解析)
- tool_handler.go:44-48 (page/size 解析)

**严重程度**: MEDIUM
**建议**: 提取通用的分页参数解析函数

#### 1.3 列表查询模式重复
**位置**: internal/assignment/store.go:73-79
```go
if false {
    q += " WHERE _id=$1"
    args = append(args, "")
}
```
这是死代码，永远不会执行

**严重程度**: HIGH (死代码)
**建议**: 删除死代码

### 2. 错误处理问题

#### 2.1 错误消息不一致
**位置**: internal/httpapi/
- 有些返回 `gin.H{"error": err.Error()}`
- 有些返回 `gin.H{"error": msg, "id": id}`
- 错误响应格式不统一

**严重程度**: MEDIUM
**建议**: 统一错误响应格式

#### 2.2 错误检查方式不一致
**位置**: internal/httpapi/traffic_handler.go:164
```go
if strings.Contains(err.Error(), "no rows in result set")
```
这种字符串匹配不可靠，应该使用类型断言

**严重程度**: HIGH
**建议**: 使用 `errors.Is(err, pgx.ErrNoRows)`

### 3. 抽象问题

#### 3.1 接口定义过于宽泛
**位置**: internal/httpapi/handlers.go
- `TaskAPI` 接口只有 2 个方法，但命名过于通用
- `ScanAPI` 接口只有 1 个方法，是否需要接口化值得商榷

**严重程度**: LOW
**建议**: 评估接口必要性

#### 3.2 scanner 接口重复定义
**位置**: 
- internal/task/store.go:84-86
- internal/assignment/store.go:129-131
- internal/finding/store.go:475-477

相同的 `scanner` 接口在多个包中重复定义

**严重程度**: MEDIUM
**建议**: 提取到共享包或使用泛型

### 4. 类型设计问题

#### 4.1 不必要的类型转换
**位置**: internal/finding/store.go:34
```go
"id, task_id::text AS task_id, ..."
```
task_id 是 UUID，转成 text 后又在代码中当字符串用

**严重程度**: LOW
**建议**: 保持类型一致性

#### 4.2 字符串到枚举的转换散落各处
**位置**: 
- internal/task/store.go:96 `t.Status = Status(status)`
- internal/assignment/store.go:140 `a.Source = Source(source)`

**严重程度**: LOW
**建议**: 考虑在类型上添加 Scan/Value 方法实现 sql.Scanner 接口

### 5. 复杂度问题

#### 5.1 函数过长
**位置**: internal/finding/store.go:126-183 (Update 方法，58 行)
- 动态构建 SQL 的逻辑过于复杂

**严重程度**: MEDIUM
**建议**: 提取 SQL 构建逻辑到独立函数

#### 5.2 SQL 查询构建逻辑复杂
**位置**: internal/traffic/proxy_store.go:158-188
- 动态构建查询条件，可读性差

**严重程度**: MEDIUM
**建议**: 使用 SQL 构建器或提取辅助函数

### 6. 日志问题

#### 6.1 日志级别使用不当
**位置**: internal/finding/store.go:99-108
- dedup_hit 用 Info 级别记录，但这可能是需要关注的事件

**严重程度**: LOW
**建议**: 评估是否应该用 Warn 级别

### 7. 测试覆盖问题

#### 7.1 核心领域缺少单元测试
**位置**: 
- internal/assignment/ - [no test files]
- internal/task/ - [no test files]

**严重程度**: CRITICAL
**建议**: 为核心领域添加单元测试

### 8. 命名和注释问题

#### 8.1 常量命名不统一
**位置**: 
- internal/httpapi/finding_handler.go:28-31 (驼峰命名)
- internal/httpapi/traffic_handler.go:34-37 (驼峰命名)

**严重程度**: LOW
**建议**: 保持命名一致性

### 9. 性能问题

#### 9.1 字符串拼接效率低
**位置**: internal/assignment/store.go:79
```go
q += " ORDER BY created_at DESC LIMIT $" + fmt.Sprint(len(args)+1)
```

**严重程度**: LOW
**建议**: 使用 strings.Builder

### 10. 代码异味

#### 10.1 魔法数字
**位置**: internal/httpapi/finding_handler.go:172
```go
"_id": "",
```
空字符串作为占位符，意图不明确

**严重程度**: LOW
**建议**: 添加注释说明或使用常量

## 优先修复顺序

1. **P0 (Critical)**: 添加核心领域单元测试
2. **P1 (High)**: 删除死代码、修复错误检查
3. **P2 (Medium)**: 消除代码重复、提取工具函数
4. **P3 (Low)**: 优化命名、改进注释

## 总体评价

项目整体结构清晰，采用 DDD 架构，各层职责分明。主要问题集中在：
1. 工具函数和模式的重复
2. 错误处理不够严谨
3. 测试覆盖不足
4. 部分函数复杂度较高

建议按优先级逐步修复。
