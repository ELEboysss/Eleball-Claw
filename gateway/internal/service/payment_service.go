package service

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/google/uuid"
	alipay "github.com/smartwalle/alipay/v3"
	"gorm.io/gorm"
)

// PaymentService 支付订单服务
type PaymentService struct {
	db          *gorm.DB
	userRepo    *repository.UserRepo
	packageRepo *repository.RechargePackageRepo
	orderRepo   *repository.OrderRepo
	billRepo    *repository.BillingRepo
	vipService  *VIPService
	// alipayClient 支付宝客户端；为 nil 表示支付未开通（未配置或 disabled）
	alipayClient *AlipayClient
	// orderExpire 订单过期时长（默认 30 分钟，与支付宝二维码 timeout_express 对齐）
	orderExpire time.Duration
	// TODO: 微信支付为预留骨架，接入时从配置中心读取商户参数
	wechatConfig WechatConfig
}

type WechatConfig struct {
	AppID      string
	MchID      string // 商户号
	APIKey     string // API 密钥
	NotifyURL  string
}

// NewPaymentService 创建支付服务
func NewPaymentService(db *gorm.DB, userRepo *repository.UserRepo, packageRepo *repository.RechargePackageRepo, orderRepo *repository.OrderRepo, billRepo *repository.BillingRepo, vipService *VIPService, alipayClient *AlipayClient) *PaymentService {
	return &PaymentService{
		db:           db,
		userRepo:     userRepo,
		packageRepo:  packageRepo,
		orderRepo:    orderRepo,
		billRepo:     billRepo,
		vipService:   vipService,
		alipayClient: alipayClient,
		orderExpire:  30 * time.Minute,
		wechatConfig: WechatConfig{
			AppID:     "wx_test_app_id",
			MchID:     "test_mch_id",
			APIKey:    "test_api_key",
			NotifyURL: "https://api.eleball.cn/v1/payment/wechat/notify",
		},
	}
}

// SetOrderExpiry 设置订单过期时长（>0 时生效），与 payment.order_expire_minutes 配置对应。
func (s *PaymentService) SetOrderExpiry(d time.Duration) {
	if d > 0 {
		s.orderExpire = d
	}
}

// SweepResult 过期订单扫描结果
type SweepResult struct {
	Closed  int // 本轮关闭的订单数
	Paid    int // 查单发现已支付并补发权益的订单数（notify 漏单兜底）
	Skipped int // 查单/关单失败本轮跳过的订单数（下轮重试）
}

// SweepExpiredOrders 扫描创建时间早于 before 的 pending 订单并处理：
// - 支付宝订单且已配置客户端：先查单，已支付则补发权益（防 notify 漏单）；
//   未支付/交易不存在则先关支付宝交易（防止关本地单后用户又付款形成差错），再关本地订单；
// - 其余情况（未配置支付宝、非支付宝渠道）：直接关闭本地订单。
// 状态流转为 pending → closed，条件更新保证与并发 notify/确认不冲突。
func (s *PaymentService) SweepExpiredOrders(ctx context.Context, before time.Time) SweepResult {
	var res SweepResult
	orders, err := s.orderRepo.ListPendingBefore(before, 100)
	if err != nil {
		return res
	}
	for _, order := range orders {
		if ctx.Err() != nil {
			break
		}
		s.expireOneOrder(ctx, order, &res)
	}
	return res
}

func (s *PaymentService) expireOneOrder(ctx context.Context, order *model.Order, res *SweepResult) {
	if s.alipayClient != nil && order.Channel == "alipay" {
		status, tradeNo, err := s.alipayClient.QueryTradeStatus(ctx, order.ID)
		if err != nil {
			res.Skipped++ // 查单失败本轮跳过，下轮重试
			return
		}
		if status == string(alipay.TradeStatusSuccess) || status == string(alipay.TradeStatusFinished) {
			// notify 漏单：按查单结果补发权益（幂等）
			if err := s.ProcessPaidOrder(order.ID, tradeNo); err != nil {
				res.Skipped++
				return
			}
			res.Paid++
			return
		}
		// 未支付（WAIT_BUYER_PAY）或交易不存在：先关支付宝交易
		if err := s.alipayClient.CloseTrade(ctx, order.ID); err != nil {
			res.Skipped++
			return
		}
	}
	closed, err := s.orderRepo.UpdateStatusIf(order.ID, "pending", "closed")
	if err == nil && closed {
		res.Closed++
	}
}

