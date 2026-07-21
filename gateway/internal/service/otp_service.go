package service

import (
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"
)

// OTP 验证码服务常量
const (
	otpLength        = 6                 // 验证码位数
	otpTTL           = 10 * time.Minute   // 验证码有效期
	otpResendCooldown = 60 * time.Second // 同一邮箱重发冷却
	otpMaxSendPerIP  = 10                // 同一 IP 每小时最大发码次数
	otpIPWindow      = time.Hour         // IP 限流窗口
	otpMaxVerifyFail = 5                 // 同一邮箱验证失败达此次数后失效该码
)

// otpRecord 单个 OTP 的状态
type otpRecord struct {
	code       string
	expireAt   time.Time
	sentAt     time.Time
	failCount  int
}

// ipSendRecord 单个 IP 的发码计数
type ipSendRecord struct {
	count    int
	windowStart time.Time
}

// OTPService 邮箱验证码服务
// 内存存储（单机），生产分布式可换 Redis（接口不变）。
// 防滥用：同一邮箱 60s 重发冷却 + 同一 IP 每小时 10 次 + 验证失败 5 次失效码。
type OTPService struct {
	mu      sync.Mutex
	records map[string]*otpRecord // key: email
	ipRecs  map[string]*ipSendRecord // key: ip
	mail    *MailService
}

// NewOTPService 创建 OTP 服务
func NewOTPService(mail *MailService) *OTPService {
	return &OTPService{
		records: make(map[string]*otpRecord),
		ipRecs:  make(map[string]*ipSendRecord),
		mail:    mail,
	}
}

// SendOTP 生成并发送验证码到邮箱。
// 限流：同一邮箱 60s 内只能发 1 次；同一 IP 每小时最多 10 次。
func (s *OTPService) SendOTP(email, ip string) error {
	if s.mail == nil || !s.mail.Enabled() {
		return errors.New("邮件服务未开通")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// IP 限流
	if ip != "" {
		rec, exists := s.ipRecs[ip]
		if !exists || time.Since(rec.windowStart) > otpIPWindow {
			s.ipRecs[ip] = &ipSendRecord{count: 1, windowStart: time.Now()}
		} else {
			rec.count++
			if rec.count > otpMaxSendPerIP {
				return fmt.Errorf("发送过于频繁，请 %d 分钟后再试", int(otpIPWindow.Minutes()))
			}
		}
	}

	// 同邮箱重发冷却
	if rec, exists := s.records[email]; exists {
		if time.Since(rec.sentAt) < otpResendCooldown {
			remaining := int((otpResendCooldown - time.Since(rec.sentAt)).Seconds())
			return fmt.Errorf("验证码已发送，请 %d 秒后重试", remaining)
		}
	}

	code := genOTP(otpLength)
	s.records[email] = &otpRecord{
		code:     code,
		expireAt: time.Now().Add(otpTTL),
		sentAt:   time.Now(),
	}

	if err := s.mail.SendOTP(email, code); err != nil {
		// 发送失败清理记录，允许立即重试
		delete(s.records, email)
		return fmt.Errorf("验证码发送失败: %w", err)
	}
	return nil
}

// VerifyOTP 校验验证码。校验成功后清除记录；失败累计计数，达上限失效。
func (s *OTPService) VerifyOTP(email, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, exists := s.records[email]
	if !exists {
		return errors.New("验证码不存在或已失效，请重新发送")
	}
	if time.Now().After(rec.expireAt) {
		delete(s.records, email)
		return errors.New("验证码已过期，请重新发送")
	}
	if rec.failCount >= otpMaxVerifyFail {
		delete(s.records, email)
		return errors.New("验证码错误次数过多，请重新发送")
	}
	if rec.code != code {
		rec.failCount++
		return errors.New("验证码错误")
	}
	delete(s.records, email)
	return nil
}

// genOTP 生成定长数字验证码（crypto/rand 安全随机）
func genOTP(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(safeRandIntn(10)) + '0'
	}
	return string(b)
}

// safeRandIntn 返回 [0, n) 随机数，crypto/rand + 拒绝采样消除模偏差
func safeRandIntn(n int) int {
	if n <= 0 {
		return 0
	}
	max := byte(256 - 256/n) // 拒绝采样阈值
	for {
		b := randByte()
		if b < max {
			return int(b) % n
		}
	}
}

// randByte 读 1 字节随机数
func randByte() byte {
	b := make([]byte, 1)
	_, _ = rand.Read(b)
	return b[0]
}
