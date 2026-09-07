package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetupRequiresOwnerCodeAndSingleBoundedJSON(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"missing proof", `{"username":"owner","password":"password-123"}`, 403},
		{"wrong proof", `{"setup_code":"wrong","username":"owner","password":"password-123"}`, 403},
		{"trailing value", `{"setup_code":"owner-proof","username":"owner","password":"password-123"}{}`, 400},
		{"oversized", `{"setup_code":"` + strings.Repeat("x", 5000) + `"}`, 413},
		{"valid proof", `{"setup_code":"owner-proof","username":"owner","password":"password-123"}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newFakeAuth()
			s := New(Options{Auth: a, SetupCode: "owner-proof"})
			r := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(tc.body))
			r.Header.Set(requestedWithHeader, requestedWithValue)
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			require.Equal(t, tc.status, w.Code)
			require.Equal(t, tc.status == 200, a.CredentialSet())
		})
	}
}

func TestSecurityHeadersCoverHTMLAndPrivateAPI(t *testing.T) {
	s := New(Options{})
	for _, path := range []string{"/", "/api/live/snapshot"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Contains(t, w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'")
		require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
		if strings.HasPrefix(path, "/api/") {
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		}
	}
}

func TestPruneRejectsUnapprovedTargets(t *testing.T) {
	called := false
	s := New(Options{PruneImages: func(context.Context, string, []string) (ImagePruneResult, error) {
		called = true
		return ImagePruneResult{}, nil
	}})
	r := httptest.NewRequest(http.MethodPost, "/api/images/prune", strings.NewReader(`{"mode":"unused"}`))
	r.Header.Set(gantryConfirmHeader, gantryConfirmValue)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	require.Equal(t, 400, w.Code)
	require.False(t, called)
}

func TestHistoryRejectsUnboundedQueries(t *testing.T) {
	s := New(Options{})
	for _, query := range []string{"from=200&to=100", "from=0&to=9999999999999", "metrics=" + strings.Repeat("cpu.pct,", 20)} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/series?"+query, nil))
		require.Equal(t, 400, w.Code, query)
	}
}
