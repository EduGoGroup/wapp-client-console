package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// invitations_test.go cubre las CUATRO pantallas de la invitación (T-A7) y la revocación (T-A8).
//
// El criterio caro es el primero —que un F5 no vuelva a emitir— y NO se comprueba «a ojo» mirando si
// la página se ve igual: se CUENTAN las emisiones contra el doble de la API. Y se comprueba con un
// navegador de verdad —cliente con `cookiejar`— porque la cookie efímera del código depende de dos
// cosas que un httptest.NewRecorder no simula: que el tarro honre el borrado (MaxAge=-1) y que el
// Path acote a qué peticiones se manda.

// Identificadores de las invitaciones de prueba. Los cuatro estados van juntos porque la tabla los
// pinta distinto y el botón de anular solo sale en uno.
const (
	testInvitacionPendiente = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	testInvitacionCanjeada  = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	testInvitacionAnulada   = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	testInvitacionCaducada  = "99999999-9999-4999-8999-999999999999"
	// testInviteToken es un código en claro como el que devuelve la emisión. Es reconocible a
	// propósito: los tests afirman dónde aparece y, sobre todo, dónde NO.
	testInviteToken = "INV-CODIGO-EN-CLARO-1"
)

// invitacionesBody arma la respuesta de GET /api/v1/invitations con los cuatro estados.
//
// 🔴 NINGUNA fila trae `token`, igual que en la API de verdad: el listado no lo devuelve y en la
// tabla solo vive su digest. El caso contrario —un servidor que sí lo mandara— tiene su propio test,
// que es donde se afirma que esta consola no lo pintaría de todas formas.
func invitacionesBody() string {
	return `[` +
		`{"id":"` + testInvitacionPendiente + `","status":"pending","expires_at":"2026-08-29T10:00:00Z",` +
		`"role_id":"` + testTenantRoleID + `","created_at":"2026-08-28T10:00:00Z"},` +
		`{"id":"` + testInvitacionCanjeada + `","status":"redeemed","expires_at":"2026-08-29T09:00:00Z",` +
		`"created_at":"2026-08-28T09:00:00Z","redeemed_at":"2026-08-28T09:30:00Z"},` +
		`{"id":"` + testInvitacionAnulada + `","status":"revoked","expires_at":"2026-08-29T08:00:00Z",` +
		`"created_at":"2026-08-28T08:00:00Z","revoked_at":"2026-08-28T08:30:00Z"},` +
		`{"id":"` + testInvitacionCaducada + `","status":"expired","expires_at":"2026-08-27T08:00:00Z",` +
		`"created_at":"2026-08-26T08:00:00Z"}` +
		`]`
}

// invitacionEmitidaBody arma la respuesta 201 del POST: la invitación MÁS su código en claro.
func invitacionEmitidaBody(token string) string {
	return `{"id":"` + testInvitacionPendiente + `","status":"pending","expires_at":"2026-08-29T10:00:00Z",` +
		`"created_at":"2026-08-28T10:00:00Z","token":"` + token + `"}`
}

// invitacionesOK son las rutas del doble para una pantalla de invitaciones que funciona.
func invitacionesOK() map[string]stubResponse {
	return map[string]stubResponse{
		"GET /api/v1/invitations":         {http.StatusOK, invitacionesBody()},
		"POST /api/v1/invitations":        {http.StatusCreated, invitacionEmitidaBody(testInviteToken)},
		"DELETE /api/v1/invitations/{id}": {http.StatusNoContent, ""},
		"POST /api/v1/invitations/accept": {http.StatusNoContent, ""},
		"GET /api/v1/roles":               {http.StatusOK, rolesBody},
	}
}

// emitirInvitacion manda el formulario de emisión con la caducidad indicada ("" = la que traiga el
// formulario por defecto, que aquí se escribe explícita para no depender de la plantilla).
func emitirInvitacion(router http.Handler, ttl string, sess *http.Cookie) *httptest.ResponseRecorder {
	form := url.Values{}
	if ttl != "" {
		form.Set("ttl", ttl)
	}
	return postFormWithCSRF(router, "/invitaciones", form, sess)
}

// cookieDeInvitacion emite una invitación y devuelve la cookie efímera con la que el GET pinta la
// caja del código. Falla el test si la emisión no la puso: sin ella, la pantalla que se quiere
// examinar no existe y el test que la use estaría mirando otra cosa.
func cookieDeInvitacion(t *testing.T, router http.Handler) *http.Cookie {
	t.Helper()
	rec := emitirInvitacion(router, "86400", clientSessionCookie(t))
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == invitacionCookieName {
			return ck
		}
	}
	t.Fatalf("la emisión no puso la cookie del código (status %d, cookies %v)", rec.Code, rec.Result().Cookies())
	return nil
}

// --- El criterio del PRG, contado y no mirado ---

