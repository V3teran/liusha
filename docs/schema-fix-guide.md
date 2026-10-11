# Schema 修复执行指南

**相关文档**: `docs/schema-audit-2024.md`（完整审查报告）

---

## 一、修复概览

创建了 3 个 migration 文件，修复以下问题：

| Migration | 问题 | 优先级 | 工作量 |
|-----------|------|--------|--------|
| 0162 | agent_run.role 约束不一致 | P0 | 1 分钟 |
| 0163 | exploration 触发器命名残留 | P1 | 1 分钟 |
| 0164 | 缺少业务索引 | P1 | 5-10 分钟 |

**总计**: 约 10-15 分钟

---

## 二、执行步骤

### 方式 A：使用 migrate 工具（推荐）

```bash
cd /Users/Xlbula/workspace/programs/go/liusha

# 1. 检查当前 migration 版本
docker exec liusha-postgres psql -U liusha -d liusha -c \
  "SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1;"

# 当前应该是：0161

# 2. 运行新的 migrations
migrate -database "postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable" \
        -path db/migrations up

# 预期输出：
# 0162/u fix_agent_run_role_constraint (xxxms)
# 0163/u rename_exploration_triggers (xxxms)
# 0164/u add_business_indexes (xxxms)
```

### 方式 B：手动执行（如果没有 migrate 工具）

```bash
cd /Users/Xlbula/workspace/programs/go/liusha

# 1. 执行 0162（修复 role 约束）
docker exec -i liusha-postgres psql -U liusha -d liusha < \
  db/migrations/0162_fix_agent_run_role_constraint.up.sql

# 2. 执行 0163（重命名触发器）
docker exec -i liusha-postgres psql -U liusha -d liusha < \
  db/migrations/0163_rename_exploration_triggers.up.sql

# 3. 执行 0164（添加索引，耗时较长）
docker exec -i liusha-postgres psql -U liusha -d liusha < \
  db/migrations/0164_add_business_indexes.up.sql

# 4. 记录 migration 版本
docker exec liusha-postgres psql -U liusha -d liusha -c \
  "INSERT INTO schema_migrations (version, dirty) VALUES 
   (162, false), (163, false), (164, false);"
```

---

## 三、验证步骤

### 验证 1：agent_run.role 约束

```bash
# 检查约束定义
docker exec liusha-postgres psql -U liusha -d liusha -c "
SELECT conname, pg_get_constraintdef(oid) 
FROM pg_constraint 
WHERE conrelid='agent_run'::regclass AND contype='c' AND conname LIKE '%role%';
"

# ✅ 预期输出：
# agent_run_role_check | CHECK ((role = ANY (ARRAY['planner'::text, 'executor'::text, 'evaluator'::text, 'monitor'::text])))
```

### 验证 2：触发器命名

```bash
# 检查触发器名称
docker exec liusha-postgres psql -U liusha -d liusha -c "
SELECT tgname FROM pg_trigger WHERE tgrelid='exploration_node'::regclass ORDER BY tgname;
"

# ✅ 预期输出（无 wm 前缀）：
# trg_expl_node_increment_version
# trg_expl_node_updated_at
```

### 验证 3：业务索引

```bash
# 检查新增索引
docker exec liusha-postgres psql -U liusha -d liusha -c "
SELECT schemaname, tablename, indexname, indexdef 
FROM pg_indexes 
WHERE indexname LIKE 'idx_finding_%' OR indexname LIKE 'idx_expl_%'
ORDER BY indexname;
"

# ✅ 预期包含：
# idx_expl_node_content_gin
# idx_expl_node_task_kind_state
# idx_finding_severity_status
# idx_finding_target_gin
# idx_finding_target_host
# idx_finding_task_created
```

### 验证 4：索引状态（重要！）

```bash
# 检查是否有 INVALID 索引（CONCURRENTLY 创建失败会留下 INVALID 索引）
docker exec liusha-postgres psql -U liusha -d liusha -c "
SELECT indexrelid::regclass AS index_name, 
       indisvalid AS is_valid
FROM pg_index 
WHERE indrelid IN ('finding'::regclass, 'exploration_node'::regclass)
  AND NOT indisvalid;
"

# ✅ 预期输出：(0 rows)
# ❌ 如果有行，说明索引创建失败，需要 DROP 后重建
```

---

## 四、性能影响评估

### 0162（role 约束）
- ✅ **无性能影响**：仅修改约束定义，不涉及数据重写
- ✅ **无停机时间**：约束修改是原子操作

### 0163（触发器重命名）
- ✅ **无性能影响**：仅重命名元数据
- ✅ **无停机时间**：重命名不阻塞数据操作

### 0164（添加索引）
- ⚠️ **创建期间影响**：
  - CONCURRENTLY 模式：写入性能降低 ~5-10%
  - 预计耗时：5-10 分钟（取决于表大小）
