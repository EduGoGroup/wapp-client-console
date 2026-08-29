package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	sharedjwt "github.com/EduGoGroup/wapp-shared/auth/jwt"
	sharedweb "github.com/EduGoGroup/wapp-shared/web"
	"github.com/golang-jwt/jwt/v5"
)

// tenants_test.go cubre el SELECTOR DE EMPRESAS (T5.3): el tercer estado, el cambio y su reemisión.

// contextTokenPara forja un Context Token con la empresa que se le diga, para poder comprobar que
// tras elegir empresa la sesión lleva OTRA distinta y no la de antes.
func contextTokenPara(t *testing.T, userID, tenantID string) string {
	t.Helper()
	claims := sharedjwt.Claims{
		UserID:           userID,
		TenantID:         tenantID,
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
	}
	firmado, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("dummy"))
	if err != nil {
		t.Fatalf("firmar token: %v", err)
	}
	return firmado
}

// empresaDeLaSesion decodifica la cookie de sesión que la respuesta emitió y devuelve su empresa.
// Cadena vacía si no se emitió cookie nueva.
func empresaDeLaSesion(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	ck := sessionCookieFrom(rec)
	if ck == nil {
		return ""
	}
	sess, err := sharedweb.DecodeSession(ck.Value)
	if err != nil {
		t.Fatalf("la cookie de sesión emitida no se pudo decodificar: %v", err)
	}
	claims, err := parseAccessClaims(sess.AccessToken)
	if err != nil {
		t.Fatalf("el token de la cookie emitida no se pudo leer: %v", err)
	}
	return claims.TenantID
}

// routerConCambioDeEmpresa monta la consola con los DOS upstreams que el cambio necesita: la API
// pública (que acepta la elección y luego re-canja) e identity (que refresca).
//
// El canje devuelve un token acotado a `tenantTrasElCambio`, que es lo que permite comprobar que la
// sesión REALMENTE cambió de empresa y no solo que la llamada salió.
func routerConCambioDeEmpresa(t *testing.T, tenantTrasElCambio string, statusEleccion int) (http.Handler, *stubAPI) {
	t.Helper()
	api := newStubAPI(t, map[string]stubResponse{
		rutaListadoDeEmpresas:                {http.StatusOK, dosEmpresas()},
		"POST /api/v1/auth/active-tenant":    {statusEleccion, ""},
		"POST /api/v1/auth/exchange":         {http.StatusOK, `{"context_token":"` + contextTokenPara(t, testUserID, tenantTrasElCambio) + `","expires_at":"` + time.Now().Add(time.Hour).Format(time.RFC3339) + `"}`},
		"GET /api/v1/entitlements":           {http.StatusOK, entitlementsBody("commerce")},
		"GET /api/v1/members":                {http.StatusOK, membersBody(testUserID)},
		"GET /api/v1/roles":                  {http.StatusOK, rolesBody},
		"POST /api/v1/members/{user_id}/rol": {http.StatusNoContent, ""},
	})
	identity := newFakeIdentity(t)
	return NewRouter(testConfig(api.URL, identity.URL)), api
}

