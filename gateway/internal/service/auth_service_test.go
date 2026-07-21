package service

import (
	"testing"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/pkg/util"
	"github.com/stretchr/testify/assert"
	sqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupAuthService(t *testing.T) (*AuthService, *repository.UserRepo) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.AutoMigrate(&model.User{}, &model.Device{})

	jwtUtil := util.NewJWTUtil("test-secret", 24, 168)
	userRepo := repository.NewUserRepo(db)
	eleAgentModelService := NewTestEleAgentModelServiceWithConfigs([]*model.EleAgentModelConfig{
		{Provider: "qwen", Protocol: model.EleAgentUpstreamOpenAICompatible, ModelName: "Qwen/Qwen3.5-4B", DisplayName: "通义千问 Qwen3.5-4B", BaseURL: "https://api.siliconflow.cn/v1", IsEnabled: true},
	})
	authService := NewAuthService(userRepo, jwtUtil, nil, "http://localhost:8080/v1", eleAgentModelService, nil)

	return authService, userRepo
}

func TestAuthService_Register(t *testing.T) {
	svc, _ := setupAuthService(t)

	tokens, err := svc.Register(RegisterRequest{
		Username:    "user1@example.com",
		Password: "password123",
		DeviceID: "device-1",
	})

	assert.NoError(t, err)
	assert.NotNil(t, tokens)
	assert.NotEmpty(t, tokens.AccessToken)
	assert.NotEmpty(t, tokens.RefreshToken)
	assert.NotEmpty(t, tokens.UserID)
	assert.NotNil(t, tokens.DefaultModelProfile)
	assert.Equal(t, "eleagent", tokens.DefaultModelProfile.Provider)
	assert.Equal(t, "qwen/Qwen/Qwen3.5-4B", tokens.DefaultModelProfile.ModelName)
	assert.NotEmpty(t, tokens.DefaultModelProfile.APIKey)
}

func TestAuthService_RegisterDuplicate(t *testing.T) {
	svc, _ := setupAuthService(t)

	_, err := svc.Register(RegisterRequest{
		Username:    "dup@example.com",
		Password: "password123",
		DeviceID: "device-1",
	})
	assert.NoError(t, err)

	_, err = svc.Register(RegisterRequest{
		Username:    "dup@example.com",
		Password: "password456",
		DeviceID: "device-2",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "用户名已被注册")
}

func TestAuthService_Login(t *testing.T) {
	svc, _ := setupAuthService(t)

	// 先注册
	_, err := svc.Register(RegisterRequest{
		Username:    "login@example.com",
		Password: "mypassword",
		DeviceID: "device-1",
	})
	assert.NoError(t, err)

	// 再登录
	tokens, err := svc.Login(LoginRequest{
		Username:    "login@example.com",
		Password: "mypassword",
		DeviceID: "device-1",
	})
	assert.NoError(t, err)
	assert.NotNil(t, tokens)
	assert.NotEmpty(t, tokens.AccessToken)
	assert.NotNil(t, tokens.DefaultModelProfile)
	assert.Equal(t, "eleagent", tokens.DefaultModelProfile.Provider)
}

func TestAuthService_LoginWrongPassword(t *testing.T) {
	svc, _ := setupAuthService(t)

	_, _ = svc.Register(RegisterRequest{
		Username:    "wrong@example.com",
		Password: "correctpass",
		DeviceID: "device-1",
	})

	_, err := svc.Login(LoginRequest{
		Username:    "wrong@example.com",
		Password: "wrongpass",
		DeviceID: "device-1",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "用户名或密码错误")
}

func TestAuthService_Refresh(t *testing.T) {
	svc, _ := setupAuthService(t)

	// 注册获取 refresh token
	tokens, err := svc.Register(RegisterRequest{
		Username:    "refresh@example.com",
		Password: "password123",
		DeviceID: "device-1",
	})
	assert.NoError(t, err)

	// 刷新
	newTokens, err := svc.Refresh(tokens.RefreshToken)
	assert.NoError(t, err)
	assert.NotNil(t, newTokens)
	assert.NotEmpty(t, newTokens.AccessToken)
	assert.NotEqual(t, tokens.AccessToken, newTokens.AccessToken)
}