- ✅ **创建后收益**：
  - 查询性能提升 10-100 倍（对覆盖的查询）
  - GIN 索引会增加 ~10-20% 写入开销

**建议**：
- 在低峰期执行 0164
- 监控创建进度：`SELECT * FROM pg_stat_progress_create_index;`

---

## 五、回滚方案

### 回滚 0164（如果索引影响性能）

```bash
# 删除新增索引
docker exec -i liusha-postgres psql -U liusha -d liusha < \
  db/migrations/0164_add_business_indexes.down.sql

# 更新 migration 版本
docker exec liusha-postgres psql -U liusha -d liusha -c \
  "DELETE FROM schema_migrations WHERE version=164;"
```

### 回滚 0163（如果需要）

```bash
docker exec -i liusha-postgres psql -U liusha -d liusha < \
  db/migrations/0163_rename_exploration_triggers.down.sql

docker exec liusha-postgres psql -U liusha -d liusha -c \
  "DELETE FROM schema_migrations WHERE version=163;"
```

### 回滚 0162（不推荐）

```bash
# ⚠️ 不推荐回滚：旧约束与新架构不兼容
docker exec -i liusha-postgres psql -U liusha -d liusha < \
  db/migrations/0162_fix_agent_run_role_constraint.down.sql
```

---

## 六、监控和调优

### 监控索引使用情况（1 周后）

```sql
-- 检查索引是否被使用
SELECT schemaname, tablename, indexname, idx_scan, idx_tup_read, idx_tup_fetch
FROM pg_stat_user_indexes
WHERE indexname LIKE 'idx_finding_%' OR indexname LIKE 'idx_expl_%'
ORDER BY idx_scan;

-- idx_scan = 0 表示索引从未被使用，可考虑删除
```

### 检查索引膨胀

```sql
-- 检查索引大小
SELECT schemaname, tablename, indexname, 
       pg_size_pretty(pg_relation_size(indexrelid)) AS size
FROM pg_stat_user_indexes
WHERE indexname LIKE 'idx_finding_%' OR indexname LIKE 'idx_expl_%'
ORDER BY pg_relation_size(indexrelid) DESC;
```

### 查询性能对比

```sql
-- 测试查询（执行前后对比）
EXPLAIN ANALYZE
SELECT * FROM finding 
WHERE severity='critical' AND status='open'
LIMIT 20;

-- ✅ 修复前：Seq Scan on finding (cost=0.00..1234.56 rows=100 width=500)
-- ✅ 修复后：Index Scan using idx_finding_severity_status (cost=0.42..12.34 rows=10 width=500)
```

---

## 七、常见问题

### Q1: migrate 工具报错 "no change"

**原因**：migrations 已经执行过了

**解决**：
```bash
# 检查当前版本
docker exec liusha-postgres psql -U liusha -d liusha -c \
  "SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 5;"
```

### Q2: CONCURRENTLY 创建索引失败

**原因**：并发事务冲突 / 磁盘空间不足

**解决**：
```bash
# 1. 检查 INVALID 索引
docker exec liusha-postgres psql -U liusha -d liusha -c \
  "SELECT indexrelid::regclass FROM pg_index WHERE NOT indisvalid;"

# 2. 删除 INVALID 索引
docker exec liusha-postgres psql -U liusha -d liusha -c \
  "DROP INDEX CONCURRENTLY idx_finding_xxx;"

# 3. 重新创建
docker exec -i liusha-postgres psql -U liusha -d liusha < \
  db/migrations/0164_add_business_indexes.up.sql
```

### Q3: 索引创建太慢

**原因**：表数据量大 / 磁盘 I/O 慢

**解决**：
```bash
# 1. 监控进度
docker exec liusha-postgres psql -U liusha -d liusha -c \
  "SELECT * FROM pg_stat_progress_create_index;"

# 2. 分批创建（注释掉部分索引）
# 编辑 0164_add_business_indexes.up.sql，只保留一个索引
# 执行后再创建下一个
```

---

## 八、成功标准

执行完成后，应满足：

- ✅ 所有 3 个 migrations 成功执行
- ✅ 无 INVALID 索引
- ✅ agent_run.role 约束包含四个角色
- ✅ exploration 相关对象无 wm 命名残留
- ✅ 6 个新索引已创建
- ✅ 查询性能提升可观测

**验证命令**：
```bash
cd /Users/Xlbula/workspace/programs/go/liusha
./scripts/validate-schema.sh  # 如果有验证脚本
```

---

**执行前备份建议**：
```bash
# 备份数据库（可选，谨慎起见）
docker exec liusha-postgres pg_dump -U liusha liusha > backup_before_migration.sql
```

**执行时间建议**：低峰期（深夜/周末）

**预计停机时间**：0 分钟（CONCURRENTLY 模式）
