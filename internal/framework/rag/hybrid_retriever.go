package rag

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/go-ego/gse"
)

// HybridRetriever 混合检索器（结合关键词和语义检索）
type HybridRetriever struct {
	vectorRetriever  Retriever
	keywordRetriever KeywordRetriever
	weights          HybridWeights
}

// HybridWeights 混合检索权重配置
type HybridWeights struct {
	// Vector 语义检索权重（0-1）
	Vector float64

	// Keyword 关键词检索权重（0-1）
	Keyword float64
}

// NewHybridRetriever 创建混合检索器
func NewHybridRetriever(vectorRetriever Retriever, keywordRetriever KeywordRetriever, weights HybridWeights) *HybridRetriever {
	// 归一化权重
	total := weights.Vector + weights.Keyword
	if total == 0 {
		weights.Vector = 0.5
		weights.Keyword = 0.5
	} else {
		weights.Vector = weights.Vector / total
		weights.Keyword = weights.Keyword / total
	}

	return &HybridRetriever{
		vectorRetriever:  vectorRetriever,
		keywordRetriever: keywordRetriever,
		weights:          weights,
	}
}

// Retrieve 混合检索
func (r *HybridRetriever) Retrieve(ctx context.Context, query string, topK int) ([]Document, error) {
	// 1. 语义检索
	vectorResults, err := r.vectorRetriever.Retrieve(ctx, query, topK*2)
	if err != nil {
		return nil, fmt.Errorf("vector retrieval failed: %w", err)
	}

	// 2. 关键词检索
	keywordResults, err := r.keywordRetriever.Search(ctx, query, topK*2)
	if err != nil {
		return nil, fmt.Errorf("keyword retrieval failed: %w", err)
	}

	// 3. 合并结果（倒数排序融合）
	merged := r.reciprocalRankFusion(vectorResults, keywordResults)

	// 4. 截取 Top K
	if len(merged) > topK {
		merged = merged[:topK]
	}

	return merged, nil
}

