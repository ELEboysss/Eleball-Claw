package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AgentMarketService Agent 市场服务
type AgentMarketService struct {
	agentRepo      *repository.AgentRepo
	userRepo       *repository.UserRepo
	vipService     *VIPService
	moduleRegistry *ModuleRegistry
	moduleService  *ModuleService
	db             *gorm.DB
	agentToolLoader *AgentToolLoader
}

// NewAgentMarketService 创建服务
func NewAgentMarketService(db *gorm.DB, agentRepo *repository.AgentRepo, userRepo *repository.UserRepo, vipService *VIPService, moduleRegistry *ModuleRegistry) *AgentMarketService {
	return &AgentMarketService{
		agentRepo:      agentRepo,
		userRepo:       userRepo,
		vipService:     vipService,
		moduleRegistry: moduleRegistry,
		db:             db,
	}
}

// SetAgentToolLoader 设置动态工具加载器，用于购买后激活动态工具
func (s *AgentMarketService) SetAgentToolLoader(loader *AgentToolLoader) {
	s.agentToolLoader = loader
}

// SetModuleService 设置模块服务，用于审批时自动创建/更新驱动别名。
func (s *AgentMarketService) SetModuleService(svc *ModuleService) {
	s.moduleService = svc
}

// ====== 秘技管理 ======

// CreateAgentRequest 创建秘技请求
type CreateAgentRequest struct {
	Name         string `json:"name" binding:"required"`
	Description  string `json:"description"`
	SystemPrompt string `json:"system_prompt"`
	ToolsJSON    string `json:"tools_json"`
	ManifestJSON string `json:"manifest_json"`
	Category     string `json:"category" binding:"required"`
	PriceDanwan  int64  `json:"price_danwan" binding:"min=0"`
	PriceElegant *int64 `json:"price_elegant"`
}

// CreateAgent 创建秘技
func (s *AgentMarketService) CreateAgent(creatorID, creatorName string, req CreateAgentRequest) (*model.AgentItem, error) {
	// 如果提交了 manifest，则必须有 name/description 和 driver
	if req.ManifestJSON != "" {
		var manifest model.ToolManifest
		if err := json.Unmarshal([]byte(req.ManifestJSON), &manifest); err != nil {
			return nil, fmt.Errorf("manifest 解析失败: %w", err)
		}
		if manifest.Name == "" || manifest.Driver == "" {
			return nil, errors.New("manifest 必须包含 name 和 driver")
		}
		// Prompt 型秘技与工具型秘技二选一：有 manifest 时允许 system_prompt 为空
	} else if req.SystemPrompt == "" {
		return nil, errors.New("system_prompt 和 manifest_json 至少提供一个")
	}

	agent := &model.AgentItem{
		ID:           uuid.New().String(),
		Name:         req.Name,
		Description:  req.Description,
		SystemPrompt: req.SystemPrompt,
		ToolsJSON:    req.ToolsJSON,
		ManifestJSON: req.ManifestJSON,
		Category:     req.Category,
		PriceDanwan:  req.PriceDanwan,
		PriceElegant: req.PriceElegant,
		CreatorID:    creatorID,
		CreatorName:  creatorName,
		Status:       model.AgentStatusPending, // 默认待审核
		Level:        model.AgentLevelHuang,
	}
	if err := s.agentRepo.Create(agent); err != nil {
		return nil, err
	}
	// 更新开发者秘技计数
	s.db.Model(&model.DeveloperAccount{}).Where("user_id = ?", creatorID).
		Update("agent_count", gorm.Expr("agent_count + 1"))
	return agent, nil
}

// GetAgent 获取秘技详情
func (s *AgentMarketService) GetAgent(id string) (*model.AgentItem, error) {
	return s.agentRepo.GetByID(id)
}

// GetCategories 获取已上架秘技的分类列表
func (s *AgentMarketService) GetCategories() ([]string, error) {
	return s.agentRepo.GetCategories()
}

