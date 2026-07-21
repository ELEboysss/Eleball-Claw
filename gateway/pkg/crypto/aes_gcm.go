package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// KeyEncryption 提供 AES-256-GCM 信封加密能力，用于加密存储后端 LLM API Key。
// Master Key 由调用方从环境变量或安全存储中加载，本包不管理 Master Key 的持久化。
type KeyEncryption struct {
	masterKey []byte
	version   string
}

// NewKeyEncryption 从十六进制字符串创建加密器。
// masterKeyHex 必须是 64 字符（32 字节）的十六进制字符串。
func NewKeyEncryption(masterKeyHex string) (*KeyEncryption, error) {
	if masterKeyHex == "" {
		return nil, errors.New("master key 不能为空")
	}

	key, err := hex.DecodeString(masterKeyHex)
	if err != nil {
		return nil, fmt.Errorf("master key 十六进制解码失败: %w", err)
	}

	if len(key) != 32 {
		return nil, fmt.Errorf("master key 长度必须为 32 字节，当前 %d 字节", len(key))
	}

	return &KeyEncryption{
		masterKey: key,
		version:   "v1",
	}, nil
}

// NewKeyEncryptionWithVersion 指定版本创建加密器，用于 Master Key 轮换场景。
func NewKeyEncryptionWithVersion(masterKeyHex, version string) (*KeyEncryption, error) {
	ke, err := NewKeyEncryption(masterKeyHex)
	if err != nil {
		return nil, err
	}
	if version == "" {
		version = "v1"
	}
	ke.version = version
	return ke, nil
}

// Encrypt 使用 AES-256-GCM 加密明文，返回 base64 编码的密文和 nonce。
func (k *KeyEncryption) Encrypt(plaintext string) (ciphertextB64, nonceB64, version string, err error) {
	block, err := aes.NewCipher(k.masterKey)
	if err != nil {
		return "", "", "", fmt.Errorf("创建 AES cipher 失败: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", "", fmt.Errorf("创建 GCM 失败: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", "", "", fmt.Errorf("生成 nonce 失败: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	// ciphertext 前 gcm.NonceSize() 字节为 nonce，后面为密文+tag
	return base64.StdEncoding.EncodeToString(ciphertext[gcm.NonceSize():]),
		base64.StdEncoding.EncodeToString(nonce),
		k.version,
		nil
}

// Decrypt 使用 AES-256-GCM 解密密文。
func (k *KeyEncryption) Decrypt(ciphertextB64, nonceB64 string) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return "", fmt.Errorf("密文 base64 解码失败: %w", err)
	}

	nonce, err := base64.StdEncoding.DecodeString(nonceB64)
	if err != nil {
		return "", fmt.Errorf("nonce base64 解码失败: %w", err)
	}

	block, err := aes.NewCipher(k.masterKey)
	if err != nil {
		return "", fmt.Errorf("创建 AES cipher 失败: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("创建 GCM 失败: %w", err)
	}

	if len(nonce) != gcm.NonceSize() {
		return "", fmt.Errorf("nonce 长度错误: expected %d, got %d", gcm.NonceSize(), len(nonce))
	}

	// ciphertext 为 sealedData（含密文 + auth tag），直接用 nonce 解密
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("解密失败: %w", err)
	}

	return string(plaintext), nil
}

// Version 返回当前加密器版本。
func (k *KeyEncryption) Version() string {
	return k.version
}
