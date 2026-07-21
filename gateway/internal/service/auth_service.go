package service

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/pkg/util"
	"github.com/google/uuid"
)

// 登录安全策略常量
const (
	loginMaxAttempts   = 3                // 允许的最大连续失败次数
	loginLockDuration  = 15 * time.Minute // 超过阈值后锁定时长
	loginAttemptWindow = 15 * time.Minute // 失败次数统计窗口
)

// loginAttempt 记录某用户名的登录失败状态
type loginAttempt struct {
	failedCount  int
	lastFailedAt time.Time
	lockedUntil  time.Time
}

// DefaultModelProfile 登录/注册后推荐的默认 Ele Agent 模型配置
type DefaultModelProfile struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Provider     string `json:"provider"`
	ModelName    string `json:"model_name"`
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"api_key"`
	SystemPrompt string `json:"system_prompt"`
}

// AuthService 认证服务
type AuthService struct {
	userRepo             *repository.UserRepo
	activityService      *ActivityService
	jwtUtil              *util.JWTUtil
	eleagentBaseURL      string
	eleAgentModelService *EleAgentModelService
	loginAttempts        map[string]*loginAttempt // 内存中记录登录失败次数（分布式部署需换 Redis）
	loginMu              sync.Mutex
	otpService           *OTPService // 邮箱验证码服务（nil 表示未启用邮箱登录）
}

// NewAuthService 创建服务
// eleagentBaseURL: Ele Agent 代理 BaseURL，如 https://api.eleball.cn/v1
// eleAgentModelService: 用于动态获取当前可用的 Ele Agent 模型，避免登录默认模型写死
// otpService: 邮箱验证码服务，nil 表示未启用邮箱 OTP 登录
func NewAuthService(userRepo *repository.UserRepo, jwtUtil *util.JWTUtil, activityService *ActivityService, eleagentBaseURL string, eleAgentModelService *EleAgentModelService, otpService *OTPService) *AuthService {
	if eleagentBaseURL == "" {
		eleagentBaseURL = "https://api.eleball.cn/v1"
	}
	return &AuthService{
		userRepo:             userRepo,
		activityService:      activityService,
		jwtUtil:              jwtUtil,
		eleagentBaseURL:      eleagentBaseURL,
		eleAgentModelService: eleAgentModelService,
		loginAttempts:        make(map[string]*loginAttempt),
		otpService:           otpService,
	}
}

// RegisterRequest 注册请求
type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=3"`
	Password string `json:"password" binding:"required,min=6"`
	DeviceID string `json:"device_id" binding:"required"`
}

// LoginRequest 登录请求
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	DeviceID string `json:"device_id" binding:"required"`
}

// EmailOTPRequest 发送邮箱验证码请求
type EmailOTPRequest struct {
	Email string `json:"email" binding:"required,email"`
}

// EmailLoginRequest 邮箱验证码登录请求
type EmailLoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Code     string `json:"code" binding:"required,len=6"`
	DeviceID string `json:"device_id" binding:"required"`
}

// TokenPair 令牌对（含默认 Ele Agent 配置）
type TokenPair struct {
	AccessToken         string               `json:"access_token"`
	RefreshToken        string               `json:"refresh_token"`
	UserID              string               `json:"user_id"`
	DefaultModelProfile *DefaultModelProfile `json:"default_model_profile"`
}

// Register 用户注册
func (s *AuthService) Register(req RegisterRequest) (*TokenPair, error) {
	// 检查用户名是否已存在
	if _, err := s.userRepo.GetByUsername(req.Username); err == nil {
		return nil, errors.New("用户名已被注册")
	}

	hash, err := util.HashPassword(req.Password)
	if err != nil {
		return nil, fmt.Errorf("密码加密失败: %w", err)
	}

	user := &model.User{
		ID:       uuid.New().String(),
		Username: req.Username,
		Role:     model.UserRoleUser,
		Password: hash,
	}
	if err := s.userRepo.Create(user); err != nil {
		return nil, fmt.Errorf("创建用户失败: %w", err)
	}

	// 记录用户注册动态
	if s.activityService != nil {
		s.activityService.RecordUserRegistered(user.ID, user.Username)
	}

	return s.generateTokens(user.ID, req.DeviceID, user.Role)
}

// Login 用户登录
// 安全策略：连续失败 3 次后锁定 15 分钟
func (s *AuthService) Login(req LoginRequest) (*TokenPair, error) {
	// 检查账号是否被临时锁定
	if err := s.checkLoginLock(req.Username); err != nil {
		return nil, err
	}

	user, err := s.userRepo.GetByUsername(req.Username)
	if err != nil {
		s.recordLoginFailure(req.Username)
		return nil, errors.New("用户名或密码错误")
	}

	if !util.CheckPassword(req.Password, user.Password) {
		s.recordLoginFailure(req.Username)
		return nil, errors.New("用户名或密码错误")
	}

	// 登录成功，清除失败记录
	s.clearLoginAttempts(req.Username)

	return s.generateTokens(user.ID, req.DeviceID, user.Role)
}

// SendEmailOTP 发送邮箱验证码。限流由 OTPService 处理。
func (s *AuthService) SendEmailOTP(email, clientIP string) error {
	if s.otpService == nil {
		return errors.New("邮箱登录未开通")
	}
	return s.otpService.SendOTP(email, clientIP)
}

