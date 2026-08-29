package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	sharedjwt "github.com/EduGoGroup/wapp-shared/auth/jwt"
	"github.com/golang-jwt/jwt/v5"
)

// adminapi_test.go es el arnés de las pantallas de administración: un doble de la API PÚBLICA que
// responde lo que se le diga y GUARDA cada petición que recibe.
//
// La captura no es un lujo: media docena de criterios de esta ola son sobre la petición SALIENTE —que
// asignar un rol vaya a la ruta de `roles.write` y no a la de `members.write`, que el `tenant_id` no
// viaje nunca (INV-04), que la baja propia no llegue a salir— y eso no se puede afirmar mirando el
// HTML. Un doble en memoria tampoco valdría: los tags json y el escapado de la ruta solo se
// ejercitan si hay un servidor HTTP real al otro lado.

// stubResponse es lo que el doble responde a una ruta.
type stubResponse struct {
	status int
	body   string
}

// capturedRequest es la petición saliente tal como llegó al upstream.
type capturedRequest struct {
	Method string
	Path   string
	Query  url.Values
	Body   string
	Auth   string
}

// Route devuelve "MÉTODO /ruta", la forma en la que los tests nombran una llamada.
func (r capturedRequest) Route() string { return r.Method + " " + r.Path }

// stubAPI es el doble de la API pública.
type stubAPI struct {
	*httptest.Server

	mu       sync.Mutex
	requests []capturedRequest
}

// newStubAPI levanta el doble con las rutas dadas, en patrones de Go 1.22
// ("GET /api/v1/members", "DELETE /api/v1/members/{user_id}").
//
// Lo que NO esté registrado responde 404 y se registra igual: así un test que se equivoque de ruta
// falla por el desenlace y además deja en `requests` la prueba de a dónde fue de verdad.
func newStubAPI(t *testing.T, routes map[string]stubResponse) *stubAPI {
	t.Helper()

	api := &stubAPI{}
	mux := http.NewServeMux()
	for pattern, resp := range routes {
		r := resp
		mux.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(r.status)
			if r.body != "" {
				_, _ = io.WriteString(w, r.body)
			}
		}))
	}

	api.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body string
		if req.Body != nil {
			raw, _ := io.ReadAll(io.LimitReader(req.Body, 1<<20))
			body = string(raw)
		}
		api.mu.Lock()
		api.requests = append(api.requests, capturedRequest{
			Method: req.Method,
			Path:   req.URL.Path,
			Query:  req.URL.Query(),
			Body:   body,
			Auth:   req.Header.Get("Authorization"),
		})
		api.mu.Unlock()
		mux.ServeHTTP(w, req)
	}))
	t.Cleanup(api.Close)
	return api
}

// Requests devuelve una copia de lo capturado.
func (s *stubAPI) Requests() []capturedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]capturedRequest, len(s.requests))
	copy(out, s.requests)
	return out
}

// Called dice si el doble recibió esa ruta exacta.
func (s *stubAPI) Called(route string) bool {
	for _, r := range s.Requests() {
		if r.Route() == route {
			return true
		}
	}
	return false
}

// Last devuelve la última petición a esa ruta, o falla el test si no hubo ninguna.
func (s *stubAPI) Last(t *testing.T, route string) capturedRequest {
	t.Helper()
	reqs := s.Requests()
	for i := len(reqs) - 1; i >= 0; i-- {
		if reqs[i].Route() == route {
			return reqs[i]
		}
	}
	t.Fatalf("el upstream nunca recibió %q; recibió: %s", route, strings.Join(routesOf(reqs), ", "))
	return capturedRequest{}
}

func routesOf(reqs []capturedRequest) []string {
	out := make([]string, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, r.Route())
	}
	return out
}

// --- Cuerpos de respuesta que usan varios tests ---

// membersBody arma la respuesta de GET /api/v1/members con los user_id dados.
func membersBody(userIDs ...string) string {
	filas := make([]string, 0, len(userIDs))
	for _, id := range userIDs {
		filas = append(filas, `{"user_id":"`+id+`","tenant_id":"`+testTenantID+
			`","created_at":"2026-08-01T10:00:00Z"}`)
	}
	return "[" + strings.Join(filas, ",") + "]"
}

