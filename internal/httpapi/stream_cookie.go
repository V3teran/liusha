// stream_cookie.go：SSE stream 端点的短时效签名 cookie。
//
// EventSource 不能带自定义 header，故 POST /chat 下发此 cookie，stream 端点据此鉴权。
// token = {convID}.{expUnix}.{hexHMAC}，绑 convID（泄露只影响单会话）+ 过期时间。
package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// streamCookieName 是 SSE 鉴权 cookie 名。
const streamCookieName = "liusha_stream"

// signStreamToken 签发绑定 convID + 过期时间的 token。
func signStreamToken(secret []byte, convID string, exp time.Time) string {
	if len(secret) == 0 || convID == "" {
		return ""
	}
	payload := convID + "." + strconv.FormatInt(exp.Unix(), 10)
	return payload + "." + hexHMAC(secret, payload)
}

// verifyStreamToken 校验 token 的签名、过期、convID 绑定。
func verifyStreamToken(secret []byte, token, convID string) bool {
	if len(secret) == 0 || token == "" {
		return false
	}
	i := strings.LastIndex(token, ".")
	if i < 0 {
		return false
	}
	payload, sig := token[:i], token[i+1:]
	// 常量时间比签名。
	if subtle.ConstantTimeCompare([]byte(sig), []byte(hexHMAC(secret, payload))) != 1 {
		return false
	}
	parts := strings.Split(payload, ".")
	if len(parts) != 2 || parts[0] != convID {
		return false
	}
	expUnix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > expUnix {
		return false
	}
	return true
}

func hexHMAC(secret []byte, payload string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(payload))
	return hex.EncodeToString(m.Sum(nil))
}