// invitacionesAPIStub levanta un doble de la API pública que CUENTA las emisiones y devuelve un
// código distinto en cada una, para que una segunda emisión no se pueda confundir con la primera.
func invitacionesAPIStub(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	var emisiones atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/invitations" && r.Method == http.MethodPost:
			n := emisiones.Add(1)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, invitacionEmitidaBody(codigoInvitacion(int(n))))
		case r.URL.Path == "/api/v1/invitations" && r.Method == http.MethodGet:
			_, _ = io.WriteString(w, invitacionesBody())
		case r.URL.Path == "/api/v1/roles" && r.Method == http.MethodGet:
			_, _ = io.WriteString(w, rolesBody)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &emisiones
}

// codigoInvitacion nombra la n-ésima emisión. Si un F5 reemitiera, en pantalla aparecería la 2.ª.
func codigoInvitacion(n int) string { return "INV-EMISION-" + strconv.Itoa(n) }

// consolaConTarro arranca la consola completa contra el doble y devuelve un cliente con TARRO DE
// COOKIES, ya con la sesión dentro y sin seguir redirects (para poder mirar cada 303 por dentro).
func consolaConTarro(t *testing.T, apiURL string) (*httptest.Server, *http.Client) {
	t.Helper()

	srv := httptest.NewServer(NewRouter(testConfig(apiURL, "http://127.0.0.1:8200")))
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("url del servidor: %v", err)
	}
	jar.SetCookies(base, []*http.Cookie{clientSessionCookie(t)})

	cliente := &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	// El middleware CSRF siembra su cookie en el primer GET; el tarro la recoge solo.
	resp, err := cliente.Get(srv.URL + "/login")
	if err != nil {
		t.Fatalf("GET /login: %v", err)
	}
	_ = leerCuerpo(t, resp)

	return srv, cliente
}

// tokenCSRFDelTarro saca del tarro el token que hay que devolver en el formulario.
func tokenCSRFDelTarro(t *testing.T, cliente *http.Client, srvURL string) string {
	t.Helper()
	u, err := url.Parse(srvURL)
	if err != nil {
		t.Fatalf("url del servidor: %v", err)
	}
	for _, ck := range cliente.Jar.Cookies(u) {
		if ck.Name == csrfCookieName {
			return ck.Value
		}
	}
	t.Fatal("el tarro no tiene la cookie CSRF: el POST se rechazaría con 403 y el test no probaría nada")
	return ""
}

func leerCuerpo(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("leer cuerpo: %v", err)
	}
	return string(b)
}

// TestInvitaciones_F5NoReemite es EL criterio de T-A7, y se afirma CONTANDO emisiones.
//
// El fallo que evita no es cosmético: el código se enseña una sola vez, así que cada reenvío del
// formulario crearía una invitación válida y viva durante horas QUE YA NADIE PUEDE VER — y limpiarla
// sería trabajo manual. Es la deuda M-10 de la consola de plataforma, que aquí no se hereda.
//
// Mutación comprobada: renderizar sobre el POST (devolver la página en vez del 303) deja este test en
// rojo en el primer aserto, porque sin Location no hay pantalla a la que ir.
func TestInvitaciones_F5NoReemite(t *testing.T) {
	t.Parallel()

	api, emisiones := invitacionesAPIStub(t)
	srv, cliente := consolaConTarro(t, api.URL)

	// 1. El POST emite y REDIRIGE. Si volviera a renderizar (200), el F5 reenviaría el formulario.
	form := url.Values{"csrf_token": {tokenCSRFDelTarro(t, cliente, srv.URL)}, "ttl": {"86400"}}
	post, err := cliente.PostForm(srv.URL+"/invitaciones", form)
	if err != nil {
		t.Fatalf("POST /invitaciones: %v", err)
	}
	cuerpoPost := leerCuerpo(t, post)
	if post.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST = %d, want 303: sin redirect, un F5 reenvía el POST y emite otra invitación. Body: %s",
			post.StatusCode, cuerpoPost)
	}
	destino := post.Header.Get("Location")
	if destino != "/invitaciones" {
		t.Fatalf("Location = %q, want /invitaciones", destino)
	}
	// 🔴 El código NO puede ir en la URL: acabaría en el log de acceso, en el Referer y en el
	// historial, y autoriza a entrar en la empresa. Es la misma fuga que movió el token al cuerpo en
	// la API.
	if strings.Contains(destino, codigoInvitacion(1)) || strings.Contains(destino, "token") {
		t.Fatalf("el código viaja en la URL del redirect: %q", destino)
	}
	if strings.Contains(cuerpoPost, codigoInvitacion(1)) {
		t.Fatalf("el código viene en el cuerpo del POST: %q", cuerpoPost)
	}
	if n := emisiones.Load(); n != 1 {
		t.Fatalf("emisiones tras el POST = %d, want 1", n)
	}

	// 2. La pantalla del código: aquí, y solo aquí, se ve.
	primero, err := cliente.Get(srv.URL + destino)
	if err != nil {
		t.Fatalf("GET %s: %v", destino, err)
	}
	cuerpo := leerCuerpo(t, primero)
	if primero.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200. Body: %s", destino, primero.StatusCode, cuerpo)
	}
	if !strings.Contains(cuerpo, codigoInvitacion(1)) {
		t.Fatalf("la pantalla no muestra el código emitido. Body: %s", cuerpo)
	}
	if !strings.Contains(cuerpo, "</html>") {
		t.Fatal("la página del código quedó truncada: el código se pierde a medias")
	}

	// 3. F5. El navegador repite el GET —no hay POST que reenviar— y la cookie ya no está.
	segundo, err := cliente.Get(srv.URL + destino)
	if err != nil {
		t.Fatalf("F5 sobre %s: %v", destino, err)
	}
	cuerpo2 := leerCuerpo(t, segundo)
	if segundo.StatusCode != http.StatusOK {
		t.Fatalf("F5 = %d, want 200 (el listado, sin el código). Body: %s", segundo.StatusCode, cuerpo2)
	}
	if strings.Contains(cuerpo2, codigoInvitacion(1)) {
		t.Error("el código reaparece en la recarga: la cookie no se consumió")
	}
	if strings.Contains(cuerpo2, codigoInvitacion(2)) {
		t.Error("la recarga enseña un código NUEVO: el F5 reemitió")
	}
	// Y la pantalla sigue siendo la pantalla: el listado está ahí.
	if !strings.Contains(cuerpo2, `id="table-invitaciones"`) {
		t.Errorf("tras el F5 no se pinta el listado. Body: %s", cuerpo2)
	}

	// 4. EL CRITERIO: una sola emisión en todo el recorrido.
	if n := emisiones.Load(); n != 1 {
		t.Fatalf("emisiones = %d, want 1: el F5 volvió a emitir una invitación (M-10)", n)
	}
}

