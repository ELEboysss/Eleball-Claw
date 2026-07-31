package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eleball/gateway/internal/model"
)

// ============================================================================
//  简易 JWT 工具（HS256）— 纯标准库实现，仅用于 E2E 测试环境
// ============================================================================

type JWTClaims struct {
	UserID    string `json:"user_id"`
	DeviceID  string `json:"device_id"`
	Role      string `json:"role"`
	TokenType string `json:"token_type"`
	Exp       int64  `json:"exp"`
}

var jwtSecret = []byte("e2e-test-secret-change-in-production")

// eleagentBaseURL 返回 Ele Agent 代理 BaseURL。
// E2E 环境下可通过 ELEAGENT_BASE_URL 环境变量覆盖默认值（例如真机调试时设为宿主机局域网 IP），
// 默认值保持 localhost，便于本地 curl/管理后台自测。
func eleagentBaseURL() string {
	if v := os.Getenv("ELEAGENT_BASE_URL"); v != "" {
		return v
	}
	return "http://localhost:8080/v1"
}

// getLocalIP 获取本机局域网 IPv4 地址，用于启动提示。
// 优先返回 RFC1918 私有地址；跳过 169.254.x.x（APIPA 自动私有地址）。
func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	var fallback string
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		ip := ipnet.IP.To4()
		if ip == nil {
			continue
		}
		s := ip.String()
		if strings.HasPrefix(s, "169.254.") {
			continue
		}
		// 优先 RFC1918 私有地址
		if strings.HasPrefix(s, "192.168.") || strings.HasPrefix(s, "10.") ||
			(strings.HasPrefix(s, "172.") && ip[1] >= 16 && ip[1] <= 31) {
			return s
		}
		if fallback == "" {
			fallback = s
		}
	}
	return fallback
}

func generateJWT(userID, deviceID, role, tokenType string, expire time.Duration) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	claims := JWTClaims{
		UserID:    userID,
		DeviceID:  deviceID,
		Role:      role,
		TokenType: tokenType,
		Exp:       time.Now().Add(expire).Unix(),
	}
	claimsJSON, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signature := signHMAC(header + "." + payload)
	return header + "." + payload + "." + signature
}

func parseJWT(token string) (*JWTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}
	expectedSig := signHMAC(parts[0] + "." + parts[1])
	if !hmac.Equal([]byte(expectedSig), []byte(parts[2])) {
		return nil, fmt.Errorf("invalid signature")
	}
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims JWTClaims
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return nil, err
	}
	if time.Now().Unix() > claims.Exp {
		return nil, fmt.Errorf("token expired")
	}
	return &claims, nil
}

func signHMAC(data string) string {
	h := hmac.New(sha256.New, jwtSecret)
	h.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// ============================================================================
//  UUID 工具 — 纯标准库
// ============================================================================

func newUUID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return fmt.Sprintf("uid_%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC4122
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return strings.Repeat("0", n)
	}
	for i := range b {
		b[i] = letters[b[i]%byte(len(letters))]
	}
	return string(b)
}

// ============================================================================
//  统一响应格式
// ============================================================================

type APIResponse struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func respondJSON(w http.ResponseWriter, code int, message string, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(APIResponse{Code: code, Message: message, Data: data})
}

func respondSuccess(w http.ResponseWriter, data interface{}) {
	respondJSON(w, 0, "success", data)
}

func respondError(w http.ResponseWriter, code int, message string) {
	respondJSON(w, code, message, nil)
}

// ============================================================================
//  CORS 中间件
// ============================================================================

func cors(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}

// ============================================================================
//  JWT 认证中间件
// ============================================================================

func jwtAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			respondError(w, 2001, "登录状态为空，请先登录")
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")
		claims, err := parseJWT(token)
		if err != nil {
			respondError(w, 2001, "登录已过期，请重新登录")
			return
		}
		if claims.TokenType != "access" {
			respondError(w, 2001, "登录信息异常，请重新登录")
			return
		}
		r = r.WithContext(contextWithUserID(r, claims.UserID, claims.Role))
		next(w, r)
	}
}

// optionalJwtAuth 可选鉴权：有合法 Bearer Token 时注入 user_id，否则继续执行
func optionalJwtAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			token := strings.TrimPrefix(auth, "Bearer ")
			claims, err := parseJWT(token)
			if err == nil && claims.TokenType == "access" {
				r = r.WithContext(contextWithUserID(r, claims.UserID, claims.Role))
			}
		}
		next(w, r)
	}
}

// 极简 context 传递
type ctxKey string

const ctxUserIDKey ctxKey = "user_id"
const ctxUserRoleKey ctxKey = "user_role"
const ctxCDKIDKey ctxKey = "cdk_id"

func contextWithUserID(r *http.Request, userID, role string) context.Context {
	ctx := context.WithValue(r.Context(), ctxUserIDKey, userID)
	ctx = context.WithValue(ctx, ctxUserRoleKey, role)
	return ctx
}

func userIDFrom(r *http.Request) string {
	v, _ := r.Context().Value(ctxUserIDKey).(string)
	return v
}

func userRoleFrom(r *http.Request) string {
	v, _ := r.Context().Value(ctxUserRoleKey).(string)
	return v
}

func adminAuth(next http.HandlerFunc) http.HandlerFunc {
	return jwtAuth(func(w http.ResponseWriter, r *http.Request) {
		if userRoleFrom(r) != "admin" {
			respondError(w, 2003, "无权限")
			return
		}
		next(w, r)
	})
}

// ============================================================================
//  内存数据模型与存储
// ============================================================================

type User struct {
	ID              string `json:"id"`
	Username        string `json:"username"`
	Password        string `json:"password_hash"`
	Email           string `json:"email"` // 邮箱（邮箱 OTP 登录用，可空）
	Nickname        string `json:"nickname"`
	AvatarURL       string `json:"avatar_url"` // 头像地址
	Role            string `json:"role"`
	Status          int    `json:"status"`
	TotalRecharged  int64  `json:"total_recharged"`    // 累计充值金额（人民币分）
	AsrQuotaMonthly int64  `json:"asr_quota_monthly"`  // 语音识别月度额度
	AsrQuotaUsed    int64  `json:"asr_quota_used"`     // 本月已用次数
	AsrQuotaResetAt int64  `json:"asr_quota_reset_at"` // 最近一次刷新时间（Unix 秒）
	IsVIP           bool   `json:"is_vip"`
	VIPLevel        int    `json:"vip_level"`
	VIPExpireAt     int64  `json:"vip_expire_at"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
}

type AgentItem struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Description   string  `json:"description"`
	IconURL       string  `json:"icon_url"`
	CreatorID     string  `json:"creator_id"`
	CreatorName   string  `json:"creator_name"`
	SystemPrompt  string  `json:"system_prompt"`
	ToolsJSON     string  `json:"tools_json"`
	ManifestJSON  string  `json:"manifest_json"`
	Category      string  `json:"category"`
	PriceDanwan   int64   `json:"price_danwan"`
	PriceElegant  *int64  `json:"price_elegant,omitempty"`
	Level         int     `json:"level"`
	PurchaseCount int64   `json:"purchase_count"`
	AvgRating     float64 `json:"avg_rating"`
	FavoriteCount int64   `json:"favorite_count"`
	UseCount      int64   `json:"use_count"`
	Status        string  `json:"status"`
	CreatedAt     int64   `json:"created_at"`
	// 运行时字段：当前登录用户是否已激活该秘技
	IsActive           bool  `json:"is_active"`
	DriverRegistered   bool  `json:"driver_registered"`
	ModuleOnline       *bool `json:"module_online,omitempty"`
	CredentialComplete bool  `json:"credential_complete"`
}

// Manifest 解析 ManifestJSON 为 ToolManifest
func (a *AgentItem) Manifest() (*model.ToolManifest, error) {
	if a.ManifestJSON == "" {
		return nil, nil
	}
	var m model.ToolManifest
	if err := json.Unmarshal([]byte(a.ManifestJSON), &m); err != nil {
		return nil, err
	}
	return &m, nil
}

type AgentUserTool struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	AgentID   string `json:"agent_id"`
	ToolName  string `json:"tool_name"`
	CreatedAt int64  `json:"created_at"`
}

type AgentReview struct {
	ID        string `json:"id"`
	AgentID   string `json:"agent_id"`
	UserID    string `json:"user_id"`
	UserName  string `json:"user_name"`
	Rating    int    `json:"rating"`
	Comment   string `json:"comment"`
	CreatedAt int64  `json:"created_at"`
}

type DeveloperAccount struct {
	UserID         string `json:"user_id"`
	ElegantBalance int64  `json:"elegant_balance"`
	TotalEarnings  int64  `json:"total_earnings"`
	TotalWithdrawn int64  `json:"total_withdrawn"`
	AgentCount     int64  `json:"agent_count"`
	IsVerified     bool   `json:"is_verified"`
}

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

type WithdrawalRecord struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	UserName    string `json:"user_name"`
	Amount      int64  `json:"amount"`
	Channel     string `json:"channel"`
	AccountInfo string `json:"account_info"`
	RealName    string `json:"real_name"`
	Status      string `json:"status"`
	AdminNote   string `json:"admin_note"`
	CreatedAt   int64  `json:"created_at"`
}

type SyncRecord struct {
	ID                string `json:"id"`
	EntityType        string `json:"entity_type"`
	EntityID          string `json:"entity_id"`
	Operation         string `json:"operation"`
	SyncVersion       int64  `json:"sync_version"`
	PayloadCiphertext string `json:"payload_ciphertext"`
	CreatedAt         int64  `json:"created_at"`
}

type ActivityEvent struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Metadata    string `json:"metadata"`
	CreatedAt   int64  `json:"created_at"`
}

var (
	mu                sync.RWMutex
	users             = make(map[string]*User)  // id -> User
	usernameIndex     = make(map[string]string) // username -> userID
	balances          = make(map[string]int64)  // userID -> 弹丸余额（分）
	elegantBalances   = make(map[string]int64)  // userID -> 优雅弹丸余额（分）
	agents            = make([]*AgentItem, 0)
	agentMap          = make(map[string]*AgentItem)      // id -> AgentItem
	purchases         = make(map[string]map[string]bool) // userID -> agentID -> true
	agentUserTools    = make([]*AgentUserTool, 0)
	favorites         = make(map[string]map[string]bool)   // userID -> agentID -> true
	e2eCredentials = make(map[string]map[string]string) // userID:agentID -> key -> value
	reviews           = make([]*AgentReview, 0)
	devAccounts       = make(map[string]*DeveloperAccount)
	withdrawals       = make([]*WithdrawalRecord, 0)
	syncStore         = make(map[string][]*SyncRecord) // userID -> records
	nextSyncVer       = make(map[string]int64)         // userID -> max version
	activities        = make([]*ActivityEvent, 0)      // 最近动态事件

	// 视觉生成任务内存存储
	visualTasks       = make(map[string]*E2EVisualTask) // taskID -> task
	visualTaskCounter = 0

	// 视觉参考图/首帧图上传内存存储
	visualUploads             = make(map[string][]byte) // fileID -> bytes
	visualUploadContentTypes  = make(map[string]string) // fileID -> content-type

	// 视觉创作会话内存存储
	visualConversations      = make(map[string]*E2EVisualConversation) // convID -> conversation
	visualConversationsByUser = make(map[string][]string)              // userID -> []convID
)

// E2EVisualTask E2E 视觉生成任务内存模型
type E2EVisualTask struct {
	ID             string                 `json:"id"`
	UserID         string                 `json:"user_id"`
	ConversationID string                 `json:"conversation_id"`
	MediaType      string                 `json:"media_type"`
	Provider       string                 `json:"provider"`
	Model          string                 `json:"model"`
	Prompt         string                 `json:"prompt"`
	ImageURL       string                 `json:"image_url"`
	Params         map[string]interface{} `json:"params"`
	Status         string                 `json:"status"`
	ResultURL      string                 `json:"result_url"`
	ErrorMessage   string                 `json:"error_message"`
	Progress       int                    `json:"progress"`
	Cost           int64                  `json:"cost"`
	Currency       string                 `json:"currency"`
	CreatedAt      int64                  `json:"created_at"`
	UpdatedAt      int64                  `json:"updated_at"`
	CompletedAt    *int64                 `json:"completed_at"`
}

// E2EVisualConversation E2E 视觉创作会话内存模型
type E2EVisualConversation struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	Title     string `json:"title"`
	MediaType string `json:"media_type"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// AdminOrder 管理后台订单模型
type AdminOrder struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	Channel     string `json:"channel"`
	Amount      int64  `json:"amount"`
	Currency    string `json:"currency"`
	Status      string `json:"status"`
	ProductType string `json:"product_type"` // recharge | vip
	ProductID   string `json:"product_id,omitempty"`
	TradeNo     string `json:"trade_no"`
	CreatedAt   int64  `json:"created_at"`
	PaidAt      *int64 `json:"paid_at"`
}

// AdminTransaction 管理后台交易记录
type AdminTransaction struct {
	ID           string `json:"id"`
	UserID       string `json:"user_id"`
	Type         string `json:"type"`
	Amount       int64  `json:"amount"`
	Currency     string `json:"currency"`
	BalanceAfter int64  `json:"balance_after"`
	Description  string `json:"description"`
	CreatedAt    int64  `json:"created_at"`
}

// CDK 兑换码（E2E 内存模型）
type CDK struct {
	ID        string  `json:"id"`
	Code      string  `json:"code"`
	Value     int64   `json:"value"`
	Used      bool    `json:"used"`
	UsedBy    *string `json:"used_by,omitempty"`
	UsedAt    *int64  `json:"used_at,omitempty"`
	BatchID   string  `json:"batch_id"`
	Note      string  `json:"note"`
	CreatedAt int64   `json:"created_at"`
	UpdatedAt int64   `json:"updated_at"`
}

// VIPPlan E2E 内存模型
type VIPPlan struct {
	ID               string `json:"id"`
	Level            int    `json:"level"`
	Name             string `json:"name"`
	PriceFen         int64  `json:"price_fen"`
	DurationDays     int    `json:"duration_days"`
	DiscountPercent  int    `json:"discount_percent"`
	MaxConversations int    `json:"max_conversations"`
	MaxAgentSessions int    `json:"max_agent_sessions"`
	AsrQuotaMonthly  int64  `json:"asr_quota_monthly"`
	AgentEnabled     bool   `json:"agent_enabled"`
	FileToolsEnabled bool   `json:"file_tools_enabled"`
	SortOrder        int    `json:"sort_order"`
	IsEnabled        bool   `json:"is_enabled"`
	Description      string `json:"description"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
}

// VIPSubscription E2E 内存模型
type VIPSubscription struct {
	ID           string `json:"id"`
	UserID       string `json:"user_id"`
	PlanID       string `json:"plan_id"`
	Level        int    `json:"level"`
	PriceFen     int64  `json:"price_fen"`
	DurationDays int    `json:"duration_days"`
	StartedAt    int64  `json:"started_at"`
	ExpiresAt    int64  `json:"expires_at"`
	Status       string `json:"status"`
}

var (
	orders        = make([]*AdminOrder, 0)
	transactions  = make([]*AdminTransaction, 0)
	cdks          = make([]*CDK, 0)
	vipPlans      = make([]*VIPPlan, 0)
	vipSubs       = make([]*VIPSubscription, 0)
	settingsStore = map[string]string{
		"site_name":              "Eleball",
		"register_open":          "true",
		"default_model":          "qwen/Qwen/Qwen3-8B",
		"max_tokens_per_request": "4096",
		"free_quota":             "1000",
		"xianyu_product_url":     "",
		"taobao_product_url":     "",
		"prompt_fusion_model":    "",
		"maintenance_mode":       "false",
	}
)

// E2EModule E2E 环境下的模块记录
type E2EModule struct {
	ID           string   `json:"module_id"`
	Name         string   `json:"name"`
	URL          string   `json:"url"`
	TransportType string   `json:"transport_type"`
	Online       bool     `json:"online"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
	AuthToken    string   `json:"auth_token,omitempty"`
}

// E2EDriver E2E 环境下的动态驱动映射
type E2EDriver struct {
	ID          string `json:"driver_id"`
	Name        string `json:"name"`
	TransportType string `json:"transport_type"`
	ModuleID    string `json:"module_id,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
	AuthToken   string `json:"auth_token,omitempty"`
}

var (
	e2eModules = make(map[string]*E2EModule)
	e2eDrivers = make(map[string]*E2EDriver)
	modulesMu  sync.RWMutex
)

func init() {
	// 预置官方演示秘技
	now := time.Now().Unix()
	mockAgents := []*AgentItem{
		{ID: "agent-001", Name: "小红书文案专家", Description: "自动生成小红书风格种草文案", Category: "内容创作", PriceDanwan: 100, AvgRating: 4.8, PurchaseCount: 1250, FavoriteCount: 320, Status: "approved", Level: 2, CreatedAt: now},
		{ID: "agent-002", Name: "代码解释器", Description: "解释任意代码片段的原理和逻辑", Category: "编程", PriceDanwan: 50, AvgRating: 4.6, PurchaseCount: 890, FavoriteCount: 210, Status: "approved", Level: 2, CreatedAt: now},
		{ID: "agent-003", Name: "法律合同审查", Description: "快速识别合同中的风险条款", Category: "法律", PriceDanwan: 300, AvgRating: 4.9, PurchaseCount: 340, FavoriteCount: 150, Status: "approved", Level: 3, CreatedAt: now},
		{ID: "agent-004", Name: "会议纪要整理", Description: "将会议录音或速记整理为正式会议纪要", Category: "办公", PriceDanwan: 0, AvgRating: 4.5, PurchaseCount: 876, FavoriteCount: 180, Status: "approved", Level: 1, CreatedAt: now},
		{ID: "agent-005", Name: "SQL 优化大师", Description: "分析 SQL 查询语句，指出性能瓶颈并给出优化方案", Category: "编程", PriceDanwan: 150, AvgRating: 4.9, PurchaseCount: 2103, FavoriteCount: 560, Status: "approved", Level: 4, CreatedAt: now},
		{ID: "agent-006", Name: "睡前故事创作", Description: "根据指定主题或角色，创作温暖治愈的睡前故事", Category: "创意", PriceDanwan: 0, AvgRating: 4.6, PurchaseCount: 654, FavoriteCount: 120, Status: "approved", Level: 1, CreatedAt: now},
		{ID: "agent-reach-web", Name: "全网洞察（基础版）", Description: "基于 Agent-Reach 的网页阅读与全网语义搜索，零配置即用", Category: "互联网", PriceDanwan: 0, AvgRating: 4.7, PurchaseCount: 320, FavoriteCount: 80, Status: "approved", Level: 2, CreatedAt: now, ManifestJSON: `{"id":"com.eleball.tools.agent_reach.web","name":"全网洞察","description":"基于 Agent-Reach 的网页阅读与全网语义搜索，零配置即用。","driver":"agent_reach","runtime_type":"remote","category":"互联网","level":2,"permissions":["network"],"parameters":{"type":"object","properties":{"action":{"type":"string","enum":["web_read","search"],"description":"模块 action"},"params":{"type":"object","description":"Agent-Reach 参数","properties":{"query":{"type":"string","description":"URL 或搜索关键词"},"limit":{"type":"integer","description":"返回条数","default":5}}}},"required":["action"]},"actions":[{"name":"web_read","description":"读取任意网页"},{"name":"search","description":"全网语义搜索"}],"credentials":{"exa_api_key":{"type":"api_key","label":"Exa API Key","description":"全网语义搜索依赖 Exa.ai","placeholder":"exa-xxxxxxxx","required":true,"scope":"module"}},"timeout_seconds":60}`},
		{ID: "agent-reach-video", Name: "视频解析器", Description: "基于 Agent-Reach 的 YouTube/B站 字幕提取与搜索", Category: "互联网", PriceDanwan: 200, AvgRating: 4.8, PurchaseCount: 156, FavoriteCount: 45, Status: "approved", Level: 3, CreatedAt: now, ManifestJSON: `{"id":"com.eleball.tools.agent_reach.video","name":"视频解析器","description":"基于 Agent-Reach 的 YouTube/B站 字幕提取与搜索。","driver":"agent_reach","runtime_type":"remote","category":"互联网","level":3,"permissions":["network"],"parameters":{"type":"object","properties":{"action":{"type":"string","enum":["youtube_subtitles","bilibili_search"],"description":"模块 action"},"params":{"type":"object","description":"Agent-Reach 参数","properties":{"query":{"type":"string","description":"视频 URL 或搜索关键词"},"limit":{"type":"integer","description":"返回条数","default":5}}}},"required":["action"]},"actions":[{"name":"youtube_subtitles","description":"提取 YouTube 字幕"},{"name":"bilibili_search","description":"B站搜索"}],"credentials":{"youtube_cookie":{"type":"cookie","label":"YouTube Cookie","description":"用于提取 YouTube 字幕","placeholder":"粘贴 YouTube Cookie","required":false},"bilibili_cookie":{"type":"cookie","label":"B站 Cookie","description":"用于 B站搜索","placeholder":"粘贴 B站 Cookie","required":false,"scope":"module"}},"timeout_seconds":120}`},
		{ID: "agent-reach-github", Name: "开发者雷达", Description: "基于 Agent-Reach 的 GitHub 仓库查看与代码搜索", Category: "开发", PriceDanwan: 80, AvgRating: 4.5, PurchaseCount: 210, FavoriteCount: 60, Status: "approved", Level: 2, CreatedAt: now, ManifestJSON: `{"id":"com.eleball.tools.agent_reach.github","name":"开发者雷达","description":"基于 Agent-Reach 的 GitHub 仓库查看与代码搜索。","driver":"agent_reach","runtime_type":"remote","category":"开发","level":2,"permissions":["network"],"parameters":{"type":"object","properties":{"action":{"type":"string","enum":["github_repo","github_search"],"description":"模块 action"},"params":{"type":"object","description":"Agent-Reach 参数","properties":{"query":{"type":"string","description":"仓库名或搜索关键词"},"limit":{"type":"integer","description":"返回条数","default":5}}}},"required":["action"]},"actions":[{"name":"github_repo","description":"查看 GitHub 仓库"},{"name":"github_search","description":"搜索 GitHub 仓库"}],"credentials":{"github_token":{"type":"api_key","label":"GitHub Token","description":"可选，用于提高 GitHub API 速率限额或访问私有仓库","placeholder":"ghp_...","required":false}},"timeout_seconds":60}`},
		{ID: "agent-reach-social", Name: "社媒雷达", Description: "基于 Agent-Reach 的社媒内容搜索与阅读，支持 Twitter、小红书、Reddit、B站", Category: "互联网", PriceDanwan: 150, AvgRating: 4.6, PurchaseCount: 98, FavoriteCount: 30, Status: "approved", Level: 3, CreatedAt: now, ManifestJSON: `{"id":"com.eleball.tools.agent_reach.social","name":"社媒雷达","description":"基于 Agent-Reach 的社媒内容搜索与阅读，支持 Twitter、小红书、Reddit、B站。","driver":"agent_reach","runtime_type":"remote","category":"互联网","level":3,"permissions":["network"],"parameters":{"type":"object","properties":{"action":{"type":"string","enum":["social_search","social_read"],"description":"模块 action"},"params":{"type":"object","description":"Agent-Reach 参数","properties":{"social_platform":{"type":"string","enum":["twitter","xiaohongshu","reddit","bilibili"],"description":"社交平台"},"query":{"type":"string","description":"搜索关键词或帖子链接"},"limit":{"type":"integer","description":"返回条数","default":5}}}},"required":["action"]},"actions":[{"name":"social_search","description":"搜索社媒内容"},{"name":"social_read","description":"读取社媒帖子详情"}],"credentials":{"twitter_cookie":{"type":"cookie","label":"Twitter Cookie","description":"用于 Twitter 搜索/阅读的浏览器登录态 Cookie","placeholder":"粘贴 Twitter Cookie","required":false},"xiaohongshu_cookie":{"type":"cookie","label":"小红书 Cookie","description":"用于小红书搜索/阅读的浏览器登录态 Cookie","placeholder":"粘贴小红书 Cookie","required":false},"bilibili_cookie":{"type":"cookie","label":"B站 Cookie","description":"用于 B站搜索/阅读的 SESSDATA 或完整 Cookie Header String。与「视频解析器」共享此凭证（模块级）。","placeholder":"粘贴 B站 Cookie","required":false,"scope":"module"}},"timeout_seconds":120}`},
		{ID: "firecrawl-scrape", Name: "Firecrawl 网页抓取", Description: "将任意网页转换为干净 Markdown，返回标题、URL、描述等元数据", Category: "互联网", PriceDanwan: 0, AvgRating: 4.7, PurchaseCount: 0, FavoriteCount: 0, Status: "approved", Level: 2, CreatedAt: now, ManifestJSON: `{"id":"com.eleball.tools.firecrawl.scrape","name":"Firecrawl 网页抓取","description":"将任意网页转换为干净 Markdown，返回标题、URL、描述等元数据。","driver":"firecrawl","runtime_type":"remote","category":"互联网","level":2,"permissions":["network"],"parameters":{"type":"object","properties":{"action":{"type":"string","enum":["scrape"],"description":"抓取动作"},"params":{"type":"object","description":"Firecrawl scrape 参数，如 {url: \"https://example.com\"}"}},"required":["action"]},"actions":[{"name":"scrape","description":"抓取单个网页并返回 Markdown / 元数据"}],"credentials":{"firecrawl_api_key":{"type":"api_key","label":"Firecrawl API Key","description":"用于调用 Firecrawl Cloud API","placeholder":"fc-...","required":true,"scope":"module"}},"timeout_seconds":120}`},
		{ID: "firecrawl-crawl", Name: "Firecrawl 网站爬虫", Description: "对指定网站启动批量爬取任务，返回任务 ID", Category: "互联网", PriceDanwan: 100, AvgRating: 4.6, PurchaseCount: 0, FavoriteCount: 0, Status: "approved", Level: 3, CreatedAt: now, ManifestJSON: `{"id":"com.eleball.tools.firecrawl.crawl","name":"Firecrawl 批量爬取","description":"对指定网站启动批量爬取任务，返回任务 ID。","driver":"firecrawl","runtime_type":"remote","category":"互联网","level":3,"permissions":["network"],"parameters":{"type":"object","properties":{"action":{"type":"string","enum":["crawl"],"description":"爬取动作"},"params":{"type":"object","description":"Firecrawl crawl 参数，如 {url: \"https://example.com\", limit: 10}"}},"required":["action"]},"actions":[{"name":"crawl","description":"批量爬取网站，返回任务 ID"}],"credentials":{"firecrawl_api_key":{"type":"api_key","label":"Firecrawl API Key","description":"用于调用 Firecrawl Cloud API","placeholder":"fc-...","required":true,"scope":"module"}},"timeout_seconds":120}`},
		{ID: "firecrawl-extract", Name: "Firecrawl 结构化提取", Description: "按 JSON Schema 从网页中提取结构化数据", Category: "互联网", PriceDanwan: 100, AvgRating: 4.8, PurchaseCount: 0, FavoriteCount: 0, Status: "approved", Level: 3, CreatedAt: now, ManifestJSON: `{"id":"com.eleball.tools.firecrawl.extract","name":"Firecrawl 结构化提取","description":"按 JSON Schema 从网页中提取结构化数据。","driver":"firecrawl","runtime_type":"remote","category":"互联网","level":3,"permissions":["network"],"parameters":{"type":"object","properties":{"action":{"type":"string","enum":["extract"],"description":"提取动作"},"params":{"type":"object","description":"Firecrawl extract 参数，如 {urls: [...], schema: {...}}"}},"required":["action"]},"actions":[{"name":"extract","description":"按 JSON Schema 提取结构化数据"}],"credentials":{"firecrawl_api_key":{"type":"api_key","label":"Firecrawl API Key","description":"用于调用 Firecrawl Cloud API","placeholder":"fc-...","required":true,"scope":"module"}},"timeout_seconds":120}`},
	}
	for _, a := range mockAgents {
		agents = append(agents, a)
		agentMap[a.ID] = a
	}
	loadData() // 启动时加载持久化数据

	// 预置 Ele Agent 默认模型配置（仅当持久化数据中不存在时）
	ensureDefaultEleAgentConfig()

	// 预置默认充值套餐
	ensureDefaultRechargePackages()

	// 预置默认 VIP 套餐
	ensureDefaultVIPPlans()

	// 预置内置集市模块，支持动态注册与模块离线过滤测试
	e2eModules["agent-reach"] = &E2EModule{ID: "agent-reach", Name: "Agent-Reach", URL: "http://agent-reach:8080", TransportType: "module", Online: true, Version: "1.0.0", Capabilities: []string{"web_read", "search", "subtitles"}}
	e2eModules["firecrawl"] = &E2EModule{ID: "firecrawl", Name: "Firecrawl", URL: "http://firecrawl:8080", TransportType: "module", Online: false, Version: "1.0.0", Capabilities: []string{"scrape", "crawl", "extract"}}

	// 预置官方驱动别名，SKU 的 ToolManifest 通过 driver 字段引用这些别名
	e2eDrivers["agent_reach"] = &E2EDriver{ID: "agent_reach", Name: "Agent-Reach 互联网能力", TransportType: "module", ModuleID: "agent-reach"}
	e2eDrivers["firecrawl"] = &E2EDriver{ID: "firecrawl", Name: "Firecrawl 网页抓取", TransportType: "module", ModuleID: "firecrawl"}

	// 预置管理员账号，便于调试弹丸市场能力门控
	if uid, exists := usernameIndex["admin"]; exists {
		// 从持久化文件加载后可能丢失密码哈希，重新设置为默认密码
		if users[uid] != nil && users[uid].Password == "" {
			users[uid].Password = hashPassword("admin123")
			saveData()
		}
	} else {
		uid := newUUID()
		now := time.Now().Unix()
		users[uid] = &User{
			ID:              uid,
			Username:        "admin",
			Password:        hashPassword("admin123"),
			Nickname:        "管理员",
			Role:            "admin",
			Status:          1,
			AsrQuotaMonthly: 1000,
			AsrQuotaResetAt: now,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		usernameIndex["admin"] = uid
		balances[uid] = 10000 // 管理员默认更多余额
		saveData()
	}
}

// ============================================================================
//  Handlers
// ============================================================================

func healthHandler(w http.ResponseWriter, r *http.Request) {
	respondSuccess(w, nil)
}

// --- Auth ---

func registerHandler(w http.ResponseWriter, r *http.Request) {
	defer saveData()
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		DeviceID string `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误: "+err.Error())
		return
	}
	if req.Username == "" || req.Password == "" {
		respondError(w, 1001, "用户名和密码不能为空")
		return
	}

	mu.Lock()
	defer mu.Unlock()

	if _, exists := usernameIndex[req.Username]; exists {
		respondError(w, 1000, "用户名已被注册")
		return
	}

	uid := newUUID()
	now := time.Now().Unix()
	user := &User{
		ID:              uid,
		Username:        req.Username,
		Password:        hashPassword(req.Password),
		Nickname:        req.Username,
		Role:            "user",
		Status:          1,
		AsrQuotaMonthly: 1000,
		AsrQuotaResetAt: now,
		IsVIP:           true,
		VIPLevel:        0,
		VIPExpireAt:     time.Now().AddDate(100, 0, 0).Unix(),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	users[uid] = user
	usernameIndex[req.Username] = uid
	balances[uid] = 1000     // 赠送 1000 弹丸
	elegantBalances[uid] = 0 // 优雅弹丸初始为 0

	// 记录用户注册动态
	activities = append(activities, &ActivityEvent{
		ID:          newUUID(),
		UserID:      uid,
		Type:        "user_registered",
		Title:       "新用户注册",
		Description: fmt.Sprintf("用户 %s（user_id:%s）注册了账户", user.Username, uid),
		Metadata:    fmt.Sprintf(`{"username":"%s"}`, user.Username),
		CreatedAt:   now,
	})

	accessToken := generateJWT(uid, req.DeviceID, user.Role, "access", 2*time.Hour)
	refreshToken := generateJWT(uid, req.DeviceID, user.Role, "refresh", 720*time.Hour)

	respondSuccess(w, map[string]interface{}{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"user_id":       uid,
		"user": map[string]interface{}{
			"id":       user.ID,
			"username": user.Username,
			"nickname": user.Nickname,
			"role":     user.Role,
			"status":   user.Status,
		},
		"default_model_profile": map[string]interface{}{
			"id":            "ele_agent_default_" + uid,
			"name":          "Ele Agent",
			"provider":      "eleagent",
			"model_name":    "qwen/Qwen/Qwen3-8B",
			"base_url":      "http://localhost:8080/v1",
			"api_key":       "eleagent_" + newUUID(),
			"system_prompt": "你是 Eleball 官方智能助手 Ele Agent。",
		},
	})
}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		DeviceID string `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误: "+err.Error())
		return
	}
	if req.Username == "" || req.Password == "" {
		respondError(w, 1001, "用户名或密码不能为空")
		return
	}

	mu.RLock()
	uid, ok := usernameIndex[req.Username]
	user := users[uid]
	mu.RUnlock()

	if !ok || user == nil || user.Password != hashPassword(req.Password) {
		respondError(w, 2001, "用户名或密码错误")
		return
	}

	accessToken := generateJWT(uid, req.DeviceID, user.Role, "access", 2*time.Hour)
	refreshToken := generateJWT(uid, req.DeviceID, user.Role, "refresh", 720*time.Hour)

	respondSuccess(w, map[string]interface{}{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"user_id":       uid,
		"user": map[string]interface{}{
			"id":       user.ID,
			"username": user.Username,
			"nickname": user.Nickname,
			"role":     user.Role,
			"status":   user.Status,
		},
		"default_model_profile": map[string]interface{}{
			"id":            "ele_agent_default_" + uid,
			"name":          "Ele Agent",
			"provider":      "eleagent",
			"model_name":    "qwen/Qwen/Qwen3-8B",
			"base_url":      "http://localhost:8080/v1",
			"api_key":       "eleagent_" + newUUID(),
			"system_prompt": "你是 Eleball 官方智能助手 Ele Agent。",
		},
	})
}

func refreshHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误")
		return
	}
	claims, err := parseJWT(req.RefreshToken)
	if err != nil || claims.TokenType != "refresh" {
		respondError(w, 2001, "Refresh Token 无效")
		return
	}
	mu.RLock()
	user := users[claims.UserID]
	mu.RUnlock()
	if user == nil {
		respondError(w, 2001, "用户不存在")
		return
	}
	accessToken := generateJWT(user.ID, claims.DeviceID, user.Role, "access", 2*time.Hour)
	refreshToken := generateJWT(user.ID, claims.DeviceID, user.Role, "refresh", 720*time.Hour)
	respondSuccess(w, map[string]interface{}{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"user_id":       user.ID,
		"default_model_profile": map[string]interface{}{
			"id":            "ele_agent_default_" + user.ID,
			"name":          "Ele Agent",
			"provider":      "eleagent",
			"model_name":    "qwen/Qwen/Qwen3-8B",
			"base_url":      "http://localhost:8080/v1",
			"api_key":       "eleagent_" + newUUID(),
			"system_prompt": "你是 Eleball 官方智能助手 Ele Agent。",
		},
	})
}

// sendEmailOTPE2E E2E 桩：不真发邮件，固定验证码 123456，控制台打印便于测试。
func sendEmailOTPE2E(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误: "+err.Error())
		return
	}
	if req.Email == "" {
		respondError(w, 1001, "邮箱不能为空")
		return
	}
	// 固定码 123456，打印供测试查看
	fmt.Printf("[E2E OTP] 邮箱 %s 验证码: 123456\n", req.Email)
	respondSuccess(w, map[string]interface{}{})
}

// emailLoginE2E E2E 桩：验证码固定 123456 即登录或创建用户。
func emailLoginE2E(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Code     string `json:"code"`
		DeviceID string `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误: "+err.Error())
		return
	}
	if req.Email == "" || req.Code == "" || req.DeviceID == "" {
		respondError(w, 1001, "邮箱、验证码、设备ID 不能为空")
		return
	}
	if req.Code != "123456" {
		respondError(w, 2001, "验证码错误")
		return
	}

	// 查找或创建用户
	uid := ""
	var user *User
	mu.Lock()
	for id, u := range users {
		if u.Email == req.Email && req.Email != "" {
			uid = id
			user = u
			break
		}
	}
	if uid == "" {
		uid = newUUID()
		user = &User{
			ID:       uid,
			Username: req.Email,
			Email:    req.Email,
			Password: "", // 邮箱用户无密码
			Role:     "user",
			Status:   1,
		}
		users[uid] = user
		usernameIndex[req.Email] = uid
	}
	mu.Unlock()

	accessToken := generateJWT(uid, req.DeviceID, user.Role, "access", 2*time.Hour)
	refreshToken := generateJWT(uid, req.DeviceID, user.Role, "refresh", 720*time.Hour)
	respondSuccess(w, map[string]interface{}{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"user_id":       uid,
		"default_model_profile": map[string]interface{}{
			"id":            "ele_agent_default_" + uid,
			"name":          "Ele Agent",
			"provider":      "eleagent",
			"model_name":    "qwen/Qwen/Qwen3-8B",
			"base_url":      "http://localhost:8080/v1",
			"api_key":       "eleagent_" + newUUID(),
			"system_prompt": "你是 Eleball 官方智能助手 Ele Agent。",
		},
	})
}

