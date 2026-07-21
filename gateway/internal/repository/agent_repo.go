package repository

import (
	"errors"

	"github.com/eleball/gateway/internal/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AgentRepo 秘技数据访问
type AgentRepo struct {
	db *gorm.DB
}

// NewAgentRepo 创建仓库
func NewAgentRepo(db *gorm.DB) *AgentRepo {
	return &AgentRepo{db: db}
}

// Create 创建秘技
func (r *AgentRepo) Create(agent *model.AgentItem) error {
	return r.db.Create(agent).Error
}

// Count 查询已上架秘技总数
func (r *AgentRepo) Count() (int64, error) {
	var count int64
	err := r.db.Model(&model.AgentItem{}).Count(&count).Error
	return count, err
}

// GetCategories 查询已上架秘技的所有分类（去重，不含空值）
func (r *AgentRepo) GetCategories() ([]string, error) {
	var categories []string
	err := r.db.Model(&model.AgentItem{}).
		Where("status = ?", model.AgentStatusApproved).
		Where("category != ?", "").
		Distinct("category").
		Pluck("category", &categories).Error
	return categories, err
}

// GetByID 根据 ID 查询
func (r *AgentRepo) GetByID(id string) (*model.AgentItem, error) {
	var agent model.AgentItem
	if err := r.db.First(&agent, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &agent, nil
}

// List 秘技列表（支持分页、分类、排序）
func (r *AgentRepo) List(page, pageSize int, category, sortBy string) ([]*model.AgentItem, int64, error) {
	var items []*model.AgentItem
	var total int64

	query := r.db.Model(&model.AgentItem{}).Where("status = ?", model.AgentStatusApproved)
	if category != "" {
		query = query.Where("category = ?", category)
	}

	switch sortBy {
	case "hot":
		query = query.Order("purchase_count DESC")
	case "rating":
		query = query.Order("avg_rating DESC")
	case "new":
		query = query.Order("created_at DESC")
	default:
		query = query.Order("purchase_count DESC")
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if err := query.Offset(offset).Limit(pageSize).Find(&items).Error; err != nil {
		return nil, 0, err
	}

	return items, total, nil
}

// ListByCreator 查询某用户创建的秘技
func (r *AgentRepo) ListByCreator(creatorID string) ([]*model.AgentItem, error) {
	var items []*model.AgentItem
	err := r.db.Where("creator_id = ?", creatorID).Order("created_at DESC").Find(&items).Error
	return items, err
}

// ListPurchasedByUser 查询某用户已购买的所有秘技
func (r *AgentRepo) ListPurchasedByUser(buyerID string) ([]*model.AgentItem, error) {
	var items []*model.AgentItem
	err := r.db.Table("agent_items").
		Joins("JOIN agent_purchases ON agent_purchases.agent_id = agent_items.id").
		Where("agent_purchases.buyer_id = ?", buyerID).
		Where("agent_items.status = ?", model.AgentStatusApproved).
		Order("agent_purchases.created_at DESC").
		Find(&items).Error
	return items, err
}

// Update 更新秘技
func (r *AgentRepo) Update(agent *model.AgentItem) error {
	return r.db.Save(agent).Error
}

// ListByStatus 按状态查询秘技（含分页，管理员审核用）
func (r *AgentRepo) ListByStatus(status model.AgentStatus, page, pageSize int) ([]*model.AgentItem, int64, error) {
	var items []*model.AgentItem
	var total int64

	query := r.db.Model(&model.AgentItem{}).Where("status = ?", status)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// UpdateStatus 更新秘技状态
func (r *AgentRepo) UpdateStatus(id string, status model.AgentStatus) error {
	return r.db.Model(&model.AgentItem{}).Where("id = ?", id).Update("status", status).Error
}

// UpdateStats 更新统计数据（购买数、评分、收藏、使用次数）
func (r *AgentRepo) UpdateStats(id string, purchaseCount int64, avgRating float64, favoriteCount, useCount int64) error {
	return r.db.Model(&model.AgentItem{}).Where("id = ?", id).Updates(map[string]interface{}{
		"purchase_count": purchaseCount,
		"avg_rating":     avgRating,
		"favorite_count": favoriteCount,
		"use_count":      useCount,
	}).Error
}

// UpdateLevel 更新等级
func (r *AgentRepo) UpdateLevel(id string, level model.AgentLevel) error {
	return r.db.Model(&model.AgentItem{}).Where("id = ?", id).Update("level", level).Error
}

// ====== 购买记录 ======

// CreatePurchase 创建购买记录
func (r *AgentRepo) CreatePurchase(purchase *model.AgentPurchase) error {
	return r.db.Create(purchase).Error
}

// HasPurchased 检查用户是否已购买
func (r *AgentRepo) HasPurchased(agentID, buyerID string) (bool, error) {
	var count int64
	err := r.db.Model(&model.AgentPurchase{}).
		Where("agent_id = ? AND buyer_id = ?", agentID, buyerID).
		Count(&count).Error
	return count > 0, err
}

// CountPurchases 统计购买次数
func (r *AgentRepo) CountPurchases(agentID string) (int64, error) {
	var count int64
	err := r.db.Model(&model.AgentPurchase{}).Where("agent_id = ?", agentID).Count(&count).Error
	return count, err
}

// ====== 评价 ======

// CreateReview 创建评价
func (r *AgentRepo) CreateReview(review *model.AgentReview) error {
	return r.db.Create(review).Error
}

// ListReviews 评价列表
func (r *AgentRepo) ListReviews(agentID string, page, pageSize int) ([]*model.AgentReview, int64, error) {
	var items []*model.AgentReview
	var total int64

	query := r.db.Model(&model.AgentReview{}).Where("agent_id = ?", agentID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// AvgRating 平均评分
func (r *AgentRepo) AvgRating(agentID string) (float64, error) {
	var result float64
	err := r.db.Model(&model.AgentReview{}).
		Where("agent_id = ?", agentID).
		Select("COALESCE(AVG(rating), 0)").
		Scan(&result).Error
	return result, err
}

// ====== 收藏 ======

// CreateFavorite 收藏
func (r *AgentRepo) CreateFavorite(fav *model.AgentFavorite) error {
	return r.db.Create(fav).Error
}

// DeleteFavorite 取消收藏
func (r *AgentRepo) DeleteFavorite(agentID, userID string) error {
	return r.db.Where("agent_id = ? AND user_id = ?", agentID, userID).
		Delete(&model.AgentFavorite{}).Error
}

// IsFavorited 是否已收藏
func (r *AgentRepo) IsFavorited(agentID, userID string) (bool, error) {
	var count int64
	err := r.db.Model(&model.AgentFavorite{}).
		Where("agent_id = ? AND user_id = ?", agentID, userID).
		Count(&count).Error
	return count > 0, err
}

// CountFavorites 收藏数
func (r *AgentRepo) CountFavorites(agentID string) (int64, error) {
	var count int64
	err := r.db.Model(&model.AgentFavorite{}).Where("agent_id = ?", agentID).Count(&count).Error
	return count, err
}

// ====== 开发者账户 ======

// GetDeveloperAccount 查询开发者账户
func (r *AgentRepo) GetDeveloperAccount(userID string) (*model.DeveloperAccount, error) {
	var acc model.DeveloperAccount
	if err := r.db.First(&acc, "user_id = ?", userID).Error; err != nil {
		return nil, err
	}
	return &acc, nil
}

// CreateOrUpdateDeveloperAccount 创建或更新开发者账户
func (r *AgentRepo) CreateOrUpdateDeveloperAccount(acc *model.DeveloperAccount) error {
	return r.db.Save(acc).Error
}

// IncrementElegantBalance 增加优雅弹丸余额
func (r *AgentRepo) IncrementElegantBalance(userID string, amount int64) error {
	return r.db.Model(&model.DeveloperAccount{}).
		Where("user_id = ?", userID).
		Updates(map[string]interface{}{
			"elegant_balance": gorm.Expr("elegant_balance + ?", amount),
			"total_earnings":  gorm.Expr("total_earnings + ?", amount),
		}).Error
}

// ====== 用户动态工具 ======

// ListPurchasedExecutableTools 查询用户已购买且处于激活状态的可执行秘技（driver != none）
func (r *AgentRepo) ListPurchasedExecutableTools(userID string) ([]*model.AgentItem, error) {
	var items []*model.AgentItem
	err := r.db.Table("agent_items").
		Joins("JOIN agent_purchases ON agent_purchases.agent_id = agent_items.id").
		Joins("JOIN agent_user_tools ON agent_user_tools.agent_id = agent_items.id AND agent_user_tools.user_id = ?", userID).
		Where("agent_purchases.buyer_id = ?", userID).
		Where("agent_user_tools.active = ?", true).
		Where("agent_items.status = ?", model.AgentStatusApproved).
		Where("agent_items.manifest_json IS NOT NULL AND agent_items.manifest_json != ''").
		Order("agent_purchases.created_at DESC").
		Find(&items).Error
	return items, err
}

// CreateUserTool 创建用户已激活的动态工具记录
func (r *AgentRepo) CreateUserTool(tool *model.AgentUserTool) error {
	return r.db.Create(tool).Error
}

// ListUserTools 查询用户已激活的动态工具
func (r *AgentRepo) ListUserTools(userID string) ([]*model.AgentUserTool, error) {
	var tools []*model.AgentUserTool
	err := r.db.Where("user_id = ?", userID).Order("created_at DESC").Find(&tools).Error
	return tools, err
}

// HasUserTool 检查用户是否已激活某秘技对应的动态工具
func (r *AgentRepo) HasUserTool(userID, agentID string) (bool, error) {
	var count int64
	err := r.db.Model(&model.AgentUserTool{}).
		Where("user_id = ? AND agent_id = ?", userID, agentID).
		Count(&count).Error
	return count > 0, err
}

// CountActiveUsers 查询某秘技的激活人数
func (r *AgentRepo) CountActiveUsers(agentID string) (int64, error) {
	var count int64
	err := r.db.Model(&model.AgentUserTool{}).
		Where("agent_id = ? AND active = ?", agentID, true).
		Count(&count).Error
	return count, err
}

// IsToolActive 查询某用户是否已激活某秘技
func (r *AgentRepo) IsToolActive(userID, agentID string) (bool, error) {
	var count int64
	err := r.db.Model(&model.AgentUserTool{}).
		Where("user_id = ? AND agent_id = ? AND active = ?", userID, agentID, true).
		Count(&count).Error
	return count > 0, err
}

// SetToolActive 设置某用户某秘技的激活状态（不存在则创建）
func (r *AgentRepo) SetToolActive(userID, agentID, toolName string, active bool) error {
	var tool model.AgentUserTool
	err := r.db.Where("user_id = ? AND agent_id = ?", userID, agentID).First(&tool).Error
	if err == nil {
		tool.Active = active
		return r.db.Save(&tool).Error
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return r.db.Create(&model.AgentUserTool{
			ID:       uuid.New().String(),
			UserID:   userID,
			AgentID:  agentID,
			ToolName: toolName,
			Active:   active,
		}).Error
	}
	return err
}
