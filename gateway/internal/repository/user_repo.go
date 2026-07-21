package repository

import (
	"errors"
	"time"

	"github.com/eleball/gateway/internal/model"
	"gorm.io/gorm"
)

// ErrAsrQuotaExceeded 语音识别月度额度已用完
var ErrAsrQuotaExceeded = errors.New("asr quota exceeded")

// UserRepo 用户数据访问
type UserRepo struct {
	db *gorm.DB
}

// NewUserRepo 创建仓库
func NewUserRepo(db *gorm.DB) *UserRepo {
	return &UserRepo{db: db}
}

// Create 创建用户
func (r *UserRepo) Create(user *model.User) error {
	return r.db.Create(user).Error
}

// GetByUsername 根据用户名查找
func (r *UserRepo) GetByUsername(username string) (*model.User, error) {
	var user model.User
	if err := r.db.Where("username = ?", username).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// GetByEmail 根据邮箱查找（邮箱 OTP 登录用）。邮箱为空时返回 NotFound。
func (r *UserRepo) GetByEmail(email string) (*model.User, error) {
	var user model.User
	if err := r.db.Where("email = ? AND email != ''", email).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// GetByID 根据 ID 查找
func (r *UserRepo) GetByID(id string) (*model.User, error) {
	var user model.User
	if err := r.db.First(&user, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// UpdateBalance 更新弹丸余额
func (r *UserRepo) UpdateBalance(userID string, delta int64) error {
	return r.db.Model(&model.User{}).
		Where("id = ?", userID).
		UpdateColumn("balance", gorm.Expr("balance + ?", delta)).Error
}

// UpdateElegantBalance 更新优雅弹丸余额
func (r *UserRepo) UpdateElegantBalance(userID string, delta int64) error {
	return r.db.Model(&model.User{}).
		Where("id = ?", userID).
		UpdateColumn("elegant_balance", gorm.Expr("elegant_balance + ?", delta)).Error
}

// UpdateTotalRecharged 累计充值金额增加
func (r *UserRepo) UpdateTotalRecharged(userID string, delta int64) error {
	return r.db.Model(&model.User{}).
		Where("id = ?", userID).
		UpdateColumn("total_recharged", gorm.Expr("total_recharged + ?", delta)).Error
}

// Count 统计用户总数
func (r *UserRepo) Count() (int64, error) {
	var count int64
	err := r.db.Model(&model.User{}).Count(&count).Error
	return count, err
}

// CountActiveToday 统计今日活跃用户
func (r *UserRepo) CountActiveToday(date string) (int64, error) {
	var count int64
	err := r.db.Model(&model.User{}).
		Where("DATE(updated_at) = ?", date).
		Count(&count).Error
	return count, err
}

// TouchActive 更新用户最近活跃时间（updated_at），用于 DAU 统计
func (r *UserRepo) TouchActive(userID string) error {
	return r.db.Model(&model.User{}).
		Where("id = ?", userID).
		UpdateColumn("updated_at", gorm.Expr("CURRENT_TIMESTAMP")).Error
}

// List 分页查询用户列表
func (r *UserRepo) List(page, pageSize int, search string, status *int) ([]*model.User, int64, error) {
	var users []*model.User
	var total int64

	query := r.db.Model(&model.User{})
	if search != "" {
		query = query.Where("username LIKE ? OR nickname LIKE ?", "%"+search+"%", "%"+search+"%")
	}
	if status != nil {
		query = query.Where("status = ?", *status)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&users).Error; err != nil {
		return nil, 0, err
	}

	return users, total, nil
}

// UpdateStatus 更新用户状态
func (r *UserRepo) UpdateStatus(userID string, status int) error {
	return r.db.Model(&model.User{}).Where("id = ?", userID).Update("status", status).Error
}

// AsrQuota 用户语音识别额度快照
type AsrQuota struct {
	Monthly int64     `json:"monthly"`
	Used    int64     `json:"used"`
	ResetAt time.Time `json:"reset_at"`
}

// GetAsrQuota 查询用户 ASR 额度
func (r *UserRepo) GetAsrQuota(userID string) (*AsrQuota, error) {
	var user model.User
	if err := r.db.First(&user, "id = ?", userID).Error; err != nil {
		return nil, err
	}
	return &AsrQuota{
		Monthly: user.AsrQuotaMonthly,
		Used:    user.AsrQuotaUsed,
		ResetAt: user.AsrQuotaResetAt,
	}, nil
}

// CheckAndUseAsrQuota 检查并扣减一次 ASR 额度
// defaultMonthly 用于用户未设置月额度时的兜底值；跨月时自动重置已用次数。
func (r *UserRepo) CheckAndUseAsrQuota(userID string, defaultMonthly int64, now time.Time) (*AsrQuota, error) {
	var quota *AsrQuota
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := tx.First(&user, "id = ?", userID).Error; err != nil {
			return err
		}

		// 若未设置月额度，使用系统默认值
		if user.AsrQuotaMonthly <= 0 {
			user.AsrQuotaMonthly = defaultMonthly
		}

		// 跨月自动刷新
		if user.AsrQuotaResetAt.IsZero() ||
			user.AsrQuotaResetAt.Year() != now.Year() ||
			user.AsrQuotaResetAt.Month() != now.Month() {
			user.AsrQuotaUsed = 0
			user.AsrQuotaResetAt = now
		}

		if user.AsrQuotaUsed >= user.AsrQuotaMonthly {
			return ErrAsrQuotaExceeded
		}

		user.AsrQuotaUsed++
		if err := tx.Save(&user).Error; err != nil {
			return err
		}
		quota = &AsrQuota{
			Monthly: user.AsrQuotaMonthly,
			Used:    user.AsrQuotaUsed,
			ResetAt: user.AsrQuotaResetAt,
		}
		return nil
	})
	return quota, err
}

// UpdateAsrQuota 管理员设置用户 ASR 额度
func (r *UserRepo) UpdateAsrQuota(userID string, monthly int64, used int64, resetAt time.Time) error {
	updates := map[string]interface{}{
		"asr_quota_monthly": monthly,
		"asr_quota_used":    used,
		"asr_quota_reset_at": resetAt,
	}
	return r.db.Model(&model.User{}).Where("id = ?", userID).Updates(updates).Error
}

// UpdateVIP 更新用户 VIP 字段
func (r *UserRepo) UpdateVIP(userID string, level int, expireAt time.Time, planID *string) error {
	updates := map[string]interface{}{
		"vip_level":    level,
		"vip_expire_at": expireAt,
	}
	if planID != nil {
		updates["vip_plan_id"] = *planID
	}
	return r.db.Model(&model.User{}).Where("id = ?", userID).Updates(updates).Error
}

// IncrementAgentTrialUsed 增加 VIP0 用户 Agent 模式试用次数
func (r *UserRepo) IncrementAgentTrialUsed(userID string) error {
	return r.db.Model(&model.User{}).
		Where("id = ?", userID).
		UpdateColumn("agent_trial_used", gorm.Expr("agent_trial_used + ?", 1)).Error
}

// ResetAgentTrialUsed 重置 VIP0 用户 Agent 模式试用次数（每日重置）
func (r *UserRepo) ResetAgentTrialUsed(userID string) error {
	return r.db.Model(&model.User{}).
		Where("id = ?", userID).
		Updates(map[string]interface{}{
			"agent_trial_used":     0,
			"agent_trial_reset_at": time.Now(),
		}).Error
}

// UpdatePassword 更新用户密码
func (r *UserRepo) UpdatePassword(userID string, hashedPassword string) error {
	return r.db.Model(&model.User{}).Where("id = ?", userID).Update("password", hashedPassword).Error
}

// Delete 删除用户
func (r *UserRepo) Delete(userID string) error {
	return r.db.Delete(&model.User{}, "id = ?", userID).Error
}

// DailyActiveStats 日活跃用户趋势
func (r *UserRepo) DailyActiveStats(days int) ([]struct {
	Date  string `json:"date"`
	Value int64  `json:"value"`
}, error) {
	var results []struct {
		Date  string `json:"date"`
		Value int64  `json:"value"`
	}

	err := r.db.Raw(`
		SELECT DATE(updated_at) as date, COUNT(*) as value
		FROM users
		WHERE updated_at >= DATE('now', '-? days')
		GROUP BY DATE(updated_at)
		ORDER BY date
	`, days).Scan(&results).Error

	return results, err
}