// TestInvitaciones_SinCookieNoHayCodigo: entrar a la pantalla a pelo —o compartir su URL— no enseña
// ningún código ni emite ninguno. El secreto está en la cookie, no en la dirección.
func TestInvitaciones_SinCookieNoHayCodigo(t *testing.T) {
	t.Parallel()

	api, emisiones := invitacionesAPIStub(t)
	router := NewRouter(testConfig(api.URL, "http://127.0.0.1:8200"))

	out := getWithSession(t, router, "/invitaciones").Body.String()
	if strings.Contains(out, "wapp-secret-box") {
		t.Error("la pantalla pinta la caja del secreto sin que se haya emitido nada")
	}
	if strings.Contains(out, codigoInvitacion(1)) {
		t.Error("la pantalla enseña un código sin cookie")
	}
	if n := emisiones.Load(); n != 0 {
		t.Fatalf("emisiones = %d: abrir la pantalla NO puede emitir nada", n)
	}
}

// TestInvitaciones_LaCookieDelCodigoEsHttpOnlyYAcotada fija las dos propiedades del transporte que
// ningún otro test mira: si dejara de ser HttpOnly, un XSS leería el código sin raspar el DOM; si el
// Path se ensanchara a "/", el código viajaría en TODAS las peticiones de la consola —incluidas las
// de los estáticos— en vez de solo en la pantalla que lo pinta.
func TestInvitaciones_LaCookieDelCodigoEsHttpOnlyYAcotada(t *testing.T) {
	t.Parallel()

	api := newStubAPI(t, invitacionesOK())
	ck := cookieDeInvitacion(t, adminRouter(api))

	if !ck.HttpOnly {
		t.Error("la cookie del código no es HttpOnly")
	}
	if ck.Path != "/invitaciones" {
		t.Errorf("Path = %q, want /invitaciones (acotada a la pantalla, no al sitio)", ck.Path)
	}
	if ck.MaxAge <= 0 || ck.MaxAge > 120 {
		t.Errorf("MaxAge = %d s: es un tope para seguir un redirect, no una sesión de trabajo", ck.MaxAge)
	}
}

// --- El listado ---