// TestEmpresa_ElegirlaLaFijaEnElServidorYREEMITELaSesion (T5.3, el corazón de la tarea).
//
// 🔑 SON DOS PASOS Y EL SEGUNDO NO ES COSMÉTICO. El POST deja la elección escrita en el servidor,
// pero el Context Token que la persona tiene en la mano se emitió ANTES y sigue diciendo la empresa
// de antes: sin el refresco, el redirect la devolvería a la misma pantalla sin que nada haya
// cambiado a la vista. Por eso el aserto no mira el redirect: mira la EMPRESA DE LA COOKIE.
func TestEmpresa_ElegirlaLaFijaEnElServidorYREEMITELaSesion(t *testing.T) {
	t.Parallel()
	router, api := routerConCambioDeEmpresa(t, testOtherTenantID, http.StatusNoContent)

	rec := postFormWithCSRF(router, rutaEmpresa,
		url.Values{"tenant_id": {testOtherTenantID}}, clientSessionCookie(t))

	if got, want := redirectTarget(t, rec), "/?success="+flashTenantSwitched; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
	// Paso 1: la elección viajó, con la empresa elegida en el CUERPO.
	req := api.Last(t, "POST /api/v1/auth/active-tenant")
	if !strings.Contains(req.Body, `"tenant_id":"`+testOtherTenantID+`"`) {
		t.Errorf("la elección no viajó con la empresa pedida: %s", req.Body)
	}
	if !strings.HasPrefix(req.Auth, "Bearer ") {
		t.Errorf("la elección salió sin Context Token: Authorization = %q", req.Auth)
	}
	// Paso 2: la sesión se reemitió y ahora lleva la empresa NUEVA. Sin esto, todo lo anterior
	// habría pasado igual y el usuario seguiría viendo los datos de la otra empresa.
	if got := empresaDeLaSesion(t, rec); got != testOtherTenantID {
		t.Errorf("la sesión sigue con la empresa %q, want %q: faltó el refresco", got, testOtherTenantID)
	}
}

// TestEmpresa_ElRefrescoQueFALLAEsUnDesenlaceAMedias.
//
// La elección quedó escrita —eso no se deshace— y la sesión no. Pintarlo como éxito dejaría a la
// persona mirando los datos de la empresa ANTERIOR justo después de leer que ya cambió, que es la
// forma más rápida de convencer a alguien de que el selector no funciona. Mismo criterio que el
// canje de una invitación (flashJoinedRelogin).
func TestEmpresa_ElRefrescoQueFALLAEsUnDesenlaceAMedias(t *testing.T) {
	t.Parallel()
	// El canje del refresco falla: la API pública responde 500 a /auth/exchange.
	api := newStubAPI(t, map[string]stubResponse{
		rutaListadoDeEmpresas:             {http.StatusOK, dosEmpresas()},
		"POST /api/v1/auth/active-tenant": {http.StatusNoContent, ""},
		"POST /api/v1/auth/exchange":      {http.StatusInternalServerError, ""},
		"GET /api/v1/entitlements":        {http.StatusOK, entitlementsBody("commerce")},
	})
	identity := newFakeIdentity(t)
	router := NewRouter(testConfig(api.URL, identity.URL))

	rec := postFormWithCSRF(router, rutaEmpresa,
		url.Values{"tenant_id": {testOtherTenantID}}, clientSessionCookie(t))

	if got, want := redirectTarget(t, rec), "/?error="+flashTenantRelogin; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
	// La elección SÍ salió: el desenlace es a medias, no un fallo del primer paso.
	if !api.Called("POST /api/v1/auth/active-tenant") {
		t.Error("la elección ni siquiera se mandó")
	}
	// Y el texto tiene que decir las dos mitades: que quedó elegida y que hay que volver a entrar.
	aviso := flashError(flashTenantRelogin)
	if !strings.Contains(aviso, "elegida") || !strings.Contains(aviso, "vuelve a entrar") {
		t.Errorf("el aviso no cuenta las dos mitades del desenlace: %q", aviso)
	}
}