// ListAgents 秘技列表
// 对于依赖模块的 SKU（通过 driver 别名或 metadata.module 解析），仅当模块在线时才展示。
// 返回值中 AvgRating 为动态计算评分（1-5），ActiveCount 为真正激活人数，IsActive 为当前用户是否激活。
// filter 支持：空/all（全部）、owned（仅当前用户已购买）。
func (s *AgentMarketService) ListAgents(userID string, page, pageSize int, category, sortBy, filter string) ([]*model.AgentItem, int64, error) {
	var items []*model.AgentItem
	var total int64
	var err error

	if filter == "owned" && userID != "" {
		// 已购买列表：不过滤模块在线状态，用户已购的秘技始终可见
		items, err = s.agentRepo.ListPurchasedByUser(userID)
		if err != nil {
			return nil, 0, err
		}
		// 分类过滤
		if category != "" {
			filtered := make([]*model.AgentItem, 0, len(items))
			for _, item := range items {
				if item.Category == category {
					filtered = append(filtered, item)
				}
			}
			items = filtered
		}
		total = int64(len(items))
		// 分页
		offset := (page - 1) * pageSize
		if offset < len(items) {
			end := offset + pageSize
			if end > len(items) {
				end = len(items)
			}
			items = items[offset:end]
		} else {
			items = []*model.AgentItem{}
		}
	} else {
		items, total, err = s.agentRepo.List(page, pageSize, category, sortBy)
		if err != nil {
			return nil, 0, err
		}
		// 集市列表不再根据模块在线状态隐藏 SKU，仅由 enrichAgents 填充
		// driver_registered / module_online 等运行时字段，供前端展示提示并控制购买按钮。
	}

	return s.enrichAgents(userID, items, sortBy), total, nil
}

// checkModuleOnline 检查 SKU 背后模块是否在线。
// 无模块依赖的 SKU 返回 nil；模块在线返回 true 指针；离线/未注册返回 false 指针。
func (s *AgentMarketService) checkModuleOnline(item *model.AgentItem) *bool {
	manifest, _ := item.Manifest()
	if manifest == nil {
		return nil
	}
	driver := string(manifest.Driver)
	if driver == "" || driver == string(model.ToolDriverNone) || driver == string(model.ToolDriverBuiltin) {
		return nil
	}
	if s.moduleRegistry == nil {
		return nil
	}
	moduleID := s.resolveModuleID(item)
	if moduleID == "" {
		// 声明了外部驱动但解析不到模块，视为离线
		offline := false
		return &offline
	}
	status := s.moduleRegistry.Check(moduleID)
	online := status != nil && status.Online
	return &online
}

// checkDriverRegistered 检查 SKU 声明的驱动别名是否已注册。
// 无外部驱动依赖的 SKU 返回 true。
func (s *AgentMarketService) checkDriverRegistered(item *model.AgentItem) bool {
	manifest, _ := item.Manifest()
	if manifest == nil {
		return true
	}
	driver := string(manifest.Driver)
	if driver == "" || driver == string(model.ToolDriverNone) || driver == string(model.ToolDriverBuiltin) {
		return true
	}
	if s.agentToolLoader == nil {
		return false
	}
	_, ok := s.agentToolLoader.ResolveDriver(driver)
	return ok
}

// resolveModuleID 解析 SKU 依赖的模块 ID。
// 优先通过 AgentToolLoader 根据 driver 别名解析，未设置 loader 时回退到 metadata.module。
func (s *AgentMarketService) resolveModuleID(item *model.AgentItem) string {
	manifest, _ := item.Manifest()
	if manifest == nil {
		return ""
	}
	if s.agentToolLoader != nil {
		if moduleID := s.agentToolLoader.ResolveModuleID(manifest); moduleID != "" {
			return moduleID
		}
	}
	if manifest.Metadata != nil && manifest.Metadata["module"] != "" {
		return manifest.Metadata["module"]
	}
	return ""
}

