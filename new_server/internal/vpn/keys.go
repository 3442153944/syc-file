package vpn

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

// MasterKeyEnv 主密钥环境变量名。主密钥必须与数据库分离存放：
// 写进数据库等于把锁和钥匙放在同一个抽屉里。
const MasterKeyEnv = "SYC_VPN_MASTER_KEY"

// ErrNoMasterKey 未配置主密钥时返回，调用方应提示改用 key_source=client。
var ErrNoMasterKey = errors.New("未配置 " + MasterKeyEnv + "，无法托管私钥")

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

// MasterKey 从环境变量读取 32 字节主密钥（base64）。
func MasterKey() ([]byte, error) {
	v := strings.TrimSpace(os.Getenv(MasterKeyEnv))
	if v == "" {
		return nil, ErrNoMasterKey
	}
	raw, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return nil, fmt.Errorf("%s 不是合法 base64: %w", MasterKeyEnv, err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("%s 解码后应为 32 字节，实际 %d", MasterKeyEnv, len(raw))
	}
	return raw, nil
}

// EncryptPrivate 把私钥加密成入库字符串。AAD 绑定 device_id，密文挪到别的设备上解不开。
func EncryptPrivate(master []byte, privB64, deviceID string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(privB64)
	if err != nil || len(raw) != 32 {
		return "", errors.New("私钥格式非法")
	}
	block, err := aes.NewCipher(master)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, raw, []byte(deviceID))
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// DecryptPrivate 解密入库的私钥，仅在需要把托管私钥下发给设备时调用。
func DecryptPrivate(master []byte, enc, deviceID string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", errors.New("私钥密文不是合法 base64")
	}
	block, err := aes.NewCipher(master)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("私钥密文长度异常")
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, []byte(deviceID))
	if err != nil {
		return "", errors.New("私钥解密失败（主密钥不匹配或设备号被改）")
	}
	return base64.StdEncoding.EncodeToString(plain), nil
}
