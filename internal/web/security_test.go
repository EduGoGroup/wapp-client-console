package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// paginasRenderizadas devuelve una petición por cada página HTML que esta consola sabe servir, para
// que los tests de esta familia no tengan que enumerarlas una a una y se les olvide la nueva.
func paginasRenderizadas(t *testing.T, router http.Handler) map[string]*httptest.ResponseRecorder {
	t.Helper()

	sess := clientSessionCookie(t)
	conSesion := httptest.NewRequest(http.MethodGet, "/", nil)
	conSesion.AddCookie(sess)

	peticiones := map[string]*http.Request{
		"login":            httptest.NewRequest(http.MethodGet, "/login", nil),
		"login_con_aviso":  httptest.NewRequest(http.MethodGet, "/login?error="+flashSessionExpired, nil),
		"login_con_exito":  httptest.NewRequest(http.MethodGet, "/login?success="+flashLoggedOut, nil),
		"home_autenticado": conSesion,
	}

	renders := make(map[string]*httptest.ResponseRecorder, len(peticiones)+1)
	for nombre, req := range peticiones {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("la página %q respondió %d, want 200. Body: %s", nombre, rec.Code, rec.Body.String())
		}
		renders[nombre] = rec
	}

	// El login fallido es la quinta página: se renderiza en la respuesta de un POST, no de un GET.
	identity := identityReturning(t, http.StatusUnauthorized)
	routerFallo := NewRouter(testConfig("http://127.0.0.1:8103", identity.URL))
	renders["login_fallido"] = postFormWithCSRF(routerFallo, "/login", url.Values{
		"email": {loginEmail}, "password": {loginPassword},
	}, nil)

	return renders
}

// TestTemplates_SinEstilosInline: la CSP (`style-src 'self' 'nonce-…'`) no admite atributos
// `style="…"` — style-src-attr hereda de style-src y un nonce nunca habilita un ATRIBUTO. Una
// plantilla con estilo inline se sirve "a medias" en el navegador sin que ningún gate lo note, así
// que se caza aquí, sobre el HTML que sale por el cable.
func TestTemplates_SinEstilosInline(t *testing.T) {
	t.Parallel()
	router := NewRouter(offlineConfig())

	for nombre, rec := range paginasRenderizadas(t, router) {
		if strings.Contains(rec.Body.String(), `style="`) {
			t.Errorf("la página %q sirve un atributo style= inline; la CSP lo descarta en el navegador", nombre)
		}
	}
}

// TestTemplates_SinJavaScript: esta consola es server-side rendering sin SPA y hoy no sirve ni una
// línea de JavaScript. No es una casualidad que convenga vigilar: la CSP no lleva 'unsafe-inline',
// así que el día que alguien añada un <script> tendrá que llevar el nonce de ESTA respuesta
// (`{{ .Nonce }}`, que el renderizador siembra en toda página) o no ejecutará.
//
// Este test no prohíbe el JavaScript: obliga a que añadirlo sea una decisión consciente que pase
// por aquí, en vez de una etiqueta que se cuela y se descubre en el navegador de un cliente.
func TestTemplates_SinJavaScript(t *testing.T) {
	t.Parallel()
	router := NewRouter(offlineConfig())

	for nombre, rec := range paginasRenderizadas(t, router) {
		if strings.Contains(strings.ToLower(rec.Body.String()), "<script") {
			t.Errorf("la página %q sirve un <script>: si de verdad hace falta, tiene que llevar nonce=\"{{ .Nonce }}\" "+
				"y este test debe actualizarse a propósito", nombre)
		}
	}
}

var nonceEnCSP = regexp.MustCompile(`'nonce-([A-Za-z0-9_\-=+/]+)'`)

