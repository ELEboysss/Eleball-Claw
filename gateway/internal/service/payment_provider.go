package service

import (
	"context"
	"fmt"
)

// TransferRequest 企业付款请求
type TransferRequest struct {
	OrderID    string // 内部订单号
	Amount     int64  // 金额（分）
	Account    string // 收款账号（openid / 支付宝账号）
	RealName   string // 收款人真实姓名
	Desc       string // 付款备注
}

// TransferResult 企业付款结果
type TransferResult struct {
	Success bool   // 是否成功
	TxID    string // 第三方支付流水号
	ErrMsg  string // 错误信息
}

// PaymentProvider 企业付款接口抽象
// 支持微信企业付款到零钱、支付宝单笔转账到支付宝账户
type PaymentProvider interface {
	// Transfer 发起企业付款
	Transfer(ctx context.Context, req TransferRequest) (TransferResult, error)
	// QueryTransfer 查询付款状态
	QueryTransfer(ctx context.Context, orderID string) (TransferResult, error)
}

// MockPaymentProvider 模拟付款实现（测试/MVP 阶段使用）
type MockPaymentProvider struct{}

// NewMockPaymentProvider 创建模拟付款器
func NewMockPaymentProvider() PaymentProvider {
	return &MockPaymentProvider{}
}

func (m *MockPaymentProvider) Transfer(ctx context.Context, req TransferRequest) (TransferResult, error) {
	// 模拟异步处理，直接返回成功
	return TransferResult{
		Success: true,
		TxID:    "mock_tx_" + req.OrderID,
	}, nil
}

func (m *MockPaymentProvider) QueryTransfer(ctx context.Context, orderID string) (TransferResult, error) {
	return TransferResult{
		Success: true,
		TxID:    "mock_tx_" + orderID,
	}, nil
}

// WechatPaymentProvider 微信企业付款实现（占位，需接入真实 SDK）
type WechatPaymentProvider struct {
	MchID      string
	AppID      string
	APIKey     string
	CertPath   string
	KeyPath    string
}

func NewWechatPaymentProvider(mchID, appID, apiKey, certPath, keyPath string) PaymentProvider {
	return &WechatPaymentProvider{
		MchID:    mchID,
		AppID:    appID,
		APIKey:   apiKey,
		CertPath: certPath,
		KeyPath:  keyPath,
	}
}

func (w *WechatPaymentProvider) Transfer(ctx context.Context, req TransferRequest) (TransferResult, error) {
	// TODO: 接入微信支付企业付款 API（mmpaymkttransfers/promotion/transfers）
	return TransferResult{}, fmt.Errorf("微信支付企业付款尚未实现，请使用 MockPaymentProvider")
}

func (w *WechatPaymentProvider) QueryTransfer(ctx context.Context, orderID string) (TransferResult, error) {
	// TODO: 接入微信查询企业付款 API
	return TransferResult{}, fmt.Errorf("微信支付查询尚未实现")
}

// AlipayPaymentProvider 支付宝转账实现（占位，需接入真实 SDK）
type AlipayPaymentProvider struct {
	AppID           string
	PrivateKey      string
	AlipayPublicKey string
}

func NewAlipayPaymentProvider(appID, privateKey, alipayPublicKey string) PaymentProvider {
	return &AlipayPaymentProvider{
		AppID:           appID,
		PrivateKey:      privateKey,
		AlipayPublicKey: alipayPublicKey,
	}
}

func (a *AlipayPaymentProvider) Transfer(ctx context.Context, req TransferRequest) (TransferResult, error) {
	// TODO: 接入支付宝单笔转账接口（alipay.fund.trans.toaccount.transfer）
	return TransferResult{}, fmt.Errorf("支付宝转账尚未实现，请使用 MockPaymentProvider")
}

func (a *AlipayPaymentProvider) QueryTransfer(ctx context.Context, orderID string) (TransferResult, error) {
	// TODO: 接入支付宝查询转账接口
	return TransferResult{}, fmt.Errorf("支付宝查询尚未实现")
}
