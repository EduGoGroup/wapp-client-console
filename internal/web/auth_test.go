package web

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	sharedweb "github.com/EduGoGroup/wapp-shared/web"
)

// Literales de los dos upstreams de mentira. El del Identity Token es reconocible a propósito: hay
// un test que busca esta cadena por TODA la respuesta para probar que no sale de la consola.
const (
	identityTokenLiteral = "identity-token-QUE-NO-DEBE-SALIR"
	identityRefreshFirst = "rt-identity-1"
	identityRefreshAfter = "rt-identity-2-rotado"
	loginEmail           = "dueña@empresa.test"
	loginPassword        = "test-only-password"
)

// fakeIdentity es identity-api (:8200) de mentira. Guarda lo que recibe para que los tests puedan
// afirmar QUÉ viajó por el cable, no solo qué devolvió la consola.
type fakeIdentity struct {
	*httptest.Server

	mu          sync.Mutex
	loginBody   map[string]string
	logoutBody  map[string]string
	logoutHits  int
	loginHits   int
	loginStatus int // 0 = responder el par de tokens
}

func newFakeIdentity(t *testing.T) *fakeIdentity {
	t.Helper()
	f := &fakeIdentity{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)

		f.mu.Lock()
		defer f.mu.Unlock()

		switch r.URL.Path {
		case "/api/v1/auth/login":
			f.loginHits++
			f.loginBody = body
			if f.loginStatus != 0 {
				w.WriteHeader(f.loginStatus)
				return
			}
			writeJSON(w, map[string]any{
				"identity_token": identityTokenLiteral,
				"refresh_token":  identityRefreshFirst,
				"expires_in":     900,
			})
		case "/api/v1/auth/refresh":
			writeJSON(w, map[string]any{
				"identity_token": identityTokenLiteral,
				"refresh_token":  identityRefreshAfter,
				"expires_in":     900,
			})
		case "/api/v1/auth/logout":
			f.logoutHits++
			f.logoutBody = body
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeIdentity) snapshot() (login, logout map[string]string, loginHits, logoutHits int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loginBody, f.logoutBody, f.loginHits, f.logoutHits
}

// newFakePlatform es la API pública (:8103) de mentira: solo sabe canjear el Identity Token por el
// Context Token que devuelve.
func newFakePlatform(t *testing.T, contextToken string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/exchange" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{
			"context_token": contextToken,
			"expires_at":    time.Now().Add(time.Hour).Format(time.RFC3339),
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// identityReturning levanta un identity que responde SIEMPRE el mismo status: para los caminos de
// rechazo, donde el cuerpo da igual.
func identityReturning(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// sessionCookieFrom extrae del recorder la cookie de sesión (nil si no se emitió).
func sessionCookieFrom(rec *httptest.ResponseRecorder) *http.Cookie {
	var found *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			found = c
		}
	}
	return found
}

// doLogin ejecuta el login completo contra los dos upstreams de mentira y devuelve router y recorder.
func doLogin(t *testing.T, contextToken string) (http.Handler, *httptest.ResponseRecorder, *fakeIdentity) {
	t.Helper()
	identity := newFakeIdentity(t)
	platform := newFakePlatform(t, contextToken)
	router := NewRouter(testConfig(platform.URL, identity.URL))
	rec := postFormWithCSRF(router, "/login", url.Values{
		"email":    {loginEmail},
		"password": {loginPassword},
	}, nil)
	return router, rec, identity
}

func TestAuth_LoginSuccess(t *testing.T) {
	t.Parallel()
	token := makeContextToken(t, time.Now().Add(time.Hour))
	_, rec, _ := doLogin(t, token)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /login status = %d, want 303. Body: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Fatalf("Location = %q, want /", loc)
	}

	cookie := sessionCookieFrom(rec)
	if cookie == nil {
		t.Fatal("la cookie de sesión no fue emitida")
	}
	if !cookie.HttpOnly {
		t.Error("la cookie de sesión debe ser HttpOnly")
	}
	if cookie.MaxAge != sessionCookieMaxAge {
		t.Errorf("MaxAge de la cookie de sesión = %d, want %d", cookie.MaxAge, sessionCookieMaxAge)
	}
}

