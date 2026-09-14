package core

import (
	"encoding/base64"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// FileContent 文件内容（扩展 llm 包未提供的类型）
type FileContent struct {
	// 文件名
	Filename string `json:"filename"`

	// 文件数据（base64 编码或 URL）
	Data string `json:"data"`

	// 数据类型（base64 或 url）
	DataType string `json:"data_type"`

	// MIME 类型
	MIMEType string `json:"mime_type"`

	// 文件大小（字节）
	FileSize int64 `json:"file_size,omitempty"`
}

// DecodeBase64 解码 base64 数据
func (f *FileContent) DecodeBase64() ([]byte, error) {
	if f.Data == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(f.Data)
}

// ContentBuilder 旧版多模态构建器的类型别名（向后兼容）
// 新代码应使用 NewMultimodalMessage + Parts
type MultimodalMessage = Message

// NewContentBuilderCompat 兼容旧代码的构建器（实际使用新 Message）
func NewContentBuilderCompat(role llm.Role) *Message {
	return NewTextMessage("", role, "")
}