// enrichAgents 填充动态评分、激活人数、当前用户激活状态、模块在线状态等运行时字段
// 返回结果按「当前用户已激活 > 未激活」置顶，再按原排序规则二次排序。
func (s *AgentMarketService) enrichAgents(userID string, items []*model.AgentItem, sortBy string) []*model.AgentItem {
	type score struct {
		index       int
		ratio       float64
		activeCount int64
	}
	scores := make([]score, 0, len(items))

	for i, item := range items {
		activeCount, _ := s.agentRepo.CountActiveUsers(item.ID)
		favoriteCount, _ := s.agentRepo.CountFavorites(item.ID)
		isActive, _ := s.agentRepo.IsToolActive(userID, item.ID)

		items[i].ActiveCount = activeCount
		items[i].IsActive = isActive
		items[i].DriverRegistered = s.checkDriverRegistered(item)
		items[i].ModuleOnline = s.checkModuleOnline(item)

		// 动态评分：收藏率横向排名，最低 1 最高 5
		var ratio float64
		if activeCount > 0 {
			ratio = float64(favoriteCount) / float64(activeCount)
		}
		scores = append(scores, score{index: i, ratio: ratio, activeCount: activeCount})
	}

	// 按收藏率降序排名，按 percentile 映射到 1-5
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].ratio > scores[j].ratio
	})
	for rank, sc := range scores {
		var rating float64
		switch {
		case sc.activeCount == 0:
			rating = 3.0 // 无激活数据时默认 3 分
		case rank < len(scores)/5:
			rating = 5.0
		case rank < 2*len(scores)/5:
			rating = 4.0
		case rank < 3*len(scores)/5:
			rating = 3.0
		case rank < 4*len(scores)/5:
			rating = 2.0
		default:
			rating = 1.0
		}
		items[sc.index].AvgRating = rating
	}

	// 已激活秘技始终排在未激活前面；同组内保持原排序规则
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].IsActive != items[j].IsActive {
			return items[i].IsActive && !items[j].IsActive
		}
		// 同组内按原排序规则保持相对顺序（List 已按 sortBy 排序）
		return false
	})

	return items
}

// ====== 购买 ======

// PurchaseAgentRequest 购买请求
type PurchaseAgentRequest struct {
	AgentID  string `json:"agent_id" binding:"required"`
	Currency string `json:"currency" binding:"required,oneof=danwan elegant"`
}

// PurchaseAgent 购买秘技
func (s *AgentMarketService) PurchaseAgent(buyerID string, req PurchaseAgentRequest) error {
	agent, err := s.agentRepo.GetByID(req.AgentID)
	if err != nil {
		return errors.New("秘技不存在")
	}
	if agent.Status != model.AgentStatusApproved {
		return errors.New("秘技未上架")
	}
	// 驱动别名未注册时不允许购买（防止购买到无法执行的 SKU）
	if !s.checkDriverRegistered(agent) {
		return errors.New("该秘技依赖的驱动未注册，暂不可购买")
	}

	// 检查是否已购买
	purchased, _ := s.agentRepo.HasPurchased(req.AgentID, buyerID)
	if purchased {
		return errors.New("已购买该秘技")
	}

	// 确定价格
	var price int64
	if req.Currency == "elegant" && agent.PriceElegant != nil {
		price = *agent.PriceElegant
	} else {
		price = agent.PriceDanwan
		req.Currency = "danwan"
	}

	if price <= 0 {
		// 免费秘技直接记录
		purchase := &model.AgentPurchase{
			ID:              uuid.New().String(),
			AgentID:         req.AgentID,
			BuyerID:         buyerID,
			PricePaid:       0,
			Currency:        req.Currency,
			CreatorEarnings: 0,
			PlatformFee:     0,
		}
		return s.agentRepo.CreatePurchase(purchase)
	}

	// 扣除购买者余额（弹丸从 user.balance，优雅弹丸从 developer_account）
	if req.Currency == "danwan" {
		user, err := s.userRepo.GetByID(buyerID)
		if err != nil {
			return errors.New("用户不存在")
		}
		if user.Balance < price {
			return errors.New("弹丸余额不足")
		}
		if err := s.userRepo.UpdateBalance(buyerID, -price); err != nil {
			return fmt.Errorf("扣费失败: %w", err)
		}
	} else {
		acc, err := s.agentRepo.GetDeveloperAccount(buyerID)
		if err != nil {
			return errors.New("开发者账户不存在")
		}
		if acc.ElegantBalance < price {
			return errors.New("优雅弹丸余额不足")
		}
		if err := s.db.Model(&model.DeveloperAccount{}).Where("user_id = ?", buyerID).
			Update("elegant_balance", gorm.Expr("elegant_balance - ?", price)).Error; err != nil {
			return fmt.Errorf("扣费失败: %w", err)
		}
	}

	// 平台抽成 20%，开发者获得 80%
	platformFee := price * 20 / 100
	creatorEarnings := price - platformFee

	// 记录购买
	purchase := &model.AgentPurchase{
		ID:              uuid.New().String(),
		AgentID:         req.AgentID,
		BuyerID:         buyerID,
		PricePaid:       price,
		Currency:        req.Currency,
		CreatorEarnings: creatorEarnings,
		PlatformFee:     platformFee,
	}
	if err := s.agentRepo.CreatePurchase(purchase); err != nil {
		return err
	}

	// 增加开发者优雅弹丸余额
	if creatorEarnings > 0 {
		if err := s.agentRepo.IncrementElegantBalance(agent.CreatorID, creatorEarnings); err != nil {
			// 日志记录但不阻断，可后续对账补偿
		}
	}

	// 激活用户购买的动态工具（若该 SKU 携带可执行 manifest）
	if s.agentToolLoader != nil {
		if err := s.agentToolLoader.ActivateToolOnPurchase(buyerID, req.AgentID); err != nil && s.agentToolLoader != nil {
			// 激活失败不影响购买主流程，可记录日志后续补偿
		}
	}

	// 更新购买计数
	purchaseCount, _ := s.agentRepo.CountPurchases(req.AgentID)
	s.agentRepo.UpdateStats(req.AgentID, purchaseCount, agent.AvgRating, agent.FavoriteCount, agent.UseCount+1)

	return nil
}