func meHandler(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	mu.RLock()
	user := users[uid]
	mu.RUnlock()
	if user == nil {
		respondError(w, 2001, "用户不存在")
		return
	}
	// 与 specs/api-schema.yml 中 /auth/me 响应字段保持一致
	respondSuccess(w, map[string]interface{}{
		"user_id":       user.ID,
		"username":      user.Username,
		"nickname":      user.Nickname,
		"avatar_url":    user.AvatarURL,
		"role":          user.Role,
		"is_vip":        user.IsVIP,
		"vip_level":     user.VIPLevel,
		"vip_expire_at": time.Unix(user.VIPExpireAt, 0).Format(time.RFC3339),
	})
}

// --- Chat ---

type ChatMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // 支持 string 或 content parts 数组
}

type ChatRequest struct {
	Provider string        `json:"provider"`
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type StreamChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int     `json:"index"`
		Delta        Delta   `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

type Delta struct {
	Content string `json:"content"`
}

func chatHandler(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误: "+err.Error())
		return
	}
	if req.Model == "" || len(req.Messages) == 0 {
		respondError(w, 1001, "model, messages 不能为空")
		return
	}

	// Ele Agent 后端代理：解析子平台并继续按子平台处理
	provider := req.Provider
	// 兼容旧客户端：未传 provider 且模型名含 "/" 时，按 Ele Agent 子平台格式推断
	if provider == "" && strings.Contains(req.Model, "/") {
		provider = "eleagent"
	}

	var apiKey, baseURL string
	var selectedCfg *EleAgentModelConfig
	// 优先使用管理员后台配置的模型凭据
	// 先尝试用完整模型名匹配（兼容后台配置填了完整格式的情况）
	if cfg := findEleAgentConfigByFullModel(req.Model); cfg != nil {
		apiKey = cfg.APIKey
		baseURL = cfg.BaseURL + eleAgentUpstreamPath(cfg.Protocol)
		provider = cfg.Provider
		req.Model = cfg.ModelName
		selectedCfg = cfg
		fmt.Fprintf(os.Stderr, "[chat] use full-model config provider=%s model=%s baseURL=%s hasKey=%v\n", cfg.Provider, cfg.ModelName, cfg.BaseURL, apiKey != "")
	} else if provider == "eleagent" {
		parts := strings.SplitN(req.Model, "/", 2)
		if len(parts) != 2 {
			respondError(w, 1001, "Ele Agent 模型名格式错误，应为 subProvider/subModel")
			return
		}
		provider = parts[0]
		req.Model = parts[1]
		if cfg := findEleAgentConfig(provider, req.Model); cfg != nil {
			apiKey = cfg.APIKey
			baseURL = cfg.BaseURL + eleAgentUpstreamPath(cfg.Protocol)
			selectedCfg = cfg
			fmt.Fprintf(os.Stderr, "[chat] use split config provider=%s model=%s baseURL=%s hasKey=%v\n", cfg.Provider, cfg.ModelName, cfg.BaseURL, apiKey != "")
		} else {
			fmt.Fprintf(os.Stderr, "[chat] no config for provider=%s model=%s, fallback to env\n", provider, req.Model)
		}
	} else {
		fmt.Fprintf(os.Stderr, "[chat] non-eleagent provider=%s model=%s, fallback to env\n", provider, req.Model)
	}

	// Ele Agent 付费模型调用前校验余额：输入/输出/按次附加费单价任一大于 0 且弹丸余额为负则拒绝
	uid := userIDFrom(r)
	if selectedCfg != nil && (selectedCfg.InputPricePerCall > 0 || selectedCfg.PricePerCall > 0 || selectedCfg.PricePerGeneration > 0) {
		mu.RLock()
		bal := balances[uid]
		mu.RUnlock()
		if bal <= 0 {
			respondError(w, 4002, "弹丸余额不足，请充值")
			return
		}
	}

	// 未配置时回退到环境变量兜底
	if apiKey == "" {
		switch provider {
		case "deepseek":
			apiKey = os.Getenv("DEEPSEEK_API_KEY")
			baseURL = "https://api.deepseek.com/v1/chat/completions"
		case "qwen":
			apiKey = os.Getenv("QWEN_API_KEY")
			baseURL = "https://api.siliconflow.cn/v1/chat/completions"
		case "moonshot":
			apiKey = os.Getenv("MOONSHOT_API_KEY")
			baseURL = "https://api.moonshot.cn/v1/chat/completions"
		default:
			apiKey = os.Getenv("OPENAI_API_KEY")
			baseURL = "https://api.openai.com/v1/chat/completions"
		}
	}

	if apiKey == "" {
		if req.Stream {
			mockStreamResponse(w, req.Model)
		} else {
			respondSuccess(w, map[string]interface{}{
				"id":      "chatcmpl-mock",
				"object":  "chat.completion",
				"created": time.Now().Unix(),
				"model":   req.Model,
				"choices": []map[string]interface{}{{
					"index": 0,
					"message": map[string]string{
						"role":    "assistant",
						"content": "（演示模式）当前未配置 LLM API Key，请在服务器环境变量中设置 OPENAI_API_KEY、DEEPSEEK_API_KEY 或 MOONSHOT_API_KEY 后重启服务。",
					},
					"finish_reason": "stop",
				}},
				"usage": map[string]int{
					"prompt_tokens":     10,
					"completion_tokens": 30,
					"total_tokens":      40,
				},
			})
		}
		return
	}

	// 对齐正式网关：将 file.text 降级为纯文本，保留 image_url
	req.Messages = normalizeE2EMessages(req.Messages)

	if req.Stream {
		proxyStream(w, baseURL, apiKey, req)
	} else {
		proxyNonStream(w, baseURL, apiKey, req)
	}
}

func mockStreamResponse(w http.ResponseWriter, model string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	chunks := []string{
		"你好！", "我是 Eleball 演示助手。", "\n\n",
		"当前处于**演示模式**，", "未配置真实的大模型 API Key。", "\n\n",
		"请在服务器环境变量中设置 ", "`OPENAI_API_KEY` 或 `DEEPSEEK_API_KEY`，", "然后重启服务即可调用真实模型。",
	}
	for _, text := range chunks {
		chunk := StreamChunk{
			ID:      "chatcmpl-mock",
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   model,
			Choices: []struct {
				Index        int     `json:"index"`
				Delta        Delta   `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			}{{Index: 0, Delta: Delta{Content: text}}},
		}
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", data)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(80 * time.Millisecond)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func proxyStream(w http.ResponseWriter, url, apiKey string, req ChatRequest) {
	body, _ := json.Marshal(req)
	r2, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	r2.Header.Set("Authorization", "Bearer "+apiKey)
	r2.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(r2)
	if err != nil {
		respondError(w, 3001, "模型调用失败: "+err.Error())
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func proxyNonStream(w http.ResponseWriter, url, apiKey string, req ChatRequest) {
	body, _ := json.Marshal(req)
	r2, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	r2.Header.Set("Authorization", "Bearer "+apiKey)
	r2.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(r2)
	if err != nil {
		respondError(w, 3001, "模型调用失败: "+err.Error())
		return
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	respondSuccess(w, result)
}

// normalizeE2EMessages 将 content parts 中的 file.text 降级为纯文本，
// 保留 image_url 供视觉模型使用；使 E2E 服务器与正式网关行为一致。
func normalizeE2EMessages(messages []ChatMessage) []ChatMessage {
	result := make([]ChatMessage, len(messages))
	for i, msg := range messages {
		parts, ok := msg.Content.([]interface{})
		if !ok {
			result[i] = msg
			continue
		}
		var textParts []string
		var otherParts []interface{}
		for _, p := range parts {
			part, ok := p.(map[string]interface{})
			if !ok {
				otherParts = append(otherParts, p)
				continue
			}
			partType, _ := part["type"].(string)
			if partType == "text" {
				if t, ok := part["text"].(string); ok {
					textParts = append(textParts, t)
				}
			} else if partType == "file" {
				file, ok := part["file"].(map[string]interface{})
				if !ok {
					continue
				}
				name, _ := file["name"].(string)
				if t, ok := file["text"].(string); ok && t != "" {
					if name != "" {
						textParts = append(textParts, fmt.Sprintf("【文件：%s】\n%s", name, t))
					} else {
						textParts = append(textParts, t)
					}
				}
				if _, ok := file["data"].(string); ok {
					if name != "" {
						textParts = append(textParts, fmt.Sprintf("【文件：%s】（二进制文件，当前模型可能无法直接解析）", name))
					}
				}
			} else if partType == "image_url" {
				otherParts = append(otherParts, part)
			} else {
				otherParts = append(otherParts, part)
			}
		}
		if len(textParts) > 0 {
			mergedText := strings.Join(textParts, "\n\n")
			if len(otherParts) == 0 {
				msg.Content = mergedText
			} else {
				msg.Content = append([]interface{}{map[string]interface{}{"type": "text", "text": mergedText}}, otherParts...)
			}
		} else if len(otherParts) > 0 {
			msg.Content = otherParts
		} else {
			msg.Content = ""
		}
		result[i] = msg
	}
	return result
}

// --- Billing ---

func balanceHandler(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	if uid == "" {
		respondError(w, 2001, "未登录")
		return
	}
	mu.RLock()
	danwan := balances[uid]
	elegant := elegantBalances[uid]
	mu.RUnlock()
	respondSuccess(w, map[string]interface{}{
		"danwan":  danwan,
		"elegant": elegant,
		"unit":    "cent",
	})
}

func eleagentCredentialsHandler(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	if uid == "" {
		respondError(w, 2001, "未登录")
		return
	}
	subProvider := r.URL.Query().Get("subProvider")
	subModel := r.URL.Query().Get("subModel")
	if subProvider == "" || subModel == "" {
		respondError(w, 1001, "subProvider 和 subModel 不能为空")
		return
	}

	// 付费模型校验余额：输入/输出/按次附加费单价任一大于 0 且弹丸余额为负则拒绝发放凭证（402）
	if cfg := findEleAgentConfig(subProvider, subModel); cfg != nil && (cfg.InputPricePerCall > 0 || cfg.PricePerCall > 0 || cfg.PricePerGeneration > 0) {
		mu.RLock()
		bal := balances[uid]
		mu.RUnlock()
		if bal <= 0 {
			w.WriteHeader(http.StatusPaymentRequired)
			respondError(w, 4002, "弹丸余额不足，请充值")
			return
		}
	}

	respondSuccess(w, map[string]interface{}{
		"baseUrl":   "http://localhost:8080/v1",
		"apiKey":    "eleagent_" + newUUID(),
		"expiresAt": time.Now().Add(24 * time.Hour).Format(time.RFC3339),
	})
}

func eleagentModelsHandler(w http.ResponseWriter, r *http.Request) {
	// 模型列表公开，无需登录
	respondSuccess(w, listEleAgentOptions())
}

// --- Admin Ele Agent Models ---

type AdminCreateEleAgentModelReq struct {
	Provider          string `json:"provider"`
	Protocol          string `json:"protocol"`
	ModelName         string `json:"model_name"`
	DisplayName       string `json:"display_name"`
	BaseURL           string `json:"base_url"`
	APIKey            string `json:"api_key"`
	Priority          int    `json:"priority"`
	InputPricePerCall int64  `json:"input_price_per_call"`
	PricePerCall      int64  `json:"price_per_call"`
	PricePerGeneration int64 `json:"price_per_generation"`
	VideoMinDuration   int   `json:"video_min_duration"`
	VideoMaxDuration   int   `json:"video_max_duration"`
	VideoDurationStep  int   `json:"video_duration_step"`
	SupportsChat              bool `json:"supports_chat"`
	SupportsVision            bool `json:"supports_vision"`
	SupportsImage             bool `json:"supports_image"`
	SupportsVideo             bool `json:"supports_video"`
	SupportsImageInput        bool `json:"supports_image_input"`
	SupportsContinuousContext bool `json:"supports_continuous_context"`
	SupportsTools             bool `json:"supports_tools"`
}

type AdminUpdateEleAgentModelReq struct {
	Protocol          string `json:"protocol"`
	DisplayName       string `json:"display_name"`
	BaseURL           string `json:"base_url"`
	IsEnabled         *bool  `json:"is_enabled"`
	Priority          *int   `json:"priority"`
	InputPricePerCall *int64 `json:"input_price_per_call"`
	PricePerCall      *int64 `json:"price_per_call"`
	PricePerGeneration *int64 `json:"price_per_generation"`
	VideoMinDuration   *int   `json:"video_min_duration"`
	VideoMaxDuration   *int   `json:"video_max_duration"`
	VideoDurationStep  *int   `json:"video_duration_step"`
	SupportsChat              *bool  `json:"supports_chat"`
	SupportsVision    *bool  `json:"supports_vision"`
	SupportsImage             *bool  `json:"supports_image"`
	SupportsVideo             *bool  `json:"supports_video"`
	SupportsImageInput        *bool  `json:"supports_image_input"`
	SupportsContinuousContext *bool  `json:"supports_continuous_context"`
	SupportsTools             *bool  `json:"supports_tools"`
}

func adminEleAgentModelsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		adminListEleAgentModels(w, r)
	case http.MethodPost:
		adminCreateEleAgentModel(w, r)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func adminEleAgentModelItemHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		adminGetEleAgentModel(w, r)
	case http.MethodPatch:
		adminUpdateEleAgentModel(w, r)
	case http.MethodDelete:
		adminDeleteEleAgentModel(w, r)
	case http.MethodPost:
		adminRotateEleAgentModelKey(w, r)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func adminListEleAgentModels(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	mu.RLock()
	all := make([]*EleAgentModelConfig, 0, len(eleAgentConfigs))
	for _, cfg := range eleAgentConfigs {
		if provider != "" && cfg.Provider != provider {
			continue
		}
		all = append(all, cfg)
	}
	mu.RUnlock()

	// 与正式网关保持一致：priority ASC, created_at ASC（数字小优先级高，见 eleagent_model_repo）
	sort.Slice(all, func(i, j int) bool {
		if all[i].Priority != all[j].Priority {
			return all[i].Priority < all[j].Priority
		}
		return all[i].CreatedAt < all[j].CreatedAt
	})

	total := len(all)
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	items := all[start:end]

	respondSuccess(w, map[string]interface{}{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func adminCreateEleAgentModel(w http.ResponseWriter, r *http.Request) {
	defer saveData()
	var req AdminCreateEleAgentModelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误: "+err.Error())
		return
	}
	if req.Provider == "" || req.ModelName == "" || req.BaseURL == "" || req.APIKey == "" {
		respondError(w, 1001, "provider, model_name, base_url, api_key 不能为空")
		return
	}
	if req.InputPricePerCall < 0 || req.PricePerCall < 0 || req.Priority < 0 {
		respondError(w, 1001, "单价与优先级不能为负数")
		return
	}

	mu.Lock()
	defer mu.Unlock()
	for _, cfg := range eleAgentConfigs {
		if cfg.Provider == req.Provider && cfg.ModelName == req.ModelName {
			respondError(w, 1000, "该 provider/model 已存在")
			return
		}
	}

	uid := newUUID()
	now := time.Now().Unix()
	protocol := req.Protocol
	if protocol == "" {
		protocol = "openai_compatible"
	}
	cfg := &EleAgentModelConfig{
		ID:                uid,
		Provider:          req.Provider,
		Protocol:          protocol,
		ModelName:         req.ModelName,
		DisplayName:       req.DisplayName,
		BaseURL:           strings.TrimSuffix(req.BaseURL, "/chat/completions"),
		APIKey:            req.APIKey,
		IsEnabled:         true,
		SupportsChat:              req.SupportsChat,
		SupportsVision:            req.SupportsVision,
		SupportsImage:             req.SupportsImage,
		SupportsVideo:             req.SupportsVideo,
		SupportsImageInput:        req.SupportsImageInput,
		SupportsContinuousContext: req.SupportsContinuousContext,
		SupportsTools:             req.SupportsTools,
		Priority:          req.Priority,
		InputPricePerCall: req.InputPricePerCall,
		PricePerCall:      req.PricePerCall,
		PricePerGeneration: req.PricePerGeneration,
		VideoMinDuration:   req.VideoMinDuration,
		VideoMaxDuration:   req.VideoMaxDuration,
		VideoDurationStep:  req.VideoDurationStep,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	eleAgentConfigs[uid] = cfg
	respondSuccess(w, cfg)
}

func adminGetEleAgentModel(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/admin/eleagent/models/")
	mu.RLock()
	cfg, ok := eleAgentConfigs[id]
	mu.RUnlock()
	if !ok {
		respondError(w, 1000, "配置不存在")
		return
	}
	respondSuccess(w, cfg)
}

func adminUpdateEleAgentModel(w http.ResponseWriter, r *http.Request) {
	defer saveData()
	id := strings.TrimPrefix(r.URL.Path, "/v1/admin/eleagent/models/")
	mu.Lock()
	defer mu.Unlock()
	cfg, ok := eleAgentConfigs[id]
	if !ok {
		respondError(w, 1000, "配置不存在")
		return
	}

	var req AdminUpdateEleAgentModelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误: "+err.Error())
		return
	}
	if (req.InputPricePerCall != nil && *req.InputPricePerCall < 0) ||
		(req.PricePerCall != nil && *req.PricePerCall < 0) ||
		(req.Priority != nil && *req.Priority < 0) {
		respondError(w, 1001, "单价与优先级不能为负数")
		return
	}
	if req.Protocol != "" {
		cfg.Protocol = req.Protocol
	}
	if req.DisplayName != "" {
		cfg.DisplayName = req.DisplayName
	}
	if req.BaseURL != "" {
		cfg.BaseURL = strings.TrimSuffix(req.BaseURL, "/chat/completions")
	}
	if req.IsEnabled != nil {
		cfg.IsEnabled = *req.IsEnabled
	}
	if req.Priority != nil {
		cfg.Priority = *req.Priority
	}
	if req.InputPricePerCall != nil {
		cfg.InputPricePerCall = *req.InputPricePerCall
	}
	if req.PricePerCall != nil {
		cfg.PricePerCall = *req.PricePerCall
	}
	if req.PricePerGeneration != nil {
		cfg.PricePerGeneration = *req.PricePerGeneration
	}
	if req.VideoMinDuration != nil {
		cfg.VideoMinDuration = *req.VideoMinDuration
	}
	if req.VideoMaxDuration != nil {
		cfg.VideoMaxDuration = *req.VideoMaxDuration
	}
	if req.VideoDurationStep != nil {
		cfg.VideoDurationStep = *req.VideoDurationStep
	}
	if req.SupportsTools != nil {
		cfg.SupportsTools = *req.SupportsTools
	}
	if req.SupportsChat != nil {
		cfg.SupportsChat = *req.SupportsChat
	}
	if req.SupportsVision != nil {
		cfg.SupportsVision = *req.SupportsVision
	}
	if req.SupportsImage != nil {
		cfg.SupportsImage = *req.SupportsImage
	}
	if req.SupportsVideo != nil {
		cfg.SupportsVideo = *req.SupportsVideo
	}
	if req.SupportsImageInput != nil {
		cfg.SupportsImageInput = *req.SupportsImageInput
	}
	if req.SupportsContinuousContext != nil {
		cfg.SupportsContinuousContext = *req.SupportsContinuousContext
	}
	cfg.UpdatedAt = time.Now().Unix()
	respondSuccess(w, cfg)
}

func adminDeleteEleAgentModel(w http.ResponseWriter, r *http.Request) {
	defer saveData()
	id := strings.TrimPrefix(r.URL.Path, "/v1/admin/eleagent/models/")
	mu.Lock()
	delete(eleAgentConfigs, id)
	mu.Unlock()
	respondSuccess(w, nil)
}

func adminRotateEleAgentModelKey(w http.ResponseWriter, r *http.Request) {
	defer saveData()
	id := strings.TrimPrefix(r.URL.Path, "/v1/admin/eleagent/models/")
	id = strings.TrimSuffix(id, "/rotate-key")
	mu.Lock()
	defer mu.Unlock()
	cfg, ok := eleAgentConfigs[id]
	if !ok {
		respondError(w, 1000, "配置不存在")
		return
	}
	var req struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误: "+err.Error())
		return
	}
	if req.APIKey == "" {
		respondError(w, 1001, "api_key 不能为空")
		return
	}
	cfg.APIKey = req.APIKey
	cfg.UpdatedAt = time.Now().Unix()
	respondSuccess(w, cfg)
}

// ============================================================================
//  Ele Agent 模型配置批量导出 / 导入（与正式网关契约一致）
// ============================================================================

// E2EEleAgentModelExportItem 单条配置导出/导入结构
type E2EEleAgentModelExportItem struct {
	Provider                  string `json:"provider"`
	Protocol                  string `json:"protocol"`
	ModelName                 string `json:"model_name"`
	DisplayName               string `json:"display_name,omitempty"`
	BaseURL                   string `json:"base_url"`
	APIKey                    string `json:"api_key,omitempty"`
	IsEnabled                 *bool  `json:"is_enabled,omitempty"`
	SupportsChat              bool   `json:"supports_chat"`
	SupportsVision            bool   `json:"supports_vision"`
	SupportsImage             bool   `json:"supports_image"`
	SupportsVideo             bool   `json:"supports_video"`
	SupportsImageInput        bool   `json:"supports_image_input"`
	SupportsContinuousContext bool   `json:"supports_continuous_context"`
	SupportsTools             bool   `json:"supports_tools"`
	Priority                  int    `json:"priority"`
	InputPricePerCall         int64  `json:"input_price_per_call"`
	PricePerCall              int64  `json:"price_per_call"`
	PricePerGeneration        int64  `json:"price_per_generation"`
	VideoMinDuration          int    `json:"video_min_duration"`
	VideoMaxDuration          int    `json:"video_max_duration"`
	VideoDurationStep         int    `json:"video_duration_step"`

	Present map[string]bool `json:"-"`
}

// UnmarshalJSON 与正式网关一致：解析字段值的同时记录 JSON 中出现的字段，
// 更新已有配置时只覆盖出现的字段，未出现的字段保持原值。
func (item *E2EEleAgentModelExportItem) UnmarshalJSON(data []byte) error {
	type alias E2EEleAgentModelExportItem
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*item = E2EEleAgentModelExportItem(a)
	item.Present = make(map[string]bool, len(raw))
	for k := range raw {
		item.Present[k] = true
	}
	return nil
}

// Has 判断导入项是否提供了指定字段；代码内直接构造（Present 为 nil）视为全字段提供。
func (item *E2EEleAgentModelExportItem) Has(field string) bool {
	if item.Present == nil {
		return true
	}
	return item.Present[field]
}

// E2EEleAgentModelExportData 导出文件整体结构
type E2EEleAgentModelExportData struct {
	Version     int                           `json:"version"`
	ExportedAt  string                        `json:"exported_at"`
	IncludeKeys bool                          `json:"include_keys"`
	Usage       string                        `json:"usage"`
	FieldNotes  map[string]string             `json:"field_notes"`
	Items       []E2EEleAgentModelExportItem  `json:"items"`
}

// e2eEleAgentModelExportUsage 与正式网关一致的导入规则说明
const e2eEleAgentModelExportUsage = "本文件由 Ele Agent 模型配置导出，可直接用于批量导入：按 provider + model_name 匹配，已存在则只覆盖文件中出现的字段（api_key 省略=保留原 Key，提供=轮换），不存在则创建（需完整字段与 api_key）。usage 与 field_notes 仅供阅读，导入时忽略。"

// e2eEleAgentModelFieldNotes 逐字段含义与取值范围说明（与正式网关一致）
var e2eEleAgentModelFieldNotes = map[string]string{
	"provider":                  "平台标识（自定义，用于配置匹配与统计），如 kimi / volcengine / agnes / qwen；与 model_name 组成唯一匹配键",
	"protocol":                  "上游协议：openai_compatible（对话）/ anthropic_messages（对话）/ agnes_image（图片）/ agnes_video（视频）/ seedance（火山视频）/ seedream（火山方舟·即梦图片）/ openai_image、openai_video（预留）；缺省为 openai_compatible",
	"model_name":                "上游模型 ID，如 k3、doubao-seedream-4-0-250828、doubao-seedance-1-0-pro-250528",
	"display_name":              "展示名称（可选），客户端模型列表中显示",
	"base_url":                  "上游 API 地址；新建必填，更新时省略表示保持原值",
	"api_key":                   "明文 API Key；新建必填；更新时省略=保留原 Key，提供=轮换 Key",
	"is_enabled":                "是否启用；更新时省略表示保持原启用状态",
	"supports_chat":             "能力开关：支持文字对话（对话页）；纯图片/纯视频生成模型应为 false，对话/图片/视频至少需开启一项",
	"supports_vision":           "能力开关：支持视觉理解（图片输入）",
	"supports_image":            "能力开关：支持图片生成（需搭配 agnes_image / seedream 协议）",
	"supports_video":            "能力开关：支持视频生成（需搭配 agnes_video / seedance 协议）",
	"supports_image_input":      "能力开关：支持上传图片作为生成输入（图生图/图生视频）",
	"supports_continuous_context": "能力开关（产品声明）：支持连续上下文创作，运行时由 protocol 决定",
	"supports_tools":            "能力开关：支持 Agent 工具调用（Function Call）",
	"priority":                  "优先级（整数 ≥0，越小越靠前），用于客户端模型列表排序",
	"input_price_per_call":      "输入单价（弹丸 / 1M tokens，≥0），0 表示免费",
	"price_per_call":            "输出单价（弹丸 / 1M tokens，≥0），0 表示免费",
	"price_per_generation":      "按次附加费（弹丸/次，≥0），与输入/输出 token 费用相加，适用于对话/图片/视频模型，0 表示不附加",
	"video_min_duration":        "视频最小时长（秒，≥0），0 表示不限制；不能超过 video_max_duration",
	"video_max_duration":        "视频最大时长（秒，≥0），0 表示不限制；示例：Seedance 1.0 Pro 支持 5~10 秒",
	"video_duration_step":       "视频时长步长（秒，≥1），前端按 min~max 以步长生成可选档位；示例：5~10 秒步长 5 → 可选 5s / 10s",
}

func adminExportEleAgentModels(w http.ResponseWriter, r *http.Request) {
	includeKeys := r.URL.Query().Get("include_keys") == "true"

	mu.RLock()
	all := make([]*EleAgentModelConfig, 0, len(eleAgentConfigs))
	for _, cfg := range eleAgentConfigs {
		all = append(all, cfg)
	}
	mu.RUnlock()
	sort.Slice(all, func(i, j int) bool {
		if all[i].Priority != all[j].Priority {
			return all[i].Priority < all[j].Priority
		}
		return all[i].CreatedAt < all[j].CreatedAt
	})

	out := E2EEleAgentModelExportData{
		Version:     1,
		ExportedAt:  time.Now().Format(time.RFC3339),
		IncludeKeys: includeKeys,
		Usage:       e2eEleAgentModelExportUsage,
		FieldNotes:  e2eEleAgentModelFieldNotes,
		Items:       make([]E2EEleAgentModelExportItem, 0, len(all)),
	}
	for _, cfg := range all {
		enabled := cfg.IsEnabled
		item := E2EEleAgentModelExportItem{
			Provider:                  cfg.Provider,
			Protocol:                  cfg.Protocol,
			ModelName:                 cfg.ModelName,
			DisplayName:               cfg.DisplayName,
			BaseURL:                   cfg.BaseURL,
			IsEnabled:                 &enabled,
			SupportsChat:              cfg.SupportsChat,
			SupportsVision:            cfg.SupportsVision,
			SupportsImage:             cfg.SupportsImage,
			SupportsVideo:             cfg.SupportsVideo,
			SupportsImageInput:        cfg.SupportsImageInput,
			SupportsContinuousContext: cfg.SupportsContinuousContext,
			SupportsTools:             cfg.SupportsTools,
			Priority:                  cfg.Priority,
			InputPricePerCall:         cfg.InputPricePerCall,
			PricePerCall:              cfg.PricePerCall,
			PricePerGeneration:        cfg.PricePerGeneration,
			VideoMinDuration:          cfg.VideoMinDuration,
			VideoMaxDuration:          cfg.VideoMaxDuration,
			VideoDurationStep:         cfg.VideoDurationStep,
		}
		if includeKeys {
			item.APIKey = cfg.APIKey
		}
		out.Items = append(out.Items, item)
	}

	filename := "eleagent-models-" + time.Now().Format("20060102-150405") + ".json"
	// 输出缩进对齐的代码风格 JSON（非单行压缩），便于人工查看与编辑后再导入
	indented, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		respondError(w, 1000, "序列化导出数据失败: "+err.Error())
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(indented)
}

func adminImportEleAgentModels(w http.ResponseWriter, r *http.Request) {
	defer saveData()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		respondError(w, 1001, "读取请求体失败: "+err.Error())
		return
	}

	// 先按导出文件结构解析，失败再按纯数组解析
	var items []E2EEleAgentModelExportItem
	var wrapper E2EEleAgentModelExportData
	if err := json.Unmarshal(body, &wrapper); err == nil && wrapper.Items != nil {
		items = wrapper.Items
	} else if err := json.Unmarshal(body, &items); err != nil {
		respondError(w, 1001, "JSON 格式错误，应为导出文件（含 items 字段）或配置数组")
		return
	}
	if len(items) == 0 {
		respondError(w, 1001, "导入内容为空")
		return
	}
	if len(items) > 500 {
		respondError(w, 1001, "单次最多导入 500 条配置")
		return
	}

	type failure struct {
		Index     int    `json:"index"`
		Provider  string `json:"provider"`
		ModelName string `json:"model_name"`
		Error     string `json:"error"`
	}
	created, updated := 0, 0
	failures := make([]failure, 0)
	seen := make(map[string]bool, len(items))

	mu.Lock()
	defer mu.Unlock()
	for i, item := range items {
		fail := func(msg string) {
			failures = append(failures, failure{Index: i, Provider: item.Provider, ModelName: item.ModelName, Error: msg})
		}
		item.Provider = strings.TrimSpace(item.Provider)
		item.ModelName = strings.TrimSpace(item.ModelName)
		item.BaseURL = strings.TrimSpace(item.BaseURL)
		if item.Provider == "" || item.ModelName == "" {
			fail("provider、model_name 不能为空")
			continue
		}
		if item.Has("base_url") && item.BaseURL == "" {
			fail("base_url 不能为空")
			continue
		}
		mapKey := item.Provider + "/" + item.ModelName
		if seen[mapKey] {
			fail("导入文件内存在重复的 provider/model_name")
			continue
		}
		seen[mapKey] = true

		// 按 provider + model_name 查找现有配置
		var existing *EleAgentModelConfig
		for _, cfg := range eleAgentConfigs {
			if cfg.Provider == item.Provider && cfg.ModelName == item.ModelName {
				existing = cfg
				break
			}
		}

		if existing == nil {
			// 新建路径：完整字段校验
			if item.BaseURL == "" {
				fail("新建配置必须提供 base_url")
				continue
			}
			protocol := item.Protocol
			if protocol == "" {
				protocol = "openai_compatible"
			}
			if item.Priority < 0 || item.InputPricePerCall < 0 || item.PricePerCall < 0 || item.PricePerGeneration < 0 ||
				item.VideoMinDuration < 0 || item.VideoMaxDuration < 0 || item.VideoDurationStep < 0 {
				fail("单价、时长与优先级不能为负数")
				continue
			}
			if item.VideoMaxDuration > 0 && item.VideoMinDuration > item.VideoMaxDuration {
				fail("视频最小时长不能大于最大时长")
				continue
			}
			if item.APIKey == "" {
				fail("新建配置必须提供 api_key")
				continue
			}
			isEnabled := true
			if item.IsEnabled != nil {
				isEnabled = *item.IsEnabled
			}
			now := time.Now().Unix()
			newCfg := &EleAgentModelConfig{
				ID:                        newUUID(),
				Provider:                  item.Provider,
				Protocol:                  protocol,
				ModelName:                 item.ModelName,
				DisplayName:               item.DisplayName,
				BaseURL:                   item.BaseURL,
				APIKey:                    item.APIKey,
				IsEnabled:                 isEnabled,
				SupportsChat:              item.SupportsChat,
				SupportsVision:            item.SupportsVision,
				SupportsImage:             item.SupportsImage,
				SupportsVideo:             item.SupportsVideo,
				SupportsImageInput:        item.SupportsImageInput,
				SupportsContinuousContext: item.SupportsContinuousContext,
				SupportsTools:             item.SupportsTools,
				Priority:                  item.Priority,
				InputPricePerCall:         item.InputPricePerCall,
				PricePerCall:              item.PricePerCall,
				PricePerGeneration:        item.PricePerGeneration,
				VideoMinDuration:          item.VideoMinDuration,
				VideoMaxDuration:          item.VideoMaxDuration,
				VideoDurationStep:         item.VideoDurationStep,
				CreatedAt:                 now,
				UpdatedAt:                 now,
			}
			eleAgentConfigs[newCfg.ID] = newCfg
			created++
			continue
		}

		// 更新路径：只覆盖文件中出现的字段（未出现保持原值；api_key 不提供时保留原 Key）
		if (item.Has("priority") && item.Priority < 0) ||
			(item.Has("input_price_per_call") && item.InputPricePerCall < 0) ||
			(item.Has("price_per_call") && item.PricePerCall < 0) ||
			(item.Has("price_per_generation") && item.PricePerGeneration < 0) ||
			(item.Has("video_min_duration") && item.VideoMinDuration < 0) ||
			(item.Has("video_max_duration") && item.VideoMaxDuration < 0) ||
			(item.Has("video_duration_step") && item.VideoDurationStep < 0) {
			fail("单价、时长与优先级不能为负数")
			continue
		}
		effMinDuration := existing.VideoMinDuration
		if item.Has("video_min_duration") {
			effMinDuration = item.VideoMinDuration
		}
		effMaxDuration := existing.VideoMaxDuration
		if item.Has("video_max_duration") {
			effMaxDuration = item.VideoMaxDuration
		}
		if effMaxDuration > 0 && effMinDuration > effMaxDuration {
			fail("视频最小时长不能大于最大时长")
			continue
		}

		if item.Has("protocol") {
			existing.Protocol = item.Protocol
			if existing.Protocol == "" {
				existing.Protocol = "openai_compatible"
			}
		}
		if item.Has("display_name") {
			existing.DisplayName = item.DisplayName
		}
		if item.Has("base_url") {
			existing.BaseURL = item.BaseURL
		}
		if item.Has("supports_chat") {
			existing.SupportsChat = item.SupportsChat
		}
		if item.Has("supports_vision") {
			existing.SupportsVision = item.SupportsVision
		}
		if item.Has("supports_image") {
			existing.SupportsImage = item.SupportsImage
		}
		if item.Has("supports_video") {
			existing.SupportsVideo = item.SupportsVideo
		}
		if item.Has("supports_image_input") {
			existing.SupportsImageInput = item.SupportsImageInput
		}
		if item.Has("supports_continuous_context") {
			existing.SupportsContinuousContext = item.SupportsContinuousContext
		}
		if item.Has("supports_tools") {
			existing.SupportsTools = item.SupportsTools
		}
		if item.Has("priority") {
			existing.Priority = item.Priority
		}
		if item.Has("input_price_per_call") {
			existing.InputPricePerCall = item.InputPricePerCall
		}
		if item.Has("price_per_call") {
			existing.PricePerCall = item.PricePerCall
		}
		if item.Has("price_per_generation") {
			existing.PricePerGeneration = item.PricePerGeneration
		}
		if item.Has("video_min_duration") {
			existing.VideoMinDuration = item.VideoMinDuration
		}
		if item.Has("video_max_duration") {
			existing.VideoMaxDuration = item.VideoMaxDuration
		}
		if item.Has("video_duration_step") {
			existing.VideoDurationStep = item.VideoDurationStep
		}
		if item.IsEnabled != nil {
			existing.IsEnabled = *item.IsEnabled
		}
		if item.APIKey != "" {
			existing.APIKey = item.APIKey
		}
		existing.UpdatedAt = time.Now().Unix()
		updated++
	}

	respondSuccess(w, map[string]interface{}{
		"created": created,
		"updated": updated,
		"failed":  failures,
	})
}

// --- Agent Market ---

func listAgentsHandler(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	category := r.URL.Query().Get("category")
	sortBy := r.URL.Query().Get("sort")
	filter := r.URL.Query().Get("filter")
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	uid := userIDFrom(r)
	ownedIDs := make(map[string]bool)
	mu.RLock()
	if filter == "owned" && uid != "" && purchases[uid] != nil {
		for id := range purchases[uid] {
			ownedIDs[id] = true
		}
	}

	all := make([]*AgentItem, 0, len(agents))
	for _, a := range agents {
		if a.Status != "approved" {
			continue
		}
		if category != "" && a.Category != category {
			continue
		}
		if filter == "owned" {
			if !ownedIDs[a.ID] {
				continue
			}
		}
		// 复制一份，避免修改全局数据
		cp := *a
		cp.DriverRegistered = e2eDriverRegisteredFromManifest(a.ManifestJSON)
		cp.ModuleOnline = e2eModuleOnlinePtrFromManifest(a.ManifestJSON)
		cp.CredentialComplete = e2eCredentialComplete(uid, a)
		if uid != "" && purchases[uid] != nil && purchases[uid][a.ID] {
			cp.IsActive = true
		}
		all = append(all, &cp)
	}
	mu.RUnlock()

	// 排序
	switch sortBy {
	case "new":
		sort.SliceStable(all, func(i, j int) bool { return all[i].CreatedAt > all[j].CreatedAt })
	case "rating":
		sort.SliceStable(all, func(i, j int) bool { return all[i].AvgRating > all[j].AvgRating })
	default: // hot
		sort.SliceStable(all, func(i, j int) bool { return all[i].PurchaseCount > all[j].PurchaseCount })
	}

	// 已激活秘技始终排在未激活前面
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].IsActive != all[j].IsActive {
			return all[i].IsActive && !all[j].IsActive
		}
		return false
	})

	total := int64(len(all))
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > len(all) {
		start = len(all)
	}
	if end > len(all) {
		end = len(all)
	}
	items := all[start:end]

	respondSuccess(w, map[string]interface{}{
		"total": total,
		"items": items,
	})
}

func getAgentHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/agents/")
	mu.RLock()
	agent := agentMap[id]
	mu.RUnlock()
	if agent == nil {
		respondError(w, 4004, "秘技不存在")
		return
	}
	respondSuccess(w, agent)
}

func purchaseAgentHandler(w http.ResponseWriter, r *http.Request) {
	defer saveData()
	uid := userIDFrom(r)
	if uid == "" {
		respondError(w, 2001, "未登录")
		return
	}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		respondError(w, 1001, "参数错误")
		return
	}
	agentID := parts[len(parts)-2]

	mu.Lock()
	defer mu.Unlock()

	agent := agentMap[agentID]
	if agent == nil {
		respondError(w, 4004, "秘技不存在")
		return
	}
	if agent.Status != "approved" {
		respondError(w, 3001, "秘技未上架")
		return
	}
	if !e2eDriverRegisteredFromManifest(agent.ManifestJSON) {
		respondError(w, 3001, "该秘技依赖的驱动未注册，暂不可购买")
		return
	}
	if purchases[uid] == nil {
		purchases[uid] = make(map[string]bool)
	}
	if purchases[uid][agentID] {
		respondError(w, 3001, "已购买该秘技")
		return
	}

	// 扣费（支持 danwan / elegant）
	var purchaseReq struct {
		Currency string `json:"currency"`
	}
	_ = json.NewDecoder(r.Body).Decode(&purchaseReq)
	currency := purchaseReq.Currency
	if currency == "" {
		currency = "danwan"
	}
	if currency == "elegant" && agent.PriceElegant != nil && *agent.PriceElegant > 0 {
		if elegantBalances[uid] < *agent.PriceElegant {
			respondError(w, 3001, "优雅弹丸余额不足")
			return
		}
		elegantBalances[uid] -= *agent.PriceElegant
	} else if agent.PriceDanwan > 0 {
		if balances[uid] < agent.PriceDanwan {
			respondError(w, 3001, "弹丸余额不足")
			return
		}
		balances[uid] -= agent.PriceDanwan
	}

	purchases[uid][agentID] = true
	agent.PurchaseCount++
	agent.UseCount++

	// 若该 SKU 附带 ToolManifest，则解锁用户动态工具
	if agent.ManifestJSON != "" {
		toolName := agent.ID
		// 尝试解析 manifest 中的 id 作为工具名，便于后续 Agent 工作流匹配
		var mf struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal([]byte(agent.ManifestJSON), &mf)
		if mf.ID != "" {
			toolName = mf.ID
		}
		already := false
		for _, ut := range agentUserTools {
			if ut.UserID == uid && ut.AgentID == agentID {
				already = true
				break
			}
		}
		if !already {
			agentUserTools = append(agentUserTools, &AgentUserTool{
				ID:        newUUID(),
				UserID:    uid,
				AgentID:   agentID,
				ToolName:  toolName,
				CreatedAt: time.Now().Unix(),
			})
		}
	}

	respondSuccess(w, nil)
}

func toggleActiveHandler(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	if uid == "" {
		respondError(w, 2001, "未登录")
		return
	}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		respondError(w, 1001, "参数错误")
		return
	}
	agentID := parts[len(parts)-2]

	mu.Lock()
	defer mu.Unlock()
	if purchases[uid] == nil || !purchases[uid][agentID] {
		respondError(w, 4003, "未购买该秘技")
		return
	}

	// E2E 环境简化为：购买即视为激活；切换仅返回当前状态
	active := true
	respondSuccess(w, map[string]interface{}{"active": active})
}

func listReviewsHandler(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		respondError(w, 1001, "参数错误")
		return
	}
	agentID := parts[len(parts)-2]
	mu.RLock()
	var items []*AgentReview
	for _, rv := range reviews {
		if rv.AgentID == agentID {
			items = append(items, rv)
		}
	}
	mu.RUnlock()
	respondSuccess(w, map[string]interface{}{
		"total": int64(len(items)),
		"items": items,
	})
}

func createReviewHandler(w http.ResponseWriter, r *http.Request) {
	defer saveData()
	uid := userIDFrom(r)
	if uid == "" {
		respondError(w, 2001, "未登录")
		return
	}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		respondError(w, 1001, "参数错误")
		return
	}
	agentID := parts[len(parts)-2]

	var req struct {
		AgentID string `json:"agent_id"`
		Rating  int    `json:"rating"`
		Comment string `json:"comment"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	mu.Lock()
	defer mu.Unlock()

	if purchases[uid] == nil || !purchases[uid][agentID] {
		respondError(w, 3001, "购买后才能评价")
		return
	}

	user := users[uid]
	name := "用户"
	if user != nil && user.Nickname != "" {
		name = user.Nickname
	}
	review := &AgentReview{
		ID:        newUUID(),
		AgentID:   agentID,
		UserID:    uid,
		UserName:  name,
		Rating:    req.Rating,
		Comment:   req.Comment,
		CreatedAt: time.Now().Unix(),
	}
	reviews = append(reviews, review)

	// 重新计算平均评分
	agent := agentMap[agentID]
	if agent != nil {
		var sum int
		var count int
		for _, rv := range reviews {
			if rv.AgentID == agentID {
				sum += rv.Rating
				count++
			}
		}
		if count > 0 {
			agent.AvgRating = float64(sum) / float64(count)
		}
	}
	respondSuccess(w, nil)
}

func toggleFavoriteHandler(w http.ResponseWriter, r *http.Request) {
	defer saveData()
	uid := userIDFrom(r)
	if uid == "" {
		respondError(w, 2001, "未登录")
		return
	}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		respondError(w, 1001, "参数错误")
		return
	}
	agentID := parts[len(parts)-2]

	mu.Lock()
	defer mu.Unlock()

	if favorites[uid] == nil {
		favorites[uid] = make(map[string]bool)
	}
	favorited := !favorites[uid][agentID]
	if favorited {
		favorites[uid][agentID] = true
	} else {
		delete(favorites[uid], agentID)
	}

	agent := agentMap[agentID]
	if agent != nil {
		var count int64
		for _, m := range favorites {
			if m[agentID] {
				count++
			}
		}
		agent.FavoriteCount = count
	}

	respondSuccess(w, map[string]bool{"favorited": favorited})
}

func categoriesHandler(w http.ResponseWriter, r *http.Request) {
	// 从已上架秘技中动态聚合分类，与正式网关保持一致
	seen := make(map[string]struct{})
	cats := []string{}
	for _, a := range agents {
		if a.Status != "approved" || a.Category == "" {
			continue
		}
		if _, ok := seen[a.Category]; ok {
			continue
		}
		seen[a.Category] = struct{}{}
		cats = append(cats, a.Category)
	}
	// 保持相对稳定的展示顺序
	sort.Strings(cats)
	respondSuccess(w, cats)
}

func agentCredentialsHandler(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	if uid == "" {
		respondError(w, 2001, "未登录")
		return
	}
	// Path: /v1/agents/{id}/credentials
	path := strings.TrimPrefix(r.URL.Path, "/v1/agents/")
	path = strings.TrimSuffix(path, "/credentials")
	agentID := path

	mu.RLock()
	item, ok := agentMap[agentID]
	mu.RUnlock()
	if !ok {
		respondError(w, 3004, "秘技不存在")
		return
	}

	manifest, _ := item.Manifest()

	switch r.Method {
	case http.MethodGet:
		values := make(map[string]string)
		if manifest != nil {
			mu.RLock()
			for k, def := range manifest.Credentials {
				bucket := e2eCredentialBucket(uid, def, manifest, agentID)
				if stored := e2eCredentials[bucket]; stored != nil {
					if v := stored[k]; v != "" {
						values[k] = v
					}
				}
			}
			mu.RUnlock()
		}
		respondSuccess(w, map[string]interface{}{
			"agent_id":    agentID,
			"credentials": manifest.Credentials,
			"values":      values,
		})
	case http.MethodPost:
		var req struct {
			Values map[string]string `json:"values"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, 1001, "参数错误")
			return
		}
		mu.Lock()
		for k, v := range req.Values {
			var def model.CredentialDef
			if manifest != nil {
				def = manifest.Credentials[k]
			}
			bucket := e2eCredentialBucket(uid, def, manifest, agentID)
			if e2eCredentials[bucket] == nil {
				e2eCredentials[bucket] = make(map[string]string)
			}
			if v == "" {
				delete(e2eCredentials[bucket], k)
				continue
			}
			e2eCredentials[bucket][k] = v
		}
		mu.Unlock()
		respondSuccess(w, nil)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

// --- User Space & Developer ---

func userSpaceHandler(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	if uid == "" {
		respondError(w, 2001, "未登录")
		return
	}
	mu.RLock()
	user := users[uid]
	var created, purchased []*AgentItem
	for _, a := range agents {
		if a.CreatorID == uid {
			created = append(created, a)
		}
		if purchases[uid] != nil && purchases[uid][a.ID] {
			purchased = append(purchased, a)
		}
	}
	devAcc := devAccounts[uid]
	if devAcc == nil {
		devAcc = &DeveloperAccount{UserID: uid}
	}
	mu.RUnlock()

	name := ""
	if user != nil {
		name = user.Nickname
	}
	mu.RLock()
	balance := balances[uid]
	elegantBalance := elegantBalances[uid]
	totalRecharged := int64(0)
	if user != nil {
		totalRecharged = user.TotalRecharged
	}
	mu.RUnlock()
	respondSuccess(w, UserSpace{
		UserID:           uid,
		UserName:         name,
		Balance:          balance,
		ElegantBalance:   elegantBalance,
		TotalRecharged:   totalRecharged,
		CreatedAgents:    created,
		PurchasedAgents:  purchased,
		DeveloperAccount: devAcc,
	})
}

