package service

import (
	"encoding/json"
	"fmt"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/google/uuid"
)

// ActivityService 动态事件服务
type ActivityService struct {
	repo *repository.ActivityRepo
}

// NewActivityService 创建服务
func NewActivityService(repo *repository.ActivityRepo) *ActivityService {
	return &ActivityService{repo: repo}
}

// RecordUserRegistered 记录用户注册动态
func (s *ActivityService) RecordUserRegistered(userID, username string) {
	if s == nil || s.repo == nil {
		return
	}
	metadata, _ := json.Marshal(map[string]string{"username": username})
	event := &model.ActivityEvent{
		ID:          uuid.New().String(),
		UserID:      userID,
		Type:        model.ActivityEventUserRegistered,
		Title:       "新用户注册",
		Description: fmt.Sprintf("用户 %s（user_id:%s）注册了账户", username, userID),
		Metadata:    string(metadata),
	}
	_ = s.repo.Create(event)
}

// RecordUserRecharged 记录用户充值动态
func (s *ActivityService) RecordUserRecharged(userID string, amount int64, currency string) {
	if s == nil || s.repo == nil {
		return
	}
	currencyLabel := "弹丸"
	if currency == CurrencyElegant {
		currencyLabel = "优雅弹丸"
	}
	metadata, _ := json.Marshal(map[string]interface{}{
		"amount":   amount,
		"currency": currency,
	})
	event := &model.ActivityEvent{
		ID:          uuid.New().String(),
		UserID:      userID,
		Type:        model.ActivityEventUserRecharged,
		Title:       "用户充值",
		Description: fmt.Sprintf("用户（user_id:%s）充值了 %d %s，花费 ¥%.2f", userID, amount, currencyLabel, float64(amount)/100),
		Metadata:    string(metadata),
	}
	_ = s.repo.Create(event)
}

// RecordModelUsage 记录模型调用扣费动态
func (s *ActivityService) RecordModelUsage(userID, username, provider, modelName string, amount int64, currency string, inputTokens, outputTokens int64) {
	if s == nil || s.repo == nil {
		return
	}
	currencyLabel := "弹丸"
	if currency == CurrencyElegant {
		currencyLabel = "优雅弹丸"
	}
	totalTokens := inputTokens + outputTokens
	metadata, _ := json.Marshal(map[string]interface{}{
		"amount":        amount,
		"currency":      currency,
		"provider":      provider,
		"model":         modelName,
		"input_tokens":  inputTokens,
		"output_tokens": outputTokens,
		"total_tokens":  totalTokens,
		"tokens":        totalTokens, // 兼容旧版前端/统计
	})
	event := &model.ActivityEvent{
		ID:          uuid.New().String(),
		UserID:      userID,
		Type:        model.ActivityEventModelUsage,
		Title:       "模型调用扣费",
		Description: fmt.Sprintf("用户 %s 调用 %s/%s 消耗 %d %s", username, provider, modelName, amount, currencyLabel),
		Metadata:    string(metadata),
	}
	_ = s.repo.Create(event)
}

// ListRecent 查询最近动态
func (s *ActivityService) ListRecent(limit int) ([]*model.ActivityEvent, error) {
	if s == nil || s.repo == nil {
		return []*model.ActivityEvent{}, nil
	}
	return s.repo.ListRecent(limit)
}
