package service

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/eleball/gateway/internal/config"
	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	sqlite "github.com/glebarez/sqlite"
	"github.com/google/uuid"
	alipay "github.com/smartwalle/alipay/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ====== 测试辅助 ======

// genAlipayTestKeys 生成 RSA2 测试密钥对（应用/平台两侧各一对）
func genAlipayTestKeys(t *testing.T) (privPEM, pubPEM string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privBytes := x509.MarshalPKCS1PrivateKey(key)
	privPEM = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: privBytes}))
	pubBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	pubPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes}))
	return privPEM, pubPEM
}

// signAlipayNotifyValues 模拟支付宝侧对异步通知参数做 RSA2 签名
// （字典序拼接 k=v，排除 sign/sign_type，SHA256withRSA 后 base64）
func signAlipayNotifyValues(t *testing.T, values url.Values, privPEM string) string {
	t.Helper()
	block, _ := pem.Decode([]byte(privPEM))
	require.NotNil(t, block)
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	require.NoError(t, err)

	keys := make([]string, 0, len(values))
	for k := range values {
		if k == "sign" || k == "sign_type" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+values.Get(k))
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "&")))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(sig)
}

// newTestAlipayClient 构造使用测试密钥的支付宝客户端：
// appPriv 为应用私钥（SDK 签名用），platformPub 为“支付宝平台”公钥（验签用）。
func newTestAlipayClient(t *testing.T, appID, appPriv, platformPub string) *AlipayClient {
	t.Helper()
	client, err := alipay.New(appID, appPriv, false)
	require.NoError(t, err)
	require.NoError(t, client.LoadAliPayPublicKey(platformPub))
	return &AlipayClient{
		cfg:    config.AlipayPaymentConfig{Enabled: true, AppID: appID, NotifyURL: "http://localhost/v1/payment/alipay/notify"},
		client: client,
	}
}

// setupPaymentServiceTest 创建基于内存数据库的 PaymentService 测试环境
func setupPaymentServiceTest(t *testing.T, alipayClient *AlipayClient) (*gorm.DB, *PaymentService) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.BalanceTransaction{}, &model.Order{}, &model.VIPPlan{}, &model.VIPSubscription{}))

	userRepo := repository.NewUserRepo(db)
	billRepo := repository.NewBillingRepo(db)
	orderRepo := repository.NewOrderRepo(db)
	packageRepo := repository.NewRechargePackageRepo(db)
	vipService := newTestVIPService(db)
	return db, NewPaymentService(db, userRepo, packageRepo, orderRepo, billRepo, vipService, alipayClient)
}

func createPaymentTestUser(t *testing.T, db *gorm.DB) *model.User {
	t.Helper()
	user := &model.User{ID: uuid.New().String(), Username: "pay_user_" + uuid.New().String()[:8], Password: "x"}
	require.NoError(t, db.Create(user).Error)
	return user
}

// buildSignedNotify 构造一份带有效签名的支付宝回调参数
func buildSignedNotify(t *testing.T, appID, outTradeNo, tradeNo, totalAmount, tradeStatus, platformPriv string) url.Values {
	values := url.Values{}
	values.Set("app_id", appID)
	values.Set("out_trade_no", outTradeNo)
	values.Set("trade_no", tradeNo)
	values.Set("total_amount", totalAmount)
	values.Set("trade_status", tradeStatus)
	values.Set("notify_time", time.Now().Format("2006-01-02 15:04:05"))
	values.Set("sign_type", "RSA2")
	values.Set("sign", signAlipayNotifyValues(t, values, platformPriv))
	return values
}

// ====== 用例 ======

