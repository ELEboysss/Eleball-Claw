package handler_test

import (
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/internal/service"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// newTestVIPService 为 handler 测试快速构造一个基于内存数据库的 VIPService
func newTestVIPService(db *gorm.DB) *service.VIPService {
	return service.NewVIPService(
		db,
		repository.NewVIPRepo(db),
		repository.NewUserRepo(db),
		repository.NewBillingRepo(db),
		repository.NewOrderRepo(db),
		zap.NewNop(),
	)
}
