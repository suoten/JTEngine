package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// AUTO-FIX-2026-07-25 [P2-R9]: 订单号白名单校验，防止 SSRF 路径注入。
// 仅允许字母、数字、中划线，长度 1-64。
var orderNoRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// maxProxyResponseBytes 限制代理官网响应体大小（1MB），防止内存耗尽 DoS。
const maxProxyResponseBytes = 1 << 20

// PurchaseHandler 代理官网购买请求。
// v3.0 核心设计：前端不直接调用官网API（跨域+密钥安全），
// JTE后端代理官网API，支付密钥只在官网后端，JTE后端只传递请求。
type PurchaseHandler struct {
	websiteURL  string
	httpClient  *http.Client
	logger      *zap.Logger
}

// NewPurchaseHandler 创建购买代理 handler。
// websiteURL 为官网后端地址（如 https://www.jtengine.cn）。
func NewPurchaseHandler(websiteURL string, logger *zap.Logger) *PurchaseHandler {
	url := strings.TrimRight(websiteURL, "/")
	if url == "" {
		url = "https://www.jtengine.cn"
	}
	return &PurchaseHandler{
		websiteURL: url,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger,
	}
}

// createOrderRequest 创建订单请求体（前端 → JTE后端）
type createOrderRequest struct {
	ModuleName    string `json:"module_name" binding:"required"`
	Duration      string `json:"duration" binding:"required"`       // 1year, 3year, permanent
	PaymentMethod string `json:"payment_method" binding:"required"` // wechat, alipay
}

// CreateOrder 创建订单
// POST /api/v1/purchase/order
// 代理到官网 POST /api/v1/orders，返回支付二维码。
// 前端不直接调用官网API，通过JTE后端代理避免跨域+密钥安全。
func (h *PurchaseHandler) CreateOrder(c *gin.Context) {
	var req createOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	// 从 JTE 的 JWT 中提取用户信息，转发给官网
	// 官网需要用户认证，这里通过 JTE 的 token 派生一个临时 token
	// 或者直接以匿名方式创建订单（官网会通过其他方式关联用户）
	token := c.GetHeader("Authorization")

	// 构建转发到官网的请求
	websiteReqBody, _ := json.Marshal(map[string]interface{}{
		"module_name":    req.ModuleName,
		"duration":       req.Duration,
		"payment_method": req.PaymentMethod,
	})

	websiteURL := h.websiteURL + "/api/v1/orders"
	httpReq, err := http.NewRequest("POST", websiteURL, strings.NewReader(string(websiteReqBody)))
	if err != nil {
		h.logger.Error("create purchase request failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to create request"})
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if token != "" {
		httpReq.Header.Set("Authorization", token)
	}

	resp, err := h.httpClient.Do(httpReq)
	if err != nil {
		h.logger.Error("proxy to website failed", zap.Error(err), zap.String("url", websiteURL))
		c.JSON(http.StatusBadGateway, gin.H{"code": 502, "message": "website service unavailable"})
		return
	}
	defer resp.Body.Close()

	// AUTO-FIX-2026-07-25 [P2-R9]: 限制响应体大小，防止内存耗尽 DoS
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProxyResponseBytes))
	if err != nil {
		h.logger.Error("read website response failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to read response"})
		return
	}

	// 透传官网响应
	c.Data(resp.StatusCode, "application/json", body)
}

// CheckOrderStatus 查询订单状态（前端轮询）
// GET /api/v1/purchase/order/:orderNo/status
// 代理到官网 GET /api/v1/orders/:orderNo/status，支付成功时返回 license_key。
func (h *PurchaseHandler) CheckOrderStatus(c *gin.Context) {
	orderNo := c.Param("orderNo")
	if orderNo == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "order number required"})
		return
	}
	// AUTO-FIX-2026-07-25 [P2-R9]: 校验 orderNo 防止 SSRF 路径注入
	if !orderNoRe.MatchString(orderNo) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid order number format"})
		return
	}

	token := c.GetHeader("Authorization")

	websiteURL := fmt.Sprintf("%s/api/v1/orders/%s/status", h.websiteURL, orderNo)
	httpReq, err := http.NewRequest("GET", websiteURL, nil)
	if err != nil {
		h.logger.Error("create status request failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to create request"})
		return
	}
	if token != "" {
		httpReq.Header.Set("Authorization", token)
	}

	resp, err := h.httpClient.Do(httpReq)
	if err != nil {
		h.logger.Error("proxy to website failed", zap.Error(err), zap.String("url", websiteURL))
		c.JSON(http.StatusBadGateway, gin.H{"code": 502, "message": "website service unavailable"})
		return
	}
	defer resp.Body.Close()

	// AUTO-FIX-2026-07-25 [P2-R9]: 限制响应体大小，防止内存耗尽 DoS
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProxyResponseBytes))
	if err != nil {
		h.logger.Error("read website response failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to read response"})
		return
	}

	// 透传官网响应（支付成功时响应中包含 license_key）
	c.Data(resp.StatusCode, "application/json", body)
}

// GetWebsiteInfo 获取官网信息（购买入口、文档地址等）
// GET /api/v1/purchase/info
// 如果官网可达，代理官网 /api/v1/website/info；否则返回本地配置的 purchase_url。
func (h *PurchaseHandler) GetWebsiteInfo(c *gin.Context) {
	websiteURL := h.websiteURL + "/api/v1/website/info"

	resp, err := h.httpClient.Get(websiteURL)
	if err != nil {
		// 官网不可达时降级返回本地配置
		h.logger.Debug("website unreachable, returning local config", zap.Error(err))
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{
				"website_url":     h.websiteURL,
				"purchase_url":    h.websiteURL + "/store",
				"docs_url":        h.websiteURL + "/docs",
				"trial_available": true,
				"online":          false,
			},
		})
		return
	}
	defer resp.Body.Close()

	// AUTO-FIX-2026-07-25 [P2-R9]: 限制响应体大小，防止内存耗尽 DoS
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProxyResponseBytes))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{
				"website_url":     h.websiteURL,
				"purchase_url":    h.websiteURL + "/store",
				"online":          false,
			},
		})
		return
	}

	c.Data(resp.StatusCode, "application/json", body)
}