// RetrieveWithScore 混合检索（带分数过滤）
func (r *HybridRetriever) RetrieveWithScore(ctx context.Context, query string, topK int, minScore float64) ([]Document, error) {
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

// reciprocalRankFusion 倒数排序融合算法
// RRF(d) = Σ 1 / (k + rank(d))
func (r *HybridRetriever) reciprocalRankFusion(vectorResults, keywordResults []Document) []Document {
	const k = 60 // 常数，用于平滑排名

	// 文档 ID -> 综合分数
	scores := make(map[string]float64)
	docMap := make(map[string]Document)

	// 计算语义检索的 RRF 分数
	for rank, doc := range vectorResults {
		id := doc.ID
		if id == "" {
			id = doc.Content // 使用内容作为 ID
		}
		scores[id] += r.weights.Vector / float64(k+rank+1)
		docMap[id] = doc
	}

	// 计算关键词检索的 RRF 分数
	for rank, doc := range keywordResults {
		id := doc.ID
		if id == "" {
			id = doc.Content
		}
		scores[id] += r.weights.Keyword / float64(k+rank+1)
		if _, exists := docMap[id]; !exists {
			docMap[id] = doc
		}
	}

	// 构建结果列表
	type scoredDoc struct {
		doc   Document
		score float64
	}

	results := make([]scoredDoc, 0, len(scores))
	for id, score := range scores {
		doc := docMap[id]
		doc.Score = score
		results = append(results, scoredDoc{doc: doc, score: score})
	}

	// 按分数降序排序
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	// 提取文档
	docs := make([]Document, len(results))
	for i, r := range results {
		docs[i] = r.doc
	}

	return docs
}

// ──────────────────────────────────────────────────────
//  关键词检索（BM25 算法）
// ──────────────────────────────────────────────────────

// KeywordRetriever 关键词检索器接口
type KeywordRetriever interface {
	// Search 关键词搜索
	Search(ctx context.Context, query string, topK int) ([]Document, error)

	// Index 索引文档
	Index(ctx context.Context, documents []Document) error
}

// BM25Retriever BM25 算法检索器
type BM25Retriever struct {
	documents []Document
	index     *BM25Index
}

// BM25Index BM25 索引
type BM25Index struct {
	// 文档总数
	numDocs int

	// 平均文档长度
	avgDocLen float64

	// 文档长度
	docLens []int

	// 词频统计 (文档ID -> 词 -> 频率)
	termFreqs []map[string]int

	// 逆文档频率 (词 -> IDF 值)
	idf map[string]float64

	// BM25 参数
	k1 float64 // 词频饱和参数 (通常 1.2-2.0)
	b  float64 // 长度归一化参数 (通常 0.75)
}

// NewBM25Retriever 创建 BM25 检索器
func NewBM25Retriever() *BM25Retriever {
	return &BM25Retriever{
		documents: []Document{},
		index: &BM25Index{
			k1: 1.5,
			b:  0.75,
		},
	}
}

// Index 索引文档
func (r *BM25Retriever) Index(ctx context.Context, documents []Document) error {
	r.documents = documents
	r.index.numDocs = len(documents)

	// 初始化
	r.index.docLens = make([]int, len(documents))
	r.index.termFreqs = make([]map[string]int, len(documents))
	r.index.idf = make(map[string]float64)

	totalLen := 0
	docFreq := make(map[string]int) // 词 -> 包含该词的文档数

	// 第一遍：计算词频和文档频率
	for i, doc := range documents {
		terms := tokenize(doc.Content)
		r.index.docLens[i] = len(terms)
		totalLen += len(terms)

		termFreq := make(map[string]int)
		termSet := make(map[string]bool)

		for _, term := range terms {
			termFreq[term]++
			termSet[term] = true
		}

		r.index.termFreqs[i] = termFreq

		// 记录包含该词的文档
		for term := range termSet {
			docFreq[term]++
		}
	}

	// 计算平均文档长度
	if len(documents) > 0 {
		r.index.avgDocLen = float64(totalLen) / float64(len(documents))
	}

	// 第二遍：计算 IDF
	for term, df := range docFreq {
		// 标准 IDF 公式: log((N - df + 0.5) / (df + 0.5) + 1)
		// 为避免负数，使用: log(N / df)
		idf := math.Log(float64(r.index.numDocs) / float64(df))
		r.index.idf[term] = idf
	}

	return nil
}

// Search 关键词搜索
func (r *BM25Retriever) Search(ctx context.Context, query string, topK int) ([]Document, error) {
	if r.index.numDocs == 0 {
		return []Document{}, nil
	}

	// 分词
	queryTerms := tokenize(query)

	// 计算每个文档的 BM25 分数
	type scoredDoc struct {
		doc   Document
		score float64
	}

	results := make([]scoredDoc, 0, len(r.documents))

	for i, doc := range r.documents {
		score := r.computeBM25Score(queryTerms, i)
		if score > 0 {
			doc.Score = score
			results = append(results, scoredDoc{doc: doc, score: score})
		}
	}

	// 按分数降序排序
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	// 截取 Top K
	if len(results) > topK {
		results = results[:topK]
	}

	// 提取文档
	docs := make([]Document, len(results))
	for i, r := range results {
		docs[i] = r.doc
	}

	return docs, nil
}

// Retrieve 实现 Retriever 接口（别名到 Search）
func (r *BM25Retriever) Retrieve(ctx context.Context, query string, topK int) ([]Document, error) {
	return r.Search(ctx, query, topK)
}

// RetrieveWithScore 实现 Retriever 接口
func (r *BM25Retriever) RetrieveWithScore(ctx context.Context, query string, topK int) ([]Document, error) {
	return r.Search(ctx, query, topK)
}

// computeBM25Score 计算文档的 BM25 分数
func (r *BM25Retriever) computeBM25Score(queryTerms []string, docIndex int) float64 {
	score := 0.0
	docLen := float64(r.index.docLens[docIndex])
	termFreq := r.index.termFreqs[docIndex]

	for _, term := range queryTerms {
		// 词频
		tf := float64(termFreq[term])
		if tf == 0 {
			continue
		}

		// IDF
		idf := r.index.idf[term]

		// BM25 公式
		// score = IDF * (tf * (k1 + 1)) / (tf + k1 * (1 - b + b * (docLen / avgDocLen)))
		numerator := tf * (r.index.k1 + 1)
		denominator := tf + r.index.k1*(1-r.index.b+r.index.b*(docLen/r.index.avgDocLen))
		score += idf * (numerator / denominator)
	}

	return score
}

var (
	segmenter     gse.Segmenter
	segmenterOnce sync.Once
)

// initSegmenter 初始化分词器（单例）
func initSegmenter() {
	segmenterOnce.Do(func() {
		segmenter.LoadDict() // 加载默认词典
	})
}

// tokenize 分词（支持中英文）
func tokenize(text string) []string {
	// 转小写
	text = strings.ToLower(text)

	// 尝试使用 gse 分词（中文）
	tokens := tokenizeWithGse(text)
	if len(tokens) > 0 {
		return filterStopWords(tokens)
	}

	// 降级到简单分词（英文或分词失败）
	return tokenizeSimple(text)
}

// tokenizeWithGse 使用 gse 分词
func tokenizeWithGse(text string) []string {
	// 检测是否包含中文
	if !containsChinese(text) {
		return nil // 不是中文，返回 nil 让调用者使用简单分词
	}

	// 初始化分词器
	initSegmenter()

	// 分词
	segments := segmenter.Cut(text, true) // true = 搜索引擎模式
	return segments
}

// tokenizeSimple 简单分词（按空格和标点分割）
func tokenizeSimple(text string) []string {
	var tokens []string
	var current strings.Builder

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(r)
		} else {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		}
	}

	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return filterStopWords(tokens)
}

// containsChinese 检测文本是否包含中文
func containsChinese(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// filterStopWords 过滤停用词
func filterStopWords(tokens []string) []string {
	// 英文停用词
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true,
		"is": true, "are": true, "was": true, "were": true, "be": true,
		"to": true, "of": true, "in": true, "on": true, "at": true,
		"for": true, "with": true, "by": true, "from": true,
	}

	// 中文停用词
	chineseStopWords := map[string]bool{
		"的": true, "了": true, "在": true, "是": true, "我": true,
		"有": true, "和": true, "就": true, "不": true, "人": true,
		"都": true, "一": true, "一个": true, "上": true, "也": true,
		"很": true, "到": true, "说": true, "要": true, "去": true,
		"你": true, "会": true, "着": true, "没有": true, "看": true,
	}

	filtered := []string{}
	for _, token := range tokens {
		if len(token) > 1 && !stopWords[token] && !chineseStopWords[token] {
			filtered = append(filtered, token)
		}
	}

	return filtered
}
