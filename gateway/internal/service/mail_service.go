package service

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net/smtp"
	"strings"

	"github.com/eleball/gateway/internal/config"
)

// MailService 邮件发送服务，用于发送邮箱验证码（OTP）。
// 使用 Go 标准库 net/smtp，零外部依赖；支持 465（SSL 直连）与 587（STARTTLS）。
type MailService struct {
	cfg config.MailConfig
}

// NewMailService 创建邮件服务。配置未启用时仍可创建，SendOTP 返回明确错误。
func NewMailService(cfg config.MailConfig) *MailService {
	return &MailService{cfg: cfg}
}

// Enabled 是否启用邮件发送
func (m *MailService) Enabled() bool {
	return m.cfg.Enabled
}

// SendOTP 发送 6 位验证码邮件到指定邮箱。
// HTML 模板，编码 UTF-8。SMTP 失败返回错误，调用方决定是否重试。
func (m *MailService) SendOTP(toEmail, code string) error {
	if !m.cfg.Enabled {
		return fmt.Errorf("邮件服务未开通")
	}
	if toEmail == "" || code == "" {
		return fmt.Errorf("收件人与验证码不能为空")
	}

	subject := "Eleball 登录验证码"
	body := fmt.Sprintf(`<html><body style="font-family:-apple-system,Segoe UI,sans-serif;color:#333;line-height:1.6;">
<p>你正在登录 Eleball。验证码：</p>
<p style="font-size:28px;font-weight:bold;letter-spacing:4px;color:#6750A4;">%s</p>
<p>验证码 10 分钟内有效。如果不是你本人操作，请忽略此邮件。</p>
<p style="color:#999;font-size:12px;margin-top:24px;">Eleball - 优雅的弹丸，随时待命</p>
</body></html>`, code)

	msg := buildMessage(m.cfg.From, toEmail, subject, body)

	addr := fmt.Sprintf("%s:%d", m.cfg.Host, m.cfg.Port)
	auth := smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)

	// 465 走 SSL 直连；587/25 走 STARTTLS（标准 smtp.SendMail 自动处理）
	if m.cfg.Port == 465 {
		return dialSSL(addr, m.cfg.Host, auth, m.cfg.Username, []string{toEmail}, msg)
	}
	return smtp.SendMail(addr, auth, m.cfg.Username, []string{toEmail}, msg)
}

// buildMessage 组装 RFC 822 邮件，含 MIME 头以支持 HTML 与 UTF-8
func buildMessage(from, to, subject, htmlBody string) []byte {
	var sb strings.Builder
	fmt.Fprintf(&sb, "From: Eleball <%s>\r\n", from)
	fmt.Fprintf(&sb, "To: %s\r\n", to)
	fmt.Fprintf(&sb, "Subject: =?UTF-8?B?%s?=\r\n", base64.StdEncoding.EncodeToString([]byte(subject)))
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	sb.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	sb.WriteString(base64.StdEncoding.EncodeToString([]byte(htmlBody)))
	return []byte(sb.String())
}

// dialSSL 465 端口 SSL 直连发送（标准库 smtp.SendMail 仅支持 STARTTLS，465 需手动 TLS）
func dialSSL(addr, host string, auth smtp.Auth, from string, to []string, msg []byte) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host})
	if err != nil {
		return fmt.Errorf("SMTP SSL 连接失败: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("SMTP 客户端创建失败: %w", err)
	}
	defer client.Close()

	if err = client.Auth(auth); err != nil {
		return fmt.Errorf("SMTP 认证失败: %w", err)
	}
	if err = client.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err = client.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = w.Write(msg); err != nil {
		return err
	}
	return w.Close()
}
