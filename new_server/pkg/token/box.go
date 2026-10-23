package token

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

// 小型对称加密盒：用 JWT 密钥派生一把 AES-256-GCM 密钥，供管理端保存「需要回读的」第三方凭据
// （例如 VPS 节点面板的访问秘钥——签发链接时要把明文拼进 URL，所以不能只存哈希）。
//
// 库内保存的值格式：base64url(nonce || ciphertext)。派生串里带上用途前缀，避免与 JWT 签名用途混用。

const boxPurpose = "syc-secret-box-v1:"

func boxKey() ([]byte, error) {
	s := getSecret()
	if len(s) == 0 {
		return nil, errors.New("密钥未初始化")
	}
	sum := sha256.Sum256(append([]byte(boxPurpose), s...))
	return sum[:], nil
}

// SealString 加密明文，返回可入库的字符串。
func SealString(plain string) (string, error) {
	key, err := boxKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	out := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.RawURLEncoding.EncodeToString(out), nil
}

// OpenString 解密 SealString 的结果。
func OpenString(enc string) (string, error) {
	key, err := boxKey()
	if err != nil {
		return "", err
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("密文长度不足")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
