package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// E2E 加密层（P5.4）：中继只见密文，claw 与 APP 端到端加密。
//
// 密钥协商：
// - claw 持静态 P-256 ECDH 密钥对（首启生成，公钥注册云端设备列表）。
// - APP 每会话生成临时 P-256 密钥对，与 claw 静态公钥 ECDH 派生共享密钥。
// - 派生密钥用于 AES-256-GCM 加解密 payload。
//
// 选 P-256 而非 X25519：Android API 29+ 原生支持 ECDH P-256（KeyAgreement "ECDH"），
// X25519 需 API 31+ 或 Tink，P-256 跨平台一致性更好（claw minSdk 对齐 APP 29）。
//
// 帧格式（encryptedPayload，base64 编码的 JSON）：
//   { "eph_pub": "<app 临时公钥 base64>", "nonce": "<12B base64>", "ct": "<密文 base64>" }
//
// 中继仍只转发 relayMsg.Payload 字符串（现在是 encryptedPayload 的 JSON），无法解密。
// 详见 docs/marketing/claw-app-dualtrack-design.md §6。
//
// Go 侧用 stdlib crypto/ecdh（P256）+ crypto/aes + crypto/cipher (GCM)。

// E2ECipher claw 侧 E2E 加密器：持静态 P-256 私钥，解密 APP 请求 / 加密响应。
type E2ECipher struct {
	staticPriv *ecdh.PrivateKey
}

// NewE2ECipher 生成 claw 静态密钥对。生产应持久化私钥并注册公钥到云端设备列表。
func NewE2ECipher() (*E2ECipher, error) {
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("生成 P-256 密钥对失败: %w", err)
	}
	return &E2ECipher{staticPriv: priv}, nil
}

// PublicKeyBase64 返回 claw 静态公钥（base64），供注册云端设备列表 / APP 协商用。
func (c *E2ECipher) PublicKeyBase64() string {
	return base64.StdEncoding.EncodeToString(c.staticPriv.PublicKey().Bytes())
}

// encryptedPayload E2E 加密载荷结构（relay 见到的 payload 内容）
type encryptedPayload struct {
	EphPub string `json:"eph_pub"` // APP 临时公钥 base64
	Nonce  string `json:"nonce"`   // 12B GCM nonce base64
	CT     string `json:"ct"`       // 密文 base64
}

// deriveKey 用 APP 临时公钥与 claw 静态私钥 ECDH 派生 32 字节共享密钥。
func (c *E2ECipher) deriveKey(ephPubB64 string) ([]byte, error) {
	ephPubBytes, err := base64.StdEncoding.DecodeString(ephPubB64)
	if err != nil {
		return nil, fmt.Errorf("解码临时公钥失败: %w", err)
	}
	ephPub, err := ecdh.P256().NewPublicKey(ephPubBytes)
	if err != nil {
		return nil, fmt.Errorf("解析临时公钥失败: %w", err)
	}
	shared, err := c.staticPriv.ECDH(ephPub)
	if err != nil {
		return nil, fmt.Errorf("ECDH 派生失败: %w", err)
	}
	return shared, nil
}

// Decrypt 解密 APP 发来的 encryptedPayload（JSON），返回明文。
func (c *E2ECipher) Decrypt(payloadStr string) ([]byte, error) {
	var ep encryptedPayload
	if err := json.Unmarshal([]byte(payloadStr), &ep); err != nil {
		return nil, fmt.Errorf("解析加密载荷失败: %w", err)
	}
	shared, err := c.deriveKey(ep.EphPub)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(shared)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce, err := base64.StdEncoding.DecodeString(ep.Nonce)
	if err != nil {
		return nil, fmt.Errorf("解码 nonce 失败: %w", err)
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("nonce 长度异常: %d", len(nonce))
	}
	ct, err := base64.StdEncoding.DecodeString(ep.CT)
	if err != nil {
		return nil, fmt.Errorf("解码密文失败: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("AES-GCM 解密失败: %w", err)
	}
	return plaintext, nil
}

// EncryptResponse 加密 claw 响应：用同一会话密钥（APP 临时公钥派生）加密响应明文。
// 复用 APP 请求的 eph_pub 派生密钥（同一会话双向通信用同一密钥）。
func (c *E2ECipher) EncryptResponse(ephPubB64 string, plaintext []byte) (string, error) {
	shared, err := c.deriveKey(ephPubB64)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(shared)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	ep := encryptedPayload{
		EphPub: ephPubB64,
		Nonce:  base64.StdEncoding.EncodeToString(nonce),
		CT:     base64.StdEncoding.EncodeToString(ct),
	}
	out, err := json.Marshal(ep)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// randomBytes 工具（供其他模块复用）
func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, err
	}
	return b, nil
}

var _ = errors.New // 保留 errors 供未来扩展（错误包装）