// TestInvitaciones_ElListadoNoEnsenaNingunCodigo es el segundo criterio de la ola.
//
// El doble devuelve un listado que SÍ trae `token` en cada fila —lo que haría un servidor con una
// regresión—, y aun así no aparece en la pantalla: esta consola no vuelca lo que llega, decodifica un
// tipo que no tiene ese campo y pinta las columnas que decidió. Sin esa fila envenenada el test sería
// vacuo: estaría comprobando que no se pinta algo que nunca llegó.
//
// El gemelo POSITIVO va al lado: la fila SÍ se pinta (su identificador está), así que el negativo no
// puede pasar por no haberse renderizado nada.
func TestInvitaciones_ElListadoNoEnsenaNingunCodigo(t *testing.T) {
	t.Parallel()

	const veneno = "TOKEN-QUE-EL-SERVIDOR-NO-DEBERIA-MANDAR"
	rutas := invitacionesOK()
	rutas["GET /api/v1/invitations"] = stubResponse{http.StatusOK,
		`[{"id":"` + testInvitacionPendiente + `","status":"pending","expires_at":"2026-08-29T10:00:00Z",` +
			`"token":"` + veneno + `","token_hash":"sha256-de-mentira"}]`}
	api := newStubAPI(t, rutas)

	out := getWithSession(t, adminRouter(api), "/invitaciones").Body.String()

	// Positivo: la fila se pintó.
	if !strings.Contains(out, shortID(testInvitacionPendiente)) {
		t.Fatalf("la invitación no aparece en el listado: el negativo de abajo sería vacuo. Body: %s", out)
	}
	// Negativo: ni el código ni su digest.
	if strings.Contains(out, veneno) {
		t.Error("el listado pintó el código de la invitación")
	}
	if strings.Contains(out, "sha256-de-mentira") {
		t.Error("el listado pintó el digest del código: es la clave de acceso del canje")
	}
}

// TestInvitaciones_CadaEstadoSePintaYSoloLaVivaSeAnula.
//
// Los cuatro estados con su chip, y el botón de anular SOLO en la pendiente: ofrecerlo sobre una
// canjeada prometería deshacer una membresía que la revocación no toca.
//
// El negativo (tres filas sin botón) se ancla en el positivo de la primera: si el botón dejara de
// pintarse por cualquier motivo, el positivo cae y el negativo no podría pasar solo.
func TestInvitaciones_CadaEstadoSePintaYSoloLaVivaSeAnula(t *testing.T) {
	t.Parallel()

	api := newStubAPI(t, invitacionesOK())
	out := getWithSession(t, adminRouter(api), "/invitaciones").Body.String()

	for _, etiqueta := range []string{"pendiente", "canjeada", "anulada", "caducada"} {
		if !strings.Contains(out, ">"+etiqueta+"<") {
			t.Errorf("el estado %q no se pinta en la tabla", etiqueta)
		}
	}
	// Los cuatro chips son los del módulo compartido, y quien decide cuál va en cada fila es
	// estadoInvitacion: el único sitio de esta pantalla donde se escribe un nombre de clase de chip.
	if !strings.Contains(out, `class="wapp-chip wapp-chip--info"`) {
		t.Error("el estado vivo no lleva el chip compartido")
	}

	// Positivo: la invitación viva SÍ ofrece anular.
	if !strings.Contains(out, `action="/invitaciones/`+testInvitacionPendiente+`/revocar"`) {
		t.Fatal("la invitación pendiente no ofrece anularse: el negativo de abajo sería vacuo")
	}
	// Negativo: las otras tres, no.
	for _, id := range []string{testInvitacionCanjeada, testInvitacionAnulada, testInvitacionCaducada} {
		if strings.Contains(out, `action="/invitaciones/`+id+`/revocar"`) {
			t.Errorf("la invitación %s ofrece anularse y ya no está viva", shortID(id))
		}
	}
}

// TestInvitaciones_AnularLlamaAlDeleteDeLaAPI es el tercer criterio, por el CABLE: el formulario POST
// de la consola se traduce en el DELETE que la plataforma espera, con la invitación en la RUTA.
func TestInvitaciones_AnularLlamaAlDeleteDeLaAPI(t *testing.T) {
	t.Parallel()

	api := newStubAPI(t, invitacionesOK())
	router := adminRouter(api)

	rec := postFormWithCSRF(router, "/invitaciones/"+testInvitacionPendiente+"/revocar",
		url.Values{}, clientSessionCookie(t))

	if got, want := redirectTarget(t, rec), "/invitaciones?success="+flashInvitationRevoked; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	req := api.Last(t, "DELETE /api/v1/invitations/"+testInvitacionPendiente)
	if req.Body != "" {
		t.Errorf("el DELETE viajó con cuerpo (%q); la invitación va en la ruta", req.Body)
	}
	if !strings.HasPrefix(req.Auth, "Bearer ") {
		t.Errorf("el DELETE no llevó el Context Token: Authorization = %q", req.Auth)
	}
}

