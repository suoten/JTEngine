package websocket

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/jte-engine/jte/internal/config"
	"github.com/jte-engine/jte/internal/util"
	"go.uber.org/zap"
)

// upgrader 在 CheckOrigin 中校验请求来源是否在配置的 CORS 白名单内。
// AUTO-FIX-2026-06-29: 原实现 CheckOrigin 永远返回 true，存在 CSWSH 跨站 WebSocket 劫持风险。
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			// 非浏览器客户端（如 curl、设备 SDK）无 Origin 头，允许通过
			return true
		}
		// 从配置上下文获取 CORS 白名单——此处用全局 defaultUpgraderOrigins
		return defaultUpgraderOrigins.isAllowed(origin)
	},
}

// defaultUpgraderOrigins 是全局 CORS 白名单，由 SetCORSOrigins 在启动时注入。
var defaultUpgraderOrigins = newOriginSet()

type originSet struct {
	origins map[string]bool
	allowAll bool
}

func newOriginSet() *originSet {
	return &originSet{origins: make(map[string]bool)}
}

func (o *originSet) set(origins []string) {
	o.origins = make(map[string]bool, len(origins))
	for _, origin := range origins {
		o.origins[origin] = true
		if origin == "*" {
			o.allowAll = true
		}
	}
}

func (o *originSet) isAllowed(origin string) bool {
	if o.allowAll || len(o.origins) == 0 {
		return true
	}
	return o.origins[origin]
}

// SetCORSOrigins 在启动时由 server.go 调用，注入 CORS 白名单到 upgrader。
func SetCORSOrigins(origins []string) {
	defaultUpgraderOrigins.set(origins)
}

// Upgrader 返回共享 upgrader（已配置 CORS 白名单 CheckOrigin），供其他 WebSocket
// handler（如 AI ChatWS）复用，避免每个 handler 各自实现 CheckOrigin 导致白名单失效。
// 返回指针因为 gorilla/websocket.Upgrader.Upgrade 是指针方法。
func Upgrader() *websocket.Upgrader {
	return &upgrader
}

type Handler struct {
	hub    *Hub
	cfg    *config.Config
	logger *zap.Logger
}

func NewHandler(hub *Hub, cfg *config.Config, logger *zap.Logger) *Handler {
	return &Handler{hub: hub, cfg: cfg, logger: logger}
}

// verifyToken 验证 JWT token 签名、过期时间，返回解析后的 token。
// 支持 kid 多密钥轮换：优先用 token.Header["kid"] 查找对应 secret，
// 未找到则回退到 cfg.API.JWTSecret。
func (h *Handler) verifyToken(tokenStr string) (*jwt.Token, error) {
	return jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		// 确保使用 HMAC 签名方法（防止 alg=none 攻击）
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		if kid, ok := token.Header["kid"].(string); ok && kid != "" {
			if h.cfg.API.JWT != nil {
				if secret, found := h.cfg.API.JWT.GetSecret(kid); found {
					return []byte(secret), nil
				}
			}
		}
		return []byte(h.cfg.API.JWTSecret), nil
	})
}

// extractToken 从 query 参数或 Authorization 头提取 JWT token。
// Authorization 头格式：Bearer <token>
func extractToken(c *gin.Context) string {
	token := c.Query("token")
	if token != "" {
		return token
	}
	auth := c.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return auth
}

func (h *Handler) Handle(c *gin.Context) {
	// AUTO-FIX-2026-06-29 [P0]: 原实现仅判断 token 非空且 JWTSecret 非空，
	// 从不验证签名——任意字符串（如 ?token=x）即可建立 WS 连接，
	// 匿名订阅所有车辆位置/报警/AI 告警。
	// 修复：必须调用 jwt.Parse 验证签名+过期，失败则返回 401。
	token := extractToken(c)
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "token required"})
		return
	}

	parsed, err := h.verifyToken(token)
	if err != nil || !parsed.Valid {
		h.logger.Warn("websocket token verification failed", zap.Error(err))
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "invalid or expired token"})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Error("websocket upgrade failed", zap.Error(err))
		return
	}

	clientID := fmt.Sprintf("ws_%s", conn.RemoteAddr().String())
	client := NewClient(clientID, h.hub, conn)

	h.hub.register <- client

	// 订阅主题须与 server.go 中 EventBus->wsHub.Publish 的 topic 完全一致，
	// 否则 exact-match 路由下客户端收不到实时推送。
	h.hub.Subscribe(client, "location_update")
	h.hub.Subscribe(client, "alarm_event")
	h.hub.Subscribe(client, "ai_alert")
	h.hub.Subscribe(client, "system_event")

	util.SafeGo(h.logger, "websocket.writePump", client.WritePump)
	util.SafeGo(h.logger, "websocket.readPump", client.ReadPump)
}
