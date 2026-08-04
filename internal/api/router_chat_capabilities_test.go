package api_test

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/tamcore/kadence/internal/api"
	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/ingest"
	"github.com/tamcore/kadence/internal/store"
)

func TestRouterMountsChatUploadCapabilitiesWithoutDocumentCRUD(t *testing.T) {
	router := mustNewRouter(t, api.Deps{
		Users:    store.NewUserRepository(nil),
		Sessions: store.NewSessionRepository(nil),
		Config:   config.Config{},
		// Chat supports attachment documents even when RAG document storage is
		// disabled. A capabilities-only handler must not expose nil-backed CRUD.
		Documents: handlers.NewDocuments(nil, nil, ingest.UploadCapabilities{
			Accept: "application/pdf,.pdf", MaxBytes: 12 << 20,
		}),
	})
	chiRouter, ok := router.(chi.Router)
	if !ok {
		t.Fatalf("NewRouter() = %T, want chi.Router", router)
	}

	seen := map[string]bool{}
	if err := chi.Walk(chiRouter, func(
		method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler,
	) error {
		seen[method+" "+route] = true
		return nil
	}); err != nil {
		t.Fatalf("walk routes: %v", err)
	}

	if !seen["GET /api/documents/capabilities"] {
		t.Fatal("chat upload capabilities route is absent without RAG document CRUD")
	}
	for _, route := range []string{
		"GET /api/documents",
		"POST /api/documents",
		"DELETE /api/documents/{id}",
		"GET /api/documents/references",
		"GET /api/admin/documents",
		"POST /api/admin/documents",
		"DELETE /api/admin/documents/{id}",
	} {
		if seen[route] {
			t.Errorf("capabilities-only handler mounted unsafe nil-backed route %s", route)
		}
	}
}