// TestInvitaciones_AnularUnaYaCanjeadaNoSeDaPorHecha.
//
// El 409 de revocar significa «esa persona ya está dentro», y darlo por bueno le diría a quien
// administra que acaba de retirarle el acceso a alguien que sigue siendo miembro. El texto tiene que
// decir las dos cosas: que no cambió nada y dónde se retira de verdad.
func TestInvitaciones_AnularUnaYaCanjeadaNoSeDaPorHecha(t *testing.T) {
	t.Parallel()

	rutas := invitacionesOK()
	rutas["DELETE /api/v1/invitations/{id}"] = stubResponse{http.StatusConflict, `{"error":"ya fue canjeada"}`}
	api := newStubAPI(t, rutas)
	router := adminRouter(api)

	rec := postFormWithCSRF(router, "/invitaciones/"+testInvitacionCanjeada+"/revocar",
		url.Values{}, clientSessionCookie(t))
	destino := redirectTarget(t, rec)
	if destino != "/invitaciones?error="+flashInvitationRedeemed {
		t.Fatalf("Location = %q, want /invitaciones?error=%s", destino, flashInvitationRedeemed)
	}

	out := getWithSession(t, router, destino).Body.String()
	if !strings.Contains(out, "ya se canjeó") {
		t.Errorf("el aviso no dice que la invitación ya se usó. Body: %s", out)
	}
	if !strings.Contains(out, "Miembros") {
		t.Error("el aviso no dice dónde se retira de verdad el acceso de esa persona")
	}
	// Y el detalle del upstream no se pinta: el texto sale del catálogo.
	if strings.Contains(out, "ya fue canjeada") {
		t.Error("el mensaje del upstream acabó en pantalla")
	}
}

// --- La emisión ---

// TestInvitaciones_ElTTLViajaConLaClaveTtlYNoTtlSeconds.
//
// 🔴 Equivocar el nombre de la clave NO DA ERROR: `encoding/json` ignora en silencio lo que el
// servidor no conoce, así que con `ttl_seconds` el valor elegido nunca llegaría, la invitación viviría
// el default de 24 h y la pantalla seguiría diciendo «1 hora». Ningún gate se pondría rojo. La misma
// trampa costó un test dedicado en la consola de operadores; este es su hermano, mirando el cuerpo
// que sale POR EL CABLE.
func TestInvitaciones_ElTTLViajaConLaClaveTtlYNoTtlSeconds(t *testing.T) {
	t.Parallel()

	api := newStubAPI(t, invitacionesOK())
	router := adminRouter(api)

	emitirInvitacion(router, "3600", clientSessionCookie(t))

	body := api.Last(t, "POST /api/v1/invitations").Body
	if !strings.Contains(body, `"ttl":3600`) {
		t.Errorf("el cuerpo no lleva la caducidad elegida con la clave `ttl`: %s", body)
	}
	if strings.Contains(body, "ttl_seconds") {
		t.Errorf("el cuerpo usa `ttl_seconds`, que el servidor ignora en silencio: %s", body)
	}
}

// TestInvitaciones_UnaCaducidadQueNoSeOfreceNoSaleALaRed.
//
// El servidor recortaría cualquier número a [60 s, 30 días] y devolvería una invitación con una
// caducidad que nadie pidió. La consola no manda lo que ella misma no ofrece, y lo corta ANTES de
// llamar: el gemelo es que un valor de la lista sí sale.
func TestInvitaciones_UnaCaducidadQueNoSeOfreceNoSaleALaRed(t *testing.T) {
	t.Parallel()

	api := newStubAPI(t, invitacionesOK())
	router := adminRouter(api)

	rec := emitirInvitacion(router, "99999999", clientSessionCookie(t))
	if got, want := redirectTarget(t, rec), "/invitaciones?error="+flashInvalidTTL; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if api.Called("POST /api/v1/invitations") {
		t.Error("la emisión con una caducidad inventada llegó a la API")
	}

	// Gemelo: una de la lista sí sale. Sin esto, un handler que rechazara SIEMPRE pasaría el negativo.
	if got := emitirInvitacion(router, "604800", clientSessionCookie(t)); redirectTarget(t, got) != "/invitaciones" {
		t.Errorf("una caducidad ofrecida no se aceptó: Location = %q", redirectTarget(t, got))
	}
	if !api.Called("POST /api/v1/invitations") {
		t.Error("la emisión válida no llegó a la API")
	}
}

// TestInvitaciones_ElRolEsOpcionalYNoViajaCuandoNoSeElige: el cuerpo de una invitación sin rol no
// lleva `role_id` — el `omitempty` es lo que hace que «sin rol» se diga NO mandando el campo, en vez
// de mandando una cadena vacía que el servidor tendría que interpretar.
func TestInvitaciones_ElRolEsOpcionalYNoViajaCuandoNoSeElige(t *testing.T) {
	t.Parallel()

	api := newStubAPI(t, invitacionesOK())
	router := adminRouter(api)

	emitirInvitacion(router, "86400", clientSessionCookie(t))
	if body := api.Last(t, "POST /api/v1/invitations").Body; strings.Contains(body, "role_id") {
		t.Errorf("una invitación sin rol mandó `role_id`: %s", body)
	}

	// Y con rol, viaja.
	postFormWithCSRF(router, "/invitaciones",
		url.Values{"ttl": {"86400"}, "role_id": {testTenantRoleID}}, clientSessionCookie(t))
	if body := api.Last(t, "POST /api/v1/invitations").Body; !strings.Contains(body, `"role_id":"`+testTenantRoleID+`"`) {
		t.Errorf("el rol elegido no viajó en el cuerpo: %s", body)
	}
}

