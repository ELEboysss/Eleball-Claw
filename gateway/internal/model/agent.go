package model

import (
	"encoding/json"
	"time"
)

// AgentStatus 秘技状态
type AgentStatus string

const (
	AgentStatusPending  AgentStatus = "pending"
	AgentStatusApproved AgentStatus = "approved"
	AgentStatusRejected AgentStatus = "rejected"
	AgentStatusDelisted AgentStatus = "delisted"
)

// AgentLevel 秘技等级
type AgentLevel int

const (
	AgentLevelHuang  AgentLevel = 1 // 黄阶秘技
	AgentLevelXuan   AgentLevel = 2 // 玄阶秘技
	AgentLevelDi     AgentLevel = 3 // 地阶秘技
	AgentLevelTian   AgentLevel = 4 // 天阶秘技
	AgentLevelXian   AgentLevel = 5 // 仙阶秘技
	AgentLevelFenJue AgentLevel = 6 // 焚决
)

func (l AgentLevel) DisplayName() string {
	names := map[AgentLevel]string{
		AgentLevelHuang:  "黄阶秘技",
		AgentLevelXuan:   "玄阶秘技",
		AgentLevelDi:     "地阶秘技",
		AgentLevelTian:   "天阶秘技",
		AgentLevelXian:   "仙阶秘技",
		AgentLevelFenJue: "焚决",
	}
	if name, ok := names[l]; ok {
		return name
	}
	return "未知"
}