// ToggleAgentActive 切换当前用户对某秘技的激活状态
// 购买后默认激活；用户可随时关闭或重新开启，控制该秘技是否作为工具进入 Agent 工作流。
func (s *AgentMarketService) ToggleAgentActive(userID, agentID string) (bool, error) {
	// 校验已购买
	purchased, err := s.agentRepo.HasPurchased(agentID, userID)
	if err != nil {
		return false, err
	}
	if !purchased {
		return false, errors.New("未购买该秘技")
	}

	agent, err := s.agentRepo.GetByID(agentID)
	if err != nil {
		return false, err
	}
	manifest, _ := agent.Manifest()
	toolName := ""
	if manifest != nil {
		toolName = manifest.ID
	}
	if toolName == "" {
		toolName = fmt.Sprintf("Agent_%s", agentID)
	}

	// 查询当前状态
	active, _ := s.agentRepo.IsToolActive(userID, agentID)
	newActive := !active

	if err := s.agentRepo.SetToolActive(userID, agentID, toolName, newActive); err != nil {
		return false, err
	}
	return newActive, nil
}

// ====== 评价 ======

// ListReviews 评价列表
func (s *AgentMarketService) ListReviews(agentID string, page, pageSize int) ([]*model.AgentReview, int64, error) {
	return s.agentRepo.ListReviews(agentID, page, pageSize)
}

// CreateReviewRequest 评价请求
type CreateReviewRequest struct {
	AgentID string `json:"agent_id" binding:"required"`
	Rating  int    `json:"rating" binding:"min=1,max=5"`
	Comment string `json:"comment"`
}

// CreateReview 创建评价
func (s *AgentMarketService) CreateReview(userID, userName string, req CreateReviewRequest) error {
	// 检查是否已购买
	purchased, _ := s.agentRepo.HasPurchased(req.AgentID, userID)
	if !purchased {
		return errors.New("购买后才能评价")
	}

	review := &model.AgentReview{
		ID:       uuid.New().String(),
		AgentID:  req.AgentID,
		UserID:   userID,
		UserName: userName,
		Rating:   req.Rating,
		Comment:  req.Comment,
	}
	if err := s.agentRepo.CreateReview(review); err != nil {
		return err
	}

	// 重新计算平均评分
	avgRating, _ := s.agentRepo.AvgRating(req.AgentID)
	agent, _ := s.agentRepo.GetByID(req.AgentID)
	s.agentRepo.UpdateStats(req.AgentID, agent.PurchaseCount, avgRating, agent.FavoriteCount, agent.UseCount)

	// 重新计算等级
	score := model.CalculateScore(agent.PurchaseCount, avgRating, agent.FavoriteCount, agent.UseCount)
	newLevel := model.GetLevelByScore(score)
	if newLevel != agent.Level {
		s.agentRepo.UpdateLevel(req.AgentID, newLevel)
	}

	return nil
}

// ====== 收藏 ======

