package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	appservice "harukizmoe/pimoe/internal/application/service"
)

// SessionService 描述 SessionHandler 需要的业务能力。
type SessionService interface {
	Create(ctx context.Context, title string) (appservice.SessionMeta, error)
	List(ctx context.Context) ([]appservice.SessionMeta, error)
	Run(ctx context.Context, sessionID string, input string) (appservice.RunResult, error)
	Stream(ctx context.Context, sessionID string, input string) (<-chan appservice.StreamEvent, error)
}

// SessionHandler 处理 session 相关 HTTP 请求。
type SessionHandler struct {
	service SessionService
}

// NewSessionHandler 创建 session HTTP Handler。
func NewSessionHandler(service SessionService) *SessionHandler {
	return &SessionHandler{service: service}
}

type createSessionRequest struct {
	Input string `json:"input"`
	Title string `json:"title"`
}

type sessionResponse struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type sessionsResponse struct {
	Sessions []sessionResponse `json:"sessions"`
}

type runRequest struct {
	Input string `json:"input"`
}

type runResponse struct {
	Answer string             `json:"answer"`
	Steps  []toolStepResponse `json:"steps"`
}

type toolStepResponse struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Result    string          `json:"result"`
	Error     string          `json:"error,omitempty"`
}

// Create 创建一个 managed session。
func (h *SessionHandler) Create(ctx *gin.Context) {
	var req createSessionRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		writeError(ctx, http.StatusBadRequest, err.Error())
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = strings.TrimSpace(req.Input)
	}
	meta, err := h.service.Create(ctx.Request.Context(), title)
	if err != nil {
		writeError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	ctx.JSON(http.StatusCreated, newSessionResponse(meta))
}

// List 返回 managed sessions。
func (h *SessionHandler) List(ctx *gin.Context) {
	metas, err := h.service.List(ctx.Request.Context())
	if err != nil {
		writeError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	out := sessionsResponse{Sessions: make([]sessionResponse, 0, len(metas))}
	for _, meta := range metas {
		out.Sessions = append(out.Sessions, newSessionResponse(meta))
	}
	ctx.JSON(http.StatusOK, out)
}

// Run 在指定 session 上执行一轮 prompt。
func (h *SessionHandler) Run(ctx *gin.Context) {
	var req runRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		writeError(ctx, http.StatusBadRequest, err.Error())
		return
	}
	result, err := h.service.Run(ctx.Request.Context(), ctx.Param("id"), req.Input)
	if err != nil {
		writeError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	ctx.JSON(http.StatusOK, newRunResponse(result))
}

// Stream 在指定 session 上执行一轮 prompt，并以 SSE 返回事件流。
func (h *SessionHandler) Stream(ctx *gin.Context) {
	var req runRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		writeError(ctx, http.StatusBadRequest, err.Error())
		return
	}
	events, err := h.service.Stream(ctx.Request.Context(), ctx.Param("id"), req.Input)
	if err != nil {
		writeError(ctx, http.StatusInternalServerError, err.Error())
		return
	}

	ctx.Header("Content-Type", "text/event-stream")
	ctx.Header("Cache-Control", "no-cache")
	ctx.Header("Connection", "keep-alive")
	ctx.Status(http.StatusOK)
	flusher, ok := ctx.Writer.(http.Flusher)
	if !ok {
		writeError(ctx, http.StatusInternalServerError, "streaming is not supported")
		return
	}

	for event := range events {
		ctx.SSEvent(event.Name, event.Data)
		flusher.Flush()
	}
}

func newSessionResponse(meta appservice.SessionMeta) sessionResponse {
	return sessionResponse{
		ID:        meta.ID,
		Title:     meta.Title,
		CreatedAt: meta.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: meta.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func newRunResponse(result appservice.RunResult) runResponse {
	out := runResponse{Answer: result.Answer, Steps: make([]toolStepResponse, 0, len(result.ToolSteps))}
	for _, step := range result.ToolSteps {
		out.Steps = append(out.Steps, toolStepResponse{
			Tool:      step.ToolName,
			Arguments: json.RawMessage(step.Arguments),
			Result:    step.Result,
			Error:     step.Error,
		})
	}
	return out
}