// AgentItem 秘技（Agent/SubAgent）
type AgentItem struct {
	ID            string      `gorm:"primaryKey" json:"id"`
	Name          string      `gorm:"not null" json:"name"`
	Description   string      `json:"description"`
	IconURL       string      `json:"icon_url"`
	CreatorID     string      `gorm:"index" json:"creator_id"`
	CreatorName   string      `json:"creator_name"`
	SystemPrompt  string      `gorm:"type:text" json:"system_prompt"`
	ToolsJSON     string      `gorm:"type:text" json:"tools_json"`
	ManifestJSON  string      `gorm:"type:text" json:"manifest_json"` // ToolManifest JSON，驱动与 SKU 标准描述
	Version       string      `json:"version,omitempty"`              // T2.3：派生源版本（package.json/module.json），T4.4 更新检测比对；空=legacy/未知
	Category      string      `gorm:"index" json:"category"`
	PriceDanwan   int64       `json:"price_danwan"`
	PriceElegant  *int64      `json:"price_elegant"`
	Level         AgentLevel  `json:"level"`
	PinnedFields  string      `gorm:"type:text" json:"pinned_fields,omitempty"` // admin 覆盖保护：JSON 数组 ["name","price_danwan",...]，派生同步(DeriveSKUs/SyncOfficialSKUs)跳过这些字段，保留 admin 改的展示值
	PurchaseCount int64       `json:"purchase_count"`
	AvgRating     float64     `json:"avg_rating"`
	FavoriteCount int64       `json:"favorite_count"`
	UseCount      int64       `json:"use_count"`
	Status        AgentStatus `gorm:"default:pending" json:"status"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
	// 以下字段不在数据库中，由列表接口根据当前用户动态填充
	IsActive           bool   `gorm:"-" json:"is_active"`
	IsFavorited        bool   `gorm:"-" json:"is_favorited"`
	ActiveCount        int64  `gorm:"-" json:"active_count"`
	DriverRegistered   bool   `gorm:"-" json:"driver_registered"`        // 是否已注册对应驱动别名（仅对非内置驱动有效）
	CredentialComplete bool   `gorm:"-" json:"credential_complete"`      // 当前用户是否已配齐该 SKU 声明的必填凭证（缺则不可激活）
	ModuleOnline       *bool  `gorm:"-" json:"module_online,omitempty"`  // nil 表示无模块依赖，false 表示模块离线/未注册，true 表示在线
	HasDeps            bool   `gorm:"-" json:"has_deps,omitempty"`       // stdio+process 模块是否有第三方依赖（requirements.txt/package.json）
	DepsInstalled      bool   `gorm:"-" json:"deps_installed,omitempty"` // 依赖是否已装（venv/node_modules 存在）
	DepsType           string `gorm:"-" json:"deps_type,omitempty"`      // "python" | "node"（has_deps 时）
}

// Manifest 解析 ManifestJSON 为 ToolManifest
func (a *AgentItem) Manifest() (*ToolManifest, error) {
	if a.ManifestJSON == "" {
		return nil, nil
	}
	var manifest ToolManifest
	if err := json.Unmarshal([]byte(a.ManifestJSON), &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

// 可被 admin 覆盖保护（pin）的展示字段名。与 IsPinned/AddPin/RemovePin 及派生同步路径
// (DeriveSKUs / SyncOfficialSKUs) 配合：pin 住的字段在派生同步时不被覆写，保留 admin 改的值；
// 未 pin 的字段仍随 manifest 派生值刷新。空 PinnedFields = 全部跟随派生（向后兼容）。
const (
	pinFieldName         = "name"
	pinFieldDescription  = "description"
	pinFieldPriceDanwan  = "price_danwan"
	pinFieldPriceElegant = "price_elegant"
	pinFieldLevel        = "level"
)

// pinnedFieldNames 返回所有可 pin 字段（固定顺序，供序列化稳定输出/测试可重复）。
func pinnedFieldNames() []string {
	return []string{pinFieldName, pinFieldDescription, pinFieldPriceDanwan, pinFieldPriceElegant, pinFieldLevel}
}

// IsPinned 判断某展示字段是否被 admin 钉住（派生同步跳过该字段）。
func (a *AgentItem) IsPinned(field string) bool {
	if a == nil || a.PinnedFields == "" {
		return false
	}
	var pins []string
	if err := json.Unmarshal([]byte(a.PinnedFields), &pins); err != nil {
		return false
	}
	for _, p := range pins {
		if p == field {
			return true
		}
	}
	return false
}

// AddPin 将字段加入钉住集合（去重；仅收录已知可 pin 字段，未知名静默丢弃）。
func (a *AgentItem) AddPin(fields ...string) {
	if a == nil || len(fields) == 0 {
		return
	}
	set := a.pinSet()
	for _, f := range fields {
		for _, known := range pinnedFieldNames() {
			if f == known {
				set[f] = true
				break
			}
		}
	}
	a.writePins(set)
}

// RemovePin 从钉住集合移除字段。
func (a *AgentItem) RemovePin(fields ...string) {
	if a == nil {
		return
	}
	set := a.pinSet()
	for _, f := range fields {
		delete(set, f)
	}
	a.writePins(set)
}

// pinSet 反序列化 PinnedFields 为集合；空/非法时返回空集合。
func (a *AgentItem) pinSet() map[string]bool {
	set := make(map[string]bool)
	if a == nil || a.PinnedFields == "" {
		return set
	}
	var pins []string
	if err := json.Unmarshal([]byte(a.PinnedFields), &pins); err != nil {
		return set
	}
	for _, p := range pins {
		set[p] = true
	}
	return set
}

// writePins 按固定顺序序列化钉住字段为 JSON 数组写入 PinnedFields（空集合写空串）。
func (a *AgentItem) writePins(set map[string]bool) {
	if a == nil {
		return
	}
	if len(set) == 0 {
		a.PinnedFields = ""
		return
	}
	pins := make([]string, 0, len(set))
	for _, f := range pinnedFieldNames() {
		if set[f] {
			pins = append(pins, f)
		}
	}
	b, err := json.Marshal(pins)
	if err != nil {
		return
	}
	a.PinnedFields = string(b)
}

// SyncDerivedDisplay 用派生源(marshaled manifest JSON + 解析后的 ToolManifest)同步展示字段与 schema。
// admin pin 住的字段(name/description/price_danwan/price_elegant/level)不被覆写，保留 admin 改的展示值；
// manifest_json(派生源) 与 Category(组织分类) 始终同步，不受 pin 影响（非展示覆盖项）。
// 返回是否有字段变化，调用方据此决定是否落库。Status 不在此处理（由调用方按上下文设置）。
func (a *AgentItem) SyncDerivedDisplay(mfStr string, m *ToolManifest) bool {
	if a == nil || m == nil {
		return false
	}
	changed := false
	if a.ManifestJSON != mfStr {
		a.ManifestJSON = mfStr
		changed = true
	}
	if a.Category != m.Category {
		a.Category = m.Category
		changed = true
	}
	// Version 为派生源属性（非展示字段，不受 pin 影响），随 manifest 刷新（T2.3）。
	if a.Version != m.Version {
		a.Version = m.Version
		changed = true
	}
	if !a.IsPinned(pinFieldName) && a.Name != m.Name {
		a.Name = m.Name
		changed = true
	}
	if !a.IsPinned(pinFieldDescription) && a.Description != m.Description {
		a.Description = m.Description
		changed = true
	}
	if !a.IsPinned(pinFieldPriceDanwan) && a.PriceDanwan != m.PriceDanwan {
		a.PriceDanwan = m.PriceDanwan
		changed = true
	}
	if !a.IsPinned(pinFieldPriceElegant) && !ptrInt64Equal(a.PriceElegant, m.PriceElegant) {
		a.PriceElegant = m.PriceElegant
		changed = true
	}
	if !a.IsPinned(pinFieldLevel) && a.Level != AgentLevel(m.Level) {
		a.Level = AgentLevel(m.Level)
		changed = true
	}
	return changed
}

// ptrInt64Equal 比较 *int64：nil==nil 为真；nil vs 非 nil 为假；值相等才真。
// 用于 PriceElegant 等可空字段，避免直接 != 比较地址导致无谓更新。
func ptrInt64Equal(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// TableName 指定表名
type AgentPurchase struct {
	ID              string    `gorm:"primaryKey" json:"id"`
	AgentID         string    `gorm:"index" json:"agent_id"`
	BuyerID         string    `gorm:"index" json:"buyer_id"`
	PricePaid       int64     `json:"price_paid"`
	Currency        string    `json:"currency"` // danwan | elegant
	CreatorEarnings int64     `json:"creator_earnings"`
	PlatformFee     int64     `json:"platform_fee"`
	CreatedAt       time.Time `json:"created_at"`
}

// AgentReview 评价
type AgentReview struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	AgentID   string    `gorm:"index" json:"agent_id"`
	UserID    string    `gorm:"index" json:"user_id"`
	UserName  string    `json:"user_name"`
	Rating    int       `json:"rating"`
	Comment   string    `gorm:"type:text" json:"comment"`
	CreatedAt time.Time `json:"created_at"`
}

// AgentFavorite 收藏
type AgentFavorite struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	AgentID   string    `gorm:"uniqueIndex:idx_agent_user" json:"agent_id"`
	UserID    string    `gorm:"uniqueIndex:idx_agent_user" json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
}

