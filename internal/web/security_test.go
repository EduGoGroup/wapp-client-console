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

	// El login fallido se renderiza en la respuesta de un POST, no de un GET.
	identity := identityReturning(t, http.StatusUnauthorized)
	routerFallo := NewRouter(testConfig("http://127.0.0.1:8103", identity.URL))
	renders["login_fallido"] = postFormWithCSRF(routerFallo, "/login", url.Values{
		"email": {loginEmail}, "password": {loginPassword},
	}, nil)

	// Las pantallas de administración necesitan un upstream que CONTESTE: contra el router offline
	// degradarían a su estado vacío y esta familia de tests no vería ni las tablas, ni los
	// formularios, ni el bloque gateado por plan — que es justo donde se cuela un `style=` o un
	// `<script>`. Un router aparte con el doble de la API pública los pinta enteros.
	api := newStubAPI(t, map[string]stubResponse{
		"GET /api/v1/entitlements": {http.StatusOK, entitlementsBody("commerce", "catalog_import", "menu")},
		"GET /api/v1/members":      {http.StatusOK, membersBody(testUserID, testOtherUserID)},
		"GET /api/v1/roles":        {http.StatusOK, rolesBody},
		// La pantalla de sesiones se pinta ENTERA —con su tabla, sus chips y su formulario—, que es
		// donde se colaría un `style=` o un `<script>`. Con el listado vacío no se vería ninguna de
		// las tres cosas.
		"GET /api/v1/sessions": {http.StatusOK, sesionesBody(
			`{"session_id":"` + testSessionID + `","edge_id":"edge-alpha","state":"online",` +
				`"profile":"active","self_pn":"+593990000001",` +
				`"intent_circuit":"open","worker_taskset":"solapada"}`)},
	})
	routerAdmin := adminRouter(api)
	for nombre, ruta := range map[string]string{
		"home_con_plan":    "/",
		"sesiones":         "/sesiones",
		"miembros":         "/miembros",
		"roles":            "/roles",
		"mi_identificador": "/mi-identificador",
	} {
		rec := getWithSession(t, routerAdmin, ruta)
		if rec.Code != http.StatusOK {
			t.Fatalf("la página %q respondió %d, want 200. Body: %s", nombre, rec.Code, rec.Body.String())
		}
		renders[nombre] = rec
	}

	// Las pantallas de administración con AVISO: el texto del catálogo de flash también se sirve, y
	// es la rama por la que entra un mensaje que alguien construyera concatenando.
	renders["miembros_con_aviso"] = getWithSession(t, routerAdmin, "/miembros?error="+flashNotInYourTenant)
	renders["roles_con_exito"] = getWithSession(t, routerAdmin, "/roles?success="+flashRoleAssigned)

	// Y las MISMAS pantallas en el estado «sin empresa», que es otra rama de plantilla —el parcial
	// sin_empresa— y por tanto otro HTML que puede traer un style= o un <script> sin que ninguna de
	// las capturas de arriba lo vea.
	sinTenant := sessionCookieFor(t, testUserID, "")
	for nombre, ruta := range map[string]string{
		"home_sin_empresa":             "/",
		"sesiones_sin_empresa":         "/sesiones",
		"miembros_sin_empresa":         "/miembros",
		"roles_sin_empresa":            "/roles",
		"mi_identificador_sin_empresa": "/mi-identificador",
	} {
		rec := getConCookie(routerAdmin, ruta, sinTenant)
		if rec.Code != http.StatusOK {
			t.Fatalf("la página %q respondió %d, want 200. Body: %s", nombre, rec.Code, rec.Body.String())
		}
		renders[nombre] = rec
	}

	return renders
}

// TestPaginas_TodasLasPantallasAutenticadasEstanCubiertas evita el agujero silencioso de esta
// familia: los tests de CSP recorren `paginasRenderizadas`, así que una pantalla nueva que no se
// añada ahí queda sin vigilar y nadie lo nota, porque todo sigue verde.
//
// Aquí se compara contra las rutas GET que el router registra de verdad, no contra una lista escrita
// a mano: si mañana aparece /pedidos y nadie la añade al mapa, esto cae.
func TestPaginas_TodasLasPantallasAutenticadasEstanCubiertas(t *testing.T) {
	t.Parallel()

	router := NewRouter(offlineConfig())
	rutasGET := make(map[string]bool)
	for _, ruta := range router.Routes() {
		if ruta.Method != http.MethodGet || strings.HasPrefix(ruta.Path, "/static/") ||
			ruta.Path == "/healthz" || ruta.Path == "/login" {
			continue
		}
		rutasGET[ruta.Path] = true
	}

	cubiertas := map[string]bool{
		"/": true, "/sesiones": true, "/miembros": true, "/roles": true, "/mi-identificador": true,
	}
	for ruta := range rutasGET {
		if !cubiertas[ruta] {
			t.Errorf("la pantalla %q no está en paginasRenderizadas: los tests de CSP no la miran", ruta)
		}
	}
	if len(rutasGET) != len(cubiertas) {
		t.Errorf("el router sirve %d pantallas autenticadas y el mapa cubre %d", len(rutasGET), len(cubiertas))
	}
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