// ToggleFavorite 切换收藏状态
func (s *AgentMarketService) ToggleFavorite(agentID, userID string) (bool, error) {
	favorited, _ := s.agentRepo.IsFavorited(agentID, userID)
	if favorited {
		err := s.agentRepo.DeleteFavorite(agentID, userID)
		if err == nil {
			// 更新收藏计数
			count, _ := s.agentRepo.CountFavorites(agentID)
			agent, _ := s.agentRepo.GetByID(agentID)
			s.agentRepo.UpdateStats(agentID, agent.PurchaseCount, agent.AvgRating, count, agent.UseCount)
		}
		return false, err
	}

	fav := &model.AgentFavorite{
		ID:      uuid.New().String(),
		AgentID: agentID,
		UserID:  userID,
	}
	if err := s.agentRepo.CreateFavorite(fav); err != nil {
		return false, err
	}
	// 更新收藏计数
	count, _ := s.agentRepo.CountFavorites(agentID)
	agent, _ := s.agentRepo.GetByID(agentID)
	s.agentRepo.UpdateStats(agentID, agent.PurchaseCount, agent.AvgRating, count, agent.UseCount)
	return true, nil
}

// ====== 开发者账户 ======

// GetDeveloperAccount 获取开发者账户
func (s *AgentMarketService) GetDeveloperAccount(userID string) (*model.DeveloperAccount, error) {
	acc, err := s.agentRepo.GetDeveloperAccount(userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 自动创建空账户
			acc = &model.DeveloperAccount{
				UserID:         userID,
				ElegantBalance: 0,
				IsVerified:     false,
			}
			if createErr := s.agentRepo.CreateOrUpdateDeveloperAccount(acc); createErr != nil {
				return nil, createErr
			}
			return acc, nil
		}
		return nil, err
	}
	return acc, nil
}

// ====== 等级刷新 ======

// GetUserSpace 获取用户弹丸空间数据
func (s *AgentMarketService) GetUserSpace(userID string) (*model.UserSpace, error) {
	// 查询用户基本信息
	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return nil, err
	}

	// 用户创建的秘技
	createdAgents, _ := s.agentRepo.ListByCreator(userID)

	// 用户已购买的秘技
	purchasedAgents, _ := s.agentRepo.ListPurchasedByUser(userID)

	// 开发者账户
	devAcc, _ := s.agentRepo.GetDeveloperAccount(userID)
	if devAcc == nil {
		devAcc = &model.DeveloperAccount{
			UserID:         userID,
			ElegantBalance: 0,
			IsVerified:     false,
		}
	}

	return &model.UserSpace{
		UserID:           userID,
		UserName:         user.Nickname,
		AvatarURL:        user.AvatarURL,
		Balance:          user.Balance,
		ElegantBalance:   user.ElegantBalance,
		TotalRecharged:   user.TotalRecharged,
		CreatedAgents:    createdAgents,
		PurchasedAgents:  purchasedAgents,
		DeveloperAccount: devAcc,
	}, nil
}

// ====== 账户能力 ======

// GetCapabilities 获取当前账户功能能力
func (s *AgentMarketService) GetCapabilities(userID string, role string) (*model.Capabilities, error) {
	// 查询用户确认存在
	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return nil, errors.New("用户不存在")
	}

	// Agent 市场已对所有登录用户开放
	marketEnabled := true
	marketReason := ""

	// 根据 VIP 状态计算功能权限
	vipStatus, err := s.vipService.GetEffectiveVIP(userID)
	if err != nil {
		return nil, err
	}

	tier := "free"
	if vipStatus.Level > 0 {
		tier = fmt.Sprintf("vip%d", vipStatus.Level)
	}
	if role == model.UserRoleAdmin || user.Role == model.UserRoleAdmin {
		tier = "admin"
	}

	// 集市模块在线状态
	var modules []*model.ModuleCapability
	if s.moduleRegistry != nil {
		for _, st := range s.moduleRegistry.List() {
			modules = append(modules, &model.ModuleCapability{
				ModuleID: st.ModuleID,
				Online:   st.Online,
				Version:  st.Version,
			})
		}
	}

	return &model.Capabilities{
		AgentMarket: &model.MarketCapability{
			Enabled: marketEnabled,
			Reason:  marketReason,
		},
		Subscription: &model.SubscriptionInfo{
			Tier:             tier,
			Level:            vipStatus.Level,
			ExpireAt:         vipStatus.ExpireAt,
			DiscountPercent:  vipStatus.DiscountPercent,
			MaxConversations: vipStatus.MaxConversations,
			MaxAgentSessions: vipStatus.MaxAgentSessions,
			AsrQuotaMonthly:  vipStatus.AsrQuotaMonthly,
		},
		Features: map[string]bool{
			"agent_mode":     vipStatus.Features[model.VIPFeatureAgentMode],
			"file_tools":     vipStatus.Features[model.VIPFeatureFileTools],
			"discount":       vipStatus.Features[model.VIPFeatureDiscount],
			"cloud_sync":     true,
			"developer_mode": role == model.UserRoleAdmin || user.Role == model.UserRoleAdmin,
		},
		Modules: modules,
	}, nil
}