// TestAuth_ElSystemQueViajaAIdentityEsWappBFF verifica el CABLEADO, no la constante: que
// `systemWappBFF` esté declarada no prueba que llegue al cuerpo del login, y ese campo es justo lo
// que el System Gate evalúa. Se afirma contra el literal "wapp.bff" además de contra la constante,
// porque comparar solo con la constante pasaría igual con la constante cambiada.
func TestAuth_ElSystemQueViajaAIdentityEsWappBFF(t *testing.T) {
	t.Parallel()
	_, rec, identity := doLogin(t, makeContextToken(t, time.Now().Add(time.Hour)))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /login status = %d, want 303. Body: %s", rec.Code, rec.Body.String())
	}

	login, _, hits, _ := identity.snapshot()
	if hits != 1 {
		t.Fatalf("identity recibió %d logins, want 1", hits)
	}
	if login["system"] != "wapp.bff" {
		t.Errorf(`el login viajó con system = %q, want "wapp.bff" (el perímetro del cliente)`, login["system"])
	}
	if login["system"] != systemWappBFF {
		t.Errorf("la constante systemWappBFF (%q) no es lo que viaja (%q)", systemWappBFF, login["system"])
	}
	if login["email"] != loginEmail {
		t.Errorf("el login viajó con email = %q, want %q", login["email"], loginEmail)
	}
}

// TestAuth_ElIdentityTokenNoVuelveAlNavegador es la invariante del doble token: el Identity Token
// dice QUIÉN ERES y muere dentro del cliente `iam`; lo único que la consola custodia y presenta es
// el CONTEXT Token del canje, que es el que lleva el tenant.
//
// Se comprueba por el cable y en los dos sentidos: el literal del Identity Token no aparece en
// ninguna cabecera ni en el cuerpo, y lo que hay dentro de la cookie ES el Context Token.
func TestAuth_ElIdentityTokenNoVuelveAlNavegador(t *testing.T) {
	t.Parallel()
	contextToken := makeContextToken(t, time.Now().Add(time.Hour))
	_, rec, _ := doLogin(t, contextToken)

	respuesta := rec.Result()
	for nombre, valores := range respuesta.Header {
		for _, v := range valores {
			if strings.Contains(v, identityTokenLiteral) {
				t.Fatalf("el Identity Token salió en la cabecera %s: %s", nombre, v)
			}
		}
	}
	if strings.Contains(rec.Body.String(), identityTokenLiteral) {
		t.Fatalf("el Identity Token salió en el cuerpo de la respuesta: %s", rec.Body.String())
	}

	cookie := sessionCookieFrom(rec)
	if cookie == nil {
		t.Fatal("la cookie de sesión no fue emitida")
	}
	sess, err := sharedweb.DecodeSession(cookie.Value)
	if err != nil {
		t.Fatalf("DecodeSession: %v", err)
	}
	if sess.AccessToken != contextToken {
		t.Errorf("la cookie custodia %q; el access token tiene que ser el Context Token del canje", sess.AccessToken)
	}
	if sess.AccessToken == identityTokenLiteral {
		t.Error("la cookie custodia el Identity Token: el canje no se aplicó")
	}
	// El refresh SÍ es el de identity (es suyo, y rota en cada uso); lo que no puede viajar es el
	// Identity Token.
	if sess.RefreshToken != identityRefreshFirst {
		t.Errorf("refresh custodiado = %q, want %q", sess.RefreshToken, identityRefreshFirst)
	}
}