// DeveloperAccount 开发者账户
type DeveloperAccount struct {
	UserID         string    `gorm:"primaryKey" json:"user_id"`
	ElegantBalance int64     `json:"elegant_balance"`
	TotalEarnings  int64     `json:"total_earnings"`
	TotalWithdrawn int64     `json:"total_withdrawn"`
	AgentCount     int64     `json:"agent_count"`
	IsVerified     bool      `json:"is_verified"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// AgentLevelThreshold 等级阈值
type AgentLevelThreshold struct {
	Level    AgentLevel `json:"level"`
	MinScore int64      `json:"min_score"`
	MaxPrice int64      `json:"max_price"`
}

// LevelThresholds 等级阈值配置
var LevelThresholds = []AgentLevelThreshold{
	{AgentLevelHuang, 0, 100},
	{AgentLevelXuan, 100, 500},
	{AgentLevelDi, 500, 2000},
	{AgentLevelTian, 2000, 10000},
	{AgentLevelXian, 10000, 50000},
	{AgentLevelFenJue, 50000, 999999999},
}

// CalculateScore 计算秘技综合得分
func CalculateScore(purchaseCount int64, avgRating float64, favoriteCount, useCount int64) int64 {
	return purchaseCount*40 + int64(avgRating*30) + favoriteCount*20 + useCount*10
}

// GetLevelByScore 根据得分获取等级
func GetLevelByScore(score int64) AgentLevel {
	for i := len(LevelThresholds) - 1; i >= 0; i-- {
		if score >= LevelThresholds[i].MinScore {
			return LevelThresholds[i].Level
		}
	}
	return AgentLevelHuang
}

// UserSpace 弹丸空间主页数据
type UserSpace struct {
	UserID           string            `json:"user_id"`
	UserName         string            `json:"user_name"`
	AvatarURL        string            `json:"avatar_url"`
	Balance          int64             `json:"balance"`         // 剩余弹丸数（分）
	ElegantBalance   int64             `json:"elegant_balance"` // 剩余优雅弹丸数（分）
	TotalRecharged   int64             `json:"total_recharged"` // 累计充值金额（人民币分）
	CreatedAgents    []*AgentItem      `json:"created_agents"`
	PurchasedAgents  []*AgentItem      `json:"purchased_agents"`
	DeveloperAccount *DeveloperAccount `json:"developer_account"`
}

// MarketCapability 弹丸市场能力项
type MarketCapability struct {
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason"`
}

// ModuleCapability 集市模块在线状态
type ModuleCapability struct {
	ModuleID string `json:"module_id"`
	Online   bool   `json:"online"`
	Version  string `json:"version,omitempty"`
}

// Capabilities 当前账户功能能力
type Capabilities struct {
	AgentMarket  *MarketCapability   `json:"agent_market"`
	Subscription *SubscriptionInfo   `json:"subscription"`
	Features     map[string]bool     `json:"features"`
	Modules      []*ModuleCapability `json:"modules,omitempty"`
}

// SubscriptionInfo 订阅信息
type SubscriptionInfo struct {
	Tier             string    `json:"tier"`
	Level            int       `json:"level"`
	ExpireAt         time.Time `json:"expire_at"`
	DiscountPercent  int       `json:"discount_percent"`
	MaxConversations int       `json:"max_conversations"`
	MaxAgentSessions int       `json:"max_agent_sessions"`
	AsrQuotaMonthly  int64     `json:"asr_quota_monthly"`
}
