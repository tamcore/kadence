package auth_test

import (
	"net/http/httptest"
	"testing"

	"github.com/tamcore/kadence/internal/auth"
)

func TestClientIP(t *testing.T) {
	cases := map[string]string{"1.2.3.4:5678": "1.2.3.4", "9.9.9.9": "9.9.9.9", "": ""}
	for remote, want := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if got := auth.ClientIP(r); got != want {
			t.Errorf("ClientIP(%q)=%q want %q", remote, got, want)
		}
	}
}
