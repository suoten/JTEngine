package secret

import (
	"strings"
	"testing"
)

// 测试用 SM4 主密钥（32 hex 字符 = 16 字节），仅测试环境使用
const testMasterKey = "0123456789abcdef0123456789abcdef"

func TestDataCipher_DisabledTransparent(t *testing.T) {
	c, err := NewDataCipher("", false)
	if err != nil {
		t.Fatalf("创建未启用加密器失败: %v", err)
	}
	if c.Enabled() {
		t.Fatal("未启用加密器应为禁用状态")
	}
	// 未启用时透传明文
	enc, err := c.Encrypt("13800138000")
	if err != nil {
		t.Fatalf("Encrypt 错误: %v", err)
	}
	if enc != "13800138000" {
		t.Fatalf("未启用加密器应透传明文，got %s", enc)
	}
	dec, err := c.Decrypt("13800138000")
	if err != nil {
		t.Fatalf("Decrypt 错误: %v", err)
	}
	if dec != "13800138000" {
		t.Fatalf("未启用加密器应透传明文，got %s", dec)
	}
}

func TestDataCipher_EncryptDecryptRoundtrip(t *testing.T) {
	c, err := NewDataCipher(testMasterKey, true)
	if err != nil {
		t.Fatalf("创建加密器失败: %v", err)
	}
	if !c.Enabled() {
		t.Fatal("加密器应为启用状态")
	}

	cases := []string{
		"13800138000",                              // 手机号
		"110101199001011234",                       // 身份证号
		"京A12345",                                  // 车牌号
		"敏感数据 with spaces and 中文",               // Unicode
		"",                                          // 空字符串
	}
	for _, plain := range cases {
		enc, err := c.Encrypt(plain)
		if err != nil {
			t.Fatalf("Encrypt(%q) 错误: %v", plain, err)
		}
		if plain == "" {
			if enc != "" {
				t.Fatalf("空字符串加密应返回空，got %q", enc)
			}
			continue
		}
		if !strings.HasPrefix(enc, CipherPrefix) {
			t.Fatalf("密文应带前缀 %q，got %q", CipherPrefix, enc)
		}
		if enc == plain {
			t.Fatalf("密文不应等于明文")
		}
		// 每次加密密文不同（随机 nonce）
		enc2, _ := c.Encrypt(plain)
		if enc == enc2 && plain != "" {
			t.Fatalf("相同明文两次加密应产生不同密文（随机 nonce）")
		}
		dec, err := c.Decrypt(enc)
		if err != nil {
			t.Fatalf("Decrypt(%q) 错误: %v", enc, err)
		}
		if dec != plain {
			t.Fatalf("解密不匹配: got %q, want %q", dec, plain)
		}
	}
}

func TestDataCipher_IdempotentEncryption(t *testing.T) {
	c, _ := NewDataCipher(testMasterKey, true)
	enc, _ := c.Encrypt("13800138000")
	enc2, _ := c.Encrypt(enc) // 对已加密数据再次加密
	if enc != enc2 {
		t.Fatalf("对已加密数据应幂等返回，enc=%q enc2=%q", enc, enc2)
	}
}

func TestDataCipher_BackwardCompatiblePlaintext(t *testing.T) {
	c, _ := NewDataCipher(testMasterKey, true)
	// 存量明文数据（无前缀）应原样解密返回
	dec, err := c.Decrypt("13800138000")
	if err != nil {
		t.Fatalf("解密明文数据失败: %v", err)
	}
	if dec != "13800138000" {
		t.Fatalf("明文数据应原样返回，got %q", dec)
	}
}

func TestDataCipher_TamperDetection(t *testing.T) {
	c, _ := NewDataCipher(testMasterKey, true)
	enc, _ := c.Encrypt("110101199001011234")
	// 篡改密文
	tampered := enc[:len(enc)-2] + "XX"
	_, err := c.Decrypt(tampered)
	if err == nil {
		t.Fatal("篡改密文后解密应失败")
	}
}

func TestDataCipher_WrongKeyFails(t *testing.T) {
	c1, _ := NewDataCipher(testMasterKey, true)
	c2, _ := NewDataCipher("ffffffffffffffffffffffffffffffff", true)
	enc, _ := c1.Encrypt("secret data")
	_, err := c2.Decrypt(enc)
	if err == nil {
		t.Fatal("使用错误密钥解密应失败")
	}
}

func TestDataCipher_MasterKeyFromEnv(t *testing.T) {
	t.Setenv("JTE_SM4_KEY", testMasterKey)
	c, err := NewDataCipher("", true)
	if err != nil {
		t.Fatalf("从环境变量加载主密钥失败: %v", err)
	}
	enc, _ := c.Encrypt("test")
	dec, _ := c.Decrypt(enc)
	if dec != "test" {
		t.Fatalf("环境变量密钥加解密失败: got %q", dec)
	}
}

func TestDataCipher_InvalidKeyLength(t *testing.T) {
	_, err := NewDataCipher("short", true)
	if err == nil {
		t.Fatal("短密钥应返回错误")
	}
}

func TestDataCipher_KeyRotation(t *testing.T) {
	c, _ := NewDataCipher(testMasterKey, true)
	enc, _ := c.Encrypt("before rotation")
	// 轮换密钥
	newKey := "fedcba9876543210fedcba9876543210"
	if err := c.RotateMasterKey(newKey); err != nil {
		t.Fatalf("密钥轮换失败: %v", err)
	}
	// 旧密文无法用新密钥解密
	_, err := c.Decrypt(enc)
	if err == nil {
		t.Fatal("轮换后旧密文应无法解密")
	}
	// 新明文用新密钥加解密
	enc2, _ := c.Encrypt("after rotation")
	dec2, _ := c.Decrypt(enc2)
	if dec2 != "after rotation" {
		t.Fatalf("新密钥加解密失败: got %q", dec2)
	}
}

func TestGenerateMasterKey(t *testing.T) {
	key1, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("生成主密钥失败: %v", err)
	}
	if len(key1) != 32 {
		t.Fatalf("主密钥 hex 长度应为 32，got %d", len(key1))
	}
	key2, _ := GenerateMasterKey()
	if key1 == key2 {
		t.Fatal("两次生成的密钥应不同（随机）")
	}
}
