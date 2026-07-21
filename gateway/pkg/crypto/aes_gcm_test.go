package crypto

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKeyEncryption_EncryptDecrypt(t *testing.T) {
	masterKey := hex.EncodeToString(make([]byte, 32))
	ke, err := NewKeyEncryption(masterKey)
	assert.NoError(t, err)

	plaintext := "sk-openai-test-key-12345"
	ciphertextB64, nonceB64, version, err := ke.Encrypt(plaintext)
	assert.NoError(t, err)
	assert.NotEmpty(t, ciphertextB64)
	assert.NotEmpty(t, nonceB64)
	assert.Equal(t, "v1", version)
	assert.NotEqual(t, plaintext, ciphertextB64)

	decrypted, err := ke.Decrypt(ciphertextB64, nonceB64)
	assert.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestKeyEncryption_DifferentNonce(t *testing.T) {
	masterKey := hex.EncodeToString(make([]byte, 32))
	ke, err := NewKeyEncryption(masterKey)
	assert.NoError(t, err)

	ciphertext1, nonce1, _, err := ke.Encrypt("same plaintext")
	assert.NoError(t, err)
	ciphertext2, nonce2, _, err := ke.Encrypt("same plaintext")
	assert.NoError(t, err)

	// 两次加密应使用不同 nonce，密文也不同
	assert.NotEqual(t, nonce1, nonce2)
	assert.NotEqual(t, ciphertext1, ciphertext2)
}

func TestKeyEncryption_WrongMasterKey(t *testing.T) {
	masterKey1 := hex.EncodeToString(make([]byte, 32))
	masterKey2 := hex.EncodeToString([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32})

	ke1, err := NewKeyEncryption(masterKey1)
	assert.NoError(t, err)
	ke2, err := NewKeyEncryption(masterKey2)
	assert.NoError(t, err)

	ciphertext, nonce, _, err := ke1.Encrypt("secret")
	assert.NoError(t, err)

	_, err = ke2.Decrypt(ciphertext, nonce)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "解密失败")
}

func TestKeyEncryption_InvalidKeyLength(t *testing.T) {
	_, err := NewKeyEncryption(hex.EncodeToString(make([]byte, 16)))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "master key 长度必须为 32 字节")
}

func TestKeyEncryption_InvalidHex(t *testing.T) {
	_, err := NewKeyEncryption("not-a-hex-string")
	assert.Error(t, err)
}

func TestKeyEncryption_TamperedCiphertext(t *testing.T) {
	masterKey := hex.EncodeToString(make([]byte, 32))
	ke, err := NewKeyEncryption(masterKey)
	assert.NoError(t, err)

	ciphertext, nonce, _, err := ke.Encrypt("secret")
	assert.NoError(t, err)

	// 篡改密文：修改 base64 解码后的某字节再编码回去
	decoded, err := base64.StdEncoding.DecodeString(ciphertext)
	assert.NoError(t, err)
	decoded[0] ^= 0xFF
	tampered := base64.StdEncoding.EncodeToString(decoded)

	_, err = ke.Decrypt(tampered, nonce)
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "解密失败"))
}