// TestCSP_LaPantallaDeEntradaLlevaNonceYNoUnsafeInline afirma la política sobre la respuesta de
// /login, que es la página que ve cualquiera sin autenticarse.
//
// El nonce se comprueba además ENTRE DOS peticiones: tiene que cambiar. Un nonce constante sería
// tan inútil como no tenerlo —quien inyecta contenido en la página lo conocería—, y es justo el
// tipo de regresión que un assert de "contiene 'nonce-'" no ve.
func TestCSP_LaPantallaDeEntradaLlevaNonceYNoUnsafeInline(t *testing.T) {
	t.Parallel()
	router := NewRouter(offlineConfig())

	nonces := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))

		csp := rec.Header().Get("Content-Security-Policy")
		if csp == "" {
			t.Fatal("GET /login no emitió Content-Security-Policy")
		}
		if strings.Contains(csp, "unsafe-inline") {
			t.Fatalf("la CSP lleva 'unsafe-inline': %s", csp)
		}
		for _, directiva := range []string{"default-src 'self'", "frame-ancestors 'none'", "base-uri 'none'", "object-src 'none'"} {
			if !strings.Contains(csp, directiva) {
				t.Errorf("la CSP no lleva %q: %s", directiva, csp)
			}
		}

		coincidencias := nonceEnCSP.FindAllStringSubmatch(csp, -1)
		if len(coincidencias) != 2 {
			t.Fatalf("la CSP tiene %d nonces, want 2 (script-src y style-src): %s", len(coincidencias), csp)
		}
		if coincidencias[0][1] != coincidencias[1][1] {
			t.Errorf("script-src y style-src llevan nonces distintos: %s", csp)
		}
		if !strings.Contains(csp, "script-src 'self' 'nonce-") || !strings.Contains(csp, "style-src 'self' 'nonce-") {
			t.Errorf("el nonce no está en script-src y style-src: %s", csp)
		}
		nonces = append(nonces, coincidencias[0][1])
	}

	if nonces[0] == nonces[1] {
		t.Errorf("el nonce se repite entre peticiones (%q): tiene que ser por petición", nonces[0])
	}
}

// TestSonda_NoRecibeCookiesNiCSRF: /healthz se registra ANTES del Use(CSRF) a propósito. Una sonda
// de salud que recibe Set-Cookie ensucia el balanceador y, peor, hace creer que hay sesión donde no
// la hay.
func TestSonda_NoRecibeCookiesNiCSRF(t *testing.T) {
	t.Parallel()
	router := NewRouter(offlineConfig())

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if cookies := rec.Result().Cookies(); len(cookies) != 0 {
		t.Errorf("GET /healthz emitió cookies (%v); la sonda va por delante del CSRF", cookies)
	}
}

// TestEstaticos_NoRecibenCookies: mismo motivo que la sonda, y además una hoja de estilo con
// Set-Cookie se cachearía con la cookie dentro.
func TestEstaticos_NoRecibenCookies(t *testing.T) {
	t.Parallel()
	router := NewRouter(offlineConfig())

	for _, sheet := range append([]string{"app.css"}, sharedStylesheets...) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/css/"+sheet, nil))
		if cookies := rec.Result().Cookies(); len(cookies) != 0 {
			t.Errorf("GET /static/css/%s emitió cookies (%v)", sheet, cookies)
		}
	}
}

// TestRenderer_NoSeSirveHTMLSinPasarPorElRenderizador vigila lo que hace que las páginas lleven
// siempre CSRF y estado de sesión: el layout maestro necesita `.ContentTemplate`, y quien pinte con
// c.HTML a mano se lo puede olvidar. Aquí se comprueba el efecto observable: la página autenticada
// trae el formulario de salida con su token, y la de entrada no trae el formulario de salida.
func TestRenderer_NoSeSirveHTMLSinPasarPorElRenderizador(t *testing.T) {
	t.Parallel()
	router := NewRouter(offlineConfig())

	conSesion := httptest.NewRequest(http.MethodGet, "/", nil)
	conSesion.AddCookie(clientSessionCookie(t))
	recHome := httptest.NewRecorder()
	router.ServeHTTP(recHome, conSesion)

	recLogin := httptest.NewRecorder()
	router.ServeHTTP(recLogin, httptest.NewRequest(http.MethodGet, "/login", nil))

	if !strings.Contains(recHome.Body.String(), `name="csrf_token" value="`) {
		t.Error("la pantalla autenticada no incrusta el token CSRF del formulario de salida")
	}
	if strings.Contains(recLogin.Body.String(), `action="/logout"`) {
		t.Error("la pantalla de entrada ofrece cerrar sesión: IsAuthenticated está mal sembrado")
	}
	if !strings.Contains(recLogin.Body.String(), `name="csrf_token" value="`) {
		t.Error("el formulario de entrada no incrusta el token CSRF")
	}
	// Un token CSRF vacío pasaría los asserts de arriba y dejaría el double-submit inservible.
	if strings.Contains(recLogin.Body.String(), `name="csrf_token" value=""`) {
		t.Error("el token CSRF se incrusta vacío")
	}
}
