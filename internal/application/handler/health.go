package handler

import "github.com/gin-gonic/gin"

// HealthHandler 处理服务健康检查。
type HealthHandler struct{}

// NewHealthHandler 创建健康检查 Handler。
func NewHealthHandler() *HealthHandler {
	return &HealthHandler{}
}

// Health 返回服务存活状态。
func (h *HealthHandler) Health(ctx *gin.Context) {
	ctx.JSON(200, gin.H{"status": "ok"})
}