// TestAuth_LoginEmiteCookieSecureYSameSite complementa al de éxito, que solo puede afirmar HttpOnly
// porque testConfig fija CookieSecure:false.
func TestAuth_LoginEmiteCookieSecureYSameSite(t *testing.T) {
	t.Parallel()
	identity := newFakeIdentity(t)
	platform := newFakePlatform(t, makeContextToken(t, time.Now().Add(time.Hour)))
	cfg := testConfig(platform.URL, identity.URL)
	cfg.CookieSecure = true
	cfg.CookieSameSite = "strict"
	router := NewRouter(cfg)

	rec := postFormWithCSRF(router, "/login", url.Values{
		"email": {loginEmail}, "password": {loginPassword},
	}, nil)

	cookie := sessionCookieFrom(rec)
	if cookie == nil {
		t.Fatal("la cookie de sesión no fue emitida")
	}
	if !cookie.HttpOnly {
		t.Error("la cookie de sesión debe ser HttpOnly")
	}
	if !cookie.Secure {
		t.Error("la cookie de sesión debe ser Secure con CookieSecure=true")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", cookie.SameSite)
	}
}

// TestAuth_401Y403DanElMismoTextoAlUsuario: al que está en la pantalla de entrada no se le dice si
// su correo existe. El 401 (credenciales) y el 403 (System Gate: existe, la contraseña es correcta,
// pero no tiene esta aplicación) se funden en un solo mensaje.
//
// Es la mitad visible del par; la otra —que el LOG sí los distinga— la vigila
// TestAuth_ElLogDISTINGUE401De403. Las dos juntas son la invariante: fundir para el usuario, separar
// para quien diagnostica.
func TestAuth_401Y403DanElMismoTextoAlUsuario(t *testing.T) {
	t.Parallel()
	avisos := map[int]string{}

	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		identity := identityReturning(t, status)
		router := NewRouter(testConfig("http://127.0.0.1:8103", identity.URL))
		rec := postFormWithCSRF(router, "/login", url.Values{
			"email": {loginEmail}, "password": {loginPassword},
		}, nil)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("con identity devolviendo %d, la consola respondió %d, want 401", status, rec.Code)
		}
		aviso := avisoDeError(rec.Body.String())
		if aviso == "" {
			t.Fatalf("con identity devolviendo %d la pantalla no pinta ningún aviso: %s", status, rec.Body.String())
		}
		avisos[status] = aviso
	}

	const generico = "Credenciales inválidas o sin acceso a esta consola."
	for status, aviso := range avisos {
		if aviso != generico {
			t.Errorf("con identity devolviendo %d la pantalla dice %q, want %q", status, aviso, generico)
		}
	}
	// Se comparan los AVISOS y no los cuerpos enteros: el token CSRF y el nonce cambian en cada
	// petición, así que dos respuestas idénticas para el usuario nunca serían byte a byte iguales.
	if avisos[http.StatusUnauthorized] != avisos[http.StatusForbidden] {
		t.Errorf("la pantalla distingue el 401 (%q) del 403 (%q): eso le dice a un desconocido si el correo existe",
			avisos[http.StatusUnauthorized], avisos[http.StatusForbidden])
	}
}

// avisoDeError extrae el texto del snackbar de error de una página renderizada (vacío si no hay).
var snackbarDeError = regexp.MustCompile(`(?s)<div class="wapp-snackbar wapp-snackbar--error" role="alert">\s*(.*?)\s*</div>`)

