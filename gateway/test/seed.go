package test

import (
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SeedTestData 向测试数据库写入种子数据
func SeedTestData(db *gorm.DB) error {
	users := []model.User{
		{
			ID:        uuid.New().String(),
			Username:  "admin",
			Nickname:  "Admin",
			Role:      model.UserRoleAdmin,
			Balance:   100000,
			Status:    1,
			CreatedAt: time.Now().AddDate(0, -2, 0),
		},
		{
			ID:        "u_1001",
			Username:  "alice",
			Nickname:  "Alice",
			Role:      model.UserRoleUser,
			Balance:   5200,
			Status:    1,
			CreatedAt: time.Now().AddDate(0, -1, -5),
		},
		{
			ID:        uuid.New().String(),
			Username:  "bob",
			Nickname:  "Bob",
			Role:      model.UserRoleUser,
			Balance:   1200,
			Status:    1,
			CreatedAt: time.Now().AddDate(0, -1, -2),
		},
		{
			ID:        uuid.New().String(),
			Username:  "charlie",
			Nickname:  "Charlie",
			Role:      model.UserRoleUser,
			Balance:   0,
			Status:    0, // 禁用
			CreatedAt: time.Now().AddDate(0, 0, -7),
		},
	}

	for i := range users {
		if err := db.Create(&users[i]).Error; err != nil {
			return err
		}
	}

	transactions := []model.BalanceTransaction{
		{ID: uuid.New().String(), UserID: users[1].ID, Type: "consume", Amount: -1200, Description: "GPT-4 对话", CreatedAt: time.Now().Add(-2 * time.Hour)},
		{ID: uuid.New().String(), UserID: users[2].ID, Type: "recharge", Amount: 5000, Description: "微信支付充值", CreatedAt: time.Now().Add(-3 * time.Hour)},
		{ID: uuid.New().String(), UserID: users[1].ID, Type: "consume", Amount: -800, Description: "DeepSeek 对话", CreatedAt: time.Now().Add(-5 * time.Hour)},
		{ID: uuid.New().String(), UserID: users[1].ID, Type: "refund", Amount: 500, Description: "订单退款", CreatedAt: time.Now().Add(-24 * time.Hour)},
	}

	for i := range transactions {
		if err := db.Create(&transactions[i]).Error; err != nil {
			return err
		}
	}

	orders := []model.Order{
		{ID: uuid.New().String(), UserID: users[1].ID, Channel: "wechat", Amount: 9900, Status: "paid", CreatedAt: time.Now().Add(-3 * time.Hour)},
		{ID: uuid.New().String(), UserID: users[2].ID, Channel: "alipay", Amount: 5000, Status: "paid", CreatedAt: time.Now().Add(-24 * time.Hour)},
		{ID: uuid.New().String(), UserID: users[1].ID, Channel: "wechat", Amount: 9900, Status: "pending", CreatedAt: time.Now().Add(-1 * time.Hour)},
	}

	for i := range orders {
		if err := db.Create(&orders[i]).Error; err != nil {
			return err
		}
	}

	return nil
}

// SeedAdminUser 创建管理员账号（用于登录测试）
func SeedAdminUser(db *gorm.DB, passwordHash string) (string, error) {
	admin := model.User{
		ID:        uuid.New().String(),
		Username:  "admin",
		Nickname:  "Super Admin",
		Password:  passwordHash,
		Role:      model.UserRoleAdmin,
		Balance:   0,
		Status:    1,
		CreatedAt: time.Now(),
	}
	if err := db.Create(&admin).Error; err != nil {
		return "", err
	}
	return admin.ID, nil
}