func capabilitiesHandler(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	if uid == "" {
		respondError(w, 2001, "未登录")
		return
	}

	// Agent 市场已对所有登录用户开放
	mu.RLock()
	user := users[uid]
	mu.RUnlock()
	if user == nil {
		respondError(w, 2001, "用户不存在")
		return
	}

	enabled := true
	reason := ""

	// 根据 VIP 状态返回订阅等级与折扣权益
	tier := "free"
	discountPercent := 100
	if user.IsVIP && user.VIPExpireAt > time.Now().Unix() {
		tier = fmt.Sprintf("vip%d", user.VIPLevel)
		for _, p := range vipPlans {
			if p.Level == user.VIPLevel {
				discountPercent = p.DiscountPercent
				break
			}
		}
	}

	fileTools := false
	if user.IsVIP && user.VIPExpireAt > time.Now().Unix() {
		for _, p := range vipPlans {
			if p.Level == user.VIPLevel {
				fileTools = p.FileToolsEnabled
				break
			}
		}
	}
	if user.Role == "admin" {
		fileTools = true
	}

	respondSuccess(w, map[string]interface{}{
		"agent_market": map[string]interface{}{
			"enabled": enabled,
			"reason":  reason,
		},
		"subscription": map[string]interface{}{
			"tier": tier,
		},
		"features": map[string]interface{}{
			"cloud_sync":       false,
			"developer_mode":   user.Role == "admin",
			"file_tools":       fileTools,
			"vip_discount_pct": discountPercent,
		},
		"modules": func() []map[string]interface{} {
			modulesMu.RLock()
			defer modulesMu.RUnlock()
			out := make([]map[string]interface{}, 0, len(e2eModules))
			for _, m := range e2eModules {
				out = append(out, map[string]interface{}{
					"module_id": m.ID,
					"online":    m.Online,
					"version":   m.Version,
				})
			}
			return out
		}(),
	})
}

func developerAccountHandler(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	if uid == "" {
		respondError(w, 2001, "未登录")
		return
	}
	mu.RLock()
	devAcc := devAccounts[uid]
	mu.RUnlock()
	if devAcc == nil {
		devAcc = &DeveloperAccount{UserID: uid}
	}
	respondSuccess(w, devAcc)
}

// --- Withdrawals ---

func applyWithdrawalHandler(w http.ResponseWriter, r *http.Request) {
	defer saveData()
	uid := userIDFrom(r)
	if uid == "" {
		respondError(w, 2001, "未登录")
		return
	}
	var req struct {
		Amount      int    `json:"amount"`
		Channel     string `json:"channel"`
		AccountInfo string `json:"account_info"`
		RealName    string `json:"real_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误")
		return
	}
	if req.Amount < 1000 {
		respondError(w, 3001, "最小提现金额为 10.00 元")
		return
	}

	mu.Lock()
	defer mu.Unlock()

	devAcc := devAccounts[uid]
	if devAcc == nil {
		devAcc = &DeveloperAccount{UserID: uid}
		devAccounts[uid] = devAcc
	}
	if devAcc.ElegantBalance < int64(req.Amount) {
		respondError(w, 3001, "优雅弹丸余额不足")
		return
	}
	devAcc.ElegantBalance -= int64(req.Amount)

	user := users[uid]
	name := ""
	if user != nil {
		name = user.Nickname
	}
	record := &WithdrawalRecord{
		ID:          newUUID(),
		UserID:      uid,
		UserName:    name,
		Amount:      int64(req.Amount),
		Channel:     req.Channel,
		AccountInfo: req.AccountInfo,
		RealName:    req.RealName,
		Status:      "pending",
		CreatedAt:   time.Now().Unix(),
	}
	withdrawals = append(withdrawals, record)
	respondSuccess(w, record)
}

func listMyWithdrawalsHandler(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	if uid == "" {
		respondError(w, 2001, "未登录")
		return
	}
	mu.RLock()
	var items []*WithdrawalRecord
	for _, wv := range withdrawals {
		if wv.UserID == uid {
			items = append(items, wv)
		}
	}
	mu.RUnlock()
	respondSuccess(w, map[string]interface{}{
		"total": int64(len(items)),
		"items": items,
	})
}

// --- Sync ---

func syncPushHandler(w http.ResponseWriter, r *http.Request) {
	defer saveData()
	uid := userIDFrom(r)
	if uid == "" {
		respondError(w, 2001, "未登录")
		return
	}
	var req struct {
		Records []struct {
			EntityType        string `json:"entity_type"`
			EntityID          string `json:"entity_id"`
			Operation         string `json:"operation"`
			SyncVersion       int64  `json:"sync_version"`
			PayloadCiphertext string `json:"payload_ciphertext"`
		} `json:"records"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	mu.Lock()
	defer mu.Unlock()
	for _, rec := range req.Records {
		syncStore[uid] = append(syncStore[uid], &SyncRecord{
			ID:                newUUID(),
			EntityType:        rec.EntityType,
			EntityID:          rec.EntityID,
			Operation:         rec.Operation,
			SyncVersion:       rec.SyncVersion,
			PayloadCiphertext: rec.PayloadCiphertext,
			CreatedAt:         time.Now().Unix(),
		})
		if rec.SyncVersion > nextSyncVer[uid] {
			nextSyncVer[uid] = rec.SyncVersion
		}
	}
	respondSuccess(w, nil)
}

func syncPullHandler(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	if uid == "" {
		respondError(w, 2001, "未登录")
		return
	}
	var req struct {
		MinVersion int64 `json:"min_version"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	mu.RLock()
	var records []*SyncRecord
	for _, rec := range syncStore[uid] {
		if rec.SyncVersion > req.MinVersion {
			records = append(records, rec)
		}
	}
	maxVer := nextSyncVer[uid]
	mu.RUnlock()

	respondSuccess(w, map[string]interface{}{
		"records":     records,
		"max_version": maxVer,
	})
}

// ============================================================================
//  Conversations（对话历史服务端存储，E2E 内存实现）
// ============================================================================

type E2EChatConversation struct {
	ID              string            `json:"id"`
	UserID          string            `json:"user_id"`
	Title           string            `json:"title"`
	Model           string            `json:"model"`
	Provider        string            `json:"provider"`
	Status          string            `json:"status"`
	EnableTools     bool              `json:"enable_tools"`
	EnableWebSearch bool              `json:"enable_web_search"`
	SearchProvider  string            `json:"search_provider"`
	TeamID          string            `json:"team_id,omitempty"`
	CreatedAt       int64             `json:"created_at"`
	UpdatedAt       int64             `json:"updated_at"`
	Messages        []*E2EChatMessage `json:"messages,omitempty"`
}

type E2EChatMessage struct {
	ID               string `json:"id"`
	ConversationID   string `json:"conversation_id"`
	Role             string `json:"role"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
	ClientMessageID  string `json:"client_message_id,omitempty"`
	CreatedAt        int64  `json:"created_at"`
}

var (
	e2eConversations = make(map[string]*E2EChatConversation)
	e2eMessages      = make(map[string][]*E2EChatMessage)
)

func e2eListConversationsHandler(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 1 {
		pageSize = 20
	}

	var items []*E2EChatConversation
	teamID := r.URL.Query().Get("team_id")
	for _, conv := range e2eConversations {
		if conv.UserID == uid && (teamID == "" || conv.TeamID == teamID) {
			items = append(items, conv)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt > items[j].UpdatedAt
	})

	total := len(items)
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	respondSuccess(w, map[string]interface{}{
		"total": total,
		"items": items[start:end],
	})
}

func e2eCreateConversationHandler(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	var req struct {
		Title           string `json:"title"`
		Model           string `json:"model"`
		Provider        string `json:"provider"`
		EnableTools     bool   `json:"enable_tools"`
		EnableWebSearch bool   `json:"enable_web_search"`
		SearchProvider  string `json:"search_provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误")
		return
	}
	searchProvider := req.SearchProvider
	if searchProvider == "" {
		searchProvider = "baidu"
	}
	now := time.Now().Unix()
	conv := &E2EChatConversation{
		ID:              newUUID(),
		UserID:          uid,
		Title:           req.Title,
		Model:           req.Model,
		Provider:        req.Provider,
		Status:          "active",
		EnableTools:     req.EnableTools,
		EnableWebSearch: req.EnableWebSearch,
		SearchProvider:  searchProvider,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	e2eConversations[conv.ID] = conv
	e2eMessages[conv.ID] = []*E2EChatMessage{}
	respondSuccess(w, conv)
}

func e2eGetConversationHandler(w http.ResponseWriter, r *http.Request, id string) {
	uid := userIDFrom(r)
	conv, ok := e2eConversations[id]
	if !ok || conv.UserID != uid {
		respondError(w, 4004, "对话不存在")
		return
	}
	conv.Messages = e2eMessages[id]
	respondSuccess(w, conv)
}

func e2eUpdateConversationHandler(w http.ResponseWriter, r *http.Request, id string) {
	uid := userIDFrom(r)
	conv, ok := e2eConversations[id]
	if !ok || conv.UserID != uid {
		respondError(w, 4004, "对话不存在")
		return
	}
	var req struct {
		Title           *string `json:"title,omitempty"`
		EnableTools     *bool   `json:"enable_tools,omitempty"`
		EnableWebSearch *bool   `json:"enable_web_search,omitempty"`
		SearchProvider  *string `json:"search_provider,omitempty"`
		Model           *string `json:"model,omitempty"`
		Provider        *string `json:"provider,omitempty"`
		TeamID          *string `json:"team_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误")
		return
	}
	if req.TeamID != nil && *req.TeamID != "" {
		// 归组：校验分组存在且归属当前用户（空字符串 = 移出分组）
		team, ok := e2eTeams[*req.TeamID]
		if !ok {
			respondError(w, 3001, "组不存在")
			return
		}
		if team.UserID != uid {
			respondError(w, 3001, "无权访问该分组")
			return
		}
	}
	if req.Title != nil {
		conv.Title = *req.Title
	}
	if req.EnableTools != nil {
		conv.EnableTools = *req.EnableTools
	}
	if req.EnableWebSearch != nil {
		conv.EnableWebSearch = *req.EnableWebSearch
	}
	if req.SearchProvider != nil && *req.SearchProvider != "" {
		conv.SearchProvider = *req.SearchProvider
	}
	if req.Model != nil {
		conv.Model = *req.Model
	}
	if req.Provider != nil {
		conv.Provider = *req.Provider
	}
	if req.TeamID != nil {
		conv.TeamID = *req.TeamID
	}
	conv.UpdatedAt = time.Now().Unix()
	respondSuccess(w, nil)
}

func e2eDeleteConversationHandler(w http.ResponseWriter, r *http.Request, id string) {
	uid := userIDFrom(r)
	conv, ok := e2eConversations[id]
	if !ok || conv.UserID != uid {
		respondError(w, 4004, "对话不存在")
		return
	}
	delete(e2eConversations, id)
	delete(e2eMessages, id)
	respondSuccess(w, nil)
}

func e2eListMessagesHandler(w http.ResponseWriter, r *http.Request, id string) {
	uid := userIDFrom(r)
	conv, ok := e2eConversations[id]
	if !ok || conv.UserID != uid {
		respondError(w, 4004, "对话不存在")
		return
	}
	respondSuccess(w, map[string]interface{}{
		"total": len(e2eMessages[id]),
		"items": e2eMessages[id],
	})
}

func e2eCreateMessageHandler(w http.ResponseWriter, r *http.Request, id string) {
	uid := userIDFrom(r)
	conv, ok := e2eConversations[id]
	if !ok || conv.UserID != uid {
		respondError(w, 4004, "对话不存在")
		return
	}
	var msg E2EChatMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		respondError(w, 1001, "参数错误")
		return
	}
	msg.ID = newUUID()
	msg.ConversationID = id
	if msg.CreatedAt == 0 {
		msg.CreatedAt = time.Now().Unix()
	}
	e2eMessages[id] = append(e2eMessages[id], &msg)
	conv.UpdatedAt = msg.CreatedAt

	updatedTitle := ""
	if conv.Title == "新对话" && msg.Role == "user" && msg.Content != "" {
		updatedTitle = e2eGenerateTitle(msg.Content)
		conv.Title = updatedTitle
	}

	respondSuccess(w, map[string]interface{}{
		"message": msg,
		"title":   updatedTitle,
	})
}

// e2eGenerateTitle 根据用户消息内容生成对话标题（E2E 简单实现）
func e2eGenerateTitle(content string) string {
	text := content
	var parts []map[string]interface{}
	if err := json.Unmarshal([]byte(content), &parts); err == nil {
		var sb strings.Builder
		for _, p := range parts {
			if t, ok := p["type"].(string); ok && t == "text" {
				if txt, ok := p["text"].(string); ok {
					sb.WriteString(txt)
				}
			}
		}
		if sb.Len() > 0 {
			text = sb.String()
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > 20 {
		return string(runes[:20]) + "…"
	}
	return text
}

// ============================================================================
//  Teams（对话分组，E2E 内存实现）
// ============================================================================

// E2ETeam 对话分组（与云端 model.Team 字段对齐）
type E2ETeam struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

var e2eTeams = make(map[string]*E2ETeam)

// 组共享记忆内存存储（Agent Team P2）：memoryID -> E2ETeamMemory
var e2eTeamMemories = make(map[string]*E2ETeamMemory)

// e2eGetOwnedTeam 查询分组并校验归属（不存在与非本人统一返回 nil）
func e2eGetOwnedTeam(uid, id string) *E2ETeam {
	t := e2eTeams[id]
	if t == nil || t.UserID != uid {
		return nil
	}
	return t
}

// e2eListTeamsHandler 分组列表（含 conversation_count 统计）
func e2eListTeamsHandler(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	var items []map[string]interface{}
	for _, team := range e2eTeams {
		if team.UserID != uid {
			continue
		}
		var convCount int64
		for _, conv := range e2eConversations {
			if conv.TeamID == team.ID {
				convCount++
			}
		}
		items = append(items, map[string]interface{}{
			"id":                 team.ID,
			"user_id":            team.UserID,
			"name":               team.Name,
			"description":        team.Description,
			"created_at":         team.CreatedAt,
			"updated_at":         team.UpdatedAt,
			"conversation_count": convCount,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i]["created_at"].(int64) > items[j]["created_at"].(int64)
	})
	if items == nil {
		items = []map[string]interface{}{}
	}
	respondSuccess(w, items)
}

// e2eCreateTeamHandler 创建分组
func e2eCreateTeamHandler(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		respondError(w, 1001, "参数错误: name 必填")
		return
	}
	now := time.Now().Unix()
	team := &E2ETeam{
		ID:          newUUID(),
		UserID:      uid,
		Name:        req.Name,
		Description: req.Description,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	e2eTeams[team.ID] = team
	respondSuccess(w, team)
}

// e2eGetTeamHandler 分组详情（含组内对话摘要列表）
func e2eGetTeamHandler(w http.ResponseWriter, r *http.Request, id string) {
	uid := userIDFrom(r)
	team, ok := e2eTeams[id]
	if !ok || team.UserID != uid {
		respondError(w, 3001, "组不存在")
		return
	}
	convs := []map[string]interface{}{}
	for _, conv := range e2eConversations {
		if conv.TeamID == id {
			convs = append(convs, map[string]interface{}{
				"id":         conv.ID,
				"title":      conv.Title,
				"model":      conv.Model,
				"updated_at": conv.UpdatedAt,
			})
		}
	}
	sort.Slice(convs, func(i, j int) bool {
		return convs[i]["updated_at"].(int64) > convs[j]["updated_at"].(int64)
	})
	respondSuccess(w, map[string]interface{}{
		"id":            team.ID,
		"user_id":       team.UserID,
		"name":          team.Name,
		"description":   team.Description,
		"created_at":    team.CreatedAt,
		"updated_at":    team.UpdatedAt,
		"conversations": convs,
	})
}

// e2eUpdateTeamHandler 更新分组名称/描述
func e2eUpdateTeamHandler(w http.ResponseWriter, r *http.Request, id string) {
	uid := userIDFrom(r)
	team, ok := e2eTeams[id]
	if !ok || team.UserID != uid {
		respondError(w, 3001, "组不存在")
		return
	}
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误")
		return
	}
	if req.Name != nil {
		if *req.Name == "" {
			respondError(w, 3001, "组名称不能为空")
			return
		}
		team.Name = *req.Name
	}
	if req.Description != nil {
		team.Description = *req.Description
	}
	team.UpdatedAt = time.Now().Unix()
	respondSuccess(w, team)
}

// e2eDeleteTeamHandler 删除分组：清 conversations.team_id，不删对话
func e2eDeleteTeamHandler(w http.ResponseWriter, r *http.Request, id string) {
	uid := userIDFrom(r)
	team, ok := e2eTeams[id]
	if !ok || team.UserID != uid {
		respondError(w, 3001, "组不存在")
		return
	}
	for _, conv := range e2eConversations {
		if conv.TeamID == id {
			conv.TeamID = ""
		}
	}
	delete(e2eTeams, id)
	// 级联删除组共享记忆（Agent Team P2）
	for mid, m := range e2eTeamMemories {
		if m.TeamID == id {
			delete(e2eTeamMemories, mid)
		}
	}
	respondSuccess(w, nil)
}

// ============================================================================
//  组共享记忆（TeamMemory，Agent Team P2，E2E 内存实现，与云端 /v1/teams/:id/memories* 对齐）
// ============================================================================

// E2ETeamMemory 组共享记忆条目（scope = user + team）
type E2ETeamMemory struct {
	ID                   string `json:"id"`
	TeamID               string `json:"team_id"`
	UserID               string `json:"user_id"`
	Content              string `json:"content"`
	Tags                 string `json:"tags,omitempty"`
	SourceConversationID string `json:"source_conversation_id,omitempty"`
	CreatedAt            int64  `json:"created_at"`
	UpdatedAt            int64  `json:"updated_at"`
}

// e2eListTeamMemoriesHandler 组记忆列表（分页 page/page_size，按 created_at 倒序）
func e2eListTeamMemoriesHandler(w http.ResponseWriter, r *http.Request, teamID string) {
	uid := userIDFrom(r)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	mu.RLock()
	defer mu.RUnlock()
	if e2eGetOwnedTeam(uid, teamID) == nil {
		respondError(w, 3001, "组不存在")
		return
	}
	items := make([]*E2ETeamMemory, 0)
	for _, m := range e2eTeamMemories {
		if m.TeamID == teamID {
			items = append(items, m)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt > items[j].CreatedAt })
	total := len(items)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	respondSuccess(w, map[string]interface{}{
		"items": items[start:end],
		"total": total,
	})
}

// e2eCreateTeamMemoryHandler 手动新增组记忆（content 必填，≤500 字）
func e2eCreateTeamMemoryHandler(w http.ResponseWriter, r *http.Request, teamID string) {
	uid := userIDFrom(r)
	var req struct {
		Content string `json:"content"`
		Tags    string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Content) == "" {
		respondError(w, 1001, "参数错误: content 必填")
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if e2eGetOwnedTeam(uid, teamID) == nil {
		respondError(w, 3001, "组不存在")
		return
	}
	if len([]rune(strings.TrimSpace(req.Content))) > 500 {
		respondError(w, 3001, "记忆内容不能超过 500 字")
		return
	}
	now := time.Now().Unix()
	m := &E2ETeamMemory{
		ID:        fmt.Sprintf("tm-%s", newUUID()[:8]),
		TeamID:    teamID,
		UserID:    uid,
		Content:   strings.TrimSpace(req.Content),
		Tags:      strings.TrimSpace(req.Tags),
		CreatedAt: now,
		UpdatedAt: now,
	}
	e2eTeamMemories[m.ID] = m
	respondSuccess(w, m)
}

// e2eDeleteTeamMemoryHandler 删除组记忆条目（校验组归属，且条目属于该组）
func e2eDeleteTeamMemoryHandler(w http.ResponseWriter, r *http.Request, teamID, memoryID string) {
	uid := userIDFrom(r)
	mu.Lock()
	defer mu.Unlock()
	if e2eGetOwnedTeam(uid, teamID) == nil {
		respondError(w, 3001, "组不存在")
		return
	}
	m := e2eTeamMemories[memoryID]
	if m == nil || m.TeamID != teamID {
		respondError(w, 3001, "记忆不存在")
		return
	}
	delete(e2eTeamMemories, memoryID)
	respondSuccess(w, nil)
}

// ============================================================================
//  Agent 工作流（E2E 内存实现）
// ============================================================================

func e2eAgentExecuteHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Message         string  `json:"message"`
		EnableTools     *bool   `json:"enable_tools,omitempty"`
		EnableWebSearch *bool   `json:"enable_web_search,omitempty"`
		SearchProvider  *string `json:"search_provider,omitempty"`
		ConversationID  string  `json:"conversation_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}

	writeEvent := func(event string, data interface{}) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(b))
		flusher.Flush()
	}

	enableTools := req.EnableTools != nil && *req.EnableTools
	if !enableTools {
		writeEvent("final_answer", map[string]string{"delta": "这是普通对话回答：" + req.Message})
		writeEvent("done", map[string]interface{}{})
		return
	}

	sessionID := newUUID()
	// Agent Team P3：云端 agent_sessions 已加 parent_session_id（CallAssistant 子 session provenance）；
	// e2e 不持久化 agent_sessions（/v1/agent/sessions 返回空列表），CallAssistant 由真实 LLM 行为驱动，
	// e2e 不模拟委派闭环，故此处无字段需镜像。

	enableWebSearch := req.EnableWebSearch != nil && *req.EnableWebSearch
	searchProvider := "baidu"
	if req.SearchProvider != nil && *req.SearchProvider != "" {
		searchProvider = *req.SearchProvider
	}

	// 模拟 Agent 工具调用：仅当启用联网时才模拟 SearchWeb
	if enableWebSearch {
		writeEvent("tool_call", map[string]interface{}{
			"step":      1,
			"tool":      "SearchWeb",
			"arguments": map[string]string{"query": req.Message, "search_provider": searchProvider},
		})
		writeEvent("tool_result", map[string]interface{}{
			"step":          1,
			"tool":          "SearchWeb",
			"status":        "succeeded",
			"output":        map[string]interface{}{"results": []string{"E2E 搜索结果（源：" + searchProvider + "）"}},
			"error_message": "",
		})
	}
	// 模拟已购买的 Agent-Reach 动态工具调用
	uid := userIDFrom(r)
	mu.RLock()
	var dynamicTools []struct {
		AgentID string
		Name    string
		Action  string
	}
	for _, ut := range agentUserTools {
		if ut.UserID != uid {
			continue
		}
		agent := agentMap[ut.AgentID]
		if agent == nil || agent.ManifestJSON == "" {
			continue
		}
		var mf struct {
			ID       string            `json:"id"`
			Name     string            `json:"name"`
			Driver   string            `json:"driver"`
			Actions  []struct {
				Name string `json:"name"`
			} `json:"actions"`
			Metadata map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal([]byte(agent.ManifestJSON), &mf); err != nil || len(mf.Actions) == 0 {
			continue
		}
		if !e2eIsExecutableDriver(mf.Driver) {
			continue
		}
		// 模块离线时，E2E 环境下也跳过该工具调用，与生产网关行为对齐
		if !e2eModuleOnlineFromManifest(agent.ManifestJSON) {
			continue
		}
		toolName := mf.Name
		if toolName == "" {
			toolName = mf.ID
		}
		if toolName == "" {
			toolName = ut.ToolName
		}
		dynamicTools = append(dynamicTools, struct {
			AgentID string
			Name    string
			Action  string
		}{AgentID: agent.ID, Name: toolName, Action: mf.Actions[0].Name})
	}
	mu.RUnlock()

	step := 1
	if enableWebSearch {
		step = 2
	}
	for _, dt := range dynamicTools {
		writeEvent("tool_call", map[string]interface{}{
			"step":      step,
			"tool":      dt.Name,
			"arguments": map[string]string{"query": req.Message, "action": dt.Action},
		})
		writeEvent("tool_result", map[string]interface{}{
			"step":          step,
			"tool":          dt.Name,
			"status":        "succeeded",
			"output":        map[string]interface{}{"e2e": "E2E 模拟 " + dt.Action + " 结果", "query": req.Message},
			"error_message": "",
		})
		step++
	}

	writeEvent("reasoning", map[string]string{"delta": "这是 E2E 模拟思考过程"})
	writeEvent("final_answer", map[string]string{"delta": "这是 Agent 工作流回答：" + req.Message})
	writeEvent("done", map[string]interface{}{"session_id": sessionID})
}

// ============================================================================
//  视觉生成（图片/视频）E2E 模拟
// ============================================================================

func e2eVisualCreateHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MediaType      string                 `json:"media_type"`
		Provider       string                 `json:"provider"`
		Model          string                 `json:"model"`
		Prompt         string                 `json:"prompt"`
		ConversationID string                 `json:"conversation_id"`
		ImageURL       string                 `json:"image_url"`
		Params         map[string]interface{} `json:"params"`
		Currency       string                 `json:"currency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误")
		return
	}

	uid := userIDFrom(r)
	mu.Lock()
	defer mu.Unlock()

	// 确保有会话
	convID := req.ConversationID
	if convID == "" || visualConversations[convID] == nil || visualConversations[convID].UserID != uid {
		convID = e2eEnsureVisualConversation(uid, req.Prompt, req.MediaType)
	}

	visualTaskCounter++
	taskID := fmt.Sprintf("vg-e2e-%d", visualTaskCounter)
	now := time.Now().Unix()

	task := &E2EVisualTask{
		ID:             taskID,
		UserID:         uid,
		ConversationID: convID,
		MediaType:      req.MediaType,
		Provider:  req.Provider,
		Model:     req.Model,
		Prompt:    req.Prompt,
		ImageURL:  req.ImageURL,
		Params:    req.Params,
		Currency:  req.Currency,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if req.MediaType == "image" {
		task.Status = "succeeded"
		task.ResultURL = "https://placehold.co/1024x1024/6750A4/FFFFFF/png?text=E2E+Image"
		completedAt := now
		task.CompletedAt = &completedAt
	} else {
		task.Status = "pending"
		task.Progress = 0
	}

	visualTasks[taskID] = task
	respondSuccess(w, e2eVisualTaskToResponse(task))
}

func e2eVisualGetHandler(w http.ResponseWriter, r *http.Request, id string) {
	mu.Lock()
	defer mu.Unlock()

	task, ok := visualTasks[id]
	if !ok || task.UserID != userIDFrom(r) {
		respondError(w, 4004, "任务不存在")
		return
	}

	// 模拟视频任务状态推进
	if task.MediaType == "video" && task.Status == "pending" {
		now := time.Now().Unix()
		if now-task.UpdatedAt > 2 {
			task.Status = "running"
			task.Progress = 50
			task.UpdatedAt = now
		}
	}
	if task.MediaType == "video" && task.Status == "running" {
		now := time.Now().Unix()
		if now-task.UpdatedAt > 2 {
			task.Status = "succeeded"
			task.Progress = 100
			task.ResultURL = "https://placehold.co/1280x720/6750A4/FFFFFF/mp4?text=E2E+Video"
			task.UpdatedAt = now
			completedAt := now
			task.CompletedAt = &completedAt
		}
	}

	respondSuccess(w, e2eVisualTaskToResponse(task))
}

func e2eVisualCancelHandler(w http.ResponseWriter, r *http.Request, id string) {
	mu.Lock()
	defer mu.Unlock()

	task, ok := visualTasks[id]
	if !ok || task.UserID != userIDFrom(r) {
		respondError(w, 4004, "任务不存在")
		return
	}

	if task.Status != "pending" && task.Status != "running" {
		respondError(w, 4001, "任务不在可取消状态")
		return
	}

	task.Status = "cancelled"
	task.UpdatedAt = time.Now().Unix()
	respondSuccess(w, e2eVisualTaskToResponse(task))
}