// sesionesBody arma la respuesta de GET /api/v1/sessions a partir de filas ya escritas en JSON, para
// que cada test enseñe EXACTAMENTE los campos que le importan.
//
// No hay constructor con parámetros a propósito: media docena de criterios de esta pantalla son sobre
// campos AUSENTES —un `profile` que no viene, un `intent_circuit` que el equipo no reporta—, y una
// firma con parámetros obliga a mandar el cero de cada uno, que es justo lo contrario de ausente.
func sesionesBody(filas ...string) string {
	return "[" + strings.Join(filas, ",") + "]"
}

// Identificadores de las sesiones de prueba.
const (
	testSessionID    = "s-1111"
	testOtherSession = "s-2222"
)

// entitlementsBody arma la respuesta de GET /api/v1/entitlements.
func entitlementsBody(plan string, features ...string) string {
	quoted := make([]string, 0, len(features))
	for _, f := range features {
		quoted = append(quoted, `"`+f+`"`)
	}
	return `{"plan":"` + plan + `","features":[` + strings.Join(quoted, ",") + `],"cache_ttl_seconds":60}`
}

// rolesBody es un catálogo con una plantilla global y un rol propio de la empresa.
const rolesBody = `[` +
	`{"role_id":"` + testGlobalRoleID + `","name":"tenant_admin","global":true,"created_at":"2026-01-01T00:00:00Z"},` +
	`{"role_id":"` + testTenantRoleID + `","name":"Encargada de pedidos","tenant_id":"` + testTenantID + `",` +
	`"parent_role_id":"` + testGlobalRoleID + `","global":false,"created_at":"2026-08-01T10:00:00Z"}` +
	`]`

// Identidades de prueba. testUserID (server_test.go) es el de la sesión; testOtherUserID es otra
// persona de la misma empresa, la única sobre la que la pantalla ofrece dar de baja.
const (
	testOtherUserID  = "22222222-2222-4222-8222-222222222222"
	testGlobalRoleID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	testTenantRoleID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

// adminRouter monta el router contra el doble de la API pública. Identity apunta al puerto real y
// APAGADO a propósito: ninguna de estas pantallas debe hablar con identity, y si alguna empezara a
// hacerlo, el test fallaría por conexión rechazada en vez de pasar en silencio.
func adminRouter(api *stubAPI) http.Handler {
	return NewRouter(testConfig(api.URL, "http://127.0.0.1:8200"))
}

// getWithSession pide una página autenticada con la cookie de sesión de prueba.
func getWithSession(t *testing.T, router http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(clientSessionCookie(t))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// redirectTarget devuelve el Location de una respuesta 303, fallando si no lo es.
func redirectTarget(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 (POST-redirect-GET). Body: %s", rec.Code, rec.Body.String())
	}
	return rec.Header().Get("Location")
}

// sessionCookieFor arma la cookie de sesión de un usuario CONCRETO.
//
// `tenantID` vacío es el caso que importa: el Context Token de alguien que se registró y todavía no
// pertenece a ninguna empresa. El canje lo emite así —tenant vacío, sin grants, y NO un 401
// (D-056.12)—, así que un token de prueba con el tenant a "" es exactamente lo que llega en campo.
func sessionCookieFor(t *testing.T, userID, tenantID string) *http.Cookie {
	t.Helper()
	claims := sharedjwt.Claims{
		UserID:           userID,
		TenantID:         tenantID,
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("dummy"))
	if err != nil {
		t.Fatalf("firmar token: %v", err)
	}
	return sessionCookieWith(t, signed, "rt-1")
}

// getConCookie pide una página con la cookie de sesión que se le dé.
func getConCookie(router http.Handler, path string, sess *http.Cookie) *httptest.ResponseRecorder {
	return getConCookies(router, path, sess)
}

// getConCookies pide una página con TODAS las cookies que se le den. Hay pantallas que dependen de
// una segunda cookie además de la de sesión: la del código de invitación recién emitido, que es lo
// único que hace aparecer la caja del secreto.
func getConCookies(router http.Handler, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, ck := range cookies {
		if ck != nil {
			req.AddCookie(ck)
		}
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}
