package token

import (
	"errors"
	"github.com/golang-jwt/jwt/v5"
	"time"
)

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrExpiredToken = errors.New("token has expired")
	// 密钥未载入时拒绝签发 / 校验，绝不能退化成空密钥（任何人都能伪造 token）
	ErrSecretNotReady = errors.New("jwt secret not initialized")
)

type Claims struct {
	UserID   int64    `json:"user_id"`
	Username string   `json:"username"`
	Email    string   `json:"email,omitempty"`
	Roles    []string `json:"roles,omitempty"`
	// DeviceID 绑定登录时的设备：中间件校验请求方 device_id 与这里是否一致，
	// 防止 token 被拷到别的设备上重放。空值（旧 token）一律当校验失败处理。
	DeviceID string `json:"device_id"`
	// Level 权限级别（见 model.Level*）。指针：旧版签发的 token 没有这个字段，要和「游客 = 0」区分开。
	Level *int8 `json:"level,omitempty"`
	jwt.RegisteredClaims
}

// EffectiveLevel 取权限级别。旧 token（没有 level 字段）按角色推导：带 admin 的一律视为超级管理员，
// 与「升级时旧管理员全部成为超级管理员」一致；其余视为普通用户。
func (c *Claims) EffectiveLevel() int8 {
	if c.Level != nil {
		return *c.Level
	}
	for _, r := range c.Roles {
		if r == "admin" {
			return 3
		}
	}
	return 1
}

func GenerateToken(userID int64, username, email string, roles []string, deviceID string, level int8, expireDays int) (string, error) {
	return GenerateTokenAt(userID, username, email, roles, deviceID, level,
		time.Now().Add(time.Duration(expireDays)*24*time.Hour))
}

// GenerateTokenAt 指定绝对过期时间（访客账号的 token 不能活过账号本身的有效期）。
func GenerateTokenAt(userID int64, username, email string, roles []string, deviceID string, level int8, expiresAt time.Time) (string, error) {
	if len(getSecret()) == 0 {
		return "", ErrSecretNotReady
	}
	claims := Claims{
		UserID:   userID,
		Username: username,
		Email:    email,
		Roles:    roles,
		DeviceID: deviceID,
		Level:    &level,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(getSecret())
}

func ParseToken(tokenStr string) (*Claims, error) {
	if len(getSecret()) == 0 {
		return nil, ErrSecretNotReady
	}
	t, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return getSecret(), nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, ErrInvalidToken
	}
	claims, ok := t.Claims.(*Claims)
	if !ok || !t.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
