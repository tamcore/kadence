package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/mcp"
)

// mcpHealth reports cached health and tool listings for configured MCP
// servers. Satisfied by *mcp.HealthPoller.
type mcpHealth interface {
	StatusFor(username string) []mcp.ServerHealth
	ToolsFor(username, serverName string) ([]mcp.ToolInfo, bool)
}

// MCP serves read-only MCP server health + tool listings to the caller.
type MCP struct{ health mcpHealth }

// NewMCP constructs the MCP handler.
func NewMCP(h mcpHealth) *MCP { return &MCP{health: h} }

type mcpServerDTO struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
	Scope     string `json:"scope"` // "global" | "user"
	State     string `json:"state"` // "healthy" | "unhealthy" | "checking"
	ToolCount int    `json:"toolCount"`
	CheckedAt string `json:"checkedAt,omitempty"`
	Error     string `json:"error,omitempty"` // admin only
	URL       string `json:"url,omitempty"`   // admin only
}

type mcpToolDTO struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

type mcpToolsResponse struct {
	Name  string       `json:"name"`
	Tools []mcpToolDTO `json:"tools"`
}

// List handles GET /api/mcp.
func (h *MCP) List(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		RespondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	admin := u.IsAdmin()
	statuses := h.health.StatusFor(u.Username)
	out := make([]mcpServerDTO, 0, len(statuses))
	for _, s := range statuses {
		dto := mcpServerDTO{
			Name:      s.Name,
			Transport: s.Transport,
			Scope:     scopeLabel(s.Scope),
			State:     healthState(s),
			ToolCount: s.ToolCount,
		}
		if !s.CheckedAt.IsZero() {
			dto.CheckedAt = s.CheckedAt.Format("2006-01-02T15:04:05Z07:00")
		}
		if admin {
			dto.URL = s.URL
			if !s.OK {
				dto.Error = s.Err
			}
		}
		out = append(out, dto)
	}
	RespondJSON(w, http.StatusOK, out)
}

// Tools handles GET /api/mcp/{name}/tools.
func (h *MCP) Tools(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		RespondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	name := chi.URLParam(r, "name")
	tools, ok := h.health.ToolsFor(u.Username, name)
	if !ok {
		RespondError(w, http.StatusNotFound, "mcp server not found")
		return
	}
	dtos := make([]mcpToolDTO, 0, len(tools))
	for _, t := range tools {
		dtos = append(dtos, mcpToolDTO{Name: t.Name, Description: t.Description, InputSchema: t.Schema})
	}
	RespondJSON(w, http.StatusOK, mcpToolsResponse{Name: name, Tools: dtos})
}

func scopeLabel(scope string) string {
	if strings.EqualFold(scope, "GLOBAL") {
		return "global"
	}
	return "user"
}

func healthState(s mcp.ServerHealth) string {
	if s.CheckedAt.IsZero() {
		return "checking"
	}
	if s.OK {
		return "healthy"
	}
	return "unhealthy"
}
