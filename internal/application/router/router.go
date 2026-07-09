package router

import (
	"github.com/gin-gonic/gin"

	"harukizmoe/pimoe/internal/application/handler"
)

// Config 保存创建 Gin router 所需的应用层 handler 依赖。
type Config struct {
	// SessionService 是 session routes 使用的业务服务。
	SessionService handler.SessionService
}

// New 创建 App API 的 Gin router。
func New(cfg Config) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	healthHandler := handler.NewHealthHandler()
	sessionHandler := handler.NewSessionHandler(cfg.SessionService)

	v1 := r.Group("/v1")
	{
		v1.GET("/health", healthHandler.Health)
		v1.GET("/providers/current", sessionHandler.CurrentProvider)
		v1.POST("/sessions", sessionHandler.Create)
		v1.GET("/sessions", sessionHandler.List)
		v1.GET("/sessions/:id", sessionHandler.Get)
		v1.POST("/sessions/:id/runs", sessionHandler.Run)
		v1.POST("/sessions/:id/runs/stream", sessionHandler.Stream)
	}
	return r
}
