package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/EduGoGroup/wapp-client-console/internal/config"
	sharedjwt "github.com/EduGoGroup/wapp-shared/auth/jwt"
	sharedweb "github.com/EduGoGroup/wapp-shared/web"
	"github.com/golang-jwt/jwt/v5"
)

// testConfig es la configuración con la que se monta el router en los tests: ambiente local
// (cookies sin Secure) y rate-limit apagado, para que un test no pueda estrangular a otro.
//
// Las dos URLs se pasan EXPLÍCITAS —y no con un default cómodo— porque son los dos upstreams del
// login: identity (:8200) emite el Identity Token y la API pública (:8103) lo canjea. Un test que
// las olvide apuntaría a un puerto real de la máquina del desarrollador.
func testConfig(publicAPIURL, identityURL string) *config.Config {
	return &config.Config{
		Environment:      "local",
		HTTPAddr:         ":0",
		PublicAPIBaseURL: publicAPIURL,
		IdentityBaseURL:  identityURL,
		CookieSecure:     false,
		CookieSameSite:   "lax",
		RateLimitEnabled: false,
		UpstreamTimeout:  5 * time.Second,
	}
}

// offlineConfig es para los tests que no llegan a hablar con ningún upstream (sonda, estáticos,
// redirecciones sin sesión): apunta a los puertos reales a propósito, porque si algún día una de
// esas rutas empezara a llamar de verdad, el test fallaría por conexión rechazada en vez de pasar.
func offlineConfig() *config.Config {
	return testConfig("http://127.0.0.1:8103", "http://127.0.0.1:8200")
}

// makeContextToken forja un CONTEXT Token como el que emite la plataforma en el canje: el que la
// cookie custodia y del que el AuthMiddleware lee `exp`, usuario y empresa. La firma es de mentira
// porque esta consola NO la verifica (parseAccessClaims usa ParseUnverified): quien la valida de
// verdad es la plataforma en cada llamada.
func makeContextToken(t *testing.T, exp time.Time) string {
	t.Helper()
	claims := sharedjwt.Claims{
		UserID:           testUserID,
		TenantID:         testTenantID,
		Roles:            []string{"tenant_admin"},
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(exp)},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("dummy"))
	if err != nil {
		t.Fatalf("firmar token: %v", err)
	}
	return signed
}

// Identidad de la sesión de prueba. Son constantes para que los tests que comprueban la pantalla
// puedan afirmar que estos valores concretos llegan al HTML.
const (
	testUserID   = "u-cliente-1"
	testTenantID = "11111111-1111-1111-1111-111111111111"
)

// clientSessionCookie arma la cookie de sesión de un usuario ya autenticado, con refresh token.
func clientSessionCookie(t *testing.T) *http.Cookie {
	t.Helper()
	return sessionCookieWith(t, makeContextToken(t, time.Now().Add(time.Hour)), "rt-1")
}

// sessionCookieWith arma la cookie de sesión con el access y el refresh que se le den.
func sessionCookieWith(t *testing.T, accessToken, refreshToken string) *http.Cookie {
	t.Helper()
	val, err := sharedweb.EncodeSession(sharedweb.SessionData{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	})
	if err != nil {
		t.Fatalf("EncodeSession: %v", err)
	}
	return &http.Cookie{Name: sessionCookieName, Value: val}
}

// mintCSRF obtiene una cookie CSRF válida sembrada por la propia cadena de middleware.
func mintCSRF(router http.Handler) *http.Cookie {
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))
	for _, sc := range rec.Result().Cookies() {
		if sc.Name == csrfCookieName {
			return sc
		}
	}
	return &http.Cookie{Name: csrfCookieName, Value: ""}
}

// postFormWithCSRF manda un POST de formulario con el double-submit completo (cookie + campo).
func postFormWithCSRF(router http.Handler, path string, form url.Values, sessionCookie *http.Cookie) *httptest.ResponseRecorder {
	csrf := mintCSRF(router)
	form.Set(sharedweb.CSRFFieldName, csrf.Value)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(csrf)
	if sessionCookie != nil {
		req.AddCookie(sessionCookie)
	}
	router.ServeHTTP(rec, req)
	return rec
}

func TestHealthz_Success(t *testing.T) {
	t.Parallel()
	router := NewRouter(offlineConfig())

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
	router := NewRouter(offlineConfig())

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
	router := NewRouter(offlineConfig())

	// app.css es la hoja PROPIA (embebida en el binario); las demás las sirve wapp-shared/ui.
	for _, sheet := range append([]string{"app.css"}, sharedStylesheets...) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/css/"+sheet, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET /static/css/%s status = %d, want 200", sheet, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("GET /static/css/%s devolvió cuerpo vacío", sheet)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
			t.Errorf("GET /static/css/%s Content-Type = %q, want text/css", sheet, ct)
		}
	}
}

// TestCSRF_RechazaElLoginSinToken: el POST /login está DEBAJO del Use(CSRF), así que un formulario
// sin double-submit no llega ni a rozar identity.
func TestCSRF_RechazaElLoginSinToken(t *testing.T) {
	t.Parallel()
	router := NewRouter(offlineConfig())

	form := url.Values{"email": {"a@b.com"}, "password": {"secret"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /login sin CSRF status = %d, want 403", rec.Code)
	}
}

func TestSetTrustedProxies_PanicsOnInvalidFormat(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("esperado panic con TrustedProxies inválido")
		}
	}()

	cfg := offlineConfig()
	cfg.TrustedProxies = "not-an-ip-address"
	_ = NewRouter(cfg)
}
