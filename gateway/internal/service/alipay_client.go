package service

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/eleball/gateway/internal/config"
	alipay "github.com/smartwalle/alipay/v3"
)

// AlipayClient 支付宝客户端封装。
// 当前仅实现单次支付能力：当面付扫码预下单（alipay.trade.precreate）与异步通知验签。
// 预留扩展位（签约/周期扣款自动续费）：AgreementPageSign / AgreementUnsign，
// 设计见 docs/payment-alipay-integration.md 第 10 节。
type AlipayClient struct {
	cfg    config.AlipayPaymentConfig
	client *alipay.Client
}

// NewAlipayClient 构建支付宝客户端。
// 未启用（enabled=false）时返回 (nil, nil)，调用方据此判定“支付未开通”；
// 已启用但密钥缺失/非法时返回错误，启动阶段应 fail fast。
func NewAlipayClient(cfg config.AlipayPaymentConfig) (*AlipayClient, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.AppID == "" || cfg.PrivateKey == "" || cfg.AlipayPublicKey == "" {
		return nil, fmt.Errorf("支付宝支付已启用但配置不完整（app_id / private_key / alipay_public_key 必填）")
	}
	client, err := alipay.New(cfg.AppID, normalizeAlipayPEM(cfg.PrivateKey), !cfg.Sandbox)
	if err != nil {
		return nil, fmt.Errorf("初始化支付宝客户端失败: %w", err)
	}
	if err := client.LoadAliPayPublicKey(normalizeAlipayPEM(cfg.AlipayPublicKey)); err != nil {
		return nil, fmt.Errorf("加载支付宝公钥失败: %w", err)
	}
	return &AlipayClient{cfg: cfg, client: client}, nil
}

// normalizeAlipayPEM 兼容环境变量单行注入的 PEM 密钥（\n 转义还原为真实换行）。
func normalizeAlipayPEM(key string) string {
	return strings.ReplaceAll(strings.TrimSpace(key), `\n`, "\n")
}

// AppID 返回当前配置的支付宝应用 ID（notify 校验用）。
func (c *AlipayClient) AppID() string {
	return c.cfg.AppID
}

// Precreate 调用 alipay.trade.precreate 生成扫码二维码码串。
// outTradeNo 为商户订单号（即内部订单 ID），totalAmountYuan 单位为元（保留两位小数）。
// 同一 outTradeNo 重复调用会刷新二维码，不会重复收款，天然幂等。
func (c *AlipayClient) Precreate(ctx context.Context, outTradeNo, subject, totalAmountYuan string) (string, error) {
	var param alipay.TradePreCreate
	param.NotifyURL = c.cfg.NotifyURL
	param.Subject = subject
	param.OutTradeNo = outTradeNo
	param.TotalAmount = totalAmountYuan
	param.ProductCode = "FACE_TO_FACE_PAYMENT"
	param.TimeoutExpress = "30m"

	rsp, err := c.client.TradePreCreate(ctx, param)
	if err != nil {
		return "", fmt.Errorf("支付宝预下单请求失败: %w", err)
	}
	if rsp.Error.IsFailure() {
		return "", fmt.Errorf("支付宝预下单被拒绝: %s %s", rsp.Error.SubCode, rsp.Error.SubMsg)
	}
	if rsp.QRCode == "" {
		return "", fmt.Errorf("支付宝预下单未返回二维码")
	}
	return rsp.QRCode, nil
}

// VerifyNotification 验签并解析支付宝异步通知。
// 内部使用支付宝公钥做 RSA2 验签，签名非法时返回 error。
func (c *AlipayClient) VerifyNotification(ctx context.Context, values url.Values) (*alipay.Notification, error) {
	return c.client.DecodeNotification(ctx, values)
}

// QueryTradeStatus 查询支付宝侧交易状态（alipay.trade.query）。
// 订单从未支付过时支付宝返回 ACQ.TRADE_NOT_EXIST 业务错误，此时返回 ("", "", nil)。
func (c *AlipayClient) QueryTradeStatus(ctx context.Context, outTradeNo string) (status, tradeNo string, err error) {
	var param alipay.TradeQuery
	param.OutTradeNo = outTradeNo
	rsp, err := c.client.TradeQuery(ctx, param)
	if err != nil {
		return "", "", fmt.Errorf("支付宝查单失败: %w", err)
	}
	if rsp.Error.IsFailure() {
		if rsp.Error.SubCode == "ACQ.TRADE_NOT_EXIST" {
			return "", "", nil // 买家未付款，交易不存在
		}
		return "", "", fmt.Errorf("支付宝查单被拒绝: %s %s", rsp.Error.SubCode, rsp.Error.SubMsg)
	}
	return string(rsp.TradeStatus), rsp.TradeNo, nil
}

// CloseTrade 关闭支付宝侧交易（alipay.trade.close），
// 本地订单过期关闭前调用，防止用户随后扫码付款形成“钱已付、单已关”的差错。
func (c *AlipayClient) CloseTrade(ctx context.Context, outTradeNo string) error {
	var param alipay.TradeClose
	param.OutTradeNo = outTradeNo
	rsp, err := c.client.TradeClose(ctx, param)
	if err != nil {
		return fmt.Errorf("支付宝关单失败: %w", err)
	}
	if rsp.Error.IsFailure() {
		return fmt.Errorf("支付宝关单被拒绝: %s %s", rsp.Error.SubCode, rsp.Error.SubMsg)
	}
	return nil
}
