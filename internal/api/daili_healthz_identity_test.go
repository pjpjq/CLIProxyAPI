package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/buildinfo"
)

func TestDailiHealthzBuildIdentity(t *testing.T) {
	server := newTestServer(t)
	const (
		version = "v8.0.0"
		commit  = "test-healthz-commit"
	)
	previousVersion, previousCommit := buildinfo.Version, buildinfo.Commit
	buildinfo.Version, buildinfo.Commit = version, commit
	t.Cleanup(func() { buildinfo.Version, buildinfo.Commit = previousVersion, previousCommit })

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			rr := httptest.NewRecorder()
			server.engine.ServeHTTP(rr, httptest.NewRequest(method, "/healthz", nil))
			if rr.Code != http.StatusOK {
				t.Fatalf("healthz returned %d, want %d", rr.Code, http.StatusOK)
			}
			for name, want := range map[string]string{
				"Cache-Control": "no-store",
				"X-CPA-VERSION": version,
				"X-CPA-COMMIT":  commit,
			} {
				if got := rr.Header().Get(name); got != want {
					t.Errorf("header %s = %q, want %q", name, got, want)
				}
			}
		})
	}
}
