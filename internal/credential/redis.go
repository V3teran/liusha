package credential

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/V3teran/liusha/internal/logx"
)

// credLog 是本包共享 logger（与 finding 等存储包同一模式）。
var credLog = logx.New("credential")

// fallbackHashKeyPrefix 在 caller 传入空 keyPrefix 时使用——保持与历史一致的
// "credentials:<host>" 数据布局，避免老 redis 数据 key 漂移。
//
// 数据布局：
//
//	HSET <prefix><host> <name1> <json1> <name2> <json2> ...
//	HKEYS  <prefix><host>          // 枚举只读 field 名，不触碰凭证值
//	HGETALL <prefix><host>         // 一次往返拿全部 (name, json)
//	HDEL   <prefix><host> <name>   // 单身份删除
//	DEL    <prefix><host>          // 删整个 host 凭证
//
// 本命名空间只承载凭证 hash。信号量/聚合窗口等运行时键一律用各自的
// 前缀，禁止借用本前缀——枚举（SCAN）依赖命名空间的纯净。
const fallbackHashKeyPrefix = "credentials:"

// CanonicalHost 把目标地址归一到凭证库的 scope 键：`host[:port]`（小写主机、
// 保留端口；剥 scheme/userinfo/路径/query）。
//
// 端口参与隔离的理由：凭证库管理的是**目标应用的登录态**——同一主机的
// 不同端口几乎总是不同应用（不同源），会话材料互不通用；把 A 端口应用的
// 凭证注入 B 端口只会污染验证（跨上下文误注入曾整体证伪过真实假设）。
// Cookie 协议本身虽不隔离端口，那是浏览器实现细节；凭证库按应用（端点）
// 建模，与主流安全工具的站点/服务维度一致。
//
// 归一必须在存储边界单点完成（hashKey 是全部读写的唯一通道）：同一目标若因
// 调用方书写形态不同（带不带 scheme/路径）而散落多个键，任一读取方都只能
// 看到局部凭据——权威登录态与自动会话快照互相不可见，重放会拿到过期会话。
func CanonicalHost(addr string) string {
	h := strings.TrimSpace(addr)
	if h == "" {
		return ""
	}
	h = strings.ToLower(h)
	// scheme:// 前缀
	if i := strings.Index(h, "://"); i >= 0 {
		h = h[i+3:]
	}
	// userinfo@
	if i := strings.LastIndex(h, "@"); i >= 0 {
		h = h[i+1:]
	}
	// 路径 / query / fragment
	if i := strings.IndexAny(h, "/?#"); i >= 0 {
		h = h[:i]
	}
	// 尾点去除（DNS 绝对名书写形态）；端口保留。
	if i := strings.Index(h, "]"); i >= 0 {
		// IPv6 字面量：剥方括号后原样保留（含其端口）。
		rest := h[i+1:]
		host := strings.Trim(h[:i+1], "[]")
		if strings.HasPrefix(rest, ":") && isAllDigits(rest[1:]) {
			return host + rest
		}
		return host
	}
	return strings.TrimSuffix(h, ".")
}

// RedisProvider 是 Provider 的 Redis 实现。
//
// keyPrefix 由 cmd 层从 config.Credential.RedisKeyPrefix 注入；
// 多项目共享 redis 时通过 yaml 改前缀即可隔离命名空间。
type RedisProvider struct {
	client    *redis.Client
	keyPrefix string
}

// NewRedis 构造 RedisProvider；keyPrefix 空时回退 fallbackHashKeyPrefix。
func NewRedis(client *redis.Client, keyPrefix string) *RedisProvider {
	if keyPrefix == "" {
		keyPrefix = fallbackHashKeyPrefix
	}
	return &RedisProvider{client: client, keyPrefix: keyPrefix}
}

// hashKey 拼接 host hash 的完整 Redis key。host 经 CanonicalHost 归一——
// 所有写读共用此单点，调用方传 "host"、"host:port" 或完整 URL 落到同一键。
func (r *RedisProvider) hashKey(host string) string {
	return r.keyPrefix + CanonicalHost(host)
}

// SaveIdentities 按 Name 覆盖写（upsert）。时间戳语义：
//   - CreatedAt（录入时间）：同名身份首次出现的时刻——调用方显式指定
//     （历史回填）> 既有 field 继承 > 当前时间；
//   - UpdatedAt（修改时间）：每次保存刷新为当前时间；调用方显式指定
//     （历史回填）优先。
//
// 永久存储，无任何过期语义。
func (r *RedisProvider) SaveIdentities(ctx context.Context, host string, ids []Identity) error {
	key := r.hashKey(host)
	fields := make(map[string]any, len(ids))
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		// 防御性跳过：anonymous 不是持久化身份（是 LLM 临时构造的测试概念），
		// caller 误传也不污染存储。
		if id.Username == AnonymousName {
			continue
		}
		names = append(names, id.Username)
	}
	if len(names) == 0 {
		return nil
	}

	// 继承既有录入时间：HMGET 只取本批名字的现值（不读整 hash）。
	existing, err := r.client.HMGet(ctx, key, names...).Result()
	if err != nil {
		return fmt.Errorf("hmget credentials for host %s: %w", host, err)
	}
	firstSeen := make(map[string]time.Time, len(names))
	for i, raw := range existing {
		if raw == nil {
			continue
		}
		var prev Identity
		if err := json.Unmarshal([]byte(raw.(string)), &prev); err == nil && prev.CreatedAt != nil {
			firstSeen[names[i]] = *prev.CreatedAt
		}
	}

	now := time.Now()
	for _, id := range ids {
		if id.Username == AnonymousName {
			continue
		}
		// 用户名单点归一：trim + 小写——同名（含大小写/空白变体）在同一
		// 目标键下唯一是本库不变式（HSET field=用户名），归一杜绝 "Admin"/
		// "admin " 类变体拆成两个登录身份。
		id.Username = strings.ToLower(strings.TrimSpace(id.Username))
		if id.Username == "" {
			continue
		}
		if id.CreatedAt == nil {
			if at, ok := firstSeen[id.Username]; ok {
				id.CreatedAt = &at
			} else {
				t := now
				id.CreatedAt = &t
			}
		}
		if id.UpdatedAt == nil {
			t := now
			id.UpdatedAt = &t
		}
		payload, err := json.Marshal(id)
		if err != nil {
			return fmt.Errorf("marshal identity %s/%s: %w", host, id.Username, err)
		}
		fields[id.Username] = payload
	}
	if err := r.client.HSet(ctx, key, fields).Err(); err != nil {
		return fmt.Errorf("save credentials for host %s: %w", host, err)
	}
	return nil
}

