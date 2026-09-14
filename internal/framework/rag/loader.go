package rag

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// DocumentLoader 文档加载器接口
type DocumentLoader interface {
	// Load 加载文档
	Load(ctx context.Context) ([]Document, error)
}

// TextSplitter 文本分割器接口
type TextSplitter interface {
	// Split 分割文本为多个片段
	Split(text string) []string

	// SplitDocuments 分割文档
	SplitDocuments(documents []Document) []Document
}

// ─────────────────────────────────────────────
//  文本加载器
// ─────────────────────────────────────────────

// TextLoader 纯文本加载器
type TextLoader struct {
	filePath string
	metadata map[string]any
}

// NewTextLoader 创建文本加载器
func NewTextLoader(filePath string) *TextLoader {
	return &TextLoader{
		filePath: filePath,
		metadata: make(map[string]any),
	}
}

// WithMetadata 设置元数据
func (l *TextLoader) WithMetadata(key string, value any) *TextLoader {
	l.metadata[key] = value
	return l
}

// Load 加载文档
func (l *TextLoader) Load(ctx context.Context) ([]Document, error) {
	data, err := os.ReadFile(l.filePath)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}

	metadata := make(map[string]any)
	for k, v := range l.metadata {
		metadata[k] = v
	}
	metadata["source"] = l.filePath

	doc := Document{
		Content:  string(data),
		Metadata: metadata,
	}

	return []Document{doc}, nil
}

// ─────────────────────────────────────────────
//  Reader 加载器
// ─────────────────────────────────────────────

// ReaderLoader 从 io.Reader 加载
type ReaderLoader struct {
	reader   io.Reader
	metadata map[string]any
}

// NewReaderLoader 创建 Reader 加载器
func NewReaderLoader(reader io.Reader) *ReaderLoader {
	return &ReaderLoader{
		reader:   reader,
		metadata: make(map[string]any),
	}
}

// WithMetadata 设置元数据
func (l *ReaderLoader) WithMetadata(key string, value any) *ReaderLoader {
	l.metadata[key] = value
	return l
}

// Load 加载文档
func (l *ReaderLoader) Load(ctx context.Context) ([]Document, error) {
	data, err := io.ReadAll(l.reader)
	if err != nil {
		return nil, fmt.Errorf("读取数据失败: %w", err)
	}

	doc := Document{
		Content:  string(data),
		Metadata: l.metadata,
	}

	return []Document{doc}, nil
}

// ─────────────────────────────────────────────
//  递归字符分割器
// ─────────────────────────────────────────────

// RecursiveCharacterSplitter 递归字符分割器
// 优先使用段落、句子、单词边界，保持语义连贯性
type RecursiveCharacterSplitter struct {
	chunkSize    int      // 块大小（字符数）
	chunkOverlap int      // 块重叠（字符数）
	separators   []string // 分隔符优先级（递归尝试）
	keepSep      bool     // 保留分隔符
}

// NewRecursiveCharacterSplitter 创建递归字符分割器
func NewRecursiveCharacterSplitter(chunkSize, chunkOverlap int) *RecursiveCharacterSplitter {
	return &RecursiveCharacterSplitter{
		chunkSize:    chunkSize,
		chunkOverlap: chunkOverlap,
		separators: []string{
			"\n\n", // 段落
			"\n",   // 行
			"。",   // 中文句号
			". ",   // 英文句号
			"？",   // 中文问号
			"? ",   // 英文问号
			"！",   // 中文感叹号
			"! ",   // 英文感叹号
			"；",   // 中文分号
			"; ",   // 英文分号
			"，",   // 中文逗号
			", ",   // 英文逗号
			" ",    // 空格
			"",     // 字符
		},
		keepSep: false,
	}
}

// WithSeparators 自定义分隔符
func (s *RecursiveCharacterSplitter) WithSeparators(separators []string) *RecursiveCharacterSplitter {
	s.separators = separators
	return s
}

// WithKeepSeparator 保留分隔符
func (s *RecursiveCharacterSplitter) WithKeepSeparator(keep bool) *RecursiveCharacterSplitter {
	s.keepSep = keep
	return s
}

// Split 分割文本
func (s *RecursiveCharacterSplitter) Split(text string) []string {
	if utf8.RuneCountInString(text) <= s.chunkSize {
		return []string{text}
	}

	return s.recursiveSplit(text, s.separators)
}