// TestEmpresa_El404NoDELATAsiEsaEmpresaEXISTE.
//
// El servidor responde 404 —y no 403— tanto si la empresa no existe como si existe y quien pide no
// es miembro, a propósito: separarlas dejaría sondear UUIDs y levantar el censo de empresas de la
// plataforma. La UI es donde ese anti-oráculo se pierde con más facilidad, porque el texto es lo que
// alguien lee. El aserto es NEGATIVO sobre lo que el mensaje NO puede decir, con su positivo al lado
// para que no sea vacuo.
func TestEmpresa_El404NoDELATAsiEsaEmpresaEXISTE(t *testing.T) {
	t.Parallel()
	router, _ := routerConCambioDeEmpresa(t, testTenantID, http.StatusNotFound)

	const ajena = "99999999-9999-4999-8999-999999999999"
	rec := postFormWithCSRF(router, rutaEmpresa, url.Values{"tenant_id": {ajena}}, clientSessionCookie(t))

	destino := redirectTarget(t, rec)
	if destino != "/?error="+flashTenantNotYours {
		t.Fatalf("Location = %q, want %q", destino, "/?error="+flashTenantNotYours)
	}
	out := getWithSession(t, router, destino).Body.String()

	// POSITIVO: se explica algo y se dice qué hacer.
	if !strings.Contains(out, "Elige una de las de tu lista") {
		t.Fatalf("el 404 no se explica ni dice qué hacer; el negativo de abajo sería vacuo. Body: %s", out)
	}
	// NEGATIVO: y no se dice nada sobre esa empresa. Ni que no existe, ni que existe y no es tuya.
	for _, delator := range []string{"no existe", "no encontrad", "no eres miembro", "no pertenece"} {
		if strings.Contains(strings.ToLower(out), delator) {
			t.Errorf("el aviso dice %q: eso responde a si esa empresa existe, que es justo lo que el 404 evita", delator)
		}
	}
	if strings.Contains(out, ajena) {
		t.Error("el identificador que se pidió acabó pintado en la pantalla")
	}
}

// TestEmpresa_SinEmpresaElegidaNoSaleALaRed: un formulario a medias se corta aquí. Sin esto, el POST
// vacío sería un viaje entero para leer un 400.
func TestEmpresa_SinEmpresaElegidaNoSaleALaRed(t *testing.T) {
	t.Parallel()
	router, api := routerConCambioDeEmpresa(t, testTenantID, http.StatusNoContent)

	rec := postFormWithCSRF(router, rutaEmpresa, url.Values{"tenant_id": {"   "}}, clientSessionCookie(t))

	if got, want := redirectTarget(t, rec), "/?error="+flashMissingField; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
	if api.Called("POST /api/v1/auth/active-tenant") {
		t.Error("un formulario sin empresa salió a la red")
	}
}

// TestElEstadoSINempresaSonDOS: el reparto del parcial (T5.3).
//
// 🔴 ES EL HALLAZGO DE LA TAREA. El Context Token de quien tiene CERO empresas y el de quien tiene
// DOS y no ha elegido son IDÉNTICOS —los dos sin tenant y sin un solo grant—, así que del token no
// sale la diferencia y esta consola llevaba pintando la pantalla de espera a los dos. Lo único que
// los separa es el LISTADO.
//
// Se comprueba en DOS pantallas y no en una: el reparto vive en el parcial, así que si alguien lo
// moviera a una página concreta, la otra caería.
func TestElEstadoSINempresaSonDOS(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre      string
		listado     string
		quiero      string
		noQuiero    string
		explicacion string
	}{
		{
			nombre:      "cero empresas ⇒ la pantalla de espera de siempre",
			listado:     tenantsBody(),
			quiero:      `id="section-sin-empresa"`,
			noQuiero:    `id="section-elegir-empresa"`,
			explicacion: "quien no pertenece a ninguna empresa no tiene entre qué elegir: lo suyo es esperar (o canjear)",
		},
		{
			nombre:      "varias sin elegir ⇒ el selector",
			listado:     dosEmpresas(),
			quiero:      `id="section-elegir-empresa"`,
			noQuiero:    `id="section-sin-empresa"`,
			explicacion: "quien pertenece a dos empresas no está esperando a nada: le falta elegir",
		},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			rutas := invitacionesOK()
			rutas[rutaListadoDeEmpresas] = stubResponse{http.StatusOK, caso.listado}
			router := adminRouter(newStubAPI(t, rutas))
			sinTenant := sessionCookieFor(t, testUserID, "")

			for _, ruta := range []string{"/", "/miembros"} {
				out := getConCookie(router, ruta, sinTenant).Body.String()
				if !strings.Contains(out, caso.quiero) {
					t.Errorf("%s: falta %s (%s)", ruta, caso.quiero, caso.explicacion)
				}
				if strings.Contains(out, caso.noQuiero) {
					t.Errorf("%s: se pintó %s, que es el OTRO estado (%s)", ruta, caso.noQuiero, caso.explicacion)
				}
			}
		})
	}
}