func avisoDeError(body string) string {
	m := snackbarDeError.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// TestAuth_ElLogDISTINGUE401De403 — el mensaje de la pantalla funde credenciales y System Gate a
// propósito, así que el LOG es el ÚNICO sitio donde queda la diferencia. Quien diagnostica un «no
// puedo entrar» decide con esa línea si buscar la contraseña o la fila de `iam.user_systems`.
//
// 🔴 Nació de un fallo real en la consola de plataforma: el 2026-08-28 un operador no pudo entrar en
// UAT y el log solo tenía un 401 pelado del middleware. La causa hubo que deducirla por la AUSENCIA
// de la línea del System Gate —un razonamiento que funciona una vez y deja ciego al siguiente—,
// porque la rama de credenciales no escribía nada.
//
// Deliberadamente SIN t.Parallel(): slog.SetDefault es global. Los tests marcados Parallel están
// pausados mientras este corre, así que el buffer solo recoge lo de este login.
func TestAuth_ElLogDISTINGUE401De403(t *testing.T) {
	casos := []struct {
		nombre     string
		estado     int
		esperado   string
		noEsperado string
	}{
		{"credenciales", http.StatusUnauthorized, "credenciales inválidas", "System Gate"},
		{"system_gate", http.StatusForbidden, "System Gate", "credenciales inválidas"},
	}

	const correo = "quien-no-entra@empresa.test"

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			identity := identityReturning(t, c.estado)

			var log bytes.Buffer
			anterior := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelWarn})))
			defer slog.SetDefault(anterior)

			router := NewRouter(testConfig("http://127.0.0.1:8103", identity.URL))
			postFormWithCSRF(router, "/login", url.Values{
				"email": {correo}, "password": {loginPassword},
			}, nil)

			escrito := log.String()
			if !strings.Contains(escrito, c.esperado) {
				t.Fatalf("con identity devolviendo %d, el log tiene que decir %q y dice: %s", c.estado, c.esperado, escrito)
			}
			if strings.Contains(escrito, c.noEsperado) {
				t.Fatalf("con identity devolviendo %d, el log NO puede decir %q: %s", c.estado, c.noEsperado, escrito)
			}
			// CERO PII: el correo no entra en el log de esta consola.
			if strings.Contains(escrito, correo) {
				t.Fatalf("el correo NO puede aparecer en el log: %s", escrito)
			}
		})
	}
}

// TestAuth_ElCorreoSobreviveAlIntentoFallido — nadie debe reescribir su correo en cada intento. La
// contraseña NO se repuebla, y el test lo exige: repoblar el correo es comodidad; repoblar la
// contraseña sería mandarla de vuelta al navegador dentro del HTML.
func TestAuth_ElCorreoSobreviveAlIntentoFallido(t *testing.T) {
	t.Parallel()
	identity := identityReturning(t, http.StatusUnauthorized)
	router := NewRouter(testConfig("http://127.0.0.1:8103", identity.URL))

	const clave = "la-que-tecleo-y-no-cuela"
	rec := postFormWithCSRF(router, "/login", url.Values{
		"email": {loginEmail}, "password": {clave},
	}, nil)

	body := rec.Body.String()
	if !strings.Contains(body, loginEmail) {
		t.Fatalf("el correo tecleado tiene que volver en el formulario y no vuelve; cuerpo: %s", body)
	}
	if strings.Contains(body, clave) {
		t.Fatal("la contraseña NO puede volver al navegador dentro del HTML")
	}
}

// TestAuth_CamposVaciosNoTocanIdentity: un formulario a medias se rechaza aquí. Sin esto, cada
// campo vacío sería un viaje a identity y una entrada más en su contador de intentos fallidos.
func TestAuth_CamposVaciosNoTocanIdentity(t *testing.T) {
	t.Parallel()
	identity := newFakeIdentity(t)
	platform := newFakePlatform(t, makeContextToken(t, time.Now().Add(time.Hour)))
	router := NewRouter(testConfig(platform.URL, identity.URL))

	rec := postFormWithCSRF(router, "/login", url.Values{"email": {loginEmail}, "password": {""}}, nil)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /login sin contraseña status = %d, want 400", rec.Code)
	}
	if _, _, hits, _ := identity.snapshot(); hits != 0 {
		t.Errorf("identity recibió %d logins con el formulario a medias, want 0", hits)
	}
}

