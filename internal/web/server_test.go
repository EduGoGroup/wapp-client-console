package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/EduGoGroup/wapp-client-console/internal/config"
)

// testConfig es la configuración mínima con la que se monta el router en los tests: ambiente local
// (cookies sin Secure) y rate-limit apagado, para que un test no pueda estrangular a otro.
func testConfig() *config.Config {
	return &config.Config{
		Environment:      "local",
		HTTPAddr:         ":0",
		PublicAPIBaseURL: "http://127.0.0.1:8103",
		IdentityBaseURL:  "http://127.0.0.1:8200",
		CookieSecure:     false,
		CookieSameSite:   "lax",
		RateLimitEnabled: false,
		UpstreamTimeout:  5 * time.Second,
	}
}

func TestHealthz_Success(t *testing.T) {
	t.Parallel()
	router := NewRouter(testConfig())

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET /healthz no devolvió JSON: %v (body: %s)", err, rec.Body.String())
	}
	if body["status"] != "healthy" {
		t.Errorf(`status = %q, want "healthy"`, body["status"])
	}
}

func TestSecurityHeaders_Present(t *testing.T) {
	t.Parallel()
	router := NewRouter(testConfig())

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", got)
	}
}

func TestSharedCSS_SeSirveDesdeElModuloUI(t *testing.T) {
	t.Parallel()
	router := NewRouter(testConfig())

	for _, sheet := range sharedStylesheets {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/css/"+sheet, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET /static/css/%s status = %d, want 200", sheet, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("GET /static/css/%s devolvió cuerpo vacío", sheet)
		}
	}
}