// ====== 管理员审核 ======

// ListAgentsForAdmin 管理员查询秘技列表（按状态筛选）
func (s *AgentMarketService) ListAgentsForAdmin(status string, page, pageSize int) ([]*model.AgentItem, int64, error) {
	if status == "" || status == "all" {
		// 查询所有状态的秘技
		var items []*model.AgentItem
		var total int64
		query := s.db.Model(&model.AgentItem{})
		if err := query.Count(&total).Error; err != nil {
			return nil, 0, err
		}
		offset := (page - 1) * pageSize
		if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&items).Error; err != nil {
			return nil, 0, err
		}
		return items, total, nil
	}
	return s.agentRepo.ListByStatus(model.AgentStatus(status), page, pageSize)
}

// ReviewAgentRequest 审核请求
type ReviewAgentRequest struct {
	Status    string `json:"status" binding:"required,oneof=approved rejected"`
	AdminNote string `json:"admin_note"`
}

// ReviewAgentResult 审核结果
type ReviewAgentResult struct {
	Status    model.AgentStatus `json:"status"`
	DriverID  string            `json:"driver_id,omitempty"`
	AuthToken string            `json:"auth_token,omitempty"`
}

// ReviewAgent 审核秘技（通过/拒绝）
// 通过时：
//   1. 若 SKU 声明了非内置驱动别名，自动在 drivers 表创建/更新驱动记录，并生成 auth_token；
//   2. SKU 状态变为 approved。
// 驱动服务由开发者通过返回的 auth_token 自助注册；未注册或离线时，集市列表会自动隐藏该 SKU。
func (s *AgentMarketService) ReviewAgent(agentID string, req ReviewAgentRequest) (*ReviewAgentResult, error) {
	agent, err := s.agentRepo.GetByID(agentID)
	if err != nil {
		return nil, errors.New("秘技不存在")
	}
	if agent.Status != model.AgentStatusPending {
		return nil, errors.New("只能审核待审核状态的秘技")
	}

	result := &ReviewAgentResult{}
	if req.Status == string(model.AgentStatusApproved) {
		manifest, _ := agent.Manifest()
		if manifest != nil && manifest.Driver != "" &&
			manifest.Driver != model.ToolDriverNone && manifest.Driver != model.ToolDriverBuiltin {
			driverID, token, err := s.ensureDriverForManifest(manifest)
			if err != nil {
				return nil, fmt.Errorf("创建驱动别名失败: %w", err)
			}
			result.DriverID = driverID
			result.AuthToken = token
		}
		agent.Status = model.AgentStatusApproved
	} else {
		agent.Status = model.AgentStatusRejected
	}
	// admin_note 可存入 description 末尾或新增字段，MVP 阶段暂不存储
	_ = req.AdminNote
	if err := s.agentRepo.Update(agent); err != nil {
		return nil, err
	}
	result.Status = agent.Status
	return result, nil
}

