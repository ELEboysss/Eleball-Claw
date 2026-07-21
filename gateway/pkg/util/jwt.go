package util

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Claims 自定义 JWT Claims
type Claims struct {
	UserID   string `json:"user_id"`
	DeviceID string `json:"device_id"`
	Role     string `json:"role"`       // user / admin
	TokenType string `json:"token_type"` // access / refresh
	jwt.RegisteredClaims
}

// JWTUtil JWT 工具类
type JWTUtil struct {
	secret            []byte
	accessExpireTime  time.Duration
	refreshExpireTime time.Duration
}

// NewJWTUtil 创建 JWT 工具实例
func NewJWTUtil(secret string, accessExpireHours, refreshExpireHours int) *JWTUtil {
	return &JWTUtil{
		secret:            []byte(secret),
		accessExpireTime:  time.Duration(accessExpireHours) * time.Hour,
		refreshExpireTime: time.Duration(refreshExpireHours) * time.Hour,
	}
}

// GenerateAccessToken 生成短期访问令牌
func (j *JWTUtil) GenerateAccessToken(userID, deviceID, role string) (string, error) {
	return j.generateToken(userID, deviceID, role, "access", j.accessExpireTime)
}

// GenerateRefreshToken 生成长期刷新令牌
func (j *JWTUtil) GenerateRefreshToken(userID, deviceID, role string) (string, error) {
	return j.generateToken(userID, deviceID, role, "refresh", j.refreshExpireTime)
}

func (j *JWTUtil) generateToken(userID, deviceID, role, tokenType string, expireTime time.Duration) (string, error) {
	claims := Claims{
		UserID:    userID,
		DeviceID:  deviceID,
		Role:      role,
		TokenType: tokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(expireTime)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			ID:        uuid.New().String(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(j.secret)
}

// ParseToken 解析并验证 JWT
func (j *JWTUtil) ParseToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return j.secret, nil
	})
	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}
	return nil, fmt.Errorf("invalid token claims")
}
