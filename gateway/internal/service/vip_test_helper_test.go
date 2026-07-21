package service

import (
	"github.com/eleball/gateway/internal/repository"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// newTestVIPService 为 service 包测试快速构造一个基于内存数据库的 VIPService
func newTestVIPService(db *gorm.DB) *VIPService {
	userRepo := repository.NewUserRepo(db)
	billRepo := repository.NewBillingRepo(db)
	orderRepo := repository.NewOrderRepo(db)
	vipRepo := repository.NewVIPRepo(db)
	return NewVIPService(db, vipRepo, userRepo, billRepo, orderRepo, zap.NewNop())
}