// StartOrderExpiryJob 启动过期订单后台扫描：每隔 interval 处理一批超时 pending 订单。
// ctx 取消时退出（随服务生命周期）。
func (s *PaymentService) StartOrderExpiryJob(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.SweepExpiredOrders(ctx, time.Now().Add(-s.orderExpire))
		}
	}
}

// WechatPrepayRequest 微信预支付请求
type WechatPrepayRequest struct {
	UserID    string `json:"user_id" binding:"required"`
	PackageID string `json:"package_id"`                // recharge 时必填
	OrderID   string `json:"order_id"`                  // VIP 等已创建订单时传入
	Quantity  int    `json:"quantity" binding:"min=1"` // 默认 1
}

// WechatPrepayResponse 微信预支付响应
type WechatPrepayResponse struct {
	OrderID   string `json:"order_id"`
	AppID     string `json:"appId"`
	PartnerID string `json:"partnerId"`
	PrepayID  string `json:"prepayId"`
	Package   string `json:"package"`
	NonceStr  string `json:"nonceStr"`
	TimeStamp string `json:"timeStamp"`
	Sign      string `json:"sign"`
}

// WechatPrepay 创建微信支付预订单
func (s *PaymentService) WechatPrepay(req WechatPrepayRequest) (*WechatPrepayResponse, error) {
	// TODO: MVP 阶段为骨架实现，实际应调用微信统一下单 API：https://api.mch.weixin.qq.com/pay/unifiedorder
	var order *model.Order
	var err error

	if req.OrderID != "" {
		// VIP 等已创建订单场景：直接调起支付
		order, err = s.orderRepo.GetByID(req.OrderID)
		if err != nil {
			return nil, fmt.Errorf("订单不存在")
		}
		if order.UserID != req.UserID {
			return nil, fmt.Errorf("订单归属不一致")
		}
		if order.Status != "pending" {
			return nil, fmt.Errorf("订单状态不正确")
		}
		order.Channel = "wechat"
		if err := s.orderRepo.UpdateStatus(order.ID, "pending"); err != nil {
			return nil, err
		}
	} else {
		packageService := NewRechargePackageService(s.packageRepo)
		resolved, err := packageService.ResolvePackage(req.PackageID, req.Quantity)
		if err != nil {
			return nil, err
		}

		// 1. 生成内部订单号并落库
		orderID := uuid.New().String()
		order = &model.Order{
			ID:        orderID,
			UserID:    req.UserID,
			Channel:   "wechat",
			Amount:    resolved.AmountFen,
			Currency:  resolved.Currency,
			Status:    "pending",
			PackageID: resolved.PackageID,
			Quantity:  resolved.Quantity,
			Danwan:    resolved.Danwan,
		}
		if err := s.orderRepo.Create(order); err != nil {
			return nil, fmt.Errorf("创建订单失败: %w", err)
		}
	}

	// 2. 构造统一下单请求 XML
	// 3. 调用微信 API 获取 prepay_id
	// 4. 用返回的 prepay_id 组装调起参数并二次签名

	// MVP 占位：直接返回测试参数（客户端可验证 SDK 调通，但无法真正支付）
	nonceStr := randomString(32)
	timeStamp := fmt.Sprintf("%d", time.Now().Unix())
	prepayID := "wx_test_prepay_id"

	resp := &WechatPrepayResponse{
		OrderID:   order.ID,
		AppID:     s.wechatConfig.AppID,
		PartnerID: s.wechatConfig.MchID,
		PrepayID:  prepayID,
		Package:   "Sign=WXPay",
		NonceStr:  nonceStr,
		TimeStamp: timeStamp,
	}

	// 二次签名（appId, partnerId, prepayId, packageValue, nonceStr, timeStamp）
	signData := map[string]string{
		"appid":     resp.AppID,
		"partnerid": resp.PartnerID,
		"prepayid":  resp.PrepayID,
		"package":   resp.Package,
		"noncestr":  resp.NonceStr,
		"timestamp": resp.TimeStamp,
	}
	resp.Sign = wechatSign(signData, s.wechatConfig.APIKey)
	return resp, nil
}

