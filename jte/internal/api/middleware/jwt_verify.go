package middleware

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/suoten/jt-engine/internal/config"
)

// VerifyJWT 验证 JWT token 的签名和过期时间，返回解析后的 token。
// 用于 WebSocket 等无法使用标准 Auth 中间件的场景（WebSocket 升级前需手动验签）。
// jwtSecret 为默认密钥，jwtCfg 为多密钥轮换配置（可为 nil）。
// 安全措施：
//  1. 强制 HMAC 签名方法，防止 alg=none 攻击
//  2. 支持 kid 多密钥轮换，未找到则回退到 jwtSecret
//  3. 验证 token.Valid（包含 exp 过期检查）
func VerifyJWT(tokenStr string, jwtSecret string, jwtCfg *config.JWTConfig) (*jwt.Token, error) {
	if tokenStr == "" {
		return nil, fmt.Errorf("token is empty")
	}
	return jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		// 强制 HMAC 签名方法，防止 alg=none 攻击
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		if kid, ok := token.Header["kid"].(string); ok && kid != "" {
			if jwtCfg != nil {
				if secret, found := jwtCfg.GetSecret(kid); found {
					return []byte(secret), nil
				}
			}
		}
		return []byte(jwtSecret), nil
	})
}

// ExtractAndVerifyJWT 从 gin.Context 提取 JWT token 并验证。
// 提取顺序：Authorization: Bearer <token> → query 参数 ?token=<token>
// 验证失败时返回 error 并已写入 401 响应（调用方应 return）。
// 验证成功时将 claims 写入 context（user_id, username, role, permissions）并返回 nil。
func ExtractAndVerifyJWT(c *gin.Context, jwtSecret string, jwtCfg *config.JWTConfig) error {
	tokenStr := c.GetHeader("Authorization")
	if tokenStr == "" {
		tokenStr = c.Query("token")
	}
	tokenStr = strings.TrimPrefix(tokenStr, "Bearer ")

	token, err := VerifyJWT(tokenStr, jwtSecret, jwtCfg)
	if err != nil || !token.Valid {
		c.JSON(401, gin.H{"code": 401, "message": "invalid or expired token"})
		c.Abort()
		return fmt.Errorf("token verification failed: %w", err)
	}

	// 将 claims 写入 context，与 Auth 中间件行为一致
	if claims, ok := token.Claims.(jwt.MapClaims); ok {
		if userID, ok := claims["sub"].(string); ok {
			c.Set("user_id", userID)
		}
		if username, ok := claims["username"].(string); ok {
			c.Set("username", username)
		}
		if role, ok := claims["role"].(string); ok {
			c.Set("role", role)
		}
		if perms, ok := claims["permissions"].([]interface{}); ok {
			permStrs := make([]string, 0, len(perms))
			for _, p := range perms {
				if s, ok := p.(string); ok {
					permStrs = append(permStrs, s)
				}
			}
			c.Set("permissions", permStrs)
		}
	}
	return nil
}
