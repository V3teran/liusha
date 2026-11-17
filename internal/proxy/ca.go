package proxy

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// 根证书文件名：MITM 代理库从证书目录按这两个名字加载 CA（证书 PEM、PKCS#1 私钥 PEM）。
const (
	caCertFile = "cacert.pem"
	caKeyFile  = "cakey.pem"
)

// caCommonName 是 liusha 生成的根证书名称：客户端的证书管理器里显示的就是它。
const caCommonName = "liusha CA"

// caNotAfter 是根证书的到期时间。RFC 5280 §4.1.2.5 规定以 99991231235959Z 表示「没有明确的到期
// 时间」：根证书永久有效，客户端信任一次即可，不会因证书到期而突然无法解密 HTTPS。
var caNotAfter = time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC)

// CA 是正在使用的根证书：PEM 供客户端下载安装，指纹供核对客户端装的是不是这一张。
type CA struct {
	PEM         []byte
	Fingerprint string // SHA-256，冒号分隔的大写十六进制（与各系统证书详情的写法一致）
}

// EnsureCA 保证证书目录里是一张 liusha 生成的永久根证书，返回它。
//
// 目录里没有证书、证书或私钥不可用、或不是永久有效的 liusha 根证书（如旧版本由代理库生成的、
// 有效期一年的证书），都重新生成：旧证书到期后代理无法启动，只能趁早换掉。replaced 为 true
// 表示换掉了已有的证书，已信任旧证书的客户端需要重新安装。
func EnsureCA(dir string) (ca CA, replaced bool, err error) {
	certPath, keyPath := filepath.Join(dir, caCertFile), filepath.Join(dir, caKeyFile)
	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	switch {
	case certErr == nil && keyErr == nil:
		if usableCA(certPEM, keyPEM) {
			ca, err := newCA(certPEM)
			return ca, false, err
		}
		replaced = true
	case errors.Is(certErr, os.ErrNotExist) && errors.Is(keyErr, os.ErrNotExist):
	case certErr != nil && !errors.Is(certErr, os.ErrNotExist):
		return CA{}, false, fmt.Errorf("读取根证书 %s: %w", certPath, certErr)
	case keyErr != nil && !errors.Is(keyErr, os.ErrNotExist):
		return CA{}, false, fmt.Errorf("读取根证书私钥 %s: %w", keyPath, keyErr)
	default:
		replaced = true // 只剩证书或只剩私钥：不成对，重新生成
	}

	certPEM, keyPEM, err = generateCA(time.Now())
	if err != nil {
		return CA{}, false, err
	}
	// 先写私钥再写证书：中途失败时目录里不会留下「新证书 + 旧私钥」，下次启动按不成对重新生成。
	if err := writeFileAtomic(keyPath, keyPEM, 0o600); err != nil {
		return CA{}, false, fmt.Errorf("写入根证书私钥: %w", err)
	}
	if err := writeFileAtomic(certPath, certPEM, 0o644); err != nil {
		return CA{}, false, fmt.Errorf("写入根证书: %w", err)
	}
	ca, err = newCA(certPEM)
	return ca, replaced, err
}

// usableCA 判断已有的证书与私钥能否继续使用：成对、是 CA、永久有效、由 liusha 生成。
func usableCA(certPEM, keyPEM []byte) bool {
	cert, err := parseCert(certPEM)
	if err != nil {
		return false
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return false
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return false
	}
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	return ok && pub.Equal(&key.PublicKey) &&
		cert.IsCA && cert.Subject.CommonName == caCommonName && cert.NotAfter.Equal(caNotAfter)
}

func newCA(certPEM []byte) (CA, error) {
	cert, err := parseCert(certPEM)
	if err != nil {
		return CA{}, err
	}
	return CA{PEM: certPEM, Fingerprint: fingerprint(cert.Raw)}, nil
}

func parseCert(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("根证书不是 PEM 编码的证书")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("解析根证书: %w", err)
	}
	return cert, nil
}

// generateCA 生成永久有效的根证书与私钥（PEM）。只用于签发代理为各站点临时生成的证书。
func generateCA(now time.Time) (certPEM, keyPEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("生成根证书私钥: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("生成根证书序列号: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: caCommonName, Organization: []string{"liusha"}},
		// 起始时间往前留一天：客户端时钟略慢时，刚生成的证书也已生效。
		NotBefore:             now.Add(-24 * time.Hour),
		NotAfter:              caNotAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true, // 只直接签发站点证书，不签发下级 CA
		// 密钥标识（SubjectKeyId）由 x509.CreateCertificate 按公钥为 CA 自动生成
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("签发根证书: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM, nil
}

// fingerprint 返回证书 DER 的 SHA-256 指纹，形如 AB:CD:…。
func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	hexed := bytes.ToUpper([]byte(hex.EncodeToString(sum[:])))
	out := make([]byte, 0, len(hexed)/2*3)
	for i := 0; i < len(hexed); i += 2 {
		if i > 0 {
			out = append(out, ':')
		}
		out = append(out, hexed[i], hexed[i+1])
	}
	return string(out)
}

// writeFileAtomic 先写同目录临时文件再改名：进程中途退出不会留下半截文件。
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