// TestAuth_LogoutCierraEnIdentityYBorraLaCookie verifica el efecto local (la cookie caduca) Y el
// remoto: cerrar sesión tiene que invalidar el refresh en identity, no solo olvidarlo en el
// navegador.
func TestAuth_LogoutCierraEnIdentityYBorraLaCookie(t *testing.T) {
	t.Parallel()
	identity := newFakeIdentity(t)
	router := NewRouter(testConfig("http://127.0.0.1:8103", identity.URL))

	rec := postFormWithCSRF(router, "/logout", url.Values{}, clientSessionCookie(t))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /logout status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login?success="+flashLoggedOut {
		t.Fatalf("Location = %q, want /login?success=%s", loc, flashLoggedOut)
	}

	cookie := sessionCookieFrom(rec)
	if cookie == nil || cookie.MaxAge >= 0 {
		t.Fatalf("la cookie de sesión no fue caducada: %+v", cookie)
	}

	_, logout, _, hits := identity.snapshot()
	if hits != 1 {
		t.Fatalf("identity recibió %d logouts, want 1", hits)
	}
	if logout["refresh_token"] != "rt-1" {
		t.Errorf("el logout envió refresh_token = %q, want %q", logout["refresh_token"], "rt-1")
	}
}

// TestAuth_LogoutBorraLaCookieAunqueIdentityFalle: nadie debe quedarse con una sesión que cree
// cerrada. El fallo remoto no se traga (queda en el log), pero no impide cerrar en local.
func TestAuth_LogoutBorraLaCookieAunqueIdentityFalle(t *testing.T) {
	t.Parallel()
	identity := identityReturning(t, http.StatusInternalServerError)
	router := NewRouter(testConfig("http://127.0.0.1:8103", identity.URL))

	rec := postFormWithCSRF(router, "/logout", url.Values{}, clientSessionCookie(t))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /logout status = %d, want 303", rec.Code)
	}
	cookie := sessionCookieFrom(rec)
	if cookie == nil || cookie.MaxAge >= 0 {
		t.Fatalf("la cookie debe borrarse localmente aunque identity falle: %+v", cookie)
	}
}

// TestAuth_SinSesionRedirigeALoginSinAviso: quien nunca entró no ha perdido nada, así que la
// pantalla de entrada no le cuenta que «su sesión caducó».
func TestAuth_SinSesionRedirigeALoginSinAviso(t *testing.T) {
	t.Parallel()
	router := NewRouter(offlineConfig())

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET / sin sesión status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("Location = %q, want /login (sin aviso)", loc)
	}
}

// TestAuth_SesionExpiradaRedirigeConAviso ejercita SessionValid de verdad: la cookie SÍ decodifica a
// un Context Token legible, pero vencido, y sin refresh (para aislar la comprobación de expiración
// de la rama de refresco). Y comprueba que el aviso que se pinta sale del CATÁLOGO.
func TestAuth_SesionExpiradaRedirigeConAviso(t *testing.T) {
	t.Parallel()
	router := NewRouter(offlineConfig())

	vencida := sessionCookieWith(t, makeContextToken(t, time.Now().Add(-time.Hour)), "")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(vencida)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET / con sesión expirada status = %d, want 303", rec.Code)
	}
	destino := rec.Header().Get("Location")
	if destino != "/login?error="+flashSessionExpired {
		t.Fatalf("Location = %q, want /login?error=%s", destino, flashSessionExpired)
	}
	if cookie := sessionCookieFrom(rec); cookie == nil || cookie.MaxAge >= 0 {
		t.Errorf("la cookie vencida tiene que borrarse: %+v", cookie)
	}

	// El aviso que ve el usuario sale de la tabla, no del query string.
	recLogin := httptest.NewRecorder()
	router.ServeHTTP(recLogin, httptest.NewRequest(http.MethodGet, destino, nil))
	if !strings.Contains(recLogin.Body.String(), flashError(flashSessionExpired)) {
		t.Errorf("la pantalla de entrada no pinta el aviso de sesión caducada: %s", recLogin.Body.String())
	}
}

// TestAuth_UnCodigoDesconocidoNoSePintaTalCual: `?error=` transporta un CÓDIGO, no un mensaje. Lo
// que se pinta sale siempre del catálogo; texto arbitrario de la URL cae al genérico.
func TestAuth_UnCodigoDesconocidoNoSePintaTalCual(t *testing.T) {
	t.Parallel()
	router := NewRouter(offlineConfig())

	const inyectado = "TEXTO-INVENTADO-EN-LA-URL"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login?error="+inyectado, nil))

	body := rec.Body.String()
	if strings.Contains(body, inyectado) {
		t.Fatalf("la pantalla pintó el texto del query string: %s", body)
	}
	if !strings.Contains(body, sharedweb.DefaultFlashFallback) {
		t.Errorf("un código desconocido tiene que caer al mensaje genérico: %s", body)
	}
}

