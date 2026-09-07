package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/auth"
	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

func TestLogoutClosesAlreadyOpenLiveAndLogResponses(t *testing.T) {
	for _, route := range []string{"/api/live", "/api/containers/worker/logs?follow=1"} {
		t.Run(route, func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
			require.NoError(t, err)
			defer func() { _ = st.Close() }()
			manager, err := auth.New(auth.Options{Settings: st, Sessions: st})
			require.NoError(t, err)
			token, err := manager.Setup("local", "operator", "test-stream-password")
			require.NoError(t, err)
			pipe, writer := io.Pipe()
			defer func() { _ = writer.Close() }()
			s := New(Options{Auth: manager, Live: NewBroadcaster(), Current: func() []byte { return []byte(`{"ts":1}`) }, Logs: func(context.Context, string, bool, int) (io.ReadCloser, error) { return pipe, nil }})
			ts := httptest.NewServer(s.Handler())
			defer ts.Close()
			request, err := http.NewRequest(http.MethodGet, ts.URL+route, nil)
			require.NoError(t, err)
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
			response, err := ts.Client().Do(request)
			require.NoError(t, err)
			defer func() { _ = response.Body.Close() }()
			require.Equal(t, 200, response.StatusCode)
			ended := make(chan struct{})
			go func() { _, _ = io.Copy(io.Discard, response.Body); close(ended) }()
			manager.Logout(token)
			select {
			case <-ended:
			case <-time.After(2 * time.Second):
				t.Fatal("revoked session still has an open response")
			}
		})
	}
}

func TestLogReaderBudgetRejectsBeforeOpeningDockerStream(t *testing.T) {
	called := false
	s := New(Options{Logs: func(context.Context, string, bool, int) (io.ReadCloser, error) {
		called = true
		return io.NopCloser(nil), nil
	}})
	for i := 0; i < cap(s.logSlots); i++ {
		s.logSlots <- struct{}{}
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/containers/worker/logs?follow=1", nil))
	require.Equal(t, 429, w.Code)
	require.Equal(t, "5", w.Header().Get("Retry-After"))
	require.False(t, called)
}