// EmailLogin 邮箱验证码登录：校验 OTP，存在用户则登录，不存在则创建（Email 已验证）。
func (s *AuthService) EmailLogin(req EmailLoginRequest) (*TokenPair, error) {
	if s.otpService == nil {
		return nil, errors.New("邮箱登录未开通")
	}
	if err := s.otpService.VerifyOTP(req.Email, req.Code); err != nil {
		return nil, err
	}

	// 查找现有用户
	user, err := s.userRepo.GetByEmail(req.Email)
	if err != nil {
		// 不存在 -> 自动创建
		user = &model.User{
			ID:       uuid.New().String(),
			Username: deriveUsernameFromEmail(req.Email),
			Email:    req.Email,
			Verified: true,
			Role:     model.UserRoleUser,
			// Password 留空（邮箱 OTP 用户无密码）；Username 可能重复，生成唯一值
		}
		// 确保 Username 唯一（同名加随机后缀）
		if _, err := s.userRepo.GetByUsername(user.Username); err == nil {
			user.Username = user.Username + "_" + uuid.New().String()[:8]
		}
		if err := s.userRepo.Create(user); err != nil {
			return nil, fmt.Errorf("创建用户失败: %w", err)
		}
		if s.activityService != nil {
			s.activityService.RecordUserRegistered(user.ID, user.Username)
		}
	}

	return s.generateTokens(user.ID, req.DeviceID, user.Role)
}

// deriveUsernameFromEmail 从邮箱生成默认用户名（@ 前部分）
func deriveUsernameFromEmail(email string) string {
	for i, c := range email {
		if c == '@' {
			return email[:i]
		}
	}
	return email
}

// checkLoginLock 检查用户名是否处于登录锁定状态
func (s *AuthService) checkLoginLock(username string) error {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()

	attempt, exists := s.loginAttempts[username]
	if !exists {
		return nil
	}

	// 如果超过窗口期未再失败，重置计数
	if time.Since(attempt.lastFailedAt) > loginAttemptWindow {
		delete(s.loginAttempts, username)
		return nil
	}

	// 检查是否仍在锁定时间内
	if time.Now().Before(attempt.lockedUntil) {
		remaining := time.Until(attempt.lockedUntil)
		return fmt.Errorf("登录失败次数过多，请 %d 分钟后重试", int(remaining.Minutes())+1)
	}

	return nil
}

// recordLoginFailure 记录一次登录失败
func (s *AuthService) recordLoginFailure(username string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()

	now := time.Now()
	attempt, exists := s.loginAttempts[username]
	if !exists || time.Since(attempt.lastFailedAt) > loginAttemptWindow {
		// 首次失败或超过窗口期，重新计数
		s.loginAttempts[username] = &loginAttempt{
			failedCount:  1,
			lastFailedAt: now,
		}
		return
	}

	attempt.failedCount++
	attempt.lastFailedAt = now

	if attempt.failedCount >= loginMaxAttempts {
		attempt.lockedUntil = now.Add(loginLockDuration)
	}
}

// clearLoginAttempts 清除某用户名的登录失败记录
func (s *AuthService) clearLoginAttempts(username string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	delete(s.loginAttempts, username)
}

// UserProfile 当前登录用户信息
func (s *AuthService) UserProfile(userID string) (*model.User, error) {
	return s.userRepo.GetByID(userID)
}

// Refresh 刷新 AccessToken
func (s *AuthService) Refresh(refreshToken string) (*TokenPair, error) {
	claims, err := s.jwtUtil.ParseToken(refreshToken)
	if err != nil {
		return nil, errors.New("Refresh Token 无效")
	}
	if claims.TokenType != "refresh" {
		return nil, errors.New("Token 类型错误")
	}

	user, err := s.userRepo.GetByID(claims.UserID)
	if err != nil {
		return nil, errors.New("用户不存在")
	}

	return s.generateTokens(claims.UserID, claims.DeviceID, user.Role)
}

func (s *AuthService) generateTokens(userID, deviceID, role string) (*TokenPair, error) {
	accessToken, err := s.jwtUtil.GenerateAccessToken(userID, deviceID, role)
	if err != nil {
		return nil, err
	}
	refreshToken, err := s.jwtUtil.GenerateRefreshToken(userID, deviceID, role)
	if err != nil {
		return nil, err
	}

	// 动态组装默认 Ele Agent 模型配置，避免写死模型名
	defaultProfile := s.buildDefaultModelProfile(userID)

	return &TokenPair{
		AccessToken:         accessToken,
		RefreshToken:        refreshToken,
		UserID:              userID,
		DefaultModelProfile: defaultProfile,
	}, nil
}

// buildDefaultModelProfile 根据当前启用的 Ele Agent 模型构建默认 Profile
// 若管理员未配置任何模型，则返回 nil，由前端从 /eleagent/models 重新选择或提示配置。
func (s *AuthService) buildDefaultModelProfile(userID string) *DefaultModelProfile {
	if s.eleAgentModelService == nil {
		return nil
	}

	options := s.eleAgentModelService.ListOptions()
	if len(options) == 0 {
		return nil
	}

	// 选择优先级最高（priority 最小）的模型作为默认推荐
	opt := options[0]
	for _, o := range options[1:] {
		if o.Priority < opt.Priority {
			opt = o
		}
	}
	modelName := opt.Provider + "/" + opt.ModelName
	displayName := opt.DisplayName
	if displayName == "" {
		displayName = modelName
	}

	return &DefaultModelProfile{
		ID:           "ele_agent_default_" + userID,
		Name:         displayName,
		Provider:     "eleagent",
		ModelName:    modelName,
		BaseURL:      s.eleagentBaseURL,
		APIKey:       "eleagent_" + uuid.New().String(),
		SystemPrompt: "你是 Eleball 官方智能助手 Ele Agent。",
	}
}