// TestAlipayNotifySuccessAndIdempotent 验签通过 → 充值到账；重复通知幂等只到账一次
func TestAlipayNotifySuccessAndIdempotent(t *testing.T) {
	appPriv, _ := genAlipayTestKeys(t)
	platformPriv, platformPub := genAlipayTestKeys(t)
	client := newTestAlipayClient(t, "test-app-id", appPriv, platformPub)
	db, svc := setupPaymentServiceTest(t, client)
	user := createPaymentTestUser(t, db)

	order := &model.Order{
		ID: uuid.New().String(), UserID: user.ID, Channel: "alipay",
		Amount: 100, Currency: "danwan", Status: "pending", Danwan: 1000,
	}
	require.NoError(t, db.Create(order).Error)

	values := buildSignedNotify(t, "test-app-id", order.ID, "ALI_TRADE_1", "1.00", "TRADE_SUCCESS", platformPriv)
	require.NoError(t, svc.HandleAlipayNotify(context.Background(), values))

	// 订单已支付，交易号落库
	var updated model.Order
	require.NoError(t, db.First(&updated, "id = ?", order.ID).Error)
	assert.Equal(t, "paid", updated.Status)
	assert.Equal(t, "ALI_TRADE_1", updated.TradeNo)
	assert.NotNil(t, updated.PaidAt)

	// 弹丸到账 + 流水
	var u model.User
	require.NoError(t, db.First(&u, "id = ?", user.ID).Error)
	assert.Equal(t, int64(1000), u.Balance)
	assert.Equal(t, int64(1000), u.TotalRecharged)
	var txCount int64
	db.Model(&model.BalanceTransaction{}).Where("user_id = ? AND type = ?", user.ID, "recharge").Count(&txCount)
	assert.Equal(t, int64(1), txCount)

	// 支付宝重复通知（同交易号）：幂等，余额不变
	require.NoError(t, svc.HandleAlipayNotify(context.Background(), values))
	require.NoError(t, db.First(&u, "id = ?", user.ID).Error)
	assert.Equal(t, int64(1000), u.Balance)
	db.Model(&model.BalanceTransaction{}).Where("user_id = ? AND type = ?", user.ID, "recharge").Count(&txCount)
	assert.Equal(t, int64(1), txCount)
}

// TestAlipayNotifyBadSign 签名被篡改 → 拒绝处理，订单保持 pending
func TestAlipayNotifyBadSign(t *testing.T) {
	appPriv, _ := genAlipayTestKeys(t)
	platformPriv, platformPub := genAlipayTestKeys(t)
	client := newTestAlipayClient(t, "test-app-id", appPriv, platformPub)
	db, svc := setupPaymentServiceTest(t, client)
	user := createPaymentTestUser(t, db)

	order := &model.Order{
		ID: uuid.New().String(), UserID: user.ID, Channel: "alipay",
		Amount: 100, Currency: "danwan", Status: "pending", Danwan: 1000,
	}
	require.NoError(t, db.Create(order).Error)

	values := buildSignedNotify(t, "test-app-id", order.ID, "ALI_TRADE_2", "1.00", "TRADE_SUCCESS", platformPriv)
	// 签名后篡改金额（不重新签名）
	values.Set("total_amount", "999.00")
	require.Error(t, svc.HandleAlipayNotify(context.Background(), values))

	var updated model.Order
	require.NoError(t, db.First(&updated, "id = ?", order.ID).Error)
	assert.Equal(t, "pending", updated.Status)
}

// TestAlipayNotifyAmountMismatch 签名有效但金额与订单不一致 → 拒绝（防伪造小额单）
func TestAlipayNotifyAmountMismatch(t *testing.T) {
	appPriv, _ := genAlipayTestKeys(t)
	platformPriv, platformPub := genAlipayTestKeys(t)
	client := newTestAlipayClient(t, "test-app-id", appPriv, platformPub)
	db, svc := setupPaymentServiceTest(t, client)
	user := createPaymentTestUser(t, db)

	order := &model.Order{
		ID: uuid.New().String(), UserID: user.ID, Channel: "alipay",
		Amount: 100, Currency: "danwan", Status: "pending", Danwan: 1000,
	}
	require.NoError(t, db.Create(order).Error)

	values := buildSignedNotify(t, "test-app-id", order.ID, "ALI_TRADE_3", "0.01", "TRADE_SUCCESS", platformPriv)
	require.Error(t, svc.HandleAlipayNotify(context.Background(), values))

	var updated model.Order
	require.NoError(t, db.First(&updated, "id = ?", order.ID).Error)
	assert.Equal(t, "pending", updated.Status)
}

// TestAlipayNotifyNonSuccessStatus 非成功状态 ack 但不落单
func TestAlipayNotifyNonSuccessStatus(t *testing.T) {
	appPriv, _ := genAlipayTestKeys(t)
	platformPriv, platformPub := genAlipayTestKeys(t)
	client := newTestAlipayClient(t, "test-app-id", appPriv, platformPub)
	db, svc := setupPaymentServiceTest(t, client)
	user := createPaymentTestUser(t, db)

	order := &model.Order{
		ID: uuid.New().String(), UserID: user.ID, Channel: "alipay",
		Amount: 100, Currency: "danwan", Status: "pending", Danwan: 1000,
	}
	require.NoError(t, db.Create(order).Error)

	values := buildSignedNotify(t, "test-app-id", order.ID, "ALI_TRADE_4", "1.00", "WAIT_BUYER_PAY", platformPriv)
	require.NoError(t, svc.HandleAlipayNotify(context.Background(), values))

	var updated model.Order
	require.NoError(t, db.First(&updated, "id = ?", order.ID).Error)
	assert.Equal(t, "pending", updated.Status)
}

