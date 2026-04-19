// Package flow 定义单条 HTTP 流量记录的领域模型，镜像 http_flow 数据库表。
package flow

import (
	"time"

	"github.com/google/uuid"
)

// MaxBodyBytes 是单侧 body（req 或 resp）的截断阈值；超出部分丢弃并设 BodyTruncated。
const MaxBodyBytes = 32 * 1024 // 32 KiB

// Flow 镜像 http_flow 表的一行，BodyTruncated=true 表示任意一侧 body 已被截断。
type Flow struct {
	ID              uuid.UUID
	EngagementID    uuid.UUID
	Timestamp       time.Time
	Method          string
	URL             string
	ReqContentType  string
	ReqHeaders      map[string]string
	ReqBody         []byte
	RespStatus      int
	RespContentType string
	RespHeaders     map[string]string
	RespBody        []byte
	BodyTruncated   bool
}

// RawInput 是构造 Flow 的原始输入，body 可以超过阈值。
type RawInput struct {
	EngagementID    uuid.UUID
	Timestamp       time.Time
	Method          string
	URL             string
	ReqContentType  string
	ReqHeaders      map[string]string
	ReqBody         []byte
	RespStatus      int
	RespContentType string
	RespHeaders     map[string]string
	RespBody        []byte
}

// New 构造 Flow，超过 MaxBodyBytes 的 body 就地截断。
func New(in RawInput) Flow {
	f := Flow{
		ID:              uuid.New(),
		EngagementID:    in.EngagementID,
		Timestamp:       in.Timestamp,
		Method:          in.Method,
		URL:             in.URL,
		ReqContentType:  in.ReqContentType,
		ReqHeaders:      in.ReqHeaders,
		ReqBody:         in.ReqBody,
		RespStatus:      in.RespStatus,
		RespContentType: in.RespContentType,
		RespHeaders:     in.RespHeaders,
		RespBody:        in.RespBody,
	}
	if len(f.ReqBody) > MaxBodyBytes {
		f.ReqBody = f.ReqBody[:MaxBodyBytes]
		f.BodyTruncated = true
	}
	if len(f.RespBody) > MaxBodyBytes {
		f.RespBody = f.RespBody[:MaxBodyBytes]
		f.BodyTruncated = true
	}
	return f
}
