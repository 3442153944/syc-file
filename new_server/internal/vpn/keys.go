package vpn

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

// GenerateKeypair 生成一对 WireGuard 密钥（X25519），返回 base64 私钥与公钥。
// WireGuard 的私钥在 32 字节随机数上做 clamp，这里提前做掉，保证与 `wg genkey` 等价。
func GenerateKeypair() (privB64, pubB64 string, err error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	raw := make([]byte, 32)
	copy(raw, priv.Bytes())
	clamp(raw)
	key, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(raw), base64.StdEncoding.EncodeToString(key.PublicKey().Bytes()), nil
}

func clamp(k []byte) {
	k[0] &= 248
	k[31] &= 127
	k[31] |= 64
}

// ValidatePubKey 校验客户端提交的公钥：base64 解码后必须正好 32 字节。
func ValidatePubKey(pub string) error {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pub))
	if err != nil {
		return fmt.Errorf("公钥不是合法 base64")
	}
	if len(raw) != 32 {
		return fmt.Errorf("公钥长度应为 32 字节，实际 %d", len(raw))
	}
	return nil
}
