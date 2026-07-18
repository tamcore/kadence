package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tamcore/kadence/internal/api/handlers"
)

func TestRespondJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	handlers.RespondJSON(rec, http.StatusCreated, map[string]string{"hello": "world"})

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	var env struct {
		Data  map[string]string `json:"data"`
		Error *string           `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Data["hello"] != "world" || env.Error != nil {
		t.Fatalf("bad envelope: %s", rec.Body.String())
	}
}

func TestRespondError(t *testing.T) {
	rec := httptest.NewRecorder()
	handlers.RespondError(rec, http.StatusBadRequest, "nope")
	var env struct {
		Data  any    `json:"data"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if rec.Code != http.StatusBadRequest || env.Error != "nope" || env.Data != nil {
		t.Fatalf("bad error envelope: %s", rec.Body.String())
	}
}
