package proxy

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/projectdiscovery/martian/v3/mitm"
	"github.com/projectdiscovery/proxify/pkg/certs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readCert(t *testing.T, dir string) *x509.Certificate {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, caCertFile))
	require.NoError(t, err)
	cert, err := parseCert(raw)
	require.NoError(t, err)
	return cert
}

// 首次启动生成永久有效的 liusha 根证书；私钥只有属主可读。
func TestEnsureCA_GeneratesPermanentRoot(t *testing.T) {
	dir := t.TempDir()
	ca, replaced, err := EnsureCA(dir)
	require.NoError(t, err)
	assert.False(t, replaced, "目录里原本没有证书，不算更换")

	cert := readCert(t, dir)
	assert.Equal(t, caCommonName, cert.Subject.CommonName)
	assert.True(t, cert.IsCA)
	assert.NotEmpty(t, cert.SubjectKeyId, "站点证书按它关联签发者")
	assert.Equal(t, caNotAfter, cert.NotAfter.UTC())
	assert.True(t, cert.NotBefore.Before(time.Now()))
	assert.Equal(t, fingerprint(cert.Raw), ca.Fingerprint)
	assert.Len(t, ca.Fingerprint, 32*3-1)

	// 下载给客户端的就是磁盘上的证书，且是标准 PEM
	disk, _ := os.ReadFile(filepath.Join(dir, caCertFile))
	assert.Equal(t, disk, ca.PEM)
	block, _ := pem.Decode(ca.PEM)
	require.NotNil(t, block)
	assert.Equal(t, "CERTIFICATE", block.Type)

	if fi, err := os.Stat(filepath.Join(dir, caKeyFile)); assert.NoError(t, err) && os.PathSeparator == '/' {
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	}
}

// 再次启动沿用同一张证书：客户端信任一次即可。
func TestEnsureCA_ReusesExistingRoot(t *testing.T) {
	dir := t.TempDir()
	first, _, err := EnsureCA(dir)
	require.NoError(t, err)
	second, replaced, err := EnsureCA(dir)
	require.NoError(t, err)
	assert.False(t, replaced)
	assert.Equal(t, first.Fingerprint, second.Fingerprint)
}

// 旧版本由代理库生成、有效期一年的证书：换成永久证书并报告已更换。
func TestEnsureCA_ReplacesExpiringRoot(t *testing.T) {
	dir := t.TempDir()
	cert, key, err := mitm.NewAuthority("Proxify CA", "Proxify CA", 365*24*time.Hour)
	require.NoError(t, err)
	writePEM(t, filepath.Join(dir, caCertFile), "CERTIFICATE", cert.Raw)
	writePEM(t, filepath.Join(dir, caKeyFile), "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))

	ca, replaced, err := EnsureCA(dir)
	require.NoError(t, err)
	assert.True(t, replaced)
	assert.NotEqual(t, fingerprint(cert.Raw), ca.Fingerprint)
	assert.Equal(t, caNotAfter, readCert(t, dir).NotAfter.UTC())
}

// 证书与私钥不成对（只剩一个，或私钥对不上证书）：重新生成。
func TestEnsureCA_RegeneratesMismatchedPair(t *testing.T) {
	t.Run("只剩证书", func(t *testing.T) {
		dir := t.TempDir()
		_, _, err := EnsureCA(dir)
		require.NoError(t, err)
		require.NoError(t, os.Remove(filepath.Join(dir, caKeyFile)))
		_, replaced, err := EnsureCA(dir)
		require.NoError(t, err)
		assert.True(t, replaced)
		assert.FileExists(t, filepath.Join(dir, caKeyFile))
	})
	t.Run("私钥对不上证书", func(t *testing.T) {
		dir := t.TempDir()
		_, _, err := EnsureCA(dir)
		require.NoError(t, err)
		_, otherKey, err := generateCA(time.Now())
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, caKeyFile), otherKey, 0o600))
		_, replaced, err := EnsureCA(dir)
		require.NoError(t, err)
		assert.True(t, replaced)
	})
}

// 代理库能加载生成的证书，并用它签发站点证书。
func TestEnsureCA_LoadableByMITM(t *testing.T) {
	dir := t.TempDir()
	_, _, err := EnsureCA(dir)
	require.NoError(t, err)
	require.NoError(t, certs.LoadCerts(dir))
	raw, err := certs.GetRawCA()
	require.NoError(t, err)
	disk, _ := os.ReadFile(filepath.Join(dir, caCertFile))
	assert.Equal(t, disk, raw.Bytes())
}

func writePEM(t *testing.T, path, typ string, der []byte) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600))
}