// --- El canje (las cuatro traducciones) ---

// TestCanje_LosCuatroDesenlacesSeExplicanDistinto es el cuarto criterio de la ola.
//
// 🔴 El servidor iguala a propósito el CUERPO y el TIEMPO del 404 y el 410 —para que nadie pueda
// sondear qué códigos existieron alguna vez—, pero deja distinto el CÓDIGO DE ESTADO justo para que
// la UI pueda dar un consejo útil. Aquí se comprueba que los cuatro llegan distintos hasta la
// pantalla Y que ninguno cae al genérico: el genérico dice «inténtalo de nuevo en un momento», que
// ante un código caducado manda a repetir algo que va a fallar igual para siempre.
func TestCanje_LosCuatroDesenlacesSeExplicanDistinto(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre string
		status int
		code   string
		dice   string
	}{
		{"canjeada", http.StatusNoContent, flashJoinedRelogin, "Ya formas parte de la empresa"},
		{"ya usada o cuenta con empresa", http.StatusConflict, flashInvitationUnusable, "ya se usó o se anuló"},
		{"caducada", http.StatusGone, flashInvitationExpired, "caducó"},
		{"no existe", http.StatusNotFound, flashInvitationUnknown, "Cópiala otra vez"},
	}

	vistos := make(map[string]string, len(casos))
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			rutas := invitacionesOK()
			rutas["POST /api/v1/invitations/accept"] = stubResponse{caso.status, `{"error":"esa invitación no se puede usar"}`}
			api := newStubAPI(t, rutas)
			router := adminRouter(api)

			// Sesión SIN empresa: es quien canjea. Y el refresco contra identity está apagado, así
			// que el 204 acaba en el desenlace a medias, que también es uno de los cuatro textos.
			rec := postFormWithCSRF(router, "/invitaciones/canjear",
				url.Values{"token": {testInviteToken}}, sessionCookieFor(t, testUserID, ""))

			destino := redirectTarget(t, rec)
			if destino != "/?error="+caso.code {
				t.Fatalf("Location = %q, want /?error=%s", destino, caso.code)
			}

			out := getConCookie(router, destino, sessionCookieFor(t, testUserID, "")).Body.String()
			if !strings.Contains(out, caso.dice) {
				t.Errorf("el aviso no dice %q. Body: %s", caso.dice, out)
			}
			// Ninguno cae al genérico, que aquí no serviría de nada.
			if strings.Contains(out, "No se pudo completar la operación") {
				t.Error("el desenlace cayó al aviso genérico")
			}
			// Y el cuerpo del upstream no se pinta: el texto sale del catálogo.
			if strings.Contains(out, "esa invitación no se puede usar") {
				t.Error("el mensaje del upstream acabó en pantalla")
			}
		})
	}

	// Los cuatro códigos son distintos entre sí y todos tienen texto propio: si dos compartieran
	// código, quien canjea leería el mismo consejo ante dos problemas con soluciones distintas.
	for _, caso := range casos {
		if otro, repetido := vistos[caso.code]; repetido {
			t.Errorf("%q y %q comparten el código de aviso %q", caso.nombre, otro, caso.code)
		}
		vistos[caso.code] = caso.nombre
		if msg := flashError(caso.code); msg == "" || msg == flashError("codigo-que-no-existe") {
			t.Errorf("el desenlace %q no tiene texto propio: %q", caso.nombre, msg)
		}
	}
}

// TestCanje_TrasCanjearSeRefrescaLaSesionParaQueLaEmpresaAPAREZCA.
//
// 🔴 Es la mitad del canje que no se ve en la API: tras el 204 la persona YA es miembro, pero el
// Context Token que tiene en la mano se emitió ANTES de existir esa membresía y sigue SIN empresa. Sin
// el refresco, el redirect la devuelve a la misma pantalla de «todavía no perteneces a ninguna
// empresa» que acaba de usar, justo después de leer que ya está dentro.
//
// Aquí el refresco FALLA a propósito (identity está apagado en los tests), así que lo que se afirma es
// lo otro: que el desenlace a medias no se pinta como éxito, y que el texto dice qué hacer.
func TestCanje_TrasCanjearSeRefrescaLaSesionParaQueLaEmpresaAPAREZCA(t *testing.T) {
	t.Parallel()

	api := newStubAPI(t, invitacionesOK())
	router := adminRouter(api)
	sess := sessionCookieFor(t, testUserID, "")

	rec := postFormWithCSRF(router, "/invitaciones/canjear", url.Values{"token": {testInviteToken}}, sess)
	destino := redirectTarget(t, rec)

	// El canje SÍ salió, y con el token en el CUERPO (nunca en la ruta ni en la query).
	req := api.Last(t, "POST /api/v1/invitations/accept")
	if !strings.Contains(req.Body, `"token":"`+testInviteToken+`"`) {
		t.Errorf("el token no viajó en el cuerpo: %q", req.Body)
	}
	if strings.Contains(req.Path, testInviteToken) || req.Query.Has("token") {
		t.Errorf("el token viajó en la ruta o en la query: %s?%s", req.Path, req.Query.Encode())
	}

	// El desenlace es a medias y se cuenta como tal: NO es un éxito.
	if destino != "/?error="+flashJoinedRelogin {
		t.Fatalf("Location = %q, want /?error=%s", destino, flashJoinedRelogin)
	}
	if flashSuccesses.Known(flashJoinedRelogin) {
		t.Error("«ya eres miembro pero vuelve a entrar» está entre los ÉXITOS: la sesión no lo refleja todavía")
	}
	out := getConCookie(router, destino, sess).Body.String()
	if !strings.Contains(out, "Cierra sesión y vuelve a entrar") {
		t.Errorf("el aviso no dice qué hacer para ver la empresa. Body: %s", out)
	}
}

