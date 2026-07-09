package handler

import "github.com/gin-gonic/gin"

type errorResponse struct {
	Error string `json:"error"`
}

func writeError(ctx *gin.Context, status int, message string) {
	ctx.JSON(status, errorResponse{Error: message})
}