func e2eVisualTaskToResponse(task *E2EVisualTask) map[string]interface{} {
	result := map[string]interface{}{}
	if task.ResultURL != "" {
		result["url"] = task.ResultURL
	}
	return map[string]interface{}{
		"id":              task.ID,
		"conversation_id": task.ConversationID,
		"media_type":      task.MediaType,
		"provider":       task.Provider,
		"model":          task.Model,
		"status":         task.Status,
		"prompt":         task.Prompt,
		"params":         task.Params,
		"result":         result,
		"error_message":  task.ErrorMessage,
		"progress":       task.Progress,
		"cost":           task.Cost,
		"currency":       task.Currency,
		"created_at":     task.CreatedAt,
		"updated_at":     task.UpdatedAt,
		"completed_at":   task.CompletedAt,
	}
}

func e2eEnsureVisualConversation(userID, prompt, mediaType string) string {
	id := fmt.Sprintf("vc-e2e-%d", time.Now().UnixNano())
	title := prompt
	if len(title) > 20 {
		title = title[:20] + "..."
	}
	if title == "" {
		title = "未命名视觉创作"
	}
	if mediaType == "" {
		mediaType = "image"
	}
	now := time.Now().Unix()
	conv := &E2EVisualConversation{
		ID:        id,
		UserID:    userID,
		Title:     title,
		MediaType: mediaType,
		Status:    "active",
		CreatedAt: now,
		UpdatedAt: now,
	}
	visualConversations[id] = conv
	visualConversationsByUser[userID] = append(visualConversationsByUser[userID], id)
	return id
}

func e2eVisualCreateConversationHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title     string `json:"title"`
		MediaType string `json:"media_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误")
		return
	}
	uid := userIDFrom(r)
	mu.Lock()
	defer mu.Unlock()
	id := e2eEnsureVisualConversation(uid, req.Title, req.MediaType)
	respondSuccess(w, visualConversations[id])
}

func e2eVisualListConversationsHandler(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	mediaType := r.URL.Query().Get("media_type")
	mu.Lock()
	defer mu.Unlock()
	var items []*E2EVisualConversation
	for _, id := range visualConversationsByUser[uid] {
		conv := visualConversations[id]
		if conv == nil || conv.Status == "deleted" {
			continue
		}
		if mediaType != "" && conv.MediaType != mediaType {
			continue
		}
		items = append(items, conv)
	}
	// 按 UpdatedAt 倒序
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	respondSuccess(w, map[string]interface{}{"items": items, "total": len(items), "page": 1, "page_size": 20})
}

func e2eVisualGetConversationHandler(w http.ResponseWriter, r *http.Request, id string) {
	uid := userIDFrom(r)
	mu.Lock()
	defer mu.Unlock()
	conv := visualConversations[id]
	if conv == nil || conv.UserID != uid || conv.Status == "deleted" {
		respondError(w, 1000, "会话不存在")
		return
	}
	var tasks []*E2EVisualTask
	for _, t := range visualTasks {
		if t.ConversationID == id && t.UserID == uid {
			// E2E 模拟：视频任务 10 秒后自动成功，便于测试轮询逻辑
			if t.MediaType == "video" && (t.Status == "pending" || t.Status == "running") && time.Since(time.Unix(t.CreatedAt, 0)) > 10*time.Second {
				t.Status = "succeeded"
				t.ResultURL = "https://placehold.co/1280x720/6750A4/FFFFFF/mp4?text=E2E+Video"
				t.Progress = 100
				now := time.Now().Unix()
				t.CompletedAt = &now
				t.UpdatedAt = now
			}
			tasks = append(tasks, t)
		}
	}
	respondSuccess(w, map[string]interface{}{"conversation": conv, "tasks": tasks})
}

func e2eVisualUpdateConversationHandler(w http.ResponseWriter, r *http.Request, id string) {
	uid := userIDFrom(r)
	var req struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误")
		return
	}
	mu.Lock()
	defer mu.Unlock()
	conv := visualConversations[id]
	if conv == nil || conv.UserID != uid {
		respondError(w, 1000, "会话不存在")
		return
	}
	conv.Title = req.Title
	conv.UpdatedAt = time.Now().Unix()
	respondSuccess(w, map[string]interface{}{})
}

func e2eVisualDeleteConversationHandler(w http.ResponseWriter, r *http.Request, id string) {
	uid := userIDFrom(r)
	mu.Lock()
	defer mu.Unlock()
	conv := visualConversations[id]
	if conv == nil || conv.UserID != uid {
		respondError(w, 1000, "会话不存在")
		return
	}
	conv.Status = "deleted"
	respondSuccess(w, map[string]interface{}{})
}

func e2eVisualUploadHandler(w http.ResponseWriter, r *http.Request) {
	file, header, err := r.FormFile("file")
	if err != nil {
		respondError(w, 1001, "读取上传文件失败")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, 10*1024*1024))
	if err != nil {
		respondError(w, 1001, "读取文件内容失败")
		return
	}

	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	ext := ".bin"
	switch contentType {
	case "image/png":
		ext = ".png"
	case "image/jpeg":
		ext = ".jpg"
	case "image/webp":
		ext = ".webp"
	}

	id := fmt.Sprintf("%d-%s%s", time.Now().UnixNano(), randomString(8), ext)
	mu.Lock()
	visualUploads[id] = data
	visualUploadContentTypes[id] = contentType
	mu.Unlock()

	url := fmt.Sprintf("%s://%s/v1/visual/files/%s", "http", r.Host, id)
	respondSuccess(w, map[string]interface{}{
		"id":        id,
		"url":       url,
		"mime_type": contentType,
	})
}

func e2eVisualFileHandler(w http.ResponseWriter, r *http.Request, id string) {
	mu.Lock()
	data, ok := visualUploads[id]
	contentType := visualUploadContentTypes[id]
	mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Write(data)
}

// ============================================================================
//  Admin Web 静态文件
// ============================================================================

func staticFileHandler(w http.ResponseWriter, r *http.Request) {
	// 以可执行文件所在目录为基准定位 admin-web 构建产物
	execPath, err := os.Executable()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	execDir := filepath.Dir(execPath)
	// 兼容两种启动方式：从项目根目录直接运行，或从 gateway/cmd/e2e-server 运行
	baseDir := filepath.Join(execDir, "..", "..", "..", "admin-web", "dist")
	if _, err := os.Stat(baseDir); os.IsNotExist(err) {
		baseDir = filepath.Join(execDir, "..", "..", "admin-web", "dist")
	}
	requestPath := r.URL.Path
	// 去掉 /admin 前缀，映射到 dist 目录
	if strings.HasPrefix(requestPath, "/admin/") {
		requestPath = strings.TrimPrefix(requestPath, "/admin/")
	}
	path := filepath.Join(baseDir, requestPath)
	if requestPath == "" || requestPath == "/" {
		path = filepath.Join(baseDir, "index.html")
	}
	// 如果文件不存在，回退到 index.html（支持前端路由）
	if _, err := os.Stat(path); os.IsNotExist(err) {
		path = filepath.Join(baseDir, "index.html")
	}
	http.ServeFile(w, r, path)
}

// ============================================================================
//  辅助函数
// ============================================================================

func hashPassword(pwd string) string {
	h := sha256.New()
	h.Write([]byte(pwd + "eleball-salt"))
	return hex.EncodeToString(h.Sum(nil))
}

// ============================================================================
//  Ele Agent 模型配置（管理员后台维护）
// ============================================================================

type EleAgentModelConfig struct {
	ID                string `json:"id"`
	Provider          string `json:"provider"`
	Protocol          string `json:"protocol"` // 上游协议：openai_compatible / anthropic_messages
	ModelName         string `json:"model_name"`
	DisplayName       string `json:"display_name"`
	BaseURL           string `json:"base_url"`
	APIKey            string `json:"api_key"`
	IsEnabled         bool   `json:"is_enabled"`
	SupportsChat              bool   `json:"supports_chat"`
	SupportsVision    bool   `json:"supports_vision"`
	SupportsImage             bool   `json:"supports_image"`
	SupportsVideo             bool   `json:"supports_video"`
	SupportsImageInput        bool   `json:"supports_image_input"`
	SupportsContinuousContext bool   `json:"supports_continuous_context"`
	SupportsTools             bool   `json:"supports_tools"`
	Priority          int    `json:"priority"`
	InputPricePerCall int64  `json:"input_price_per_call"`
	PricePerCall      int64  `json:"price_per_call"`
	PricePerGeneration int64 `json:"price_per_generation"`
	VideoMinDuration   int   `json:"video_min_duration"`
	VideoMaxDuration   int   `json:"video_max_duration"`
	VideoDurationStep  int   `json:"video_duration_step"`
	CreatedAt         int64  `json:"created_at"`
	UpdatedAt         int64  `json:"updated_at"`
}

var eleAgentConfigs = make(map[string]*EleAgentModelConfig)

// eleAgentUpstreamPath 根据协议返回上游 API 路径
// E2E 服务器目前仅做简单代理，Anthropic 协议转换由正式网关实现。
func eleAgentUpstreamPath(protocol string) string {
	if protocol == "anthropic_messages" {
		return "/messages"
	}
	return "/chat/completions"
}

// RechargePackage 充值套餐（E2E 内存存储）
type RechargePackage struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	Danwan             int64   `json:"danwan"`
	PriceFen           int64   `json:"price_fen"`
	SortOrder          int     `json:"sort_order"`
	IsEnabled          bool    `json:"is_enabled"`
	IsCustomMultiplier bool    `json:"is_custom_multiplier"`
	BasePackageID      *string `json:"base_package_id,omitempty"`
	Description        string  `json:"description"`
	CreatedAt          int64   `json:"created_at"`
	UpdatedAt          int64   `json:"updated_at"`
}

var rechargePackages = make(map[string]*RechargePackage)

func ensureDefaultEleAgentConfig() {
	// 如果已经有任何配置，仅补充缺失的视觉模型，不覆盖已有数据
	hasImage := false
	hasVideo := false
	for _, cfg := range eleAgentConfigs {
		if cfg.SupportsImage {
			hasImage = true
		}
		if cfg.SupportsVideo {
			hasVideo = true
		}
	}

	if len(eleAgentConfigs) == 0 {
		uid := newUUID()
		eleAgentConfigs[uid] = &EleAgentModelConfig{
			ID:                uid,
			Provider:          "qwen",
			Protocol:          "openai_compatible",
			ModelName:         "Qwen/Qwen3-8B",
			DisplayName:       "通义千问 Qwen3-8B",
			BaseURL:           "https://api.siliconflow.cn/v1",
			APIKey:            os.Getenv("QWEN_API_KEY"),
			IsEnabled:         true,
			SupportsChat:              true,
			SupportsVision:            false,
			SupportsImage:             false,
			SupportsVideo:             false,
			SupportsImageInput:        false,
			SupportsContinuousContext: false,
			SupportsTools:             true,
			Priority:          0,
			InputPricePerCall: 0,
			PricePerCall:      0,
			CreatedAt:         time.Now().Unix(),
			UpdatedAt:         time.Now().Unix(),
		}
	}

	// 为 E2E 视觉生成提供默认可选模型
	if !hasImage {
		imageUID := newUUID()
		eleAgentConfigs[imageUID] = &EleAgentModelConfig{
			ID:                imageUID,
			Provider:          "e2e-image",
			Protocol:          "openai_compatible",
			ModelName:         "e2e-image-v1",
			DisplayName:       "E2E 图片模型",
			BaseURL:           "",
			APIKey:            "",
			IsEnabled:         true,
			SupportsChat:              false,
			SupportsVision:            false,
			SupportsImage:             true,
			SupportsVideo:             false,
			SupportsImageInput:        true,
			SupportsContinuousContext: false,
			SupportsTools:             false,
			Priority:          1,
			InputPricePerCall: 0,
			PricePerCall:      0,
			CreatedAt:         time.Now().Unix(),
			UpdatedAt:         time.Now().Unix(),
		}
	}

	if !hasVideo {
		videoUID := newUUID()
		eleAgentConfigs[videoUID] = &EleAgentModelConfig{
			ID:                videoUID,
			Provider:          "e2e-video",
			Protocol:          "openai_compatible",
			ModelName:         "e2e-video-v1",
			DisplayName:       "E2E 视频模型",
			BaseURL:           "",
			APIKey:            "",
			IsEnabled:         true,
			SupportsChat:              false,
			SupportsVision:            false,
			SupportsImage:             false,
			SupportsVideo:             true,
			SupportsImageInput:        true,
			SupportsContinuousContext: false,
			SupportsTools:             false,
			Priority:          2,
			InputPricePerCall: 0,
			PricePerCall:      0,
			VideoMinDuration:  1,
			VideoMaxDuration:  10,
			VideoDurationStep: 1,
			CreatedAt:         time.Now().Unix(),
			UpdatedAt:         time.Now().Unix(),
		}
	}
}

func ensureDefaultRechargePackages() {
	if len(rechargePackages) > 0 {
		return
	}
	now := time.Now().Unix()
	packages := []struct {
		name               string
		danwan             int64
		priceFen           int64
		sortOrder          int
		description        string
		isCustomMultiplier bool
	}{
		{"小杯", 1000, 990, 10, "适合偶尔使用", false},
		{"中杯", 3000, 2880, 20, "适合日常办公", false},
		{"大杯", 5000, 4580, 30, "适合高频创作", false},
		{"超大杯", 10000, 8880, 40, "超值大包", false},
		{"重度依赖", 0, 0, 50, "自定义数量，购买多份超大杯", true},
	}
	var xlargeID string
	for i, p := range packages {
		uid := newUUID()
		pkg := &RechargePackage{
			ID:                 uid,
			Name:               p.name,
			Danwan:             p.danwan,
			PriceFen:           p.priceFen,
			SortOrder:          p.sortOrder,
			IsEnabled:          true,
			IsCustomMultiplier: p.isCustomMultiplier,
			Description:        p.description,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		rechargePackages[uid] = pkg
		if i == 3 {
			xlargeID = uid
		}
	}
	if xlargeID != "" {
		for _, pkg := range rechargePackages {
			if pkg.IsCustomMultiplier {
				pkg.BasePackageID = &xlargeID
			}
		}
	}
}

func ensureDefaultVIPPlans() {
	if len(vipPlans) > 0 {
		return
	}
	now := time.Now().Unix()
	plans := []*VIPPlan{
		{ID: newUUID(), Level: 1, Name: "强力弹丸", PriceFen: 4900, DurationDays: 30, DiscountPercent: 100, MaxConversations: 200, MaxAgentSessions: 100, AsrQuotaMonthly: 3000, AgentEnabled: true, FileToolsEnabled: true, SortOrder: 10, IsEnabled: true, Description: "解锁 Agent 模式与文件工具", CreatedAt: now, UpdatedAt: now},
		{ID: newUUID(), Level: 2, Name: "超级弹丸", PriceFen: 9900, DurationDays: 30, DiscountPercent: 80, MaxConversations: 500, MaxAgentSessions: 100, AsrQuotaMonthly: 10000, AgentEnabled: true, FileToolsEnabled: true, SortOrder: 20, IsEnabled: true, Description: "享 8 折模型调用折扣", CreatedAt: now, UpdatedAt: now},
	}
	vipPlans = append(vipPlans, plans...)
}

func listEleAgentOptions() []map[string]interface{} {
	mu.RLock()
	defer mu.RUnlock()
	var result []map[string]interface{}
	for _, cfg := range eleAgentConfigs {
		if !cfg.IsEnabled {
			continue
		}
		result = append(result, map[string]interface{}{
			"provider":                  cfg.Provider,
			"model_name":                cfg.ModelName,
			"display_name":              cfg.DisplayName,
			"protocol":                  cfg.Protocol,
			"supports_chat":             cfg.SupportsChat,
			"supports_vision":           cfg.SupportsVision,
			"supports_image":            cfg.SupportsImage,
			"supports_video":            cfg.SupportsVideo,
			"supports_image_input":      cfg.SupportsImageInput,
			"supports_continuous_context": cfg.SupportsContinuousContext,
			"supports_tools":            cfg.SupportsTools,
			"input_price_per_call":      cfg.InputPricePerCall,
			"price_per_call":            cfg.PricePerCall,
			"price_per_generation":      cfg.PricePerGeneration,
			"video_min_duration":        cfg.VideoMinDuration,
			"video_max_duration":        cfg.VideoMaxDuration,
			"video_duration_step":       cfg.VideoDurationStep,
		})
	}
	return result
}

func findEleAgentConfig(provider, modelName string) *EleAgentModelConfig {
	mu.RLock()
	defer mu.RUnlock()
	for _, cfg := range eleAgentConfigs {
		if cfg.Provider == provider && cfg.ModelName == modelName && cfg.IsEnabled {
			return cfg
		}
	}
	return nil
}

// findEleAgentConfigByFullModel 用完整模型名（如 qwen/Qwen/Qwen3-8B）匹配配置，
// 兼容管理员在后台把 model_name 填成完整格式的情况。
func findEleAgentConfigByFullModel(fullModel string) *EleAgentModelConfig {
	mu.RLock()
	defer mu.RUnlock()
	for _, cfg := range eleAgentConfigs {
		if !cfg.IsEnabled {
			continue
		}
		candidate := cfg.Provider + "/" + cfg.ModelName
		if candidate == fullModel {
			return cfg
		}
	}
	return nil
}

// ============================================================================
//  数据持久化（JSON 文件）— E2E 测试环境用，重启不丢数据
// ============================================================================

type StoreData struct {
	Users            map[string]*User                `json:"users"`
	UsernameIndex    map[string]string               `json:"username_index"`
	Balances         map[string]int64                `json:"balances"`
	Agents           []*AgentItem                    `json:"agents"`
	AgentMap         map[string]*AgentItem           `json:"agent_map"`
	Purchases        map[string]map[string]bool      `json:"purchases"`
	AgentUserTools   []*AgentUserTool                `json:"agent_user_tools"`
	Favorites        map[string]map[string]bool      `json:"favorites"`
	Reviews          []*AgentReview                  `json:"reviews"`
	DevAccounts      map[string]*DeveloperAccount    `json:"dev_accounts"`
	Withdrawals      []*WithdrawalRecord             `json:"withdrawals"`
	SyncStore        map[string][]*SyncRecord        `json:"sync_store"`
	NextSyncVer      map[string]int64                `json:"next_sync_ver"`
	EleAgentConfigs  map[string]*EleAgentModelConfig `json:"ele_agent_configs"`
	RechargePackages map[string]*RechargePackage     `json:"recharge_packages"`
	VIPPlans         []*VIPPlan                      `json:"vip_plans"`
	VIPSubscriptions []*VIPSubscription              `json:"vip_subscriptions"`
	Orders           []*AdminOrder                   `json:"orders"`
	Transactions     []*AdminTransaction             `json:"transactions"`
	CDKs             []*CDK                          `json:"cdks"`
	Settings         map[string]string               `json:"settings"`
}

var dataFile = "e2e-data.json"

func saveData() {
	mu.RLock()
	data := StoreData{
		Users:            users,
		UsernameIndex:    usernameIndex,
		Balances:         balances,
		Agents:           agents,
		AgentMap:         agentMap,
		Purchases:        purchases,
		AgentUserTools:   agentUserTools,
		Favorites:        favorites,
		Reviews:          reviews,
		DevAccounts:      devAccounts,
		Withdrawals:      withdrawals,
		SyncStore:        syncStore,
		NextSyncVer:      nextSyncVer,
		EleAgentConfigs:  eleAgentConfigs,
		RechargePackages: rechargePackages,
		VIPPlans:         vipPlans,
		VIPSubscriptions: vipSubs,
		Orders:           orders,
		Transactions:     transactions,
		CDKs:             cdks,
		Settings:         settingsStore,
	}
	mu.RUnlock()

	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(dataFile, b, 0644)
}

func loadData() {
	b, err := os.ReadFile(dataFile)
	if err != nil {
		return // 文件不存在，使用默认内存数据
	}
	var data StoreData
	if err := json.Unmarshal(b, &data); err != nil {
		return
	}
	mu.Lock()
	if data.Users != nil {
		users = data.Users
	}
	if data.UsernameIndex != nil {
		usernameIndex = data.UsernameIndex
	}
	if data.Balances != nil {
		balances = data.Balances
	}
	if data.Agents != nil {
		agents = data.Agents
	}
	if data.AgentMap != nil {
		agentMap = data.AgentMap
	}
	if data.Purchases != nil {
		purchases = data.Purchases
	}
	if data.AgentUserTools != nil {
		agentUserTools = data.AgentUserTools
	}
	if data.Favorites != nil {
		favorites = data.Favorites
	}
	if data.Reviews != nil {
		reviews = data.Reviews
	}
	if data.DevAccounts != nil {
		devAccounts = data.DevAccounts
	}
	if data.Withdrawals != nil {
		withdrawals = data.Withdrawals
	}
	if data.SyncStore != nil {
		syncStore = data.SyncStore
	}
	if data.NextSyncVer != nil {
		nextSyncVer = data.NextSyncVer
	}
	if data.EleAgentConfigs != nil {
		eleAgentConfigs = data.EleAgentConfigs
	}
	if data.RechargePackages != nil {
		rechargePackages = data.RechargePackages
	}
	if data.VIPPlans != nil {
		vipPlans = data.VIPPlans
	}
	if data.VIPSubscriptions != nil {
		vipSubs = data.VIPSubscriptions
	}
	if data.Orders != nil {
		orders = data.Orders
	}
	if data.Transactions != nil {
		transactions = data.Transactions
	}
	if data.CDKs != nil {
		cdks = data.CDKs
	}
	if data.Settings != nil {
		settingsStore = data.Settings
	}
	mu.Unlock()
}

// ============================================================================
//  Admin 管理后台接口（E2E Mock）
// ============================================================================

func adminStatsHandler(w http.ResponseWriter, r *http.Request) {
	mu.RLock()
	defer mu.RUnlock()

	totalUsers := int64(len(users))
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	countActive := func(date string) int64 {
		var n int64
		for _, u := range users {
			if time.Unix(u.UpdatedAt, 0).Format("2006-01-02") == date {
				n++
			}
		}
		return n
	}

	todayActive := countActive(today)
	yesterdayActive := countActive(yesterday)

	var todayRevenue, yesterdayRevenue, totalRevenue int64
	for _, tx := range transactions {
		if tx.Type == "recharge" {
			totalRevenue += tx.Amount
			txDate := time.Unix(tx.CreatedAt, 0).Format("2006-01-02")
			if txDate == today {
				todayRevenue += tx.Amount
			}
			if txDate == yesterday {
				yesterdayRevenue += tx.Amount
			}
		}
	}

	respondSuccess(w, map[string]interface{}{
		"total_users":           totalUsers,
		"today_active":          todayActive,
		"yesterday_active":      yesterdayActive,
		"today_token_usage":     int64(0),
		"yesterday_token_usage": int64(0),
		"today_revenue":         todayRevenue,
		"yesterday_revenue":     yesterdayRevenue,
		"total_revenue":         totalRevenue,
	})
}

func adminDauHandler(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 {
		days = 7
	}
	mu.RLock()
	defer mu.RUnlock()
	counts := make(map[string]int64)
	now := time.Now()
	for i := 0; i < days; i++ {
		d := now.AddDate(0, 0, -i)
		counts[d.Format("2006-01-02")] = 0
	}
	for _, u := range users {
		dateStr := time.Unix(u.UpdatedAt, 0).Format("2006-01-02")
		if _, ok := counts[dateStr]; ok {
			counts[dateStr]++
		}
	}
	var result []map[string]interface{}
	for i := days - 1; i >= 0; i-- {
		d := now.AddDate(0, 0, -i)
		dateStr := d.Format("2006-01-02")
		result = append(result, map[string]interface{}{
			"date":  dateStr,
			"value": counts[dateStr],
		})
	}
	respondSuccess(w, result)
}

func adminTokenUsageHandler(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 {
		days = 7
	}
	var result []map[string]interface{}
	now := time.Now()
	for i := days - 1; i >= 0; i-- {
		d := now.AddDate(0, 0, -i)
		// E2E 环境无真实 token_usage 记录，按消费笔数生成演示趋势
		// 输出 token 通常约为输入的 2~3 倍
		consumeCount := int64(0)
		dateStr := d.Format("2006-01-02")
		for _, tx := range transactions {
			if tx.Type == "consume" && time.Unix(tx.CreatedAt, 0).Format("2006-01-02") == dateStr {
				consumeCount++
			}
		}
		base := (consumeCount + 1) * 2000
		result = append(result, map[string]interface{}{
			"date":   dateStr,
			"input":  base,
			"output": base * 3,
		})
	}
	respondSuccess(w, result)
}

func adminActivitiesHandler(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if n, _ := strconv.Atoi(r.URL.Query().Get("limit")); n > 0 && n <= 100 {
		limit = n
	}
	mu.RLock()
	defer mu.RUnlock()
	start := len(activities) - limit
	if start < 0 {
		start = 0
	}
	respondSuccess(w, activities[start:])
}

func adminUsersHandler(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	search := r.URL.Query().Get("search")
	statusStr := r.URL.Query().Get("status")
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	mu.RLock()
	var list []*User
	for _, u := range users {
		if search != "" && !strings.Contains(u.Username, search) && !strings.Contains(u.Nickname, search) {
			continue
		}
		if statusStr != "" {
			want, _ := strconv.Atoi(statusStr)
			if u.Status != want {
				continue
			}
		}
		list = append(list, u)
	}
	mu.RUnlock()

	total := int64(len(list))
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > len(list) {
		start = len(list)
	}
	if end > len(list) {
		end = len(list)
	}
	respondSuccess(w, map[string]interface{}{
		"total": total,
		"items": list[start:end],
	})
}

func adminUserItemHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/admin/users/")

	// ASR 额度子路径：/v1/admin/users/{id}/asr-quota
	if strings.HasSuffix(id, "/asr-quota") {
		adminUserAsrQuotaHandler(w, r)
		return
	}

	// VIP 授予子路径：/v1/admin/users/{id}/vip
	if strings.HasSuffix(id, "/vip") {
		adminGrantVIPHandler(w, r)
		return
	}

	mu.RLock()
	user := users[id]
	mu.RUnlock()
	if user == nil {
		respondError(w, 4004, "用户不存在")
		return
	}

	switch r.Method {
	case http.MethodGet:
		respondSuccess(w, user)
	case http.MethodPatch:
		defer saveData()
		var req struct {
			Status int `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, 1001, "参数错误")
			return
		}
		mu.Lock()
		user.Status = req.Status
		mu.Unlock()
		respondSuccess(w, nil)
	case http.MethodDelete:
		defer saveData()
		mu.Lock()
		delete(users, id)
		delete(usernameIndex, user.Username)
		mu.Unlock()
		respondSuccess(w, nil)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func adminUserAsrQuotaHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/admin/users/")
	id = strings.TrimSuffix(id, "/asr-quota")
	mu.RLock()
	user := users[id]
	mu.RUnlock()
	if user == nil {
		respondError(w, 4004, "用户不存在")
		return
	}

	switch r.Method {
	case http.MethodGet:
		respondSuccess(w, map[string]interface{}{
			"monthly":  user.AsrQuotaMonthly,
			"used":     user.AsrQuotaUsed,
			"reset_at": user.AsrQuotaResetAt,
		})
	case http.MethodPatch:
		defer saveData()
		var req struct {
			Monthly int64 `json:"monthly"`
			Used    int64 `json:"used"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, 1001, "参数错误")
			return
		}
		mu.Lock()
		user.AsrQuotaMonthly = req.Monthly
		user.AsrQuotaUsed = req.Used
		user.AsrQuotaResetAt = time.Now().Unix()
		mu.Unlock()
		respondSuccess(w, nil)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func adminTransactionsHandler(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	txType := r.URL.Query().Get("type")
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	mu.RLock()
	var list []*AdminTransaction
	for _, tx := range transactions {
		if txType != "" && tx.Type != txType {
			continue
		}
		list = append(list, tx)
	}
	mu.RUnlock()

	total := int64(len(list))
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > len(list) {
		start = len(list)
	}
	if end > len(list) {
		end = len(list)
	}
	respondSuccess(w, map[string]interface{}{
		"total": total,
		"items": list[start:end],
	})
}

func rechargeHistoryHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(string)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	mu.RLock()
	var list []map[string]interface{}
	for i := len(transactions) - 1; i >= 0; i-- {
		tx := transactions[i]
		if tx.UserID != userID || tx.Type != "recharge" {
			continue
		}
		sourceType := "unknown"
		switch {
		case strings.Contains(tx.Description, "兑换码"):
			sourceType = "cdk"
		case strings.Contains(tx.Description, "微信"):
			sourceType = "wechat"
		case strings.Contains(tx.Description, "支付宝"):
			sourceType = "alipay"
		case strings.Contains(tx.Description, "手动"):
			sourceType = "manual"
		}
		list = append(list, map[string]interface{}{
			"id":          tx.ID,
			"source_type": sourceType,
			"amount":      tx.Amount,
			"currency":    tx.Currency,
			"status":      "success",
			"description": tx.Description,
			"related_id":  tx.ID,
			"created_at":  time.Unix(tx.CreatedAt, 0).Format(time.RFC3339),
		})
	}
	mu.RUnlock()

	total := int64(len(list))
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > len(list) {
		start = len(list)
	}
	if end > len(list) {
		end = len(list)
	}
	respondSuccess(w, map[string]interface{}{
		"items":     list[start:end],
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func adminRechargeHandler(w http.ResponseWriter, r *http.Request) {
	defer saveData()
	var req struct {
		UserID   string `json:"user_id"`
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误")
		return
	}
	if req.Amount <= 0 {
		respondError(w, 1001, "充值金额必须大于 0")
		return
	}
	if req.Currency == "" {
		req.Currency = "danwan"
	}

	mu.Lock()
	defer mu.Unlock()
	if users[req.UserID] == nil {
		respondError(w, 4004, "用户不存在")
		return
	}
	switch req.Currency {
	case "elegant":
		elegantBalances[req.UserID] += req.Amount
		transactions = append(transactions, &AdminTransaction{
			ID:          newUUID(),
			UserID:      req.UserID,
			Type:        "recharge",
			Amount:      req.Amount,
			Currency:    "elegant",
			Description: "管理员手动充值",
			CreatedAt:   time.Now().Unix(),
		})
	default:
		balances[req.UserID] += req.Amount
		users[req.UserID].TotalRecharged += req.Amount
		transactions = append(transactions, &AdminTransaction{
			ID:          newUUID(),
			UserID:      req.UserID,
			Type:        "recharge",
			Amount:      req.Amount,
			Currency:    "danwan",
			Description: "管理员手动充值",
			CreatedAt:   time.Now().Unix(),
		})
	}

	// 记录充值动态
	currencyLabel := "弹丸"
	if req.Currency == "elegant" {
		currencyLabel = "优雅弹丸"
	}
	activities = append(activities, &ActivityEvent{
		ID:          newUUID(),
		UserID:      req.UserID,
		Type:        "user_recharged",
		Title:       "用户充值",
		Description: fmt.Sprintf("用户（user_id:%s）充值了 %d %s，花费 ¥%.2f", req.UserID, req.Amount, currencyLabel, float64(req.Amount)/100),
		Metadata:    fmt.Sprintf(`{"amount":%d,"currency":"%s"}`, req.Amount, req.Currency),
		CreatedAt:   time.Now().Unix(),
	})

	respondSuccess(w, nil)
}

// ============================================================================
//  兑换码接口（E2E Mock）
// ============================================================================

const cdkAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func e2eGenerateCDKCode(length int) (string, error) {
	max := big.NewInt(int64(len(cdkAlphabet)))
	var sb strings.Builder
	sb.Grow(length)
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		sb.WriteByte(cdkAlphabet[n.Int64()])
	}
	return sb.String(), nil
}

func e2eNormalizeCDKCode(code string) string {
	code = strings.ReplaceAll(code, "-", "")
	code = strings.ReplaceAll(code, " ", "")
	return strings.ToUpper(code)
}

func adminCDKBatchHandler(w http.ResponseWriter, r *http.Request) {
	defer saveData()
	var req struct {
		Value int64  `json:"value"`
		Count int    `json:"count"`
		Note  string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误")
		return
	}
	if req.Value <= 0 {
		respondError(w, 1001, "面值必须大于 0")
		return
	}
	if req.Count <= 0 || req.Count > 500 {
		respondError(w, 1001, "单次生成数量必须在 1-500 之间")
		return
	}

	batchID := newUUID()
	mu.Lock()
	defer mu.Unlock()

	existing := make(map[string]bool)
	for _, c := range cdks {
		existing[c.Code] = true
	}

	items := make([]*CDK, 0, req.Count)
	now := time.Now().Unix()
	for len(items) < req.Count {
		code, err := e2eGenerateCDKCode(16)
		if err != nil {
			respondError(w, 1000, "生成兑换码失败")
			return
		}
		if existing[code] {
			continue
		}
		existing[code] = true
		items = append(items, &CDK{
			ID:        newUUID(),
			Code:      code,
			Value:     req.Value,
			Used:      false,
			BatchID:   batchID,
			Note:      req.Note,
			CreatedAt: now,
			UpdatedAt: now,
		})
	}
	cdks = append(cdks, items...)

	respondSuccess(w, map[string]interface{}{
		"batch_id": batchID,
		"count":    len(items),
		"items":    items,
	})
}

func adminCDKListHandler(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	status := r.URL.Query().Get("status")
	valueStr := r.URL.Query().Get("value")
	search := r.URL.Query().Get("search")
	batchID := r.URL.Query().Get("batch_id")
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	mu.RLock()
	var list []*CDK
	for _, c := range cdks {
		if status == "used" && !c.Used {
			continue
		}
		if status == "unused" && c.Used {
			continue
		}
		if valueStr != "" {
			if v, err := strconv.ParseInt(valueStr, 10, 64); err == nil && c.Value != v {
				continue
			}
		}
		if batchID != "" && c.BatchID != batchID {
			continue
		}
		if search != "" && !strings.Contains(c.Code, e2eNormalizeCDKCode(search)) {
			continue
		}
		list = append(list, c)
	}
	mu.RUnlock()

	// 按创建时间倒序
	sort.Slice(list, func(i, j int) bool {
		return list[i].CreatedAt > list[j].CreatedAt
	})

	total := int64(len(list))
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > len(list) {
		start = len(list)
	}
	if end > len(list) {
		end = len(list)
	}
	respondSuccess(w, map[string]interface{}{
		"total": total,
		"items": list[start:end],
	})
}

func adminCDKItemHandler(w http.ResponseWriter, r *http.Request) {
	id := ""
	if v, ok := r.Context().Value(ctxCDKIDKey).(string); ok {
		id = v
	}
	if id == "" {
		// 兼容直接注册 /v1/admin/cdk/ 的场景
		path := strings.TrimPrefix(r.URL.Path, "/v1/admin/cdk/")
		id = path
	}
	if id == "" {
		respondError(w, 1001, "参数错误")
		return
	}

	defer saveData()
	mu.Lock()
	defer mu.Unlock()

	found := -1
	for i, c := range cdks {
		if c.ID == id {
			found = i
			break
		}
	}
	if found < 0 {
		respondError(w, 4004, "兑换码不存在")
		return
	}
	if cdks[found].Used {
		respondError(w, 1005, "已使用的兑换码不能删除")
		return
	}
	cdks = append(cdks[:found], cdks[found+1:]...)
	respondSuccess(w, nil)
}

func cdkRedeemHandler(w http.ResponseWriter, r *http.Request) {
	defer saveData()
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误")
		return
	}
	code := e2eNormalizeCDKCode(req.Code)
	if code == "" {
		respondError(w, 1001, "兑换码不能为空")
		return
	}

	userID := userIDFrom(r)
	mu.Lock()
	defer mu.Unlock()

	if users[userID] == nil {
		respondError(w, 4004, "用户不存在")
		return
	}

	var cdk *CDK
	for _, c := range cdks {
		if c.Code == code {
			cdk = c
			break
		}
	}
	if cdk == nil {
		respondError(w, 1006, "兑换码无效")
		return
	}
	if cdk.Used {
		respondError(w, 1007, "兑换码已被使用")
		return
	}
	if cdk.Value <= 0 {
		respondError(w, 1008, "兑换码面值异常")
		return
	}

	now := time.Now().Unix()
	cdk.Used = true
	cdk.UsedBy = &userID
	cdk.UsedAt = &now
	cdk.UpdatedAt = now

	balances[userID] += cdk.Value
	users[userID].TotalRecharged += cdk.Value
	transactions = append(transactions, &AdminTransaction{
		ID:           newUUID(),
		UserID:       userID,
		Type:         "recharge",
		Amount:       cdk.Value,
		Currency:     "danwan",
		BalanceAfter: balances[userID],
		Description:  fmt.Sprintf("兑换码充值: %s", cdk.Code),
		CreatedAt:    now,
	})

	respondSuccess(w, map[string]interface{}{
		"value":   cdk.Value,
		"danwan":  balances[userID],
		"elegant": elegantBalances[userID],
	})
}

func adminOrdersHandler(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	status := r.URL.Query().Get("status")
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	mu.RLock()
	var list []*AdminOrder
	for _, o := range orders {
		if status != "" && o.Status != status {
			continue
		}
		list = append(list, o)
	}
	mu.RUnlock()

	total := int64(len(list))
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > len(list) {
		start = len(list)
	}
	if end > len(list) {
		end = len(list)
	}
	respondSuccess(w, map[string]interface{}{
		"total": total,
		"items": list[start:end],
	})
}

func adminOrderItemHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/admin/orders/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	mu.RLock()
	var order *AdminOrder
	for _, o := range orders {
		if o.ID == id {
			order = o
			break
		}
	}
	mu.RUnlock()
	if order == nil {
		respondError(w, 4004, "订单不存在")
		return
	}
	switch action {
	case "refund":
		adminOrderRefundHandler(w, r, order)
	case "confirm":
		adminOrderConfirmHandler(w, r, order)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func adminOrderRefundHandler(w http.ResponseWriter, r *http.Request, order *AdminOrder) {
	defer saveData()
	mu.Lock()
	if order.Status != "paid" {
		mu.Unlock()
		respondError(w, 3001, "订单状态不允许退款")
		return
	}
	order.Status = "refunded"
	balances[order.UserID] += order.Amount
	transactions = append(transactions, &AdminTransaction{
		ID:          newUUID(),
		UserID:      order.UserID,
		Type:        "refund",
		Amount:      order.Amount,
		Currency:    order.Currency,
		Description: "订单退款: " + order.ID,
		CreatedAt:   time.Now().Unix(),
	})
	mu.Unlock()
	respondSuccess(w, nil)
}

func adminOrderConfirmHandler(w http.ResponseWriter, r *http.Request, order *AdminOrder) {
	defer saveData()
	mu.Lock()
	if order.Status != "pending" {
		mu.Unlock()
		respondError(w, 3001, "订单状态不允许确认")
		return
	}
	now := time.Now().Unix()
	order.Status = "paid"
	order.PaidAt = &now

	if order.ProductType == "vip" {
		plan := vipPlanByID(order.ProductID)
		if plan != nil {
			activateVIPForUser(order.UserID, plan, plan.DurationDays)
		}
	} else {
		// 默认弹丸充值：将订单金额加到弹丸余额
		balances[order.UserID] += order.Amount
	}
	mu.Unlock()
	respondSuccess(w, nil)
}

func vipPlanByID(id string) *VIPPlan {
	for _, p := range vipPlans {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func activateVIPForUser(userID string, plan *VIPPlan, durationDays int) {
	user := users[userID]
	if user == nil || plan == nil {
		return
	}
	now := time.Now()
	expire := time.Unix(user.VIPExpireAt, 0)
	if expire.Before(now) {
		expire = now
	}
	expire = expire.AddDate(0, 0, durationDays)
	user.IsVIP = true
	user.VIPLevel = plan.Level
	user.VIPExpireAt = expire.Unix()
	user.UpdatedAt = now.Unix()
	vipSubs = append(vipSubs, &VIPSubscription{
		ID:           newUUID(),
		UserID:       userID,
		PlanID:       plan.ID,
		Level:        plan.Level,
		PriceFen:     plan.PriceFen,
		DurationDays: durationDays,
		StartedAt:    now.Unix(),
		ExpiresAt:    expire.Unix(),
		Status:       "active",
	})
}

func adminWithdrawalsHandler(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	status := r.URL.Query().Get("status")
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	mu.RLock()
	var list []*WithdrawalRecord
	for _, wv := range withdrawals {
		if status != "" && wv.Status != status {
			continue
		}
		list = append(list, wv)
	}
	mu.RUnlock()

	total := int64(len(list))
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > len(list) {
		start = len(list)
	}
	if end > len(list) {
		end = len(list)
	}
	respondSuccess(w, map[string]interface{}{
		"total": total,
		"items": list[start:end],
	})
}

func adminWithdrawalItemHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/admin/withdrawals/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		respondError(w, 1001, "参数错误")
		return
	}
	id := parts[0]
	action := parts[1]
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	defer saveData()
	var req struct {
		AdminNote string `json:"admin_note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	mu.Lock()
	defer mu.Unlock()
	for _, wv := range withdrawals {
		if wv.ID != id {
			continue
		}
		wv.AdminNote = req.AdminNote
		switch action {
		case "approve":
			wv.Status = "approved"
		case "reject":
			wv.Status = "rejected"
			// 拒绝后退回优雅弹丸余额
			if devAccounts[wv.UserID] == nil {
				devAccounts[wv.UserID] = &DeveloperAccount{UserID: wv.UserID}
			}
			devAccounts[wv.UserID].ElegantBalance += wv.Amount
		default:
			respondError(w, 1001, "未知操作")
			return
		}
		respondSuccess(w, wv)
		return
	}
	respondError(w, 4004, "提现记录不存在")
}

// ============================================================================
//  集市模块 / 动态驱动管理（E2E）
// ============================================================================

func adminListModulesHandler(w http.ResponseWriter, r *http.Request) {
	modulesMu.RLock()
	defer modulesMu.RUnlock()
	items := make([]*E2EModule, 0, len(e2eModules))
	for _, m := range e2eModules {
		items = append(items, m)
	}
	respondSuccess(w, map[string]interface{}{"items": items})
}

func adminGetModuleHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/admin/modules/")
	id = strings.Split(id, "/")[0]
	modulesMu.RLock()
	m := e2eModules[id]
	modulesMu.RUnlock()
	if m == nil {
		respondError(w, 4004, "模块不存在")
		return
	}
	respondSuccess(w, m)
}

func adminRegisterModuleHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ModuleID     string   `json:"module_id"`
		Name         string   `json:"name"`
		Description  string   `json:"description"`
		URL          string   `json:"url"`
		TransportType  string   `json:"transport_type"`
		Capabilities []string `json:"capabilities"`
		Version      string   `json:"version"`
		AuthToken    string   `json:"auth_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误")
		return
	}
	if req.URL == "" {
		respondError(w, 1001, "url 不能为空")
		return
	}
	if req.ModuleID == "" {
		base := model.GenerateModuleID(req.Name)
		req.ModuleID = base
		for i := 1; i < 1000; i++ {
			if e2eModules[req.ModuleID] == nil {
				break
			}
			req.ModuleID = fmt.Sprintf("%s-%d", base, i)
		}
	}
	modulesMu.Lock()
	defer modulesMu.Unlock()
	e2eModules[req.ModuleID] = &E2EModule{
		ID:           req.ModuleID,
		Name:         req.Name,
		URL:          req.URL,
		TransportType:  req.TransportType,
		Capabilities: req.Capabilities,
		Version:      req.Version,
		AuthToken:    req.AuthToken,
	}
	respondSuccess(w, map[string]interface{}{"module_id": req.ModuleID})
}

func adminUnregisterModuleHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/admin/modules/")
	id = strings.Split(id, "/")[0]
	modulesMu.Lock()
	delete(e2eModules, id)
	modulesMu.Unlock()
	respondSuccess(w, nil)
}

func adminRefreshModuleHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/admin/modules/")
	id = strings.Split(id, "/")[0]
	modulesMu.Lock()
	m := e2eModules[id]
	if m != nil {
		// E2E 环境下直接认为在线，真实环境会探测 /health
		m.Online = true
		m.Version = "1.0.0"
	}
	modulesMu.Unlock()
	if m == nil {
		respondError(w, 4004, "模块不存在")
		return
	}
	respondSuccess(w, map[string]interface{}{"module_id": m.ID, "online": m.Online, "version": m.Version})
}

func adminRescanMarketplaceHandler(w http.ResponseWriter, r *http.Request) {
	modulesMu.Lock()
	defer modulesMu.Unlock()
	// E2E 环境下重新确保默认内置模块与驱动存在
	if e2eModules["agent-reach"] == nil {
		e2eModules["agent-reach"] = &E2EModule{ID: "agent-reach", Name: "Agent-Reach", URL: "http://agent-reach:8080", TransportType: "module", Online: true, Version: "1.0.0", Capabilities: []string{"web_read", "search", "subtitles"}}
	}
	if e2eModules["firecrawl"] == nil {
		e2eModules["firecrawl"] = &E2EModule{ID: "firecrawl", Name: "Firecrawl", URL: "http://firecrawl:8080", TransportType: "module", Online: false, Version: "1.0.0", Capabilities: []string{"scrape", "crawl", "extract"}}
	}
	if e2eDrivers["agent_reach"] == nil {
		e2eDrivers["agent_reach"] = &E2EDriver{ID: "agent_reach", Name: "Agent-Reach 互联网能力", TransportType: "module", ModuleID: "agent-reach"}
	}
	if e2eDrivers["firecrawl"] == nil {
		e2eDrivers["firecrawl"] = &E2EDriver{ID: "firecrawl", Name: "Firecrawl 网页抓取", TransportType: "module", ModuleID: "firecrawl"}
	}
	respondSuccess(w, nil)
}

func registerModuleFromPluginHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ModuleID     string   `json:"module_id"`
		Name         string   `json:"name"`
		Description  string   `json:"description"`
		URL          string   `json:"url"`
		TransportType  string   `json:"transport_type"`
		Capabilities []string `json:"capabilities"`
		Version      string   `json:"version"`
		AuthToken    string   `json:"auth_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误")
		return
	}
	providedToken := r.Header.Get("X-Module-Auth-Token")
	if providedToken == "" {
		providedToken = req.AuthToken
	}
	if req.URL == "" {
		respondError(w, 1001, "url 不能为空")
		return
	}

	modulesMu.Lock()
	defer modulesMu.Unlock()

	// 新流程：通过 driver 的 auth_token 绑定模块
	if providedToken != "" {
		for _, d := range e2eDrivers {
			if d.AuthToken == providedToken {
				if req.ModuleID == "" {
					if d.ModuleID != "" {
						req.ModuleID = d.ModuleID
					} else {
						req.ModuleID = model.GenerateModuleID(req.Name)
					}
				}
				if req.TransportType == "" {
					req.TransportType = d.TransportType
				}
				e2eModules[req.ModuleID] = &E2EModule{
					ID:           req.ModuleID,
					Name:         req.Name,
					URL:          req.URL,
					TransportType:  req.TransportType,
					Capabilities: req.Capabilities,
					Version:      req.Version,
					AuthToken:    providedToken,
				}
				d.ModuleID = req.ModuleID
				respondSuccess(w, map[string]interface{}{"module_id": req.ModuleID})
				return
			}
		}
	}

	// 旧流程：直接按 module_id 注册模块
	if req.ModuleID == "" {
		req.ModuleID = model.GenerateModuleID(req.Name)
	}
	existing := e2eModules[req.ModuleID]
	if existing != nil && existing.AuthToken != "" && existing.AuthToken != providedToken {
		respondError(w, 3001, "注册令牌无效")
		return
	}
	authToken := providedToken
	if existing != nil && existing.AuthToken != "" {
		authToken = existing.AuthToken
	}
	e2eModules[req.ModuleID] = &E2EModule{
		ID:           req.ModuleID,
		Name:         req.Name,
		URL:          req.URL,
		TransportType:  req.TransportType,
		Capabilities: req.Capabilities,
		Version:      req.Version,
		AuthToken:    authToken,
	}
	respondSuccess(w, map[string]interface{}{"module_id": req.ModuleID})
}

func adminListDriversHandler(w http.ResponseWriter, r *http.Request) {
	modulesMu.RLock()
	defer modulesMu.RUnlock()
	items := make([]*E2EDriver, 0, len(e2eDrivers))
	for _, d := range e2eDrivers {
		items = append(items, d)
	}
	respondSuccess(w, map[string]interface{}{"items": items})
}

func adminRegisterDriverHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID          string `json:"driver_id"`
		Name        string `json:"name"`
		TransportType string `json:"transport_type"`
		ModuleID    string `json:"module_id"`
		Endpoint    string `json:"endpoint"`
		AuthToken   string `json:"auth_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误")
		return
	}
	if req.ID == "" || req.Name == "" {
		respondError(w, 1001, "driver_id 和 name 不能为空")
		return
	}
	if req.TransportType == "module" && req.ModuleID == "" && req.AuthToken == "" {
		respondError(w, 1001, "module 型驱动必须指定 module_id 或 auth_token")
		return
	}
	modulesMu.Lock()
	defer modulesMu.Unlock()
	e2eDrivers[req.ID] = &E2EDriver{
		ID:          req.ID,
		Name:        req.Name,
		TransportType: req.TransportType,
		ModuleID:    req.ModuleID,
		Endpoint:    req.Endpoint,
		AuthToken:   req.AuthToken,
	}
	respondSuccess(w, nil)
}

func adminUnregisterDriverHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/admin/drivers/")
	modulesMu.Lock()
	delete(e2eDrivers, id)
	modulesMu.Unlock()
	respondSuccess(w, nil)
}

// e2eIsExecutableDriver 判断 driver 是否可在 E2E 环境下模拟执行
func e2eIsExecutableDriver(driver string) bool {
	if driver == "module" {
		return true
	}
	modulesMu.RLock()
	defer modulesMu.RUnlock()
	d := e2eDrivers[driver]
	return d != nil && d.TransportType == "module"
}

// e2eModuleOnlineFromManifest 解析 SKU manifest，若依赖 module 驱动则返回对应模块是否在线。
// 优先通过 driver 别名在 e2eDrivers 中查找 module_id，其次回退到 metadata.module。
// 声明了非内置驱动别名但驱动未注册、或 module_id 为空时，视为离线。
func e2eModuleOnlineFromManifest(manifestJSON string) bool {
	if manifestJSON == "" {
		return true
	}
	var mf struct {
		Driver   string            `json:"driver"`
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(manifestJSON), &mf); err != nil {
		return true
	}
	if mf.Driver == "" || mf.Driver == "none" || mf.Driver == "builtin" {
		return true
	}

	modulesMu.RLock()
	defer modulesMu.RUnlock()

	d := e2eDrivers[mf.Driver]
	if d == nil {
		return false
	}
	if d.TransportType != "module" {
		return true
	}

	moduleID := d.ModuleID
	if moduleID == "" && mf.Metadata != nil {
		moduleID = mf.Metadata["module"]
	}
	if moduleID == "" {
		return false
	}
	m := e2eModules[moduleID]
	return m != nil && m.Online
}

// e2eCredentialBucket 决定 e2e 凭证存储桶：scope=module 存 uid:module:<driver>（同 driver 共享），否则 uid:<agentID>。
func e2eCredentialBucket(uid string, def model.CredentialDef, manifest *model.ToolManifest, agentID string) string {
	if def.Scope == model.CredentialScopeModule && manifest != nil && manifest.Driver != "" {
		return uid + ":module:" + string(manifest.Driver)
	}
	return uid + ":" + agentID
}

// e2eCredentialComplete 判断 SKU 声明的必填凭证是否已配齐（按 scope 分桶校验）
func e2eCredentialComplete(uid string, a *AgentItem) bool {
	if uid == "" {
		return true
	}
	manifest, err := a.Manifest()
	if err != nil || manifest == nil || len(manifest.Credentials) == 0 {
		return true
	}
	for k, def := range manifest.Credentials {
		if !def.Required {
			continue
		}
		bucket := e2eCredentialBucket(uid, def, manifest, a.ID)
		mu.RLock()
		stored := e2eCredentials[bucket]
		mu.RUnlock()
		if stored == nil || stored[k] == "" {
			return false
		}
	}
	return true
}

// e2eDriverRegisteredFromManifest 判断 SKU 声明的驱动别名是否已注册
func e2eDriverRegisteredFromManifest(manifestJSON string) bool {
	if manifestJSON == "" {
		return true
	}
	var mf struct {
		Driver string `json:"driver"`
	}
	if err := json.Unmarshal([]byte(manifestJSON), &mf); err != nil {
		return true
	}
	if mf.Driver == "" || mf.Driver == "none" || mf.Driver == "builtin" {
		return true
	}
	modulesMu.RLock()
	defer modulesMu.RUnlock()
	return e2eDrivers[mf.Driver] != nil
}

// e2eModuleOnlinePtrFromManifest 返回 SKU 依赖模块的在线状态指针。
// 无外部模块依赖返回 nil；模块在线返回 true；离线/未注册返回 false。
func e2eModuleOnlinePtrFromManifest(manifestJSON string) *bool {
	if manifestJSON == "" {
		return nil
	}
	var mf struct {
		Driver   string            `json:"driver"`
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(manifestJSON), &mf); err != nil {
		return nil
	}
	if mf.Driver == "" || mf.Driver == "none" || mf.Driver == "builtin" {
		return nil
	}

	modulesMu.RLock()
	defer modulesMu.RUnlock()

	d := e2eDrivers[mf.Driver]
	if d == nil || d.TransportType != "module" {
		return nil
	}
	moduleID := d.ModuleID
	if moduleID == "" && mf.Metadata != nil {
		moduleID = mf.Metadata["module"]
	}
	if moduleID == "" {
		offline := false
		return &offline
	}
	online := e2eModules[moduleID] != nil && e2eModules[moduleID].Online
	return &online
}

// e2eResolveModuleID 从 manifest 或动态驱动记录中解析模块 ID
func e2eResolveModuleID(driver string, metadata map[string]string) string {
	if metadata != nil && metadata["module"] != "" {
		return metadata["module"]
	}
	modulesMu.RLock()
	defer modulesMu.RUnlock()
	d := e2eDrivers[driver]
	if d != nil {
		return d.ModuleID
	}
	return ""
}

func adminAgentsHandler(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	status := r.URL.Query().Get("status")
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	mu.RLock()
	var list []*AgentItem
	for _, a := range agents {
		if status != "" && a.Status != status {
			continue
		}
		list = append(list, a)
	}
	mu.RUnlock()

	total := int64(len(list))
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > len(list) {
		start = len(list)
	}
	if end > len(list) {
		end = len(list)
	}
	respondSuccess(w, map[string]interface{}{
		"total": total,
		"items": list[start:end],
	})
}

func adminAgentItemHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/admin/agents/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		respondError(w, 1001, "参数错误")
		return
	}
	id := parts[0]
	action := parts[1]

	mu.Lock()
	defer mu.Unlock()
	agent := agentMap[id]
	if agent == nil {
		respondError(w, 4004, "秘技不存在")
		return
	}

	// GET /v1/admin/agents/:id/dependencies 查询依赖状态
	if r.Method == http.MethodGet && action == "dependencies" {
		status := buildAgentDependencyStatus(agent)
		respondSuccess(w, status)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	defer saveData()
	var req struct {
		Status    string `json:"status"`
		AdminNote string `json:"admin_note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	switch action {
	case "approve":
		agent.Status = "approved"
		// 审批通过时自动为 SKU 创建/更新动态驱动别名，并生成注册令牌
		driverID, token := ensureDriverForAgent(agent)
		respondSuccess(w, map[string]interface{}{
			"agent":      agent,
			"driver_id":  driverID,
			"auth_token": token,
		})
		return
	case "reject":
		agent.Status = "rejected"
	case "delist":
		agent.Status = "delisted"
	default:
		respondError(w, 1001, "未知操作")
		return
	}
	respondSuccess(w, agent)
}

