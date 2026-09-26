package rag

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
	"github.com/rs/zerolog"
)

// PostgresVectorStore Postgres+pgvector 向量存储
type PostgresVectorStore struct {
	pool      *pgxpool.Pool
	tableName string
	dimension int
	logger    *zerolog.Logger // 可选：非致命错误（如索引创建失败）经此告警
}

// PostgresVectorStoreConfig Postgres 向量存储配置
type PostgresVectorStoreConfig struct {
	// 连接字符串
	ConnString string

	// 表名（默认 "documents"）
	TableName string

	// 向量维度
	Dimension int

	// Logger 可选日志器（nil 时非致命错误静默）
	Logger *zerolog.Logger
}

// NewPostgresVectorStore 创建 Postgres 向量存储
func NewPostgresVectorStore(ctx context.Context, config PostgresVectorStoreConfig) (*PostgresVectorStore, error) {
	if config.TableName == "" {
		config.TableName = "documents"
	}

	// 创建连接池
	pool, err := pgxpool.New(ctx, config.ConnString)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	store := &PostgresVectorStore{
		pool:      pool,
		tableName: config.TableName,
		dimension: config.Dimension,
		logger:    config.Logger,
	}

	// 初始化表结构
	if err := store.initSchema(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to init schema: %w", err)
	}

	return store, nil
}

// initSchema 初始化数据库表结构
func (s *PostgresVectorStore) initSchema(ctx context.Context) error {
	// 启用 pgvector 扩展
	_, err := s.pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector")
	if err != nil {
		return fmt.Errorf("failed to create vector extension: %w", err)
	}

	// 创建表
	createTableSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id SERIAL PRIMARY KEY,
			content TEXT NOT NULL,
			metadata JSONB,
			embedding vector(%d),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`, s.tableName, s.dimension)

	_, err = s.pool.Exec(ctx, createTableSQL)
	if err != nil {
		return fmt.Errorf("failed to create table: %w", err)
	}

	// 创建向量索引（IVFFlat 索引，适合大规模数据）
	createIndexSQL := fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS %s_embedding_idx ON %s
		USING ivfflat (embedding vector_cosine_ops)
		WITH (lists = 100)
	`, s.tableName, s.tableName)

	_, err = s.pool.Exec(ctx, createIndexSQL)
	if err != nil {
		// 索引创建失败不致命（降级为顺序扫描），但不能静默吞掉
		if s.logger != nil {
			s.logger.Warn().Err(err).Str("table", s.tableName).Msg("向量索引创建失败（降级为顺序扫描）")
		}
	}

	return nil
}

// Add 添加文档及其向量
func (s *PostgresVectorStore) Add(ctx context.Context, documents []Document, vectors [][]float64) error {
	if len(documents) != len(vectors) {
		return fmt.Errorf("documents count (%d) != vectors count (%d)", len(documents), len(vectors))
	}

	// 批量插入
	batch := &pgx.Batch{}
	insertSQL := fmt.Sprintf(`
		INSERT INTO %s (content, metadata, embedding)
		VALUES ($1, $2, $3)
	`, s.tableName)

	for i, doc := range documents {
		// 转换向量为 pgvector 格式
		vec := pgvector.NewVector(convertToFloat32(vectors[i]))
		batch.Queue(insertSQL, doc.Content, doc.Metadata, vec)
	}

	// 执行批量插入
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()

	for i := 0; i < len(documents); i++ {
		_, err := br.Exec()
		if err != nil {
			return fmt.Errorf("failed to insert document %d: %w", i, err)
		}
	}

	return nil
}

// Search 向量相似度搜索
func (s *PostgresVectorStore) Search(ctx context.Context, queryVector []float64, topK int) ([]Document, error) {
	// 使用余弦相似度搜索
	searchSQL := fmt.Sprintf(`
		SELECT content, metadata, 1 - (embedding <=> $1) AS score
		FROM %s
		WHERE embedding IS NOT NULL
		ORDER BY embedding <=> $1
		LIMIT $2
	`, s.tableName)

	vec := pgvector.NewVector(convertToFloat32(queryVector))
	rows, err := s.pool.Query(ctx, searchSQL, vec, topK)
	if err != nil {
		return nil, fmt.Errorf("search query failed: %w", err)
	}
	defer rows.Close()

	documents := []Document{}
	for rows.Next() {
		var doc Document
		var score float64

		err := rows.Scan(&doc.Content, &doc.Metadata, &score)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		doc.Score = score
		documents = append(documents, doc)
	}

	if rows.Err() != nil {
		return nil, fmt.Errorf("rows iteration error: %w", rows.Err())
	}

	return documents, nil
}

// Delete 删除文档
func (s *PostgresVectorStore) Delete(ctx context.Context, ids []string) error {
	// 注意：当前实现使用自增 ID，这里简化处理
	// 生产环境应该使用业务 ID
	deleteSQL := fmt.Sprintf("DELETE FROM %s WHERE id = ANY($1)", s.tableName)
	_, err := s.pool.Exec(ctx, deleteSQL, ids)
	return err
}

// Clear 清空所有文档
func (s *PostgresVectorStore) Clear(ctx context.Context) error {
	truncateSQL := fmt.Sprintf("TRUNCATE TABLE %s", s.tableName)
	_, err := s.pool.Exec(ctx, truncateSQL)
	return err
}

// Close 关闭连接
func (s *PostgresVectorStore) Close() {
	s.pool.Close()
}

// convertToFloat32 将 []float64 转换为 []float32
func convertToFloat32(f64 []float64) []float32 {
	f32 := make([]float32, len(f64))
	for i, v := range f64 {
		f32[i] = float32(v)
	}
	return f32
}

// PostgresRetriever Postgres 检索器（组合 Embedding + VectorStore）
type PostgresRetriever struct {
	embedding   Embedding
	vectorStore *PostgresVectorStore
}

// NewPostgresRetriever 创建 Postgres 检索器
func NewPostgresRetriever(embedding Embedding, vectorStore *PostgresVectorStore) *PostgresRetriever {
	return &PostgresRetriever{
		embedding:   embedding,
		vectorStore: vectorStore,
	}
}

// Retrieve 检索相关文档
func (r *PostgresRetriever) Retrieve(ctx context.Context, query string, topK int) ([]Document, error) {
	// 1. 生成查询向量
	queryVector, err := r.embedding.EmbedText(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to embed query: %w", err)
	}

	// 2. 向量搜索
	return r.vectorStore.Search(ctx, queryVector, topK)
}

// RetrieveWithScore 检索相关文档（带分数）
func (r *PostgresRetriever) RetrieveWithScore(ctx context.Context, query string, topK int, minScore float64) ([]Document, error) {
	docs, err := r.Retrieve(ctx, query, topK)
	if err != nil {
		return nil, err
	}

	// 过滤低分文档
	filtered := []Document{}
	for _, doc := range docs {
		if doc.Score >= minScore {
			filtered = append(filtered, doc)
		}
	}

	return filtered, nil
}

// AddDocuments 添加文档到向量库
func (r *PostgresRetriever) AddDocuments(ctx context.Context, documents []Document) error {
	if len(documents) == 0 {
		return nil
	}

	// 提取文本
	texts := make([]string, len(documents))
	for i, doc := range documents {
		texts[i] = doc.Content
	}

	// 批量生成向量
	vectors, err := r.embedding.EmbedTexts(ctx, texts)
	if err != nil {
		return fmt.Errorf("failed to embed texts: %w", err)
	}

	// 添加到向量库
	return r.vectorStore.Add(ctx, documents, vectors)
}
