package vpn

import (
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func testMaster(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	t.Setenv(MasterKeyEnv, base64.StdEncoding.EncodeToString(key))
	return key
}

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

func TestEncryptDecryptRoundtrip(t *testing.T) {
	master := testMaster(t)
	priv, _, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := EncryptPrivate(master, priv, "device-1")
	if err != nil {
		t.Fatal(err)
	}
	if enc == priv {
		t.Fatal("密文不应等于明文")
	}
	got, err := DecryptPrivate(master, enc, "device-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != priv {
		t.Fatalf("解密结果不一致")
	}
}

func TestCiphertextBoundToDevice(t *testing.T) {
	master := testMaster(t)
	priv, _, _ := GenerateKeypair()
	enc, err := EncryptPrivate(master, priv, "device-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecryptPrivate(master, enc, "device-2"); err == nil {
		t.Fatal("换设备号必须解密失败（AAD 绑定）")
	}
}

func TestWrongMasterKeyFails(t *testing.T) {
	master := testMaster(t)
	priv, _, _ := GenerateKeypair()
	enc, _ := EncryptPrivate(master, priv, "d")
	other := make([]byte, 32)
	if _, err := DecryptPrivate(other, enc, "d"); err == nil {
		t.Fatal("换了主密钥必须解密失败")
	}
}

func TestMasterKeyFromEnv(t *testing.T) {
	t.Setenv(MasterKeyEnv, "")
	if _, err := MasterKey(); err == nil {
		t.Fatal("未设置时应当报错，提示改用 client 模式")
	}
	t.Setenv(MasterKeyEnv, base64.StdEncoding.EncodeToString([]byte("short")))
	if _, err := MasterKey(); err == nil {
		t.Fatal("长度不足 32 字节应报错")
	}
}

func TestValidatePubKeyRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "not-base64", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if err := ValidatePubKey(bad); err == nil {
			t.Fatalf("%q 应被拒绝", bad)
		}
	}
}