// buildAgentDependencyStatus 构造 SKU 依赖状态（E2E）
func buildAgentDependencyStatus(agent *AgentItem) map[string]interface{} {
	status := map[string]interface{}{
		"driver":            "",
		"driver_name":       "",
		"driver_registered": false,
		"module_id":         "",
		"module_registered": false,
		"module_online":     nil,
	}
	if agent.ManifestJSON == "" {
		status["driver_registered"] = true
		return status
	}
	var mf struct {
		Driver   string            `json:"driver"`
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(agent.ManifestJSON), &mf); err != nil {
		return status
	}
	status["driver"] = mf.Driver
	if mf.Driver == "" || mf.Driver == "none" || mf.Driver == "builtin" {
		status["driver_registered"] = true
		return status
	}
	modulesMu.RLock()
	defer modulesMu.RUnlock()
	d := e2eDrivers[mf.Driver]
	if d != nil {
		status["driver_registered"] = true
		status["driver_name"] = d.Name
		if d.TransportType == "module" {
			moduleID := d.ModuleID
			if moduleID == "" && mf.Metadata != nil {
				moduleID = mf.Metadata["module"]
			}
			status["module_id"] = moduleID
			if m := e2eModules[moduleID]; m != nil {
				status["module_registered"] = true
				status["module_online"] = m.Online
			}
		}
	}
	return status
}

// ensureDriverForAgent 审批通过时为 SKU 自动创建/补充动态驱动别名与注册令牌
func ensureDriverForAgent(agent *AgentItem) (string, string) {
	if agent.ManifestJSON == "" {
		return "", ""
	}
	var mf struct {
		Driver string `json:"driver"`
	}
	if err := json.Unmarshal([]byte(agent.ManifestJSON), &mf); err != nil || mf.Driver == "" || mf.Driver == "none" || mf.Driver == "builtin" {
		return "", ""
	}
	modulesMu.Lock()
	defer modulesMu.Unlock()
	d := e2eDrivers[mf.Driver]
	if d == nil {
		d = &E2EDriver{
			ID:            mf.Driver,
			Name:          agent.Name,
			TransportType: "module",
			AuthToken:     randomString(32),
		}
		e2eDrivers[mf.Driver] = d
	}
	if d.AuthToken == "" {
		d.AuthToken = randomString(32)
	}
	return d.ID, d.AuthToken
}

func adminSettingsHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		result := map[string]interface{}{
			"site_name":              settingsStore["site_name"],
			"register_open":          settingsStore["register_open"] == "true",
			"default_model":          settingsStore["default_model"],
			"max_tokens_per_request": 4096,
			"free_quota":             1000,
			"maintenance_mode":       settingsStore["maintenance_mode"] == "true",
			"xianyu_product_url":     settingsStore["xianyu_product_url"],
			"taobao_product_url":     settingsStore["taobao_product_url"],
			"prompt_fusion_model":    settingsStore["prompt_fusion_model"],
		}
		if v, ok := settingsStore["max_tokens_per_request"]; ok {
			if n, err := strconv.Atoi(v); err == nil {
				result["max_tokens_per_request"] = n
			}
		}
		if v, ok := settingsStore["free_quota"]; ok {
			if n, err := strconv.Atoi(v); err == nil {
				result["free_quota"] = n
			}
		}
		respondSuccess(w, result)
	case http.MethodPut:
		defer saveData()
		var req struct {
			SiteName            string `json:"site_name"`
			RegisterOpen        bool   `json:"register_open"`
			DefaultModel        string `json:"default_model"`
			MaxTokensPerRequest int    `json:"max_tokens_per_request"`
			FreeQuota           int    `json:"free_quota"`
			MaintenanceMode     bool   `json:"maintenance_mode"`
			XianyuProductURL    string `json:"xianyu_product_url"`
			TaobaoProductURL    string `json:"taobao_product_url"`
			PromptFusionModel   string `json:"prompt_fusion_model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, 1001, "参数错误")
			return
		}
		settingsStore["site_name"] = req.SiteName
		settingsStore["register_open"] = strconv.FormatBool(req.RegisterOpen)
		settingsStore["default_model"] = req.DefaultModel
		settingsStore["max_tokens_per_request"] = strconv.Itoa(req.MaxTokensPerRequest)
		settingsStore["free_quota"] = strconv.Itoa(req.FreeQuota)
		settingsStore["maintenance_mode"] = strconv.FormatBool(req.MaintenanceMode)
		settingsStore["xianyu_product_url"] = req.XianyuProductURL
		settingsStore["taobao_product_url"] = req.TaobaoProductURL
		settingsStore["prompt_fusion_model"] = req.PromptFusionModel
		respondSuccess(w, nil)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func publicSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	mu.RLock()
	defer mu.RUnlock()
	respondSuccess(w, map[string]interface{}{
		"xianyu_product_url":  settingsStore["xianyu_product_url"],
		"taobao_product_url":  settingsStore["taobao_product_url"],
		"prompt_fusion_model": settingsStore["prompt_fusion_model"],
	})
}

// ============================================================================
//  充值套餐接口（E2E Mock）
// ============================================================================

func rechargePackagesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	mu.RLock()
	defer mu.RUnlock()

	var list []*RechargePackage
	for _, pkg := range rechargePackages {
		if pkg.IsEnabled {
			list = append(list, pkg)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].SortOrder != list[j].SortOrder {
			return list[i].SortOrder < list[j].SortOrder
		}
		return list[i].UpdatedAt > list[j].UpdatedAt
	})

	items := make([]map[string]interface{}, 0, len(list))
	for _, pkg := range list {
		item := packageToMap(pkg)
		if pkg.IsCustomMultiplier && pkg.BasePackageID != nil {
			if base := rechargePackages[*pkg.BasePackageID]; base != nil {
				item["base_package"] = map[string]interface{}{
					"id":        base.ID,
					"name":      base.Name,
					"danwan":    base.Danwan,
					"price_fen": base.PriceFen,
				}
			}
		}
		items = append(items, item)
	}
	respondSuccess(w, map[string]interface{}{"items": items})
}

func packageToMap(pkg *RechargePackage) map[string]interface{} {
	m := map[string]interface{}{
		"id":                   pkg.ID,
		"name":                 pkg.Name,
		"danwan":               pkg.Danwan,
		"price_fen":            pkg.PriceFen,
		"sort_order":           pkg.SortOrder,
		"is_enabled":           pkg.IsEnabled,
		"is_custom_multiplier": pkg.IsCustomMultiplier,
		"description":          pkg.Description,
		"created_at":           time.Unix(pkg.CreatedAt, 0).Format(time.RFC3339),
		"updated_at":           time.Unix(pkg.UpdatedAt, 0).Format(time.RFC3339),
	}
	if pkg.BasePackageID != nil {
		m["base_package_id"] = *pkg.BasePackageID
	}
	return m
}

func adminRechargePackagesHandler(w http.ResponseWriter, r *http.Request) {
	defer saveData()
	mu.Lock()
	defer mu.Unlock()

	switch r.Method {
	case http.MethodGet:
		var list []*RechargePackage
		for _, pkg := range rechargePackages {
			list = append(list, pkg)
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].SortOrder != list[j].SortOrder {
				return list[i].SortOrder < list[j].SortOrder
			}
			return list[i].UpdatedAt > list[j].UpdatedAt
		})
		items := make([]map[string]interface{}, 0, len(list))
		for _, pkg := range list {
			items = append(items, packageToMap(pkg))
		}
		respondSuccess(w, map[string]interface{}{"items": items})
	case http.MethodPost:
		var req struct {
			Name               string  `json:"name"`
			Danwan             int64   `json:"danwan"`
			PriceYuan          float64 `json:"price_yuan"`
			SortOrder          int     `json:"sort_order"`
			IsEnabled          bool    `json:"is_enabled"`
			IsCustomMultiplier bool    `json:"is_custom_multiplier"`
			BasePackageID      *string `json:"base_package_id"`
			Description        string  `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, 1001, "参数错误")
			return
		}
		if req.Name == "" {
			respondError(w, 1001, "套餐名称不能为空")
			return
		}
		if req.IsCustomMultiplier {
			if req.BasePackageID == nil || *req.BasePackageID == "" || rechargePackages[*req.BasePackageID] == nil {
				respondError(w, 1001, "自定义数量套餐必须关联有效的基础套餐")
				return
			}
		}
		now := time.Now().Unix()
		uid := newUUID()
		pkg := &RechargePackage{
			ID:                 uid,
			Name:               req.Name,
			Danwan:             req.Danwan,
			PriceFen:           int64(req.PriceYuan*100 + 0.5),
			SortOrder:          req.SortOrder,
			IsEnabled:          req.IsEnabled,
			IsCustomMultiplier: req.IsCustomMultiplier,
			BasePackageID:      req.BasePackageID,
			Description:        req.Description,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		rechargePackages[uid] = pkg
		respondSuccess(w, packageToMap(pkg))
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func adminRechargePackageItemHandler(w http.ResponseWriter, r *http.Request) {
	defer saveData()
	id := strings.TrimPrefix(r.URL.Path, "/v1/admin/recharge/packages/")
	mu.Lock()
	defer mu.Unlock()

	pkg := rechargePackages[id]
	if pkg == nil {
		respondError(w, 4004, "套餐不存在")
		return
	}

	switch r.Method {
	case http.MethodPatch:
		var req struct {
			Name               *string  `json:"name,omitempty"`
			Danwan             *int64   `json:"danwan,omitempty"`
			PriceYuan          *float64 `json:"price_yuan,omitempty"`
			SortOrder          *int     `json:"sort_order,omitempty"`
			IsEnabled          *bool    `json:"is_enabled,omitempty"`
			IsCustomMultiplier *bool    `json:"is_custom_multiplier,omitempty"`
			BasePackageID      *string  `json:"base_package_id,omitempty"`
			Description        *string  `json:"description,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, 1001, "参数错误")
			return
		}
		if req.Name != nil {
			pkg.Name = *req.Name
		}
		if req.Danwan != nil {
			pkg.Danwan = *req.Danwan
		}
		if req.PriceYuan != nil {
			pkg.PriceFen = int64(*req.PriceYuan*100 + 0.5)
		}
		if req.SortOrder != nil {
			pkg.SortOrder = *req.SortOrder
		}
		if req.IsEnabled != nil {
			pkg.IsEnabled = *req.IsEnabled
		}
		if req.IsCustomMultiplier != nil {
			pkg.IsCustomMultiplier = *req.IsCustomMultiplier
		}
		if req.BasePackageID != nil {
			pkg.BasePackageID = req.BasePackageID
		}
		if req.Description != nil {
			pkg.Description = *req.Description
		}
		if pkg.IsCustomMultiplier {
			if pkg.BasePackageID == nil || *pkg.BasePackageID == "" || rechargePackages[*pkg.BasePackageID] == nil {
				respondError(w, 1001, "自定义数量套餐必须关联有效的基础套餐")
				return
			}
		}
		pkg.UpdatedAt = time.Now().Unix()
		respondSuccess(w, packageToMap(pkg))
	case http.MethodDelete:
		delete(rechargePackages, id)
		respondSuccess(w, nil)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

// ============================================================================
//  VIP 套餐与用户订阅（E2E Mock）
// ============================================================================

func planToMap(p *VIPPlan) map[string]interface{} {
	return map[string]interface{}{
		"id":                 p.ID,
		"level":              p.Level,
		"name":               p.Name,
		"price_fen":          p.PriceFen,
		"duration_days":      p.DurationDays,
		"discount_percent":   p.DiscountPercent,
		"max_conversations":  p.MaxConversations,
		"max_agent_sessions": p.MaxAgentSessions,
		"asr_quota_monthly":  p.AsrQuotaMonthly,
		"agent_enabled":      p.AgentEnabled,
		"file_tools_enabled": p.FileToolsEnabled,
		"sort_order":         p.SortOrder,
		"is_enabled":         p.IsEnabled,
		"description":        p.Description,
		"created_at":         time.Unix(p.CreatedAt, 0).Format(time.RFC3339),
		"updated_at":         time.Unix(p.UpdatedAt, 0).Format(time.RFC3339),
	}
}

func vipPlansHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	mu.RLock()
	defer mu.RUnlock()
	var list []*VIPPlan
	for _, p := range vipPlans {
		if p.IsEnabled {
			list = append(list, p)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].SortOrder < list[j].SortOrder
	})
	items := make([]map[string]interface{}, 0, len(list))
	for _, p := range list {
		items = append(items, planToMap(p))
	}
	respondSuccess(w, map[string]interface{}{"items": items})
}

func vipStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	uid := userIDFrom(r)
	mu.RLock()
	user := users[uid]
	mu.RUnlock()
	if user == nil {
		respondError(w, 4004, "用户不存在")
		return
	}
	var plan *VIPPlan
	if user.IsVIP {
		for _, p := range vipPlans {
			if p.Level == user.VIPLevel {
				plan = p
				break
			}
		}
	}
	resp := map[string]interface{}{
		"is_vip":             user.IsVIP,
		"level":              user.VIPLevel,
		"expire_at":          time.Unix(user.VIPExpireAt, 0).Format(time.RFC3339),
		"started_at":         time.Unix(user.CreatedAt, 0).Format(time.RFC3339),
		"plan_id":            "",
		"discount_percent":   100,
		"max_conversations":  50,
		"agent_enabled":      false,
		"file_tools_enabled": false,
		"asr_quota_monthly":  1000,
	}
	if plan != nil {
		resp["plan_id"] = plan.ID
		resp["discount_percent"] = plan.DiscountPercent
		resp["max_conversations"] = plan.MaxConversations
		resp["agent_enabled"] = plan.AgentEnabled
		resp["file_tools_enabled"] = plan.FileToolsEnabled
		resp["asr_quota_monthly"] = plan.AsrQuotaMonthly
	}
	respondSuccess(w, resp)
}

func vipSubscribeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	defer saveData()
	uid := userIDFrom(r)
	var req struct {
		PlanID            string `json:"plan_id"`
		Channel           string `json:"channel"`
		UseElegantBalance bool   `json:"use_elegant_balance"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误")
		return
	}
	mu.Lock()
	defer mu.Unlock()
	user := users[uid]
	if user == nil {
		respondError(w, 4004, "用户不存在")
		return
	}
	plan := vipPlanByID(req.PlanID)
	if plan == nil || !plan.IsEnabled {
		respondError(w, 4004, "套餐不存在或已下架")
		return
	}
	amount := plan.PriceFen
	deduct := int64(0)
	if req.UseElegantBalance {
		if elegantBalances[uid] >= amount {
			deduct = amount
			amount = 0
		} else {
			deduct = elegantBalances[uid]
			amount -= deduct
		}
	}
	// 优雅弹丸足额，直接开通
	if amount == 0 {
		elegantBalances[uid] -= deduct
		activateVIPForUser(uid, plan, plan.DurationDays)
		respondSuccess(w, map[string]interface{}{
			"order_id":          "",
			"trade_no":          "",
			"channel":           req.Channel,
			"amount_fen":        0,
			"paid":              true,
			"elegant_deducted":  deduct,
			"elegant_remaining": elegantBalances[uid],
			"vip_days":          plan.DurationDays,
		})
		return
	}
	// 不足部分生成待支付订单
	orderID := newUUID()
	orders = append(orders, &AdminOrder{
		ID:          orderID,
		UserID:      uid,
		Channel:     req.Channel,
		Amount:      amount,
		Currency:    "cny",
		Status:      "pending",
		ProductType: "vip",
		ProductID:   plan.ID,
		CreatedAt:   time.Now().Unix(),
	})
	respondSuccess(w, map[string]interface{}{
		"order_id":          orderID,
		"trade_no":          "E2E_" + orderID,
		"channel":           req.Channel,
		"amount_fen":        amount,
		"paid":              false,
		"elegant_deducted":  deduct,
		"elegant_remaining": elegantBalances[uid],
		"vip_days":          plan.DurationDays,
	})
}

func adminVIPPlansHandler(w http.ResponseWriter, r *http.Request) {
	defer saveData()
	mu.Lock()
	defer mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		var list []*VIPPlan
		for _, p := range vipPlans {
			list = append(list, p)
		}
		sort.Slice(list, func(i, j int) bool {
			return list[i].SortOrder < list[j].SortOrder
		})
		items := make([]map[string]interface{}, 0, len(list))
		for _, p := range list {
			items = append(items, planToMap(p))
		}
		respondSuccess(w, map[string]interface{}{"items": items})
	case http.MethodPost:
		var req struct {
			Level            int     `json:"level"`
			Name             string  `json:"name"`
			PriceYuan        float64 `json:"price_yuan"`
			DurationDays     int     `json:"duration_days"`
			DiscountPercent  int     `json:"discount_percent"`
			MaxConversations int     `json:"max_conversations"`
			MaxAgentSessions int     `json:"max_agent_sessions"`
			AsrQuotaMonthly  int64   `json:"asr_quota_monthly"`
			AgentEnabled     bool    `json:"agent_enabled"`
			FileToolsEnabled bool    `json:"file_tools_enabled"`
			SortOrder        int     `json:"sort_order"`
			IsEnabled        bool    `json:"is_enabled"`
			Description      string  `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, 1001, "参数错误")
			return
		}
		if req.Name == "" {
			respondError(w, 1001, "套餐名称不能为空")
			return
		}
		now := time.Now().Unix()
		plan := &VIPPlan{
			ID:               newUUID(),
			Level:            req.Level,
			Name:             req.Name,
			PriceFen:         int64(req.PriceYuan*100 + 0.5),
			DurationDays:     req.DurationDays,
			DiscountPercent:  req.DiscountPercent,
			MaxConversations: req.MaxConversations,
			MaxAgentSessions: req.MaxAgentSessions,
			AsrQuotaMonthly:  req.AsrQuotaMonthly,
			AgentEnabled:     req.AgentEnabled,
			FileToolsEnabled: req.FileToolsEnabled,
			SortOrder:        req.SortOrder,
			IsEnabled:        req.IsEnabled,
			Description:      req.Description,
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		vipPlans = append(vipPlans, plan)
		respondSuccess(w, planToMap(plan))
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func adminVIPPlanItemHandler(w http.ResponseWriter, r *http.Request) {
	defer saveData()
	id := strings.TrimPrefix(r.URL.Path, "/v1/admin/vip/plans/")
	mu.Lock()
	defer mu.Unlock()
	var plan *VIPPlan
	for _, p := range vipPlans {
		if p.ID == id {
			plan = p
			break
		}
	}
	if plan == nil {
		respondError(w, 4004, "套餐不存在")
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var req struct {
			Level            *int     `json:"level,omitempty"`
			Name             *string  `json:"name,omitempty"`
			PriceYuan        *float64 `json:"price_yuan,omitempty"`
			DurationDays     *int     `json:"duration_days,omitempty"`
			DiscountPercent  *int     `json:"discount_percent,omitempty"`
			MaxConversations *int     `json:"max_conversations,omitempty"`
			MaxAgentSessions *int     `json:"max_agent_sessions,omitempty"`
			AsrQuotaMonthly  *int64   `json:"asr_quota_monthly,omitempty"`
			AgentEnabled     *bool    `json:"agent_enabled,omitempty"`
			FileToolsEnabled *bool    `json:"file_tools_enabled,omitempty"`
			SortOrder        *int     `json:"sort_order,omitempty"`
			IsEnabled        *bool    `json:"is_enabled,omitempty"`
			Description      *string  `json:"description,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, 1001, "参数错误")
			return
		}
		if req.Level != nil {
			plan.Level = *req.Level
		}
		if req.Name != nil {
			plan.Name = *req.Name
		}
		if req.PriceYuan != nil {
			plan.PriceFen = int64(*req.PriceYuan*100 + 0.5)
		}
		if req.DurationDays != nil {
			plan.DurationDays = *req.DurationDays
		}
		if req.DiscountPercent != nil {
			plan.DiscountPercent = *req.DiscountPercent
		}
		if req.MaxConversations != nil {
			plan.MaxConversations = *req.MaxConversations
		}
		if req.MaxAgentSessions != nil {
			plan.MaxAgentSessions = *req.MaxAgentSessions
		}
		if req.AsrQuotaMonthly != nil {
			plan.AsrQuotaMonthly = *req.AsrQuotaMonthly
		}
		if req.AgentEnabled != nil {
			plan.AgentEnabled = *req.AgentEnabled
		}
		if req.FileToolsEnabled != nil {
			plan.FileToolsEnabled = *req.FileToolsEnabled
		}
		if req.SortOrder != nil {
			plan.SortOrder = *req.SortOrder
		}
		if req.IsEnabled != nil {
			plan.IsEnabled = *req.IsEnabled
		}
		if req.Description != nil {
			plan.Description = *req.Description
		}
		plan.UpdatedAt = time.Now().Unix()
		respondSuccess(w, planToMap(plan))
	case http.MethodDelete:
		newList := make([]*VIPPlan, 0, len(vipPlans))
		for _, p := range vipPlans {
			if p.ID != id {
				newList = append(newList, p)
			}
		}
		vipPlans = newList
		respondSuccess(w, nil)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func adminVIPSubscriptionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	mu.RLock()
	list := make([]*VIPSubscription, len(vipSubs))
	copy(list, vipSubs)
	mu.RUnlock()
	sort.Slice(list, func(i, j int) bool {
		return list[i].StartedAt > list[j].StartedAt
	})
	total := int64(len(list))
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > len(list) {
		start = len(list)
	}
	if end > len(list) {
		end = len(list)
	}
	items := make([]map[string]interface{}, 0, len(list[start:end]))
	for _, s := range list[start:end] {
		items = append(items, map[string]interface{}{
			"id":            s.ID,
			"user_id":       s.UserID,
			"plan_id":       s.PlanID,
			"level":         s.Level,
			"price_fen":     s.PriceFen,
			"duration_days": s.DurationDays,
			"started_at":    time.Unix(s.StartedAt, 0).Format(time.RFC3339),
			"expires_at":    time.Unix(s.ExpiresAt, 0).Format(time.RFC3339),
		})
	}
	respondSuccess(w, map[string]interface{}{
		"total": total,
		"items": items,
	})
}

func adminGrantVIPHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	defer saveData()
	id := strings.TrimPrefix(r.URL.Path, "/v1/admin/users/")
	id = strings.TrimSuffix(id, "/vip")
	var req struct {
		PlanID string `json:"plan_id"`
		Months int    `json:"months"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误")
		return
	}
	mu.Lock()
	defer mu.Unlock()
	user := users[id]
	if user == nil {
		respondError(w, 4004, "用户不存在")
		return
	}
	plan := vipPlanByID(req.PlanID)
	if plan == nil {
		respondError(w, 4004, "套餐不存在")
		return
	}
	if req.Months <= 0 {
		req.Months = 1
	}
	activateVIPForUser(id, plan, plan.DurationDays*req.Months)
	respondSuccess(w, map[string]interface{}{
		"user_id":       user.ID,
		"vip_level":     user.VIPLevel,
		"vip_expire_at": time.Unix(user.VIPExpireAt, 0).Format(time.RFC3339),
	})
}

func resolvePackageAmount(packageID string, quantity int) (amount int64, danwan int64, errMsg string) {
	if quantity < 1 {
		quantity = 1
	}
	pkg := rechargePackages[packageID]
	if pkg == nil {
		return 0, 0, "套餐不存在"
	}
	if !pkg.IsEnabled {
		return 0, 0, "套餐已下架"
	}
	if pkg.IsCustomMultiplier {
		if pkg.BasePackageID == nil || *pkg.BasePackageID == "" {
			return 0, 0, "自定义数量套餐未配置基础套餐"
		}
		base := rechargePackages[*pkg.BasePackageID]
		if base == nil {
			return 0, 0, "基础套餐不存在"
		}
		if !base.IsEnabled {
			return 0, 0, "基础套餐已下架"
		}
		return base.PriceFen * int64(quantity), base.Danwan * int64(quantity), ""
	}
	return pkg.PriceFen * int64(quantity), pkg.Danwan * int64(quantity), ""
}

func wechatPrepayHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	defer saveData()
	uid := userIDFrom(r)
	if uid == "" {
		respondError(w, 2001, "未登录")
		return
	}
	var req struct {
		PackageID string `json:"package_id"`
		Quantity  int    `json:"quantity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误")
		return
	}
	mu.Lock()
	defer mu.Unlock()
	amount, danwan, errMsg := resolvePackageAmount(req.PackageID, req.Quantity)
	if errMsg != "" {
		respondError(w, 1001, errMsg)
		return
	}
	orderID := newUUID()
	orders = append(orders, &AdminOrder{
		ID:        orderID,
		UserID:    uid,
		Channel:   "wechat",
		Amount:    amount,
		Currency:  "danwan",
		Status:    "pending",
		CreatedAt: time.Now().Unix(),
	})
	respondSuccess(w, map[string]interface{}{
		"order_id":   orderID,
		"appId":      "wx_test_app_id",
		"partnerId":  "test_mch_id",
		"prepayId":   "wx_test_prepay_id",
		"package":    "Sign=WXPay",
		"nonceStr":   randomString(32),
		"timeStamp":  fmt.Sprintf("%d", time.Now().Unix()),
		"sign":       "TEST_SIGN",
		"amount_fen": amount,
		"danwan":     danwan,
	})
}

func alipayOrderHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	defer saveData()
	uid := userIDFrom(r)
	if uid == "" {
		respondError(w, 2001, "未登录")
		return
	}
	var req struct {
		PackageID string `json:"package_id"`
		Quantity  int    `json:"quantity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误")
		return
	}
	mu.Lock()
	defer mu.Unlock()
	amount, danwan, errMsg := resolvePackageAmount(req.PackageID, req.Quantity)
	if errMsg != "" {
		respondError(w, 1001, errMsg)
		return
	}
	orderID := newUUID()
	orders = append(orders, &AdminOrder{
		ID:        orderID,
		UserID:    uid,
		Channel:   "alipay",
		Amount:    amount,
		Currency:  "danwan",
		Status:    "pending",
		CreatedAt: time.Now().Unix(),
	})
	respondSuccess(w, map[string]interface{}{
		"order_id":     orderID,
		"order_string": "app_id=支付宝_APP_ID&biz_content=...&sign=TEST_SIGN",
		"amount_fen":   amount,
		"danwan":       danwan,
	})
}

// alipayPrecreateHandler 支付宝扫码预下单（收银台二维码），与正式网关同契约。
// E2E 环境返回固定假 qr_code；支付成功通过 POST /v1/payment/alipay/notify 模拟。
func alipayPrecreateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	defer saveData()
	uid := userIDFrom(r)
	if uid == "" {
		respondError(w, 2001, "未登录")
		return
	}
	var req struct {
		PackageID string `json:"package_id"`
		OrderID   string `json:"order_id"`
		Quantity  int    `json:"quantity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, 1001, "参数错误")
		return
	}
	mu.Lock()
	defer mu.Unlock()

	var order *AdminOrder
	if req.OrderID != "" {
		// 复用已创建订单（VIP 等场景）
		for _, o := range orders {
			if o.ID == req.OrderID {
				order = o
				break
			}
		}
		if order == nil {
			respondError(w, 1001, "订单不存在")
			return
		}
		if order.UserID != uid {
			respondError(w, 1001, "订单归属不一致")
			return
		}
		if order.Status != "pending" {
			respondError(w, 1001, "订单状态不正确")
			return
		}
		order.Channel = "alipay"
	} else {
		if req.PackageID == "" {
			respondError(w, 1001, "package_id 与 order_id 至少提供一个")
			return
		}
		amount, _, errMsg := resolvePackageAmount(req.PackageID, req.Quantity)
		if errMsg != "" {
			respondError(w, 1001, errMsg)
			return
		}
		order = &AdminOrder{
			ID:          newUUID(),
			UserID:      uid,
			Channel:     "alipay",
			Amount:      amount,
			Currency:    "danwan",
			Status:      "pending",
			ProductType: "recharge",
			CreatedAt:   time.Now().Unix(),
		}
		orders = append(orders, order)
	}
	respondSuccess(w, map[string]interface{}{
		"order_id":   order.ID,
		"qr_code":    "https://qr.alipay.com/e2e-mock-" + order.ID,
		"amount_fen": order.Amount,
		"status":     order.Status,
	})
}