// GetIdentitiesByHost 返回 host 下全部登录身份（录入 + 会话），按用户名排序
// 返回（HGETALL 返回序任意，稳定序让枚举/注入的行为可复现）。
// 反序列化失败的字段会被跳过（容错），避免脏数据阻塞读取。
func (r *RedisProvider) GetIdentitiesByHost(ctx context.Context, host string) ([]Identity, error) {
	out := []Identity{}

	res, err := r.client.HGetAll(ctx, r.hashKey(host)).Result()
	if err != nil {
		// hash 不存在不算错（首次访问 host）；其他错误向上传播。
		if errors.Is(err, redis.Nil) {
			return out, nil
		}
		return nil, fmt.Errorf("hgetall credentials for host %s: %w", host, err)
	}
	for _, payload := range res {
		var id Identity
		// 反序列化失败容错跳过：脏数据不应阻塞业务，但要留痕（静默丢弃会掩盖写入端损坏）。
		if err := json.Unmarshal([]byte(payload), &id); err != nil {
			credLog.Warn().Err(err).Str("host", host).Msg("credential 反序列化失败，跳过脏数据")
			continue
		}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out, nil
}

// DeleteIdentity 删除单个登录身份（field 级）。field 不存在时幂等成功。
func (r *RedisProvider) DeleteIdentity(ctx context.Context, host, name string) error {
	if err := r.client.HDel(ctx, r.hashKey(host), name).Err(); err != nil {
		return fmt.Errorf("hdel credential %s/%s: %w", host, name, err)
	}
	return nil
}

// DeleteSessions 删除 host 下全部会话用户名，返回删除数（录入身份不动）。
func (r *RedisProvider) DeleteSessions(ctx context.Context, host string) (int, error) {
	key := r.hashKey(host)
	names, err := r.client.HKeys(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("hkeys credentials for host %s: %w", host, err)
	}
	var sessionFields []string
	for _, n := range names {
		if IsSessionIdentity(n) {
			sessionFields = append(sessionFields, n)
		}
	}
	if len(sessionFields) == 0 {
		return 0, nil
	}
	if err := r.client.HDel(ctx, key, sessionFields...).Err(); err != nil {
		return 0, fmt.Errorf("hdel sessions for host %s: %w", host, err)
	}
	return len(sessionFields), nil
}

// DeleteHost 清空 host 下所有登录身份（含会话）。幂等。
func (r *RedisProvider) DeleteHost(ctx context.Context, host string) error {
	if err := r.client.Del(ctx, r.hashKey(host)).Err(); err != nil {
		return fmt.Errorf("del credentials for host %s: %w", host, err)
	}
	return nil
}

// ListHosts 枚举有凭证的 host：SCAN 前缀匹配 + 逐键类型校验（只认 hash——
// 命名空间纯净是常态，类型过滤是防御），每 host 一次 HKEYS 分类计数。
// 全程不读任何 field 值——枚举层不接触秘密材料。
func (r *RedisProvider) ListHosts(ctx context.Context) ([]HostSummary, error) {
	pattern := r.keyPrefix + "*"
	var keys []string
	var cursor uint64
	for {
		batch, next, err := r.client.Scan(ctx, cursor, pattern, 200).Result()
		if err != nil {
			return nil, fmt.Errorf("scan credentials: %w", err)
		}
		keys = append(keys, batch...)
		if next == 0 {
			break
		}
		cursor = next
	}

	out := make([]HostSummary, 0, len(keys))
	for _, key := range keys {
		kind, err := r.client.Type(ctx, key).Result()
		if err != nil || kind != "hash" {
			continue // 非 hash（历史残留/借名键）不属于凭证库
		}
		names, err := r.client.HKeys(ctx, key).Result()
		if err != nil {
			continue
		}
		summary := HostSummary{Host: key[len(r.keyPrefix):]}
		for _, n := range names {
			if IsSessionIdentity(n) {
				summary.Sessions++
			} else {
				summary.Usernames++
			}
		}
		if summary.Usernames+summary.Sessions == 0 {
			continue // 空 hash 不上枚举
		}
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out, nil
}

// LegacyRuntimeKeys 返回历史上借用凭证前缀的运行时键（聚合窗口活跃 host
// 集，无 TTL 的那一枚）。命名空间迁出后由启动流程一次性清理；计数/锁类
// 键自带短 TTL，自然消亡，无需处理。
func (r *RedisProvider) LegacyRuntimeKeys() []string {
	return []string{r.keyPrefix + ":agg:hosts"}
}

// 编译期保障：RedisProvider 满足 Provider 接口。
var _ Provider = (*RedisProvider)(nil)

// isAllDigits 判定 s 非空且全为数字（端口段判定）。
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