// TestElegirEmpresa_MarcaLaQueELSERVIDORdiceQueEstaActiva.
//
// El `active` lo calcula la plataforma con la MISMA regla que acota el token en el canje, así que la
// consola no lo deduce: si lo dedujera tendría una segunda fuente del mismo hecho. La lista se sirve
// con la activa la SEGUNDA justo para que «marca la primera» no pueda pasar por casualidad.
func TestElegirEmpresa_MarcaLaQueELSERVIDORdiceQueEstaActiva(t *testing.T) {
	t.Parallel()
	rutas := invitacionesOK()
	// Una lista donde la ACTIVA es la segunda, y donde ninguna coincide con la del token (que aquí
	// está vacío): así el `selected` no puede acertar por parecerse a otra cosa.
	rutas[rutaListadoDeEmpresas] = stubResponse{http.StatusOK, tenantsBody(
		empresaJSON(testOtherTenantID, testOtherTenantName, false),
		empresaJSON(testTenantID, testTenantName, true),
	)}
	router := adminRouter(newStubAPI(t, rutas))

	out := getConCookie(router, "/", sessionCookieFor(t, testUserID, "")).Body.String()

	if !strings.Contains(out, `<option value="`+testTenantID+`" selected>`) {
		t.Errorf("no se marcó la empresa que el servidor dice activa (%s). Body: %s", testTenantID, out)
	}
	if strings.Contains(out, `<option value="`+testOtherTenantID+`" selected>`) {
		t.Errorf("se marcó la PRIMERA de la lista (%s) en vez de la que el servidor marcó", testOtherTenantID)
	}
	// Las dos se ofrecen, por su nombre: un `<option>` vacío no se puede elegir a ciegas.
	for _, nombre := range []string{testTenantName, testOtherTenantName} {
		if !strings.Contains(out, nombre) {
			t.Errorf("el selector no ofrece %q", nombre)
		}
	}
}

// TestEmpresa_SiElListadoNoSePUEDEleerSePintaLoDeSIEMPRE.
//
// El listado es una consulta accesoria y no puede tumbar ninguna pantalla: si la plataforma no
// contesta, la sesión sin empresa vuelve a ver la espera de siempre y la sesión con empresa sigue
// viendo su empresa —por su identificador, que es lo que hay sin el nombre— y ningún selector.
// Falla hacia lo que ya había, que es el lado conservador.
func TestEmpresa_SiElListadoNoSePUEDEleerSePintaLoDeSIEMPRE(t *testing.T) {
	t.Parallel()
	rutas := membersOK()
	rutas[rutaListadoDeEmpresas] = stubResponse{http.StatusInternalServerError, ""}
	router := adminRouter(newStubAPI(t, rutas))

	conEmpresa := getWithSession(t, router, "/miembros")
	if conEmpresa.Code != http.StatusOK {
		t.Fatalf("con el listado caído la pantalla respondió %d, want 200", conEmpresa.Code)
	}
	out := conEmpresa.Body.String()
	if !strings.Contains(out, testTenantID) {
		t.Error("sin listado se perdió también el identificador de la empresa: el nombre es un extra, no el dato")
	}
	if strings.Contains(out, `id="tenant-switcher"`) {
		t.Error("se pintó el selector sin haber podido leer entre qué empresas elegir")
	}

	sinEmpresa := getConCookie(router, "/miembros", sessionCookieFor(t, testUserID, ""))
	if sinEmpresa.Code != http.StatusOK {
		t.Fatalf("sin empresa y con el listado caído la pantalla respondió %d, want 200", sinEmpresa.Code)
	}
	if !strings.Contains(sinEmpresa.Body.String(), `id="section-sin-empresa"`) {
		t.Error("sin listado no se pintó la pantalla de espera, que es lo que había ayer")
	}
}