// ensureDriverForManifest 确保 SKU 所需的驱动别名已存在并持有 auth_token。
// 返回 driver_id 和 auth_token。
func (s *AgentMarketService) ensureDriverForManifest(manifest *model.ToolManifest) (string, string, error) {
	if s.moduleService == nil {
		return "", "", errors.New("ModuleService 未初始化")
	}
	driverID := string(manifest.Driver)
	rec, err := s.moduleService.ResolveDriver(driverID)
	if err == nil && rec != nil {
		// 已存在：若没有 token，生成一个并更新；否则直接返回现有 token
		if rec.AuthToken != "" {
			return rec.ID, rec.AuthToken, nil
		}
		rec.AuthToken = model.GenerateDriverAuthToken()
		rec.UpdatedAt = time.Now()
		if err := s.moduleService.RegisterDriver(&model.DriverRegisterRequest{
			ID:            rec.ID,
			Name:          rec.Name,
			Description:   rec.Description,
			TransportType: rec.TransportType,
			ModuleID:      rec.ModuleID,
			Endpoint:      rec.Endpoint,
			AuthToken:     rec.AuthToken,
			SchemaJSON:    rec.SchemaJSON,
		}); err != nil {
			return "", "", err
		}
		return rec.ID, rec.AuthToken, nil
	}

	// 不存在：新建驱动别名
	token := model.GenerateDriverAuthToken()
	name := manifest.Name
	if name == "" {
		name = driverID
	}
	if err := s.moduleService.RegisterDriver(&model.DriverRegisterRequest{
		ID:            driverID,
		Name:          name,
		Description:   manifest.Description,
		TransportType: string(model.ModuleTransportTypeModule),
		AuthToken:     token,
	}); err != nil {
		return "", "", err
	}
	return driverID, token, nil
}

// AgentDependencyStatus SKU 依赖的驱动/模块状态，供管理后台审批时展示。
type AgentDependencyStatus struct {
	Driver          string `json:"driver"`
	DriverName      string `json:"driver_name,omitempty"`
	DriverRegistered bool  `json:"driver_registered"`
	ModuleID        string `json:"module_id,omitempty"`
	ModuleRegistered bool  `json:"module_registered,omitempty"`
	ModuleOnline    *bool  `json:"module_online,omitempty"`
}

// GetAgentDependencyStatus 获取 SKU 依赖的驱动与模块状态。
func (s *AgentMarketService) GetAgentDependencyStatus(agentID string) (*AgentDependencyStatus, error) {
	agent, err := s.agentRepo.GetByID(agentID)
	if err != nil {
		return nil, errors.New("秘技不存在")
	}
	manifest, err := agent.Manifest()
	if err != nil {
		return nil, fmt.Errorf("manifest 解析失败: %w", err)
	}
	status := &AgentDependencyStatus{Driver: string(manifest.Driver)}
	if manifest.Driver == "" || manifest.Driver == model.ToolDriverNone || manifest.Driver == model.ToolDriverBuiltin {
		status.DriverRegistered = true
		return status, nil
	}
	if s.agentToolLoader == nil {
		return status, nil
	}
	rec, ok := s.agentToolLoader.ResolveDriver(string(manifest.Driver))
	status.DriverRegistered = ok && rec != nil
	if !status.DriverRegistered {
		return status, nil
	}
	if rec != nil {
		status.DriverName = rec.Name
		if rec.TransportType == string(model.ModuleTransportTypeModule) {
			moduleID := rec.ModuleID
			if moduleID == "" {
				moduleID = s.agentToolLoader.ResolveModuleID(manifest)
			}
			status.ModuleID = moduleID
			if moduleID != "" && s.moduleRegistry != nil {
				st := s.moduleRegistry.Check(moduleID)
				registered := st != nil
				status.ModuleRegistered = registered
				if registered {
					online := st.Online
					status.ModuleOnline = &online
				}
			}
		}
	}
	return status, nil
}

// DelistAgent 下架秘技
func (s *AgentMarketService) DelistAgent(agentID string) error {
	agent, err := s.agentRepo.GetByID(agentID)
	if err != nil {
		return errors.New("秘技不存在")
	}
	if agent.Status != model.AgentStatusApproved {
		return errors.New("只能下架已上架的秘技")
	}
	agent.Status = model.AgentStatusDelisted
	return s.agentRepo.Update(agent)
}

// RefreshAllLevels 刷新所有秘技等级（定时任务调用）
func (s *AgentMarketService) RefreshAllLevels() error {
	var agents []*model.AgentItem
	if err := s.db.Where("status = ?", model.AgentStatusApproved).Find(&agents).Error; err != nil {
		return err
	}

	for _, agent := range agents {
		score := model.CalculateScore(agent.PurchaseCount, agent.AvgRating, agent.FavoriteCount, agent.UseCount)
		newLevel := model.GetLevelByScore(score)
		if newLevel != agent.Level {
			s.agentRepo.UpdateLevel(agent.ID, newLevel)
		}
	}
	return nil
}
