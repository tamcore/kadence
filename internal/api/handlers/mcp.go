package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/mcp"
	"github.com/tamcore/kadence/internal/store"
)

// mcpHealth reports cached health and tool listings for configured MCP
// servers. Satisfied by *mcp.HealthPoller.
type mcpHealth interface {
	StatusFor(username string) []mcp.ServerHealth
	ToolsFor(username, serverName string) ([]mcp.ToolInfo, bool)
}

// mcpUserStore manages per-owner user-defined MCP server definitions.
// Satisfied by *store.UserServerRepo.
type mcpUserStore interface {
	Create(ctx context.Context, ownerUserID int64, in store.UserMCPInput) (int64, error)
	Update(ctx context.Context, ownerUserID, id int64, in store.UserMCPInput) error
	Delete(ctx context.Context, ownerUserID, id int64) error
	ListForOwner(ctx context.Context, ownerUserID int64) ([]store.UserMCPRecord, error)
}

// MCP serves MCP server health/tool listings and user-defined MCP server CRUD.
type MCP struct {
	health       mcpHealth
	store        mcpUserStore
	allowedHosts []string
	enabled      bool
}

// NewMCP constructs the MCP handler. store may be nil, in which case List
// still works (canAdd=false) and the CRUD endpoints 403.
func NewMCP(h mcpHealth, s mcpUserStore, allowedHosts []string, enabled bool) *MCP {
	return &MCP{health: h, store: s, allowedHosts: allowedHosts, enabled: enabled}
}

type mcpServerDTO struct {
	ID        *int64 `json:"id,omitempty"`
	Name      string `json:"name"`
	Transport string `json:"transport"`
	Scope     string `json:"scope"` // "global" | "user"
	State     string `json:"state"` // "healthy" | "unhealthy" | "checking"
	ToolCount int    `json:"toolCount"`
	CheckedAt string `json:"checkedAt,omitempty"`
	Error     string `json:"error,omitempty"` // admin only
	URL       string `json:"url,omitempty"`   // admin only, or owner's own server
	Editable  bool   `json:"editable"`
}

type mcpListResponse struct {
	Servers []mcpServerDTO `json:"servers"`
	CanAdd  bool           `json:"canAdd"`
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

// mcpUpsertRequest is the JSON body for Create/Update.
type mcpUpsertRequest struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Transport string `json:"transport"`
	AuthUser  string `json:"authUser"`
	AuthPass  string `json:"authPass"`
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

	ownedIDs := map[string]int64{}
	if h.store != nil {
		recs, err := h.store.ListForOwner(r.Context(), u.ID)
		if err != nil {
			slog.Error("list user mcp servers", "err", err)
		}
		for _, rec := range recs {
			ownedIDs[rec.Name] = rec.ID
		}
	}

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
		if dto.Scope == "user" {
			if id, ok := ownedIDs[s.Name]; ok {
				dto.Editable = true
				dto.ID = &id
				dto.URL = s.URL
			}
		}
		out = append(out, dto)
	}
	RespondJSON(w, http.StatusOK, mcpListResponse{Servers: out, CanAdd: h.enabled})
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

// Create handles POST /api/mcp.
func (h *MCP) Create(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		RespondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if !h.enabled || h.store == nil {
		RespondError(w, http.StatusForbidden, "user MCP servers are not enabled")
		return
	}
	var in mcpUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := mcp.HostAllowed(in.URL, h.allowedHosts); err != nil {
		RespondError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := h.store.Create(r.Context(), u.ID, store.UserMCPInput{
		Name:      in.Name,
		URL:       in.URL,
		Transport: in.Transport,
		AuthUser:  in.AuthUser,
		AuthPass:  in.AuthPass,
	})
	if errors.Is(err, store.ErrDuplicateName) {
		RespondError(w, http.StatusConflict, "a server with that name already exists")
		return
	}
	if err != nil {
		slog.Error("create user mcp", "err", err)
		RespondError(w, http.StatusInternalServerError, "could not create server")
		return
	}
	RespondJSON(w, http.StatusCreated, map[string]any{
		"id": id, "name": in.Name, "transport": in.Transport, "editable": true,
	})
}

// Update handles PUT /api/mcp/{id}.
func (h *MCP) Update(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		RespondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if !h.enabled || h.store == nil {
		RespondError(w, http.StatusForbidden, "user MCP servers are not enabled")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in mcpUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := mcp.HostAllowed(in.URL, h.allowedHosts); err != nil {
		RespondError(w, http.StatusBadRequest, err.Error())
		return
	}
	err = h.store.Update(r.Context(), u.ID, id, store.UserMCPInput{
		Name:      in.Name,
		URL:       in.URL,
		Transport: in.Transport,
		AuthUser:  in.AuthUser,
		AuthPass:  in.AuthPass,
	})
	if errors.Is(err, store.ErrNotFound) {
		RespondError(w, http.StatusNotFound, "server not found")
		return
	}
	if errors.Is(err, store.ErrDuplicateName) {
		RespondError(w, http.StatusConflict, "a server with that name already exists")
		return
	}
	if err != nil {
		slog.Error("update user mcp", "err", err)
		RespondError(w, http.StatusInternalServerError, "could not update server")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Delete handles DELETE /api/mcp/{id}.
func (h *MCP) Delete(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		RespondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if !h.enabled || h.store == nil {
		RespondError(w, http.StatusForbidden, "user MCP servers are not enabled")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.store.Delete(r.Context(), u.ID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			RespondError(w, http.StatusNotFound, "server not found")
			return
		}
		slog.Error("delete user mcp", "err", err)
		RespondError(w, http.StatusInternalServerError, "could not delete server")
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
