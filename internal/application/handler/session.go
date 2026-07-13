package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	appservice "harukizmoe/pimoe/internal/application/service"
	"harukizmoe/pimoe/internal/session"
)

// SessionService 描述 SessionHandler 需要的业务能力。
type SessionService interface {
	Create(ctx context.Context, actor session.Actor, title string, opts ...appservice.CreateOptions) (appservice.SessionMeta, error)
	List(ctx context.Context, actor session.Actor) ([]appservice.SessionMeta, error)
	Get(ctx context.Context, actor session.Actor, sessionID string) (appservice.SessionDetail, error)
	Run(ctx context.Context, actor session.Actor, sessionID string, input string, opts ...appservice.RunOptions) (appservice.RunResult, error)
	Stream(ctx context.Context, actor session.Actor, sessionID string, input string, opts ...appservice.RunOptions) (<-chan appservice.StreamEvent, error)
	CurrentProviderDiagnostics(ctx context.Context) (appservice.ProviderDiagnostics, error)
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
	Input         string `json:"input"`
	Title         string `json:"title"`
	ProviderName  string `json:"provider_name"`
	MaxSteps      int    `json:"max_steps"`
	SessionPrompt string `json:"session_prompt"`
}

type sessionResponse struct {
	ID        string                `json:"id"`
	Title     string                `json:"title"`
	CreatedAt string                `json:"created_at"`
	UpdatedAt string                `json:"updated_at"`
	Config    sessionConfigResponse `json:"config"`
}

type sessionsResponse struct {
	Sessions []sessionResponse `json:"sessions"`
}

type sessionDetailResponse struct {
	ID        string                   `json:"id"`
	Title     string                   `json:"title"`
	CreatedAt string                   `json:"created_at"`
	UpdatedAt string                   `json:"updated_at"`
	Config    sessionConfigResponse    `json:"config"`
	Messages  []sessionMessageResponse `json:"messages"`
}

type sessionMessageResponse struct {
	Role       string                    `json:"role"`
	Content    string                    `json:"content,omitempty"`
	ToolCalls  []sessionToolCallResponse `json:"tool_calls,omitempty"`
	ToolCallID string                    `json:"tool_call_id,omitempty"`
	Tool       string                    `json:"tool,omitempty"`
}

type sessionToolCallResponse struct {
	ID        string          `json:"id"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

type sessionConfigResponse struct {
	ProviderName     string `json:"provider_name,omitempty"`
	MaxSteps         int    `json:"max_steps,omitempty"`
	HasSessionPrompt bool   `json:"has_session_prompt,omitempty"`
}

type runRequest struct {
	Input        string `json:"input"`
	ProviderName string `json:"provider_name"`
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

// CurrentProvider 返回当前 Provider 的本地配置诊断信息。
func (h *SessionHandler) CurrentProvider(ctx *gin.Context) {
	diagnostics, err := h.service.CurrentProviderDiagnostics(ctx.Request.Context())
	if err != nil {
		writeError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	ctx.JSON(http.StatusOK, diagnostics)
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
	meta, err := h.service.Create(ctx.Request.Context(), session.LocalActor(), title, appservice.CreateOptions{ProviderName: req.ProviderName, MaxSteps: req.MaxSteps, SessionPrompt: req.SessionPrompt})
	if err != nil {
		writeError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	ctx.JSON(http.StatusCreated, newSessionResponse(meta))
}

// List 返回 managed sessions。
func (h *SessionHandler) List(ctx *gin.Context) {
	metas, err := h.service.List(ctx.Request.Context(), session.LocalActor())
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

// Get 返回 managed session 的 metadata 和可恢复 transcript。
func (h *SessionHandler) Get(ctx *gin.Context) {
	detail, err := h.service.Get(ctx.Request.Context(), session.LocalActor(), ctx.Param("id"))
	if err != nil {
		if session.IsNotFound(err) {
			writeError(ctx, http.StatusNotFound, err.Error())
			return
		}
		writeError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	ctx.JSON(http.StatusOK, newSessionDetailResponse(detail))
}

// Run 在指定 session 上执行一轮 prompt。
func (h *SessionHandler) Run(ctx *gin.Context) {
	var req runRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		writeError(ctx, http.StatusBadRequest, err.Error())
		return
	}
	result, err := h.service.Run(ctx.Request.Context(), session.LocalActor(), ctx.Param("id"), req.Input, appservice.RunOptions{ProviderName: req.ProviderName})
	if err != nil {
		if session.IsNotFound(err) {
			writeError(ctx, http.StatusNotFound, err.Error())
			return
		}
		if appservice.IsProviderSelectionError(err) {
			writeError(ctx, http.StatusBadRequest, err.Error())
			return
		}
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
	events, err := h.service.Stream(ctx.Request.Context(), session.LocalActor(), ctx.Param("id"), req.Input, appservice.RunOptions{ProviderName: req.ProviderName})
	if err != nil {
		writeStreamError(ctx, err.Error())
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

func writeStreamError(ctx *gin.Context, message string) {
	ctx.Header("Content-Type", "text/event-stream")
	ctx.Header("Cache-Control", "no-cache")
	ctx.Header("Connection", "keep-alive")
	ctx.Status(http.StatusOK)
	ctx.SSEvent("error", gin.H{"error": message})
}

func newSessionResponse(meta appservice.SessionMeta) sessionResponse {
	return sessionResponse{
		ID:        meta.ID,
		Title:     meta.Title,
		CreatedAt: meta.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: meta.UpdatedAt.UTC().Format(time.RFC3339Nano),
		Config:    newSessionConfigResponse(meta.Config),
	}
}

func newSessionDetailResponse(detail appservice.SessionDetail) sessionDetailResponse {
	out := sessionDetailResponse{
		ID:        detail.ID,
		Title:     detail.Title,
		CreatedAt: detail.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: detail.UpdatedAt.UTC().Format(time.RFC3339Nano),
		Config:    newSessionConfigResponse(detail.Config),
		Messages:  make([]sessionMessageResponse, 0, len(detail.Messages)),
	}
	for _, message := range detail.Messages {
		out.Messages = append(out.Messages, newSessionMessageResponse(message))
	}
	return out
}

func newSessionConfigResponse(cfg session.SessionConfig) sessionConfigResponse {
	return sessionConfigResponse{
		ProviderName:     cfg.ProviderName,
		MaxSteps:         cfg.MaxSteps,
		HasSessionPrompt: strings.TrimSpace(cfg.SessionPrompt) != "",
	}
}

func newSessionMessageResponse(message appservice.SessionMessage) sessionMessageResponse {
	out := sessionMessageResponse{
		Role:       message.Role,
		Content:    message.Content,
		ToolCallID: message.ToolCallID,
		Tool:       message.Tool,
		ToolCalls:  make([]sessionToolCallResponse, 0, len(message.ToolCalls)),
	}
	for _, call := range message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, sessionToolCallResponse{ID: call.ID, Tool: call.Tool, Arguments: call.Arguments})
	}
	return out
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