// AlipayOrderRequest 支付宝订单请求
type AlipayOrderRequest struct {
	UserID    string `json:"user_id" binding:"required"`
	PackageID string `json:"package_id"`                // recharge 时必填
	OrderID   string `json:"order_id"`                  // VIP 等已创建订单时传入
	Quantity  int    `json:"quantity" binding:"min=1"` // 默认 1
}

// AlipayOrderResponse 支付宝订单响应
type AlipayOrderResponse struct {
	OrderID     string `json:"order_id"`
	OrderString string `json:"order_string"`
}

// AlipayOrder 创建支付宝订单
func (s *PaymentService) AlipayOrder(req AlipayOrderRequest) (*AlipayOrderResponse, error) {
	// TODO: MVP 阶段为骨架实现，实际应调用支付宝服务端 SDK 生成 orderString
	var order *model.Order
	var err error

	if req.OrderID != "" {
		order, err = s.orderRepo.GetByID(req.OrderID)
		if err != nil {
			return nil, fmt.Errorf("订单不存在")
		}
		if order.UserID != req.UserID {
			return nil, fmt.Errorf("订单归属不一致")
		}
		if order.Status != "pending" {
			return nil, fmt.Errorf("订单状态不正确")
		}
		order.Channel = "alipay"
		if err := s.orderRepo.UpdateStatus(order.ID, "pending"); err != nil {
			return nil, err
		}
	} else {
		packageService := NewRechargePackageService(s.packageRepo)
		resolved, err := packageService.ResolvePackage(req.PackageID, req.Quantity)
		if err != nil {
			return nil, err
		}

		// 1. 生成内部订单号并落库
		orderID := uuid.New().String()
		order = &model.Order{
			ID:        orderID,
			UserID:    req.UserID,
			Channel:   "alipay",
			Amount:    resolved.AmountFen,
			Currency:  resolved.Currency,
			Status:    "pending",
			PackageID: resolved.PackageID,
			Quantity:  resolved.Quantity,
			Danwan:    resolved.Danwan,
		}
		if err := s.orderRepo.Create(order); err != nil {
			return nil, fmt.Errorf("创建订单失败: %w", err)
		}
	}

	// 2. 构造 biz_content JSON
	// 3. 用支付宝 SDK 或 RSA 私钥签名生成请求字符串

	// MVP 占位：返回测试 orderString
	return &AlipayOrderResponse{
		OrderID:     order.ID,
		OrderString: "app_id=支付宝_APP_ID&biz_content=...&sign=TEST_SIGN",
	}, nil
}

// ErrAlipayDisabled 支付宝支付未开通（未配置密钥或 enabled=false）
var ErrAlipayDisabled = errors.New("支付宝支付未开通，请使用兑换码或联系客服")

// AlipayPrecreateRequest 支付宝扫码预下单请求。
// UserID 由 JWT context 注入，不信任客户端传入。
type AlipayPrecreateRequest struct {
	UserID    string `json:"-"`
	PackageID string `json:"package_id"`               // recharge 时必填
	OrderID   string `json:"order_id"`                 // VIP 等已创建订单时传入
	Quantity  int    `json:"quantity" binding:"min=1"` // 默认 1
}

// AlipayPrecreateResponse 支付宝扫码预下单响应
type AlipayPrecreateResponse struct {
	OrderID   string `json:"order_id"`
	QRCode    string `json:"qr_code"`
	AmountFen int64  `json:"amount_fen"`
	Status    string `json:"status"`
}

