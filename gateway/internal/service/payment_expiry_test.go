package service

import (
	"context"
	"testing"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// forceOrderCreatedAt 绕过 GORM CreatedAt 自动管理，直接回写订单创建时间（构造过期订单用）
func forceOrderCreatedAt(t *testing.T, db *gorm.DB, orderID string, at time.Time) {
	t.Helper()
	require.NoError(t, db.Exec("UPDATE orders SET created_at = ? WHERE id = ?", at, orderID).Error)
}

// TestSweepExpiredOrders 超时 pending 订单自动关闭；未到期/已支付订单不受影响
func TestSweepExpiredOrders(t *testing.T) {
	db, svc := setupPaymentServiceTest(t, nil) // 未配置支付宝：直接关闭本地订单
	user := createPaymentTestUser(t, db)
	now := time.Now()

	mkOrder := func(status string, createdAt time.Time, productType string) *model.Order {
		o := &model.Order{
			ID: uuid.New().String(), UserID: user.ID, Channel: "alipay",
			Amount: 100, Currency: "danwan", Status: status, Danwan: 100,
			ProductType: productType,
		}
		require.NoError(t, db.Create(o).Error)
		forceOrderCreatedAt(t, db, o.ID, createdAt)
		return o
	}

	stalePending := mkOrder("pending", now.Add(-31*time.Minute), "recharge")     // 过期 → closed
	stalePendingVIP := mkOrder("pending", now.Add(-45*time.Minute), "vip")      // 过期 → closed
	freshPending := mkOrder("pending", now.Add(-5*time.Minute), "recharge")     // 未过期 → 保持 pending
	stalePaid := mkOrder("paid", now.Add(-2*time.Hour), "recharge")             // 已支付 → 不受影响
	staleRefunded := mkOrder("refunded", now.Add(-3*time.Hour), "recharge")     // 已退款 → 不受影响

	res := svc.SweepExpiredOrders(context.Background(), now.Add(-30*time.Minute))
	assert.Equal(t, 2, res.Closed)
	assert.Equal(t, 0, res.Paid)
	assert.Equal(t, 0, res.Skipped)

	statusOf := func(id string) string {
		var o model.Order
		require.NoError(t, db.First(&o, "id = ?", id).Error)
		return o.Status
	}
	assert.Equal(t, "closed", statusOf(stalePending.ID))
	assert.Equal(t, "closed", statusOf(stalePendingVIP.ID))
	assert.Equal(t, "pending", statusOf(freshPending.ID))
	assert.Equal(t, "paid", statusOf(stalePaid.ID))
	assert.Equal(t, "refunded", statusOf(staleRefunded.ID))

	// 幂等：再扫一轮无新增关闭（stale 订单已是 closed，不再被处理）
	res = svc.SweepExpiredOrders(context.Background(), now.Add(-30*time.Minute))
	assert.Equal(t, 0, res.Closed)
}

// TestOrderExpiryJobDefaults 过期时长默认值与自定义设置
func TestOrderExpiryJobDefaults(t *testing.T) {
	_, svc := setupPaymentServiceTest(t, nil)
	assert.Equal(t, 30*time.Minute, svc.orderExpire)
	svc.SetOrderExpiry(15 * time.Minute)
	assert.Equal(t, 15*time.Minute, svc.orderExpire)
	svc.SetOrderExpiry(0) // 非法值不覆盖
	assert.Equal(t, 15*time.Minute, svc.orderExpire)
}