// TestAuth_ConSesionValidaElLoginMandaAHome: quien ya entró no vuelve a ver el formulario.
func TestAuth_ConSesionValidaElLoginMandaAHome(t *testing.T) {
	t.Parallel()
	router := NewRouter(offlineConfig())

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.AddCookie(clientSessionCookie(t))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET /login con sesión válida status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Fatalf("Location = %q, want /", loc)
	}
}

// TestHome_MuestraLaEmpresaYElUsuarioDeLaSesion: los dos datos salen de los claims del Context Token
// que sembró el AuthMiddleware. Es lo único que esta tanda promete pintar.
func TestHome_MuestraLaEmpresaYElUsuarioDeLaSesion(t *testing.T) {
	t.Parallel()
	router := NewRouter(offlineConfig())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(clientSessionCookie(t))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / con sesión status = %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, testTenantID) {
		t.Errorf("la pantalla no muestra la empresa (%s): %s", testTenantID, body)
	}
	if !strings.Contains(body, testUserID) {
		t.Errorf("la pantalla no muestra el usuario (%s): %s", testUserID, body)
	}
	// Con sesión, el layout tiene que ofrecer la salida.
	if !strings.Contains(body, `action="/logout"`) {
		t.Errorf("la pantalla autenticada no ofrece cerrar sesión: %s", body)
	}
}

// TestAuth_RefrescoProactivoRenuevaLaSesion ejercita la rama de refresco: un Context Token al que le
// quedan 30 s cae dentro del margen de RefreshDue, así que el middleware renueva ANTES de que
// caduque y la petición se sirve sin que el usuario note nada.
func TestAuth_RefrescoProactivoRenuevaLaSesion(t *testing.T) {
	t.Parallel()
	identity := newFakeIdentity(t)
	renovado := makeContextToken(t, time.Now().Add(time.Hour))
	platform := newFakePlatform(t, renovado)
	router := NewRouter(testConfig(platform.URL, identity.URL))

	casi := sessionCookieWith(t, makeContextToken(t, time.Now().Add(30*time.Second)), identityRefreshFirst)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(casi)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / con sesión por caducar status = %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}

	cookie := sessionCookieFrom(rec)
	if cookie == nil {
		t.Fatal("el refresco no reemitió la cookie de sesión")
	}
	sess, err := sharedweb.DecodeSession(cookie.Value)
	if err != nil {
		t.Fatalf("DecodeSession: %v", err)
	}
	if sess.AccessToken != renovado {
		t.Error("la cookie no quedó con el Context Token renovado")
	}
	if sess.RefreshToken != identityRefreshAfter {
		t.Errorf("el refresh no rotó: %q, want %q", sess.RefreshToken, identityRefreshAfter)
	}
}

// TestFlash_TodoCodigoEmitidoTieneTraduccion: un código que los handlers emiten y el catálogo no
// conoce no rompe nada —cae al genérico— y por eso se pasa por alto. Aquí se afirma que los dos
// códigos que esta consola emite de verdad tienen su texto.
func TestFlash_TodoCodigoEmitidoTieneTraduccion(t *testing.T) {
	t.Parallel()

	if !flashErrors.Known(flashSessionExpired) {
		t.Errorf("el código %q no está en el catálogo de errores", flashSessionExpired)
	}
	if !flashSuccesses.Known(flashLoggedOut) {
		t.Errorf("el código %q no está en el catálogo de éxitos", flashLoggedOut)
	}
	if flashError("") != "" || flashSuccess("") != "" {
		t.Error("sin código no se pinta ningún aviso")
	}
}