// AlipayPrecreate 创建（或复用）订单并调用支付宝当面付生成扫码二维码。
// 充值场景传 PackageID 新建 pending 订单；VIP 场景传 OrderID 复用已创建订单。
// 同一订单重复调用会刷新二维码（支付宝侧幂等），用户可关闭收银台后重新打开继续支付。
func (s *PaymentService) AlipayPrecreate(ctx context.Context, req AlipayPrecreateRequest) (*AlipayPrecreateResponse, error) {
	if s.alipayClient == nil {
		return nil, ErrAlipayDisabled
	}

	order, err := s.resolveOrCreateOrder(req.UserID, req.PackageID, req.OrderID, req.Quantity)
	if err != nil {
		return nil, err
	}
	if order.Channel != "alipay" {
		if err := s.orderRepo.UpdateChannel(order.ID, "alipay"); err != nil {
			return nil, fmt.Errorf("更新订单支付渠道失败: %w", err)
		}
		order.Channel = "alipay"
	}

	subject := "Eleball 充值"
	if order.ProductType == "vip" {
		subject = "Eleball VIP 套餐"
	} else if order.Danwan > 0 {
		subject = fmt.Sprintf("Eleball 弹丸充值 %d 弹丸", order.Danwan)
	}

	qrCode, err := s.alipayClient.Precreate(ctx, order.ID, subject, fenToYuan(order.Amount))
	if err != nil {
		return nil, err
	}
	return &AlipayPrecreateResponse{
		OrderID:   order.ID,
		QRCode:    qrCode,
		AmountFen: order.Amount,
		Status:    order.Status,
	}, nil
}

// resolveOrCreateOrder 按入参解析目标订单：order_id 复用已有 pending 订单，package_id 新建充值订单。
func (s *PaymentService) resolveOrCreateOrder(userID, packageID, orderID string, quantity int) (*model.Order, error) {
	if orderID != "" {
		order, err := s.orderRepo.GetByID(orderID)
		if err != nil {
			return nil, fmt.Errorf("订单不存在")
		}
		if order.UserID != userID {
			return nil, fmt.Errorf("订单归属不一致")
		}
		if order.Status != "pending" {
			return nil, fmt.Errorf("订单状态不正确")
		}
		return order, nil
	}
	if packageID == "" {
		return nil, fmt.Errorf("package_id 与 order_id 至少提供一个")
	}

	packageService := NewRechargePackageService(s.packageRepo)
	resolved, err := packageService.ResolvePackage(packageID, quantity)
	if err != nil {
		return nil, err
	}
	order := &model.Order{
		ID:        uuid.New().String(),
		UserID:    userID,
		Channel:   "alipay",
		Amount:    resolved.AmountFen,
		Currency:  resolved.Currency,
		Status:    "pending",
		PackageID: resolved.PackageID,
		Quantity:  resolved.Quantity,
		Danwan:    resolved.Danwan,
	}
	if err := s.orderRepo.Create(order); err != nil {
		return nil, fmt.Errorf("创建订单失败: %w", err)
	}
	return order, nil
}

// HandleAlipayNotify 处理支付宝异步通知：验签 → 状态/app_id/金额校验 → 幂等发放权益。
// 返回 nil 表示 ack（含“非成功状态不处理”）；返回 error 时 handler 应答 fail，由支付宝重试。
func (s *PaymentService) HandleAlipayNotify(ctx context.Context, values url.Values) error {
	if s.alipayClient == nil {
		return errors.New("支付宝支付未配置")
	}
	n, err := s.alipayClient.VerifyNotification(ctx, values)
	if err != nil {
		return fmt.Errorf("支付宝回调验签失败: %w", err)
	}
	// 非支付成功状态：ack 但不落单（如 WAIT_BUYER_PAY / TRADE_CLOSED）
	if n.TradeStatus != alipay.TradeStatusSuccess && n.TradeStatus != alipay.TradeStatusFinished {
		return nil
	}
	if n.AppId != s.alipayClient.AppID() {
		return fmt.Errorf("回调 app_id 与配置不匹配")
	}
	order, err := s.orderRepo.GetByID(n.OutTradeNo)
	if err != nil {
		return fmt.Errorf("回调订单不存在: %s", n.OutTradeNo)
	}
	// 金额校验：total_amount（元）换算分后必须与订单金额一致，防伪造小额单
	if yuanToFen(parseYuanAmount(n.TotalAmount)) != order.Amount {
		return fmt.Errorf("回调金额与订单金额不匹配: notify=%s order=%d", n.TotalAmount, order.Amount)
	}
	return s.ProcessPaidOrder(order.ID, n.TradeNo)
}

// OrderStatusResult 订单状态查询结果（收银台轮询）
type OrderStatusResult struct {
	OrderID     string     `json:"order_id"`
	Status      string     `json:"status"`
	ProductType string     `json:"product_type"`
	AmountFen   int64      `json:"amount_fen"`
	PaidAt      *time.Time `json:"paid_at"`
}

