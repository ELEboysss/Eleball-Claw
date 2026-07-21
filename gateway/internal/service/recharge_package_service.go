package service

import (
	"fmt"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/google/uuid"
)

// RechargePackageService 充值套餐业务服务
type RechargePackageService struct {
	repo *repository.RechargePackageRepo
}

// NewRechargePackageService 创建服务
func NewRechargePackageService(repo *repository.RechargePackageRepo) *RechargePackageService {
	return &RechargePackageService{repo: repo}
}

// RechargePackageDTO 用户端套餐展示结构，嵌套自定义数量所需的基础套餐信息
type RechargePackageDTO struct {
	model.RechargePackage
	BasePackage *model.RechargePackage `json:"base_package,omitempty"`
}

// ListForUser 返回上架套餐列表，供 eleball-web /recharge 使用
func (s *RechargePackageService) ListForUser() ([]*RechargePackageDTO, error) {
	items, err := s.repo.ListEnabled()
	if err != nil {
		return nil, err
	}

	result := make([]*RechargePackageDTO, 0, len(items))
	for _, item := range items {
		dto := &RechargePackageDTO{RechargePackage: *item}
		if item.IsCustomMultiplier && item.BasePackageID != nil && *item.BasePackageID != "" {
			base, err := s.repo.GetByID(*item.BasePackageID)
			if err == nil && base != nil {
				dto.BasePackage = base
			}
		}
		result = append(result, dto)
	}
	return result, nil
}

// ListAll 返回全部套餐，管理后台使用
func (s *RechargePackageService) ListAll() ([]*model.RechargePackage, error) {
	return s.repo.ListAll()
}

// GetByID 根据 ID 查询套餐
func (s *RechargePackageService) GetByID(id string) (*model.RechargePackage, error) {
	return s.repo.GetByID(id)
}

// CreatePackageRequest 创建套餐请求
// 前端传入价格单位为“元”，服务层转换为“分”存储。
type CreatePackageRequest struct {
	Name               string  `json:"name" binding:"required"`
	Danwan             int64   `json:"danwan"`
	PriceYuan          float64 `json:"price_yuan"`
	SortOrder          int     `json:"sort_order"`
	IsEnabled          bool    `json:"is_enabled"`
	IsCustomMultiplier bool    `json:"is_custom_multiplier"`
	BasePackageID      *string `json:"base_package_id,omitempty"`
	Description        string  `json:"description"`
}

// Create 创建套餐
func (s *RechargePackageService) Create(req *CreatePackageRequest) (*model.RechargePackage, error) {
	if req.IsCustomMultiplier {
		if req.BasePackageID == nil || *req.BasePackageID == "" {
			return nil, fmt.Errorf("自定义数量套餐必须关联一个基础套餐")
		}
		if _, err := s.repo.GetByID(*req.BasePackageID); err != nil {
			return nil, fmt.Errorf("基础套餐不存在")
		}
	}

	item := &model.RechargePackage{
		ID:                 uuid.New().String(),
		Name:               req.Name,
		Danwan:             req.Danwan,
		PriceFen:           yuanToFen(req.PriceYuan),
		SortOrder:          req.SortOrder,
		IsEnabled:          req.IsEnabled,
		IsCustomMultiplier: req.IsCustomMultiplier,
		BasePackageID:      req.BasePackageID,
		Description:        req.Description,
	}
	if err := s.repo.Create(item); err != nil {
		return nil, err
	}
	return item, nil
}

// UpdatePackageRequest 更新套餐请求
// 使用指针字段区分“未传”与“置空”。
type UpdatePackageRequest struct {
	Name               *string  `json:"name,omitempty"`
	Danwan             *int64   `json:"danwan,omitempty"`
	PriceYuan          *float64 `json:"price_yuan,omitempty"`
	SortOrder          *int     `json:"sort_order,omitempty"`
	IsEnabled          *bool    `json:"is_enabled,omitempty"`
	IsCustomMultiplier *bool    `json:"is_custom_multiplier,omitempty"`
	BasePackageID      *string  `json:"base_package_id,omitempty"`
	Description        *string  `json:"description,omitempty"`
}

// Update 更新套餐
func (s *RechargePackageService) Update(id string, req *UpdatePackageRequest) (*model.RechargePackage, error) {
	item, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}

	if req.Name != nil {
		item.Name = *req.Name
	}
	if req.Danwan != nil {
		item.Danwan = *req.Danwan
	}
	if req.PriceYuan != nil {
		item.PriceFen = yuanToFen(*req.PriceYuan)
	}
	if req.SortOrder != nil {
		item.SortOrder = *req.SortOrder
	}
	if req.IsEnabled != nil {
		item.IsEnabled = *req.IsEnabled
	}
	if req.IsCustomMultiplier != nil {
		item.IsCustomMultiplier = *req.IsCustomMultiplier
	}
	if req.BasePackageID != nil {
		item.BasePackageID = req.BasePackageID
	}
	if req.Description != nil {
		item.Description = *req.Description
	}

	if item.IsCustomMultiplier {
		if item.BasePackageID == nil || *item.BasePackageID == "" {
			return nil, fmt.Errorf("自定义数量套餐必须关联一个基础套餐")
		}
		if _, err := s.repo.GetByID(*item.BasePackageID); err != nil {
			return nil, fmt.Errorf("基础套餐不存在")
		}
	}

	if err := s.repo.Update(item); err != nil {
		return nil, err
	}
	return item, nil
}

// Delete 删除套餐
func (s *RechargePackageService) Delete(id string) error {
	return s.repo.Delete(id)
}

// ResolvedPackage 已解析的实际套餐金额与弹丸数
type ResolvedPackage struct {
	PackageID string
	Quantity  int
	AmountFen int64
	Danwan    int64
	Currency  string
}

// ResolvePackage 根据套餐 ID 与数量解析实际应支付金额/到账弹丸数
// 普通套餐 quantity 通常为 1；自定义数量套餐 quantity 由前端传入。
func (s *RechargePackageService) ResolvePackage(packageID string, quantity int) (*ResolvedPackage, error) {
	if quantity < 1 {
		quantity = 1
	}
	pkg, err := s.repo.GetByID(packageID)
	if err != nil {
		return nil, fmt.Errorf("套餐不存在")
	}
	if !pkg.IsEnabled {
		return nil, fmt.Errorf("套餐已下架")
	}

	resolved := &ResolvedPackage{
		PackageID: packageID,
		Quantity:  quantity,
		Currency:  "danwan",
	}

	if pkg.IsCustomMultiplier {
		if pkg.BasePackageID == nil || *pkg.BasePackageID == "" {
			return nil, fmt.Errorf("自定义数量套餐未配置基础套餐")
		}
		base, err := s.repo.GetByID(*pkg.BasePackageID)
		if err != nil {
			return nil, fmt.Errorf("基础套餐不存在")
		}
		if !base.IsEnabled {
			return nil, fmt.Errorf("基础套餐已下架")
		}
		resolved.AmountFen = base.PriceFen * int64(quantity)
		resolved.Danwan = base.Danwan * int64(quantity)
	} else {
		resolved.AmountFen = pkg.PriceFen * int64(quantity)
		resolved.Danwan = pkg.Danwan * int64(quantity)
	}

	return resolved, nil
}

// yuanToFen 将元转换为分，四舍五入到整数
func yuanToFen(yuan float64) int64 {
	return int64(yuan*100 + 0.5)
}

// FenToYuan 将分转换为元，保留两位小数
func FenToYuan(fen int64) float64 {
	return float64(fen) / 100
}