// TestProcessPaidVIPOrderIdempotent VIP 订单支付成功 → 开通会员；重复处理只开通一次
func TestProcessPaidVIPOrderIdempotent(t *testing.T) {
	db, svc := setupPaymentServiceTest(t, nil)
	user := createPaymentTestUser(t, db)

	plan := &model.VIPPlan{ID: uuid.New().String(), Level: 1, Name: "月卡", PriceFen: 3000, DurationDays: 30, IsEnabled: true, DiscountPercent: 100}
	require.NoError(t, db.Create(plan).Error)
	order := &model.Order{
		ID: uuid.New().String(), UserID: user.ID, Channel: "alipay",
		Amount: 3000, Currency: "cny", Status: "pending",
		ProductType: "vip", VIPPlanID: plan.ID,
	}
	require.NoError(t, db.Create(order).Error)

	require.NoError(t, svc.ProcessPaidOrder(order.ID, "ALI_TRADE_VIP"))

	var u model.User
	require.NoError(t, db.First(&u, "id = ?", user.ID).Error)
	assert.Equal(t, 1, u.VIPLevel)
	var subCount int64
	db.Model(&model.VIPSubscription{}).Where("user_id = ?", user.ID).Count(&subCount)
	assert.Equal(t, int64(1), subCount)

	// 重复处理（如 notify 与管理员确认并发）：幂等，订阅数不变
	require.NoError(t, svc.ProcessPaidOrder(order.ID, "ALI_TRADE_VIP"))
	db.Model(&model.VIPSubscription{}).Where("user_id = ?", user.ID).Count(&subCount)
	assert.Equal(t, int64(1), subCount)
}

// TestAlipayPrecreateDisabled 未配置支付宝时返回“支付未开通”
func TestAlipayPrecreateDisabled(t *testing.T) {
	db, svc := setupPaymentServiceTest(t, nil)
	user := createPaymentTestUser(t, db)

	_, err := svc.AlipayPrecreate(context.Background(), AlipayPrecreateRequest{
		UserID: user.ID, PackageID: "any", Quantity: 1,
	})
	assert.ErrorIs(t, err, ErrAlipayDisabled)
}

// TestGetOrderStatus 订单状态查询的归属校验
func TestGetOrderStatus(t *testing.T) {
	db, svc := setupPaymentServiceTest(t, nil)
	user := createPaymentTestUser(t, db)
	other := createPaymentTestUser(t, db)

	order := &model.Order{
		ID: uuid.New().String(), UserID: user.ID, Channel: "alipay",
		Amount: 100, Currency: "danwan", Status: "pending", Danwan: 1000,
	}
	require.NoError(t, db.Create(order).Error)

	res, err := svc.GetOrderStatus(user.ID, order.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", res.Status)
	assert.Equal(t, "recharge", res.ProductType)
	assert.Equal(t, int64(100), res.AmountFen)

	// 他人订单不可查
	_, err = svc.GetOrderStatus(other.ID, order.ID)
	require.Error(t, err)
}

// TestClaimPaidConcurrentSafe ClaimPaid 条件更新仅首次生效
func TestClaimPaidConcurrentSafe(t *testing.T) {
	db, _ := setupPaymentServiceTest(t, nil)
	user := createPaymentTestUser(t, db)
	orderRepo := repository.NewOrderRepo(db)

	order := &model.Order{
		ID: uuid.New().String(), UserID: user.ID, Channel: "alipay",
		Amount: 100, Currency: "danwan", Status: "pending",
	}
	require.NoError(t, db.Create(order).Error)

	claimed, err := orderRepo.ClaimPaid(order.ID, "T1", time.Now())
	require.NoError(t, err)
	assert.True(t, claimed)

	// 第二次认领失败
	claimed, err = orderRepo.ClaimPaid(order.ID, "T2", time.Now())
	require.NoError(t, err)
	assert.False(t, claimed)

	var updated model.Order
	require.NoError(t, db.First(&updated, "id = ?", order.ID).Error)
	assert.Equal(t, "T1", updated.TradeNo) // 交易号保留首次值
}