// GetOrderStatus 查询订单支付状态（仅订单所有者可查，纯展示不触发权益变更）。
func (s *PaymentService) GetOrderStatus(userID, orderID string) (*OrderStatusResult, error) {
	order, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		return nil, fmt.Errorf("订单不存在")
	}
	if order.UserID != userID {
		return nil, fmt.Errorf("订单归属不一致")
	}
	return &OrderStatusResult{
		OrderID:     order.ID,
		Status:      order.Status,
		ProductType: order.ProductType,
		AmountFen:   order.Amount,
		PaidAt:      order.PaidAt,
	}, nil
}

// fenToYuan 分转元（保留两位小数字符串，支付宝金额格式）
func fenToYuan(fen int64) string {
	return fmt.Sprintf("%.2f", float64(fen)/100)
}

// parseYuanAmount 解析支付宝回传的元金额（如 "0.01"）为 float64
func parseYuanAmount(yuan string) float64 {
	var v float64
	_, _ = fmt.Sscanf(strings.TrimSpace(yuan), "%f", &v)
	return v
}

// wechatSign 微信签名（MD5）
func wechatSign(data map[string]string, apiKey string) string {
	var keys []string
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, data[k]))
	}
	signStr := strings.Join(parts, "&") + "&key=" + apiKey
	hash := md5.Sum([]byte(signStr))
	return strings.ToUpper(hex.EncodeToString(hash[:]))
}

// randomString 生成随机字符串
func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return strings.Repeat("0", n)
	}
	for i := range b {
		b[i] = letters[b[i]%byte(len(letters))]
	}
	return string(b)
}

// ProcessPaidOrder 订单支付成功后处理：VIP 订单激活会员，充值订单到账弹丸。
// 幂等：内部以「pending→paid 条件更新认领」作为唯一发放凭证，
// 支付宝重复通知、notify 与管理员确认并发等场景下权益只发放一次。
// tradeNo 为支付宝交易号（管理员确认等无渠道单号场景传空串，不覆盖已有值）。
func (s *PaymentService) ProcessPaidOrder(orderID, tradeNo string) error {
	order, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		return fmt.Errorf("订单不存在")
	}
	if order.Status == "paid" {
		return nil
	}
	if order.Status != "pending" {
		return fmt.Errorf("订单状态不正确")
	}

	switch order.ProductType {
	case "vip":
		if s.vipService == nil {
			return fmt.Errorf("VIP 服务未初始化")
		}
		return s.vipService.ActivateSubscription(orderID, tradeNo)
	default:
		// recharge：事务内先认领再发放，任一环节失败整体回滚
		return s.db.Transaction(func(tx *gorm.DB) error {
			orderTx := repository.NewOrderRepo(tx)
			claimed, err := orderTx.ClaimPaid(order.ID, tradeNo, time.Now())
			if err != nil {
				return err
			}
			if !claimed {
				return nil // 已被并发流程处理（重复通知），幂等返回
			}
			userTx := repository.NewUserRepo(tx)
			billTx := repository.NewBillingRepo(tx)
			if order.Danwan > 0 {
				user, err := userTx.GetByID(order.UserID)
				if err != nil {
					return err
				}
				if err := userTx.UpdateBalance(order.UserID, order.Danwan); err != nil {
					return err
				}
				if err := userTx.UpdateTotalRecharged(order.UserID, order.Danwan); err != nil {
					return err
				}
				txRecord := &model.BalanceTransaction{
					ID:           uuid.New().String(),
					UserID:       order.UserID,
					Type:         "recharge",
					Amount:       order.Danwan,
					Currency:     CurrencyDanwan,
					BalanceAfter: user.Balance + order.Danwan,
					Description:  fmt.Sprintf("充值到账: %s", order.Channel),
				}
				if err := billTx.CreateTransaction(txRecord); err != nil {
					return err
				}
			}
			return nil
		})
	}
}

// VerifyWechatNotify 校验微信支付异步通知（预留骨架，未接入）
func (s *PaymentService) VerifyWechatNotify(body []byte) bool {
	// TODO: 解析 XML，校验签名，更新订单状态
	return true
}