// recursiveSplit 递归分割
func (s *RecursiveCharacterSplitter) recursiveSplit(text string, separators []string) []string {
	if len(separators) == 0 {
		// 无分隔符，按字符强制分割
		return s.splitByLength(text)
	}

	sep := separators[0]
	remaining := separators[1:]

	if sep == "" {
		// 字符级分割
		return s.splitByLength(text)
	}

	// 按当前分隔符分割
	parts := strings.Split(text, sep)

	// 如果分隔符不存在，尝试下一个
	if len(parts) == 1 {
		return s.recursiveSplit(text, remaining)
	}

	var chunks []string
	currentChunk := ""

	for _, part := range parts {
		// 跳过空部分
		if part == "" {
			continue
		}

		// 构造测试块（加回分隔符）
		testChunk := currentChunk
		if testChunk != "" && sep != "" {
			testChunk += sep
		}
		testChunk += part

		if utf8.RuneCountInString(testChunk) <= s.chunkSize {
			currentChunk = testChunk
		} else {
			// 当前块已满
			if currentChunk != "" {
				chunks = append(chunks, strings.TrimSpace(currentChunk))
			}

			// 如果单个 part 仍然过大，递归分割
			if utf8.RuneCountInString(part) > s.chunkSize {
				subChunks := s.recursiveSplit(part, remaining)
				chunks = append(chunks, subChunks...)
				currentChunk = ""
			} else {
				currentChunk = part
			}
		}
	}

	if currentChunk != "" {
		chunks = append(chunks, strings.TrimSpace(currentChunk))
	}

	// 如果没有成功分割，尝试下一个分隔符
	if len(chunks) <= 1 && len(remaining) > 0 {
		return s.recursiveSplit(text, remaining)
	}

	// 应用重叠
	return s.applyOverlap(chunks)
}

// splitByLength 按长度强制分割
func (s *RecursiveCharacterSplitter) splitByLength(text string) []string {
	runes := []rune(text)
	var chunks []string

	for i := 0; i < len(runes); i += s.chunkSize {
		end := i + s.chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))
	}

	return s.applyOverlap(chunks)
}

// applyOverlap 应用重叠
func (s *RecursiveCharacterSplitter) applyOverlap(chunks []string) []string {
	if s.chunkOverlap <= 0 || len(chunks) <= 1 {
		return chunks
	}

	result := make([]string, 0, len(chunks))

	for i, chunk := range chunks {
		if i == 0 {
			result = append(result, chunk)
			continue
		}

		// 从前一块取重叠部分
		prevChunk := chunks[i-1]
		prevRunes := []rune(prevChunk)

		overlapStart := len(prevRunes) - s.chunkOverlap
		if overlapStart < 0 {
			overlapStart = 0
		}

		overlap := string(prevRunes[overlapStart:])
		merged := overlap + chunk

		result = append(result, merged)
	}

	return result
}

// SplitDocuments 分割文档
func (s *RecursiveCharacterSplitter) SplitDocuments(documents []Document) []Document {
	var result []Document

	for _, doc := range documents {
		chunks := s.Split(doc.Content)

		for i, chunk := range chunks {
			metadata := make(map[string]any)
			for k, v := range doc.Metadata {
				metadata[k] = v
			}
			metadata["chunk_index"] = i
			metadata["chunk_total"] = len(chunks)

			result = append(result, Document{
				Content:  chunk,
				Metadata: metadata,
			})
		}
	}

	return result
}

// ─────────────────────────────────────────────
//  固定长度分割器
// ─────────────────────────────────────────────

// FixedLengthSplitter 固定长度分割器
type FixedLengthSplitter struct {
	chunkSize    int
	chunkOverlap int
}

// NewFixedLengthSplitter 创建固定长度分割器
func NewFixedLengthSplitter(chunkSize, chunkOverlap int) *FixedLengthSplitter {
	return &FixedLengthSplitter{
		chunkSize:    chunkSize,
		chunkOverlap: chunkOverlap,
	}
}

// Split 分割文本
func (s *FixedLengthSplitter) Split(text string) []string {
	runes := []rune(text)
	var chunks []string

	step := s.chunkSize - s.chunkOverlap
	if step <= 0 {
		step = s.chunkSize
	}

	for i := 0; i < len(runes); i += step {
		end := i + s.chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))

		if end >= len(runes) {
			break
		}
	}

	return chunks
}

// SplitDocuments 分割文档
func (s *FixedLengthSplitter) SplitDocuments(documents []Document) []Document {
	var result []Document

	for _, doc := range documents {
		chunks := s.Split(doc.Content)

		for i, chunk := range chunks {
			metadata := make(map[string]any)
			for k, v := range doc.Metadata {
				metadata[k] = v
			}
			metadata["chunk_index"] = i
			metadata["chunk_total"] = len(chunks)

			result = append(result, Document{
				Content:  chunk,
				Metadata: metadata,
			})
		}
	}

	return result
}
