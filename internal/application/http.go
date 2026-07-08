package application

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// NewHTTPHandler 创建 App API MVP 的 HTTP 路由。
func NewHTTPHandler(service *Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", handleHealth)
	mux.HandleFunc("/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			handleCreateSession(w, r, service)
		case http.MethodGet:
			handleListSessions(w, r, service)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})
	mux.HandleFunc("/v1/sessions/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		sessionID, ok := parseRunPath(r.URL.Path)
		if !ok {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		handleRunSession(w, r, service, sessionID)
	})
	return mux
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

type httpRunResponse struct {
	Answer string                 `json:"answer"`
	Steps  []httpToolStepResponse `json:"steps"`
}

type httpToolStepResponse struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Result    string          `json:"result"`
	Error     string          `json:"error,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleCreateSession(w http.ResponseWriter, r *http.Request, service *Service) {
	var req createSessionRequest
	if err := decodeRequestJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = strings.TrimSpace(req.Input)
	}
	meta, err := service.CreateSession(r.Context(), title)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, newSessionResponse(meta))
}

func handleListSessions(w http.ResponseWriter, r *http.Request, service *Service) {
	metas, err := service.ListSessions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := sessionsResponse{Sessions: make([]sessionResponse, 0, len(metas))}
	for _, meta := range metas {
		out.Sessions = append(out.Sessions, newSessionResponse(meta))
	}
	writeJSON(w, http.StatusOK, out)
}

func handleRunSession(w http.ResponseWriter, r *http.Request, service *Service, sessionID string) {
	var req runRequest
	if err := decodeRequestJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := service.Run(r.Context(), sessionID, req.Input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, newRunResponse(result))
}

func parseRunPath(path string) (string, bool) {
	const prefix = "/v1/sessions/"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, "/runs") {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), "/runs")
	id = strings.Trim(id, "/")
	return id, id != ""
}

func newSessionResponse(meta SessionMeta) sessionResponse {
	return sessionResponse{
		ID:        meta.ID,
		Title:     meta.Title,
		CreatedAt: meta.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: meta.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func newRunResponse(result RunResult) httpRunResponse {
	out := httpRunResponse{Answer: result.Answer, Steps: make([]httpToolStepResponse, 0, len(result.ToolSteps))}
	for _, step := range result.ToolSteps {
		out.Steps = append(out.Steps, httpToolStepResponse{
			Tool:      step.ToolName,
			Arguments: json.RawMessage(step.Arguments),
			Result:    step.Result,
			Error:     step.Error,
		})
	}
	return out
}

func decodeRequestJSON(r *http.Request, out any) error {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		return fmt.Errorf("decode json body: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}