// TestCanje_SinTokenNoSaleALaRed: el formulario vacío se corta antes de llamar.
func TestCanje_SinTokenNoSaleALaRed(t *testing.T) {
	t.Parallel()

	api := newStubAPI(t, invitacionesOK())
	router := adminRouter(api)

	rec := postFormWithCSRF(router, "/invitaciones/canjear", url.Values{}, sessionCookieFor(t, testUserID, ""))
	if got, want := redirectTarget(t, rec), "/?error="+flashMissingField; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if api.Called("POST /api/v1/invitations/accept") {
		t.Error("un canje sin código llegó a la API")
	}
}

// --- El parcial que pintan CUATRO pantallas ---

// TestSinEmpresa_LasCuatroPantallasOfrecenElMISMOCanje.
//
// El parcial `sin_empresa` es parcial justo por esto: quien llega sin empresa puede caer en
// cualquiera de las cuatro y tiene que leer y poder hacer lo mismo. Antes de la Ola A las cuatro
// decían «pásale tu identificador»; ahora todas traen el formulario del canje.
//
// El aserto NEGATIVO —que el identificador ya no sea la vía principal— va con su positivo: el enlace
// sigue existiendo, porque no todo el mundo llega con una invitación.
//
// 🔴 EL NOMBRE YA NO DICE UN NÚMERO, Y ES A PROPÓSITO. Esta enumeración ha caducado TRES veces
// —eran tres pantallas, luego cuatro, hoy cinco— y las tres veces el test siguió en verde
// recorriendo de menos: cuando se añadió `/sesiones` nadie lo noto, porque una lista escrita a mano
// no se queja de lo que le falta. Asi que la lista ya NO se escribe: se DERIVA de las plantillas
// que invocan el parcial, leidas del mismo `embed.FS` que sirve el binario. Lo unico a mano es el
// mapa plantilla→ruta, y las dos direcciones fallan: una plantilla que pinte el parcial sin ruta en
// el mapa hace fallar el test (en vez de desaparecer del recorrido en silencio), y una entrada del
// mapa cuya plantilla ya no lo pinte tambien (asi el mapa encoge solo).
func TestSinEmpresa_TodaPantallaQueLoPintaOfreceElMISMOCanje(t *testing.T) {
	t.Parallel()

	// Lo unico escrito a mano: que URL sirve cada plantilla. No se puede derivar del `embed.FS`
	// porque la asociacion la hace `server.go` al registrar la ruta, no la plantilla.
	rutaDe := map[string]string{
		"home.html":         "/",
		"miembros.html":     "/miembros",
		"invitaciones.html": "/invitaciones",
		"roles.html":        "/roles",
		"sesiones.html":     "/sesiones",
		"flujos.html":       rutaFlujos,
		// El detalle entra por el valor magico `nuevo`, que es el unico camino a esa plantilla que no
		// depende de que exista un flujo.
		"flujo.html":        rutaFlujos + "/" + flujoNuevo,
		"disparadores.html": rutaDisparadores,
		"solicitudes.html":  rutaSolicitudes,
		// El detalle de una solicitud (T7.3). Entra por un id cualquiera: sin empresa la pantalla
		// corta ANTES de salir a la red, así que no hace falta que exista en el doble de la API.
		"solicitud.html": rutaSolicitudes + "/" + testIntakeID,
	}

	entradas, err := templatesFS.ReadDir("templates/pages")
	if err != nil {
		t.Fatalf("no se pudieron listar las plantillas de pagina: %v", err)
	}
	pintanElParcial := map[string]bool{}
	var rutas []string
	for _, e := range entradas {
		cuerpo, err := templatesFS.ReadFile("templates/pages/" + e.Name())
		if err != nil {
			t.Fatalf("no se pudo leer %s: %v", e.Name(), err)
		}
		if !strings.Contains(string(cuerpo), `template "sin_empresa"`) {
			continue
		}
		pintanElParcial[e.Name()] = true
		ruta, ok := rutaDe[e.Name()]
		if !ok {
			t.Fatalf("`%s` pinta el parcial `sin_empresa` y no esta en el mapa de rutas de este test: anadela, o el canje dejara de comprobarse ahi sin que nadie se entere", e.Name())
		}
		rutas = append(rutas, ruta)
	}
	if len(rutas) < 2 {
		t.Fatalf("se derivaron %d pantallas que pintan el parcial; con menos de dos, el test no esta comprobando que el texto sea el MISMO en todas", len(rutas))
	}
	for plantilla := range rutaDe {
		if !pintanElParcial[plantilla] {
			t.Errorf("`%s` esta en el mapa de este test pero ya no pinta el parcial `sin_empresa`: quitala del mapa", plantilla)
		}
	}

	api := newStubAPI(t, invitacionesOK())
	router := adminRouter(api)
	sinTenant := sessionCookieFor(t, testUserID, "")

	for _, ruta := range rutas {
		rec := getConCookie(router, ruta, sinTenant)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s sin empresa = %d, want 200", ruta, rec.Code)
		}
		out := rec.Body.String()
		if !strings.Contains(out, `id="form-canjear"`) {
			t.Errorf("%s no ofrece pegar la invitación", ruta)
		}
		if !strings.Contains(out, `action="/invitaciones/canjear"`) {
			t.Errorf("%s pinta el formulario contra otra ruta", ruta)
		}
		// El camino de antes sigue existiendo, pero ya no es el único ni el primero.
		if !strings.Contains(out, `href="/mi-identificador"`) {
			t.Errorf("%s dejó sin salida a quien no tiene invitación", ruta)
		}
		if strings.Index(out, `id="form-canjear"`) > strings.Index(out, `id="section-sin-invitacion"`) {
			t.Errorf("%s pone el identificador ANTES del canje: la vía principal es la invitación", ruta)
		}
	}
}

