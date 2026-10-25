package vpn

import (
	"encoding/base64"
	"testing"
)

func TestGenerateKeypair(t *testing.T) {
	priv, pub, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePubKey(pub); err != nil {
		t.Fatalf("生成的公钥应合法: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(priv)
	if err != nil || len(raw) != 32 {
		t.Fatalf("私钥应为 32 字节 base64")
	}
	// clamp 检查：低 3 位清零、最高位清零、次高位保留
	if raw[0]&0b111 != 0 || raw[31]&0b1000_0000 != 0 || raw[31]&0b0100_0000 == 0 {
		t.Fatalf("私钥未按 X25519 规则 clamp: %x", raw)
	}
}

func TestValidatePubKeyRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "not-base64", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if err := ValidatePubKey(bad); err == nil {
			t.Fatalf("%q 应被拒绝", bad)
		}
	}
}

// TestHostedKeyStore 覆盖进程内存托管的存取与清理。
func TestHostedKeyStore(t *testing.T) {
	clearHostedKeys()
	t.Cleanup(clearHostedKeys)
	storeHostedKey(42, "private-key")
	if got, ok := loadHostedKey(42); !ok || got != "private-key" {
		t.Fatalf("loadHostedKey = %q, %v", got, ok)
	}
	forgetHostedKey(42)
	if _, ok := loadHostedKey(42); ok {
		t.Fatal("forgetHostedKey 后不应还能取到")
	}
}

// TestNewKeypairIsUnique 每次动态签发都应是新的密钥对（服务重启后重新签发依赖这一点）。
func TestNewKeypairIsUnique(t *testing.T) {
	priv, pub, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	priv2, pub2, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if priv == priv2 || pub == pub2 {
		t.Fatal("两次生成的密钥对应不同")
	}
}
