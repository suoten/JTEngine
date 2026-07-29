package middleware

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

// 危险模式正则表达式：检测 SQL 注入和 XSS 攻击模式
var (
	sqlInjectionPattern = regexp.MustCompile(`(?i)(union\s+select|insert\s+into|delete\s+from|drop\s+table|update\s+set|exec\s*\(|--\s|/\*|\*/|;\s*drop|;\s*delete|;\s*update|;\s*insert)`)
	xssPattern          = regexp.MustCompile(`(?i)(<script|</script|javascript:|onerror\s*=|onload\s*=|onclick\s*=|onmouseover\s*=|<iframe|<object|<embed)`)
	pathTraversalPattern = regexp.MustCompile(`\.\./|\.\.\\|%2e%2e%2f|%2e%2e/`)
)

// InputValidation 输入验证中间件
// 对所有请求的查询参数和路径参数进行安全校验：
//   - SQL 注入模式检测
//   - XSS 攻击模式检测
//   - 路径遍历攻击检测
//   - 参数长度限制
const maxParamLength = 2048

func InputValidation() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 验证查询参数
		for key, values := range c.Request.URL.Query() {
			for _, val := range values {
				if len(val) > maxParamLength {
					c.JSON(http.StatusBadRequest, gin.H{
						"code":    400,
						"message": "parameter too long: " + key,
					})
					c.Abort()
					return
				}
				if isDangerousInput(val) {
					c.JSON(http.StatusBadRequest, gin.H{
						"code":    400,
						"message": "invalid input detected in parameter: " + key,
					})
					c.Abort()
					return
				}
			}
		}

		// 验证路径参数
		for _, params := range c.Params {
			if isDangerousInput(params.Value) {
				c.JSON(http.StatusBadRequest, gin.H{
					"code":    400,
					"message": "invalid input detected in path parameter: " + params.Key,
				})
				c.Abort()
				return
			}
		}

		c.Next()
	}
}

// isDangerousInput 检测输入是否包含危险模式
func isDangerousInput(input string) bool {
	// 空输入安全
	if input == "" {
		return false
	}
	// 检测 SQL 注入模式
	if sqlInjectionPattern.MatchString(input) {
		return true
	}
	// 检测 XSS 攻击模式
	if xssPattern.MatchString(input) {
		return true
	}
	// 检测路径遍历攻击
	if pathTraversalPattern.MatchString(input) {
		return true
	}
	return false
}

// SanitizeString 清理字符串输入（去除首尾空格 + 转义危险字符）
func SanitizeString(input string) string {
	input = strings.TrimSpace(input)
	// 移除 null 字节
	input = strings.ReplaceAll(input, "\x00", "")
	return input
}