// TestSinEmpresa_ElAvisoDelCanjeSePintaDentroDelParcial.
//
// El canje redirige a la portada, y la portada SIN empresa pinta el parcial: si el parcial no
// incluyera `flashes`, los cuatro desenlaces del canje se perderían — el usuario volvería a la misma
// pantalla sin una sola palabra de por qué no entró. El gemelo negativo es que no se pinte DOS veces.
func TestSinEmpresa_ElAvisoDelCanjeSePintaDentroDelParcial(t *testing.T) {
	t.Parallel()

	api := newStubAPI(t, invitacionesOK())
	router := adminRouter(api)

	out := getConCookie(router, "/?error="+flashInvitationExpired, sessionCookieFor(t, testUserID, "")).Body.String()
	texto := flashError(flashInvitationExpired)
	if !strings.Contains(out, "caducó") {
		t.Fatalf("el aviso del canje no llega a la pantalla sin empresa. Body: %s", out)
	}
	if n := strings.Count(out, texto); n != 1 {
		t.Errorf("el aviso se pinta %d veces, want 1 (el parcial y la página lo duplicarían)", n)
	}
}

// --- La emisión, decodificada ---

// TestInvitaciones_LaRespuestaDeLaEmisionSeLeeEntera comprueba que el código y la caducidad llegan a
// la pantalla desde el JSON del servidor y no de un valor por defecto de la consola.
func TestInvitaciones_LaRespuestaDeLaEmisionSeLeeEntera(t *testing.T) {
	t.Parallel()

	api := newStubAPI(t, invitacionesOK())
	router := adminRouter(api)

	ck := cookieDeInvitacion(t, router)
	out := getConCookies(router, "/invitaciones", clientSessionCookie(t), ck).Body.String()

	if !strings.Contains(out, testInviteToken) {
		t.Fatalf("la pantalla no enseña el código emitido. Body: %s", out)
	}
	if !strings.Contains(out, "2026-08-29T10:00:00Z") {
		t.Error("la pantalla no dice cuándo caduca el código que acaba de enseñar")
	}
	if !strings.Contains(out, "No se volverá a mostrar") {
		t.Error("la pantalla no avisa de que el código no se puede volver a ver")
	}

	// La respuesta del 201 se decodifica en un tipo que SÍ tiene token; el del listado, no. Que la
	// caja aparezca aquí y el listado no lo pinte es lo que separa los dos tipos.
	var emitida struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(invitacionEmitidaBody(testInviteToken)), &emitida); err != nil {
		t.Fatalf("el cuerpo de prueba del 201 no es JSON válido: %v", err)
	}
	if emitida.Token != testInviteToken {
		t.Fatalf("el cuerpo de prueba no lleva el token: %q", emitida.Token)
	}
}