// orderStatusHandler 查询订单支付状态（收银台轮询），与正式网关同契约。
func orderStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	uid := userIDFrom(r)
	if uid == "" {
		respondError(w, 2001, "未登录")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/orders/")
	id = strings.TrimSuffix(id, "/status")
	if id == "" {
		respondError(w, 1001, "订单 ID 不能为空")
		return
	}
	mu.RLock()
	defer mu.RUnlock()
	for _, o := range orders {
		if o.ID == id {
			if o.UserID != uid {
				respondError(w, 1001, "订单归属不一致")
				return
			}
			var paidAt interface{}
			if o.PaidAt != nil {
				paidAt = time.Unix(*o.PaidAt, 0).UTC().Format(time.RFC3339)
			}
			productType := o.ProductType
			if productType == "" {
				productType = "recharge" // 与正式网关默认值对齐
			}
			respondSuccess(w, map[string]interface{}{
				"order_id":     o.ID,
				"status":       o.Status,
				"product_type": productType,
				"amount_fen":   o.Amount,
				"paid_at":      paidAt,
			})
			return
		}
	}
	respondError(w, 1001, "订单不存在")
}

// alipayNotifyHandler 支付宝异步通知 mock（免验签），与正式网关同路径。
// 供 Playwright 直接模拟支付宝回调：trade_status=TRADE_SUCCESS 时幂等发放权益。
func alipayNotifyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	defer saveData()
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("fail"))
		return
	}
	outTradeNo := r.FormValue("out_trade_no")
	tradeStatus := r.FormValue("trade_status")
	tradeNo := r.FormValue("trade_no")
	totalAmount := r.FormValue("total_amount")

	mu.Lock()
	defer mu.Unlock()
	var order *AdminOrder
	for _, o := range orders {
		if o.ID == outTradeNo {
			order = o
			break
		}
	}
	if order == nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("fail"))
		return
	}
	// 非支付成功状态：ack 但不落单（与正式网关语义一致）
	if tradeStatus != "TRADE_SUCCESS" && tradeStatus != "TRADE_FINISHED" {
		_, _ = w.Write([]byte("success"))
		return
	}
	// 金额校验：total_amount（元）换算分后必须与订单金额一致
	var yuan float64
	if _, err := fmt.Sscanf(totalAmount, "%f", &yuan); err != nil || int64(yuan*100+0.5) != order.Amount {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("fail"))
		return
	}
	// 幂等：已支付订单直接 ack
	if order.Status == "paid" {
		_, _ = w.Write([]byte("success"))
		return
	}
	now := time.Now().Unix()
	order.Status = "paid"
	order.PaidAt = &now
	order.TradeNo = tradeNo
	if order.ProductType == "vip" {
		if plan := vipPlanByID(order.ProductID); plan != nil {
			activateVIPForUser(order.UserID, plan, plan.DurationDays)
		}
	} else {
		// 默认弹丸充值：与管理员确认收款同语义
		balances[order.UserID] += order.Amount
	}
	_, _ = w.Write([]byte("success"))
}

// ============================================================================
//  版本发布（Release）接口
//  读取 releases/android/manifest.json，与正式网关行为保持一致
// ============================================================================

type E2EReleaseVersion struct {
	Version        string `json:"version"`
	Channel        string `json:"channel"`
	Path           string `json:"path"`
	ReleaseDate    string `json:"releaseDate"`
	Changelog      string `json:"changelog"`
	Size           int64  `json:"size"`
	ChecksumSha256 string `json:"checksumSha256"`
}

type E2EReleaseManifest struct {
	SchemaVersion  string                       `json:"schemaVersion"`
	Platform       string                       `json:"platform"`
	Current        map[string]string            `json:"current"`
	DefaultChannel string                       `json:"defaultChannel"`
	Versions       map[string]E2EReleaseVersion `json:"versions"`
}

func loadAndroidManifest() (*E2EReleaseManifest, error) {
	// 尝试多个候选路径，兼容从 gateway/ 或项目根目录启动
	candidates := []string{
		"releases/android/manifest.json",
		"../releases/android/manifest.json",
	}
	if root := os.Getenv("RELEASE_ROOT_PATH"); root != "" {
		candidates = append([]string{filepath.Join(root, "android", "manifest.json")}, candidates...)
	}

	var data []byte
	var err error
	for _, path := range candidates {
		data, err = os.ReadFile(path)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	var m E2EReleaseManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m.DefaultChannel == "" {
		if _, ok := m.Current["stable"]; ok {
			m.DefaultChannel = "stable"
		} else {
			for k := range m.Current {
				m.DefaultChannel = k
				break
			}
		}
	}
	return &m, nil
}

func e2eGetAndroidManifestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	manifest, err := loadAndroidManifest()
	if err != nil {
		respondError(w, 5001, "发布清单加载失败")
		return
	}
	respondSuccess(w, manifest)
}

func e2eDownloadAndroidHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	manifest, err := loadAndroidManifest()
	if err != nil {
		respondError(w, 5001, "发布清单加载失败")
		return
	}

	version := r.URL.Query().Get("version")
	channel := r.URL.Query().Get("channel")

	var ver E2EReleaseVersion
	var ok bool
	if version != "" {
		ver, ok = manifest.Versions[version]
		if !ok {
			respondError(w, 4041, "版本不存在: "+version)
			return
		}
	} else if channel != "" {
		v, cok := manifest.Current[channel]
		if !cok {
			respondError(w, 4041, "通道不存在: "+channel)
			return
		}
		ver, ok = manifest.Versions[v]
		if !ok {
			respondError(w, 4041, "通道没有可用版本")
			return
		}
	} else {
		v, cok := manifest.Current[manifest.DefaultChannel]
		if !cok {
			respondError(w, 4041, "没有可用版本")
			return
		}
		ver, ok = manifest.Versions[v]
		if !ok {
			respondError(w, 4041, "没有可用版本")
			return
		}
	}

	// 定位产物文件，支持 RELEASE_ROOT_PATH 覆盖
	releaseRoot := os.Getenv("RELEASE_ROOT_PATH")
	if releaseRoot == "" {
		// 先尝试项目根目录，再尝试 gateway/ 目录
		if _, err := os.Stat("../releases/android/manifest.json"); err == nil {
			releaseRoot = "../releases"
		} else {
			releaseRoot = "releases"
		}
	}
	filePath := filepath.Join(releaseRoot, "android", ver.Path)
	cleanPath := filepath.Clean(filePath)
	cleanRoot := filepath.Clean(releaseRoot)
	rel, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		respondError(w, 4031, "非法下载路径")
		return
	}

	info, err := os.Stat(cleanPath)
	if err != nil {
		respondError(w, 4042, "文件不存在: "+ver.Path)
		return
	}
	if info.IsDir() {
		respondError(w, 4042, "路径是目录")
		return
	}

	filename := filepath.Base(ver.Path)
	if filename == "" || filename == "." {
		filename = "eleball-" + ver.Version + ".apk"
	}

	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.QueryEscape(filename))
	if ver.ChecksumSha256 != "" {
		w.Header().Set("X-Content-SHA256", ver.ChecksumSha256)
	}
	w.Header().Set("X-Release-Version", ver.Version)
	w.Header().Set("X-Release-Channel", ver.Channel)
	http.ServeFile(w, r, cleanPath)
}

// sttHandler E2E 语音识别 Mock 接口
// 不调用真实百度 API，直接返回固定文本，并模拟 ASR 月度额度校验。
func sttHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		respondError(w, 1001, "解析表单失败: "+err.Error())
		return
	}

	_, _, err := r.FormFile("file")
	if err != nil {
		respondError(w, 1001, "请上传音频文件: "+err.Error())
		return
	}

	// ASR 额度校验
	uid := userIDFrom(r)
	mu.Lock()
	user := users[uid]
	if user != nil {
		now := time.Now()
		resetAt := time.Unix(user.AsrQuotaResetAt, 0)
		if resetAt.Year() != now.Year() || resetAt.Month() != now.Month() {
			user.AsrQuotaUsed = 0
			user.AsrQuotaResetAt = now.Unix()
		}
		if user.AsrQuotaUsed >= user.AsrQuotaMonthly {
			mu.Unlock()
			respondError(w, 3004, "本月语音识别额度已用完")
			return
		}
		user.AsrQuotaUsed++
		defer saveData()
	}
	mu.Unlock()

	// E2E 环境下返回固定识别结果，便于断言
	respondSuccess(w, map[string]interface{}{
		"text":     "这是一段来自 Eleball E2E 测试的语音输入",
		"provider": "baidu",
	})
}

// ============================================================================
//  主函数
// ============================================================================

func main() {
	// E2E 服务器默认开启管理后台，方便本地调试；也可通过 --enable-admin=false 关闭以模拟生产关闭态。
	enableAdmin := flag.Bool("enable-admin", true, "启用 /v1/admin 管理后台接口与 /admin/ 静态页面（默认开启）")
	flag.Parse()

	mux := http.NewServeMux()

	// 公开接口
	mux.HandleFunc("/health", cors(healthHandler))
	mux.HandleFunc("/v1/auth/register", cors(registerHandler))
	mux.HandleFunc("/v1/auth/login", cors(loginHandler))
	mux.HandleFunc("/v1/auth/refresh", cors(refreshHandler))
	mux.HandleFunc("/v1/auth/email/otp/send", cors(sendEmailOTPE2E))
	mux.HandleFunc("/v1/auth/email/login", cors(emailLoginE2E))
	mux.HandleFunc("/v1/auth/me", cors(jwtAuth(meHandler)))

	// 需要认证的接口
	mux.HandleFunc("/v1/chat/completions", cors(jwtAuth(chatHandler)))
	mux.HandleFunc("/v1/billing/balance", cors(jwtAuth(balanceHandler)))
	mux.HandleFunc("/v1/billing/recharge-history", cors(jwtAuth(rechargeHistoryHandler)))
	mux.HandleFunc("/v1/eleagent/credentials", cors(jwtAuth(eleagentCredentialsHandler)))
	mux.HandleFunc("/v1/eleagent/models", cors(eleagentModelsHandler))
	mux.HandleFunc("/v1/stt", cors(jwtAuth(sttHandler)))

	// 视觉生成（图片/视频）E2E 模拟
	mux.HandleFunc("/v1/visual/conversations", cors(jwtAuth(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			e2eVisualCreateConversationHandler(w, r)
		case http.MethodGet:
			e2eVisualListConversationsHandler(w, r)
		default:
			respondError(w, 1001, "方法不支持")
		}
	})))
	mux.HandleFunc("/v1/visual/conversations/", cors(jwtAuth(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v1/visual/conversations/")
		parts := strings.SplitN(path, "/", 2)
		id := parts[0]
		switch r.Method {
		case http.MethodGet:
			e2eVisualGetConversationHandler(w, r, id)
		case http.MethodPatch:
			e2eVisualUpdateConversationHandler(w, r, id)
		case http.MethodDelete:
			e2eVisualDeleteConversationHandler(w, r, id)
		default:
			respondError(w, 1001, "方法不支持")
		}
	})))
	mux.HandleFunc("/v1/visual/generations", cors(jwtAuth(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			e2eVisualCreateHandler(w, r)
		default:
			respondError(w, 1001, "方法不支持")
		}
	})))
	mux.HandleFunc("/v1/visual/generations/", cors(jwtAuth(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v1/visual/generations/")
		parts := strings.SplitN(path, "/", 2)
		id := parts[0]
		sub := ""
		if len(parts) > 1 {
			sub = parts[1]
		}
		switch r.Method {
		case http.MethodGet:
			e2eVisualGetHandler(w, r, id)
		case http.MethodPost:
			if sub == "cancel" {
				e2eVisualCancelHandler(w, r, id)
			} else {
				respondError(w, 4004, "路径不存在")
			}
		default:
			respondError(w, 1001, "方法不支持")
		}
	})))
	mux.HandleFunc("/v1/visual/upload", cors(jwtAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			e2eVisualUploadHandler(w, r)
		} else {
			respondError(w, 1001, "方法不支持")
		}
	})))
	mux.HandleFunc("/v1/visual/files/", cors(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			respondError(w, 1001, "方法不支持")
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/v1/visual/files/")
		e2eVisualFileHandler(w, r, id)
	}))

	mux.HandleFunc("/v1/recharge/packages", cors(jwtAuth(rechargePackagesHandler)))
	mux.HandleFunc("/v1/payment/wechat/prepay", cors(jwtAuth(wechatPrepayHandler)))
	mux.HandleFunc("/v1/payment/alipay/order", cors(jwtAuth(alipayOrderHandler)))
	mux.HandleFunc("/v1/payment/alipay/precreate", cors(jwtAuth(alipayPrecreateHandler)))
	mux.HandleFunc("/v1/orders/", cors(jwtAuth(orderStatusHandler)))
	// 支付宝异步通知 mock（免验签，供自动化测试模拟回调），与正式网关同路径
	mux.HandleFunc("/v1/payment/alipay/notify", cors(alipayNotifyHandler))
	mux.HandleFunc("/v1/vip/plans", cors(jwtAuth(vipPlansHandler)))
	mux.HandleFunc("/v1/vip/status", cors(jwtAuth(vipStatusHandler)))
	mux.HandleFunc("/v1/vip/subscribe", cors(jwtAuth(vipSubscribeHandler)))

	// 管理员接口
	if *enableAdmin {
		mux.HandleFunc("/v1/admin/stats", cors(adminAuth(adminStatsHandler)))
		mux.HandleFunc("/v1/admin/stats/dau", cors(adminAuth(adminDauHandler)))
		mux.HandleFunc("/v1/admin/stats/token-usage", cors(adminAuth(adminTokenUsageHandler)))
		mux.HandleFunc("/v1/admin/activities", cors(adminAuth(adminActivitiesHandler)))
		mux.HandleFunc("/v1/admin/users", cors(adminAuth(adminUsersHandler)))
		mux.HandleFunc("/v1/admin/users/", cors(adminAuth(adminUserItemHandler)))
		mux.HandleFunc("/v1/admin/billing/transactions", cors(adminAuth(adminTransactionsHandler)))
		mux.HandleFunc("/v1/admin/billing/recharge", cors(adminAuth(adminRechargeHandler)))
		cdkAdminHandler := func(w http.ResponseWriter, r *http.Request) {
			// 该前缀模式匹配 /v1/admin/cdk、/v1/admin/cdk/batch、/v1/admin/cdk/{id}
			path := strings.TrimPrefix(r.URL.Path, "/v1/admin/cdk")
			path = strings.TrimPrefix(path, "/")
			if path == "" {
				switch r.Method {
				case http.MethodGet:
					adminCDKListHandler(w, r)
				default:
					http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
				}
				return
			}
			if path == "batch" {
				if r.Method == http.MethodPost {
					adminCDKBatchHandler(w, r)
					return
				}
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
				return
			}
			// /v1/admin/cdk/{id}
			r = r.WithContext(context.WithValue(r.Context(), ctxCDKIDKey, path))
			adminCDKItemHandler(w, r)
		}
		mux.HandleFunc("/v1/admin/cdk", cors(adminAuth(cdkAdminHandler)))
		mux.HandleFunc("/v1/admin/cdk/", cors(adminAuth(cdkAdminHandler)))
		mux.HandleFunc("/v1/admin/orders", cors(adminAuth(adminOrdersHandler)))
		mux.HandleFunc("/v1/admin/orders/", cors(adminAuth(adminOrderItemHandler)))
		mux.HandleFunc("/v1/admin/withdrawals", cors(adminAuth(adminWithdrawalsHandler)))
		mux.HandleFunc("/v1/admin/withdrawals/", cors(adminAuth(adminWithdrawalItemHandler)))
		mux.HandleFunc("/v1/admin/agents", cors(adminAuth(adminAgentsHandler)))
		mux.HandleFunc("/v1/admin/agents/", cors(adminAuth(adminAgentItemHandler)))
		mux.HandleFunc("/v1/admin/settings", cors(adminAuth(adminSettingsHandler)))
		mux.HandleFunc("/v1/admin/recharge/packages", cors(adminAuth(adminRechargePackagesHandler)))
		mux.HandleFunc("/v1/admin/recharge/packages/", cors(adminAuth(adminRechargePackageItemHandler)))
		mux.HandleFunc("/v1/admin/vip/plans", cors(adminAuth(adminVIPPlansHandler)))
		mux.HandleFunc("/v1/admin/vip/plans/", cors(adminAuth(adminVIPPlanItemHandler)))
		mux.HandleFunc("/v1/admin/vip/subscriptions", cors(adminAuth(adminVIPSubscriptionsHandler)))
		mux.HandleFunc("/v1/admin/eleagent/models", cors(adminAuth(adminEleAgentModelsHandler)))
		mux.HandleFunc("/v1/admin/eleagent/models/export", cors(adminAuth(adminExportEleAgentModels)))
		mux.HandleFunc("/v1/admin/eleagent/models/import", cors(adminAuth(adminImportEleAgentModels)))
		mux.HandleFunc("/v1/admin/eleagent/models/", cors(adminAuth(adminEleAgentModelItemHandler)))

		// 集市模块 / 动态驱动管理
		modulesAdminHandler := func(w http.ResponseWriter, r *http.Request) {
			path := strings.TrimPrefix(r.URL.Path, "/v1/admin/modules")
			path = strings.TrimPrefix(path, "/")
			if path == "" {
				switch r.Method {
				case http.MethodGet:
					adminListModulesHandler(w, r)
				case http.MethodPost:
					adminRegisterModuleHandler(w, r)
				default:
					http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
				}
				return
			}
			if strings.HasSuffix(path, "/refresh") {
				if r.Method == http.MethodPost {
					adminRefreshModuleHandler(w, r)
					return
				}
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
				return
			}
			if strings.HasSuffix(path, "/rescan") {
				if r.Method == http.MethodPost {
					adminRescanMarketplaceHandler(w, r)
					return
				}
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
				return
			}
			switch r.Method {
			case http.MethodGet:
				adminGetModuleHandler(w, r)
			case http.MethodDelete:
				adminUnregisterModuleHandler(w, r)
			default:
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			}
		}
		mux.HandleFunc("/v1/admin/modules", cors(adminAuth(modulesAdminHandler)))
		mux.HandleFunc("/v1/admin/modules/", cors(adminAuth(modulesAdminHandler)))

		driversAdminHandler := func(w http.ResponseWriter, r *http.Request) {
			path := strings.TrimPrefix(r.URL.Path, "/v1/admin/drivers")
			path = strings.TrimPrefix(path, "/")
			if path == "" {
				switch r.Method {
				case http.MethodGet:
					adminListDriversHandler(w, r)
				case http.MethodPost:
					adminRegisterDriverHandler(w, r)
				default:
					http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
				}
				return
			}
			if r.Method == http.MethodDelete {
				adminUnregisterDriverHandler(w, r)
				return
			}
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
		mux.HandleFunc("/v1/admin/drivers", cors(adminAuth(driversAdminHandler)))
		mux.HandleFunc("/v1/admin/drivers/", cors(adminAuth(driversAdminHandler)))
	}
	mux.HandleFunc("/v1/public/settings", cors(jwtAuth(publicSettingsHandler)))
	mux.HandleFunc("/v1/cdk/redeem", cors(jwtAuth(cdkRedeemHandler)))

	// 插件自助注册（无需登录，需校验 auth_token）
	mux.HandleFunc("/v1/market/modules/register", cors(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		registerModuleFromPluginHandler(w, r)
	}))

	mux.HandleFunc("/v1/agents", cors(optionalJwtAuth(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			listAgentsHandler(w, r)
		default:
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})))
	mux.HandleFunc("/v1/agents/", cors(jwtAuth(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/purchase"):
			purchaseAgentHandler(w, r)
		case strings.HasSuffix(path, "/active"):
			toggleActiveHandler(w, r)
		case strings.HasSuffix(path, "/reviews"):
			if r.Method == http.MethodGet {
				listReviewsHandler(w, r)
			} else {
				createReviewHandler(w, r)
			}
		case strings.HasSuffix(path, "/favorite"):
			toggleFavoriteHandler(w, r)
		case strings.HasSuffix(path, "/credentials"):
			agentCredentialsHandler(w, r)
		default:
			getAgentHandler(w, r)
		}
	})))
	mux.HandleFunc("/v1/market/categories", cors(jwtAuth(categoriesHandler)))
	mux.HandleFunc("/v1/space", cors(jwtAuth(userSpaceHandler)))
	mux.HandleFunc("/v1/developer/account", cors(jwtAuth(developerAccountHandler)))
	mux.HandleFunc("/v1/capabilities", cors(jwtAuth(capabilitiesHandler)))
	mux.HandleFunc("/v1/developer/withdrawals", cors(jwtAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			listMyWithdrawalsHandler(w, r)
		} else {
			applyWithdrawalHandler(w, r)
		}
	})))
	mux.HandleFunc("/v1/sync/push", cors(jwtAuth(syncPushHandler)))
	mux.HandleFunc("/v1/sync/pull", cors(jwtAuth(syncPullHandler)))

	// 对话历史
	mux.HandleFunc("/v1/conversations", cors(jwtAuth(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			e2eListConversationsHandler(w, r)
		case http.MethodPost:
			e2eCreateConversationHandler(w, r)
		default:
			respondError(w, 1001, "方法不支持")
		}
	})))
	mux.HandleFunc("/v1/conversations/", cors(jwtAuth(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v1/conversations/")
		parts := strings.SplitN(path, "/", 2)
		id := parts[0]
		sub := ""
		if len(parts) > 1 {
			sub = parts[1]
		}
		switch r.Method {
		case http.MethodGet:
			if sub == "" {
				e2eGetConversationHandler(w, r, id)
			} else if sub == "messages" {
				e2eListMessagesHandler(w, r, id)
			} else {
				respondError(w, 4004, "路径不存在")
			}
		case http.MethodPatch:
			e2eUpdateConversationHandler(w, r, id)
		case http.MethodDelete:
			e2eDeleteConversationHandler(w, r, id)
		case http.MethodPost:
			if sub == "messages" {
				e2eCreateMessageHandler(w, r, id)
			} else {
				respondError(w, 4004, "路径不存在")
			}
		default:
			respondError(w, 1001, "方法不支持")
		}
	})))

	// 对话分组（Agent Team）
	mux.HandleFunc("/v1/teams", cors(jwtAuth(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			e2eListTeamsHandler(w, r)
		case http.MethodPost:
			e2eCreateTeamHandler(w, r)
		default:
			respondError(w, 1001, "方法不支持")
		}
	})))
	mux.HandleFunc("/v1/teams/", cors(jwtAuth(func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/v1/teams/")
		// 组共享记忆子路由（Agent Team P2）：/v1/teams/:id/memories[/:memoryId]
		if parts := strings.Split(rest, "/"); len(parts) >= 2 && parts[1] == "memories" {
			teamID := parts[0]
			switch {
			case len(parts) == 2 && r.Method == http.MethodGet:
				e2eListTeamMemoriesHandler(w, r, teamID)
			case len(parts) == 2 && r.Method == http.MethodPost:
				e2eCreateTeamMemoryHandler(w, r, teamID)
			case len(parts) == 3 && r.Method == http.MethodDelete:
				e2eDeleteTeamMemoryHandler(w, r, teamID, parts[2])
			default:
				respondError(w, 1001, "方法不支持")
			}
			return
		}
		id := rest
		if id == "" || strings.Contains(id, "/") {
			respondError(w, 4004, "路径不存在")
			return
		}
		switch r.Method {
		case http.MethodGet:
			e2eGetTeamHandler(w, r, id)
		case http.MethodPatch:
			e2eUpdateTeamHandler(w, r, id)
		case http.MethodDelete:
			e2eDeleteTeamHandler(w, r, id)
		default:
			respondError(w, 1001, "方法不支持")
		}
	})))

	// Agent 工作流
	mux.HandleFunc("/v1/agent/execute", cors(jwtAuth(e2eAgentExecuteHandler)))
	mux.HandleFunc("/v1/agent/search-providers", cors(jwtAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			respondError(w, 1001, "方法不支持")
			return
		}
		respondSuccess(w, []map[string]string{
			{"name": "baidu", "label": "百度"},
			{"name": "bing", "label": "Bing"},
		})
	})))
	mux.HandleFunc("/v1/agent/sessions", cors(jwtAuth(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			respondSuccess(w, map[string]interface{}{"total": 0, "items": []interface{}{}})
		case http.MethodDelete:
			respondSuccess(w, nil)
		default:
			respondError(w, 1001, "方法不支持")
		}
	})))
	mux.HandleFunc("/v1/agent/sessions/", cors(jwtAuth(func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/v1/agent/sessions/")
		// rest 形如 "{id}" / "{id}/audit" / "{id}/fork"
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) == 2 && parts[1] == "fork" && r.Method == http.MethodPost {
			// AR-12：e2e 不持久化 agent_sessions，返回桩 Session 保证 fork 契约兼容
			respondSuccess(w, map[string]interface{}{
				"id":                     "e2e-fork-" + parts[0],
				"conversation_id":        "e2e-fork-conv-" + parts[0],
				"parent_entry_id":        "e2e-entry",
				"forked_from_session_id": parts[0],
				"title":                  "E2E Fork",
				"status":                 "succeeded",
			})
			return
		}
		switch r.Method {
		case http.MethodGet:
			respondError(w, 4004, "Session 不存在")
		case http.MethodDelete:
			respondSuccess(w, nil)
		default:
			respondError(w, 1001, "方法不支持")
		}
	})))
	mux.HandleFunc("/v1/agent/resources/", cors(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))

	// 版本发布与下载（无需登录）
	mux.HandleFunc("/v1/releases/android", cors(e2eGetAndroidManifestHandler))
	mux.HandleFunc("/v1/releases/android/download", cors(e2eDownloadAndroidHandler))

	// Admin Web 静态文件（仅在管理后台开启时注册）
	if *enableAdmin {
		mux.HandleFunc("/admin/", cors(staticFileHandler))
		mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/admin/", http.StatusFound)
		})
		// 根路径重定向到管理后台，方便直接访问 8080 端口
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				http.Redirect(w, r, "/admin/", http.StatusFound)
				return
			}
			http.NotFound(w, r)
		})
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				respondSuccess(w, nil)
				return
			}
			http.NotFound(w, r)
		})
	}

	// 管理后台前置闸门（Pre-Auth Gate）端点
	adminGate := newE2EAdminGate()
	mux.HandleFunc("/admin/knock", adminGate.knockPageHandler)
	mux.HandleFunc("/_admin_gate", adminGate.verifyHandler)
	mux.HandleFunc("/_admin_gate_check", adminGate.checkHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	localIP := getLocalIP()
	if localIP == "" {
		localIP = "192.168.1.x"
	}

	adminStatus := "已关闭"
	if *enableAdmin {
		adminStatus = "已开启"
	}
	fmt.Println("╔══════════════════════════════════════════════════════════╗")
	fmt.Println("║        Eleball E2E 本地测试服务器已启动                  ║")
	fmt.Println("╠══════════════════════════════════════════════════════════╣")
	fmt.Printf("║  API 地址:    http://localhost:%s                       ║\n", port)
	fmt.Printf("║  Health:      http://localhost:%s/health                ║\n", port)
	if *enableAdmin {
		fmt.Printf("║  Admin Web:   http://localhost:%s/admin/                ║\n", port)
	}
	fmt.Printf("║  Admin 状态:  %s                                        ║\n", adminStatus)
	fmt.Println("╠══════════════════════════════════════════════════════════╣")
	fmt.Println("║  Android 调试指南:                                       ║")
	fmt.Printf("║    模拟器:    http://10.0.2.2:%s                        ║\n", port)
	fmt.Printf("║    真机:      http://%s:%s                              ║\n", localIP, port)
	fmt.Println("╠══════════════════════════════════════════════════════════╣")
	fmt.Println("║  环境变量:                                               ║")
	fmt.Println("║    OPENAI_API_KEY     - OpenAI 代理 Key                 ║")
	fmt.Println("║    DEEPSEEK_API_KEY   - DeepSeek 代理 Key               ║")
	fmt.Println("║    MOONSHOT_API_KEY   - Moonshot 代理 Key               ║")
	fmt.Println("║    ELEAGENT_BASE_URL  - Ele Agent 代理 BaseURL          ║")
	fmt.Println("╚══════════════════════════════════════════════════════════╝")
	fmt.Println()

	if err := http.ListenAndServe(":"+port, adminGate.Middleware(mux)); err != nil {
		fmt.Printf("服务启动失败: %v\n", err)
	}
}
