package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/suoten/jt-engine/internal/config"
)

// Auth JWT 鉴权中间件，支持 kid 多密钥轮换
// jwtSecret 为默认密钥（向后兼容无 kid 的旧 token）
// jwtCfg 为多密钥轮换配置，可为 nil
func Auth(jwtSecret string, jwtCfg *config.JWTConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path

		if strings.HasPrefix(path, "/api/v1/health") ||
			strings.HasPrefix(path, "/swagger") ||
			strings.HasPrefix(path, "/ws/") ||
			strings.HasPrefix(path, "/assets/") ||
			strings.HasPrefix(path, "/api/v1/auth/login") ||
			strings.HasPrefix(path, "/api/v1/auth/status") ||
			strings.HasPrefix(path, "/api/v1/auth/trial") ||
			strings.HasPrefix(path, "/api/v1/auth/refresh") {
			c.Next()
			return
		}

		tokenStr := c.GetHeader("Authorization")
		if tokenStr == "" {
			tokenStr = c.Query("token")
		}

		if tokenStr == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"message": "authorization required",
			})
			c.Abort()
			return
		}

		tokenStr = strings.TrimPrefix(tokenStr, "Bearer ")

		// 触发旧 kid 清理（懒清理，每小时最多一次）
		if jwtCfg != nil {
			jwtCfg.CleanupExpiredKids()
		}

		token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
			// 强制 HMAC 签名方法，防止 alg=none 攻击（与 VerifyJWT 保持一致）
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			// AUTO-FIX-2026-07-14 [ConvergeLoop-P1]: 显式区分三种 kid 路径
			// 1) token 有 kid 且配置中找到 → 用对应 secret
			// 2) token 有 kid 但配置中找不到 → 拒绝（避免密钥回退降级攻击）
			// 3) token 无 kid（旧版本兼容）→ 用默认 jwtSecret
			if kid, ok := token.Header["kid"].(string); ok && kid != "" {
				if jwtCfg != nil {
					if secret, found := jwtCfg.GetSecret(kid); found {
						return []byte(secret), nil
					}
					// 显式拒绝未注册的 kid，防止密钥回退降级
					return nil, fmt.Errorf("token kid '%s' not found in kms, possible key rotation or tampering", kid)
				}
				// 配置了 kid 但 jwtCfg 为 nil（KMS 未启用），拒绝带 kid 的 token
				return nil, fmt.Errorf("token carries kid '%s' but KMS not configured", kid)
			}
			// 无 kid 的旧 token，回退到默认 secret（仅当默认 secret 已配置时）
			if jwtSecret == "" {
				return nil, fmt.Errorf("no kid in token and default jwtSecret is empty, refuse to accept token")
			}
			return []byte(jwtSecret), nil
		})

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"message": "invalid token",
			})
			c.Abort()
			return
		}

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
			// AUTO-FIX-2026-07-02: 注入数据权限范围到 context（供列表查询 handler 使用）
			if ds, ok := claims["data_scope"].(map[string]interface{}); ok {
				info := DataScopeInfo{ScopeType: "all"}
				if st, ok := ds["scope_type"].(string); ok {
					info.ScopeType = st
				}
				if oid, ok := ds["org_id"].(string); ok {
					info.OrgID = oid
				}
				if vids, ok := ds["vehicle_ids"].([]interface{}); ok {
					for _, v := range vids {
						if s, ok := v.(string); ok {
							info.VehicleIDs = append(info.VehicleIDs, s)
						}
					}
				}
				c.Set("data_scope", info)
			}
		}

		c.Next()
	}
}

func RequirePermission(perm string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := c.Get("role")
		if roleStr, ok := role.(string); ok && roleStr == "super_admin" {
			c.Next()
			return
		}

		perms, exists := c.Get("permissions")
		if !exists {
			// AUTO-FIX-2026-07-14 [ConvergeLoop-UX]: 说明需要的权限与联系管理员路径
			c.JSON(http.StatusForbidden, gin.H{
				"code":     403,
				"message":  "permission denied: require permission '" + perm + "', contact system administrator to grant",
				"error":    "insufficient permissions",
				"required": perm,
				"hint":     "ask super_admin to assign the '" + perm + "' permission via /api/v1/admin/users",
			})
			c.Abort()
			return
		}

		permList, ok := perms.([]string)
		if !ok {
			c.JSON(http.StatusForbidden, gin.H{
				"code":     403,
				"message":  "permission denied: require permission '" + perm + "', but permission list type invalid",
				"error":    "permission list type mismatch",
				"required": perm,
				"hint":     "re-login to refresh token, or ask super_admin to assign the '" + perm + "' permission",
			})
			c.Abort()
			return
		}

		for _, p := range permList {
			if p == perm {
				c.Next()
				return
			}
		}

		c.JSON(http.StatusForbidden, gin.H{
			"code":     403,
			"message":  "permission denied: current role lacks '" + perm + "' permission",
			"error":    "insufficient permissions",
			"required": perm,
			"hint":     "ask super_admin to assign the '" + perm + "' permission via /api/v1/admin/users",
		})
		c.Abort()
	}
}

// DataScopeInfo 数据权限范围（从 JWT/RBAC 注入到 context，供列表查询使用）
// AUTO-FIX-2026-07-02: 数据权限基础实现
type DataScopeInfo struct {
	ScopeType  string   // all / org / vehicle / self
	OrgID      string   // 组织 ID
	VehicleIDs []string // 车辆 ID 列表
}

// GetDataScope 从 gin.Context 获取当前用户的数据权限范围
// 列表查询 handler 调用此方法，将返回的 ScopeType/OrgID/VehicleIDs 附加到查询条件
// 实现数据行级隔离：
//   - all: 不附加过滤条件（super_admin/admin）
//   - org: 附加 org_id = <OrgID>
//   - vehicle: 附加 vehicle_id IN (<VehicleIDs>)
//   - self: 附加 created_by = <user_id>
func GetDataScope(c *gin.Context) DataScopeInfo {
	if ds, exists := c.Get("data_scope"); exists {
		if info, ok := ds.(DataScopeInfo); ok {
			return info
		}
	}
	// 默认全部（向后兼容：未注入 data_scope 时不过滤）
	return DataScopeInfo{ScopeType: "all"}
}

// ApplyDataScopeToParams 将数据权限范围应用到查询参数 map
// handler 在构建查询条件前调用此方法，自动注入 org_id / vehicle_ids 等过滤条件
func ApplyDataScopeToParams(c *gin.Context, params map[string]string) map[string]string {
	ds := GetDataScope(c)
	if params == nil {
		params = make(map[string]string)
	}
	switch ds.ScopeType {
	case "all":
		// 不附加任何过滤
	case "org":
		if ds.OrgID != "" {
			params["org_id"] = ds.OrgID
		}
	case "vehicle":
		if len(ds.VehicleIDs) > 0 {
			// 逗号分隔的车辆 ID 列表，handler 层解析为 IN 查询
			params["vehicle_ids"] = strings.Join(ds.VehicleIDs, ",")
		}
	case "self":
		if uid, exists := c.Get("user_id"); exists {
			params["created_by"] = fmt.Sprintf("%v", uid)
		}
	}
	return params
}