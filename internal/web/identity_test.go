package web

import (
	"net/http"
	"strings"
	"testing"
)

// identity_test.go cubre «Mi identificador» y, con ella, el estado que el resto de la consola no
// ejercitaba: una sesión VÁLIDA y SIN EMPRESA.
//
// Ese estado no es hipotético ni un borde raro: es por donde entra todo el mundo la primera vez.
// Quien se registra y entra antes de que nadie lo incorpore recibe un Context Token con tenant vacío
// —el canje devuelve tenant vacío con cero membresías, y NO un 401 (D-056.12)—, así que llega a estas
// pantallas con sesión buena y sin nada que administrar.

// otroUsuario es una segunda persona, para poder afirmar que cada sesión ve LO SUYO.
const otroUsuario = "99999999-9999-4999-8999-999999999999"

// TestMiIdentificador_UnUsuarioSinEmpresaVeSuIdentificador.
//
// Es EL test de esta ampliación: la pantalla existe justo para el caso en que la persona todavía no
// pertenece a ninguna empresa, porque es lo único que puede hacer —dárselo a quien administra— y es
// lo que la saca de ahí.
func TestMiIdentificador_UnUsuarioSinEmpresaVeSuIdentificador(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, map[string]stubResponse{})
	router := adminRouter(api)

	rec := getConCookie(router, "/mi-identificador", sessionCookieFor(t, testUserID, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()

	if !strings.Contains(out, `value="`+testUserID+`"`) {
		t.Errorf("la pantalla no pinta el identificador de la sesión. Body: %s", out)
	}
	if !strings.Contains(out, `id="nota-sin-empresa"`) {
		t.Error("no se explica que todavía no pertenece a ninguna empresa")
	}
	// Y no se le acusa de un problema que no tiene.
	for _, mentira := range []string{"no tiene permiso", "no tienes permiso", "caducó"} {
		if strings.Contains(out, mentira) {
			t.Errorf("la pantalla dice %q a alguien cuyo problema es que aún no tiene empresa", mentira)
		}
	}
}

// TestMiIdentificador_CadaSesionVeElSuyo.
//
// Un test que solo comprobara «aparece un UUID» sería vacuo: lo pasaría igual una plantilla con el
// identificador escrito a mano. Dos sesiones distintas contra el MISMO router es lo que obliga a que
// el dato venga de la petición, y el aserto cruzado —que ninguna de las dos vea el de la otra— es lo
// que descarta que se esté pintando una lista o un valor cacheado entre peticiones.
func TestMiIdentificador_CadaSesionVeElSuyo(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, map[string]stubResponse{})
	router := adminRouter(api)

	primera := getConCookie(router, "/mi-identificador", sessionCookieFor(t, testUserID, "")).Body.String()
	segunda := getConCookie(router, "/mi-identificador", sessionCookieFor(t, otroUsuario, "")).Body.String()

	if !strings.Contains(primera, testUserID) {
		t.Errorf("la primera sesión no ve su identificador")
	}
	if !strings.Contains(segunda, otroUsuario) {
		t.Errorf("la segunda sesión no ve su identificador")
	}
	if strings.Contains(primera, otroUsuario) {
		t.Error("la primera sesión ve el identificador de la segunda")
	}
	if strings.Contains(segunda, testUserID) {
		t.Error("la segunda sesión ve el identificador de la primera")
	}
}

// TestMiIdentificador_ElValorVaEnteroYEnUnCampoParaCopiar.
//
// El identificador se pinta SIN abreviar. En la tabla de miembros sí se abrevia —ahí hay una fila por
// persona y el ancho manda—, pero aquí el único trabajo de la pantalla es que ese valor se copie
// entero: uno truncado, o partido en dos nodos, no sirve para nada y el fallo se descubre cuando ya
// se lo ha pasado a otra persona.
func TestMiIdentificador_ElValorVaEnteroYEnUnCampoParaCopiar(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, map[string]stubResponse{})

	out := getConCookie(adminRouter(api), "/mi-identificador", sessionCookieFor(t, testUserID, "")).Body.String()

	if !strings.Contains(out, `id="mi-identificador-valor"`) && !strings.Contains(out, `readonly`) {
		t.Fatal("el identificador no está en un campo pensado para copiarse")
	}
	if !strings.Contains(out, `value="`+testUserID+`" readonly`) {
		t.Errorf("el valor no viaja entero en un campo de solo lectura. Body: %s", out)
	}
	// La abreviatura de la tabla de miembros NO debe aparecer aquí: sería el valor a medias.
	if strings.Contains(out, shortID(testUserID)+"<") || strings.Contains(out, "…") {
		t.Error("el identificador se pintó abreviado: copiado así no sirve")
	}
	// Y la explicación en lenguaje llano de para qué sirve.
	if !strings.Contains(out, "Dáselo a quien administra tu empresa") {
		t.Error("la pantalla no dice para qué sirve el identificador")
	}
}

// TestMiIdentificador_NoHablaConLaAPI: es la pantalla a la que se manda a alguien cuando lo demás no
// funciona, así que no puede tener un modo de fallo. El dato sale del token que la sesión ya
// decodificó —el mismo valor que `whoami` devuelve en `subject`—, y por eso el viaje de red sobra.
func TestMiIdentificador_NoHablaConLaAPI(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, map[string]stubResponse{})

	rec := getConCookie(adminRouter(api), "/mi-identificador", sessionCookieFor(t, testUserID, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if n := len(api.Requests()); n != 0 {
		t.Errorf("la pantalla hizo %d llamadas a la API (%v); no debe hacer ninguna", n, routesOf(api.Requests()))
	}
}

// TestSinEmpresa_LaPortadaNoPreguntaPorElPlan.
//
// Sin tenant, `GET /api/v1/entitlements` responde 401 (su guarda es `!ok || id.TenantID == ""`).
// Llamarlo igual costaría, en CADA carga, un refresco contra identity —withAuthRetry reintenta ante
// el 401— para acabar mostrando «no se pudo consultar el plan», que no describe lo que pasa.
//
// El gemelo positivo está en el mismo test: CON empresa, la portada sí pregunta. Sin él, «no hubo
// llamada» lo cumpliría también una portada que dejara de consultar el plan para todo el mundo.
func TestSinEmpresa_LaPortadaNoPreguntaPorElPlan(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, map[string]stubResponse{
		"GET /api/v1/entitlements": {http.StatusOK, entitlementsBody("commerce", featureCatalogImport)},
	})
	router := adminRouter(api)

	rec := getConCookie(router, "/", sessionCookieFor(t, testUserID, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("la portada sin empresa respondió %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}
	if api.Called("GET /api/v1/entitlements") {
		t.Error("la portada preguntó por el plan de una sesión sin empresa: esa llamada responde 401 siempre")
	}

	out := rec.Body.String()
	if !strings.Contains(out, `id="section-sin-empresa"`) {
		t.Error("la portada sin empresa no explica el estado")
	}
	if strings.Contains(out, "No se pudo consultar el plan") {
		t.Error("se pintó el aviso de plan no consultado a alguien que simplemente no tiene empresa")
	}
	if strings.Contains(out, gatedBlockMarker) {
		t.Error("sin empresa el gate por plan debía estar CERRADO")
	}

	// Gemelo POSITIVO: con empresa, la portada sí pregunta.
	if getWithSession(t, router, "/"); !api.Called("GET /api/v1/entitlements") {
		t.Error("con empresa la portada dejó de preguntar por el plan: el negativo de arriba sería vacuo")
	}
}

// TestSinEmpresa_MiembrosYRolesExplicanEnVezDeAcusarDeFaltaDePermiso.
//
// Se puede llegar a las dos por la URL aunque la navegación no las ofrezca. Sin empresa la API
// responde 403 —el token sale sin un solo grant—, y ese texto («no tienes permiso») MIENTE sobre la
// causa: no le falta un permiso, le falta una empresa. Se explica, y no se llama.
func TestSinEmpresa_MiembrosYRolesExplicanEnVezDeAcusarDeFaltaDePermiso(t *testing.T) {
	t.Parallel()

	for _, ruta := range []string{"/miembros", "/roles"} {
		api := newStubAPI(t, map[string]stubResponse{
			"GET /api/v1/members": {http.StatusOK, membersBody(testUserID)},
			"GET /api/v1/roles":   {http.StatusOK, rolesBody},
		})
		rec := getConCookie(adminRouter(api), ruta, sessionCookieFor(t, testUserID, ""))

		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s sin empresa status = %d, want 200. Body: %s", ruta, rec.Code, rec.Body.String())
		}
		out := rec.Body.String()
		if !strings.Contains(out, `id="section-sin-empresa"`) {
			t.Errorf("%s no explica que todavía no hay empresa", ruta)
		}
		if strings.Contains(out, "no tiene permiso") || strings.Contains(out, "no tienes permiso") {
			t.Errorf("%s acusa de falta de permiso a quien lo que no tiene es empresa", ruta)
		}
		if n := len(api.Requests()); n != 0 {
			t.Errorf("%s sin empresa hizo %d llamadas (%v); la respuesta ya se sabe", ruta, n, routesOf(api.Requests()))
		}
		if !strings.Contains(out, `href="/mi-identificador"`) {
			t.Errorf("%s no ofrece el único camino que saca de este estado", ruta)
		}
	}
}

// TestSinEmpresa_LaNavegacionOfreceSoloLoQueSePuedeUsar: administrar no se ofrece sin empresa —el
// enlace solo llevaría a una explicación de por qué no funciona—, pero «Mi identificador» lo ve todo
// el mundo. El positivo y el negativo se afirman en el mismo test, contra el mismo router.
func TestSinEmpresa_LaNavegacionOfreceSoloLoQueSePuedeUsar(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, map[string]stubResponse{
		"GET /api/v1/entitlements": {http.StatusOK, entitlementsBody("commerce", featureCatalogImport)},
		"GET /api/v1/members":      {http.StatusOK, membersBody(testUserID)},
		"GET /api/v1/roles":        {http.StatusOK, rolesBody},
	})
	router := adminRouter(api)

	conEmpresa := getWithSession(t, router, "/").Body.String()
	sinEmpresaHTML := getConCookie(router, "/", sessionCookieFor(t, testUserID, "")).Body.String()

	// Positivo: con empresa, la barra ofrece las tres.
	for _, enlace := range []string{`href="/miembros"`, `href="/roles"`, `href="/mi-identificador"`} {
		if !strings.Contains(conEmpresa, enlace) {
			t.Fatalf("con empresa la barra no ofrece %s: el negativo de abajo sería vacuo", enlace)
		}
	}
	// Negativo: sin empresa, solo la que se puede usar.
	if strings.Contains(sinEmpresaHTML, `href="/miembros"`) || strings.Contains(sinEmpresaHTML, `href="/roles"`) {
		t.Error("la barra ofrece administrar a una sesión sin empresa")
	}
	if !strings.Contains(sinEmpresaHTML, `href="/mi-identificador"`) {
		t.Error("la barra no ofrece «Mi identificador» a quien no tiene empresa: es lo único que puede usar")
	}
}

// TestSinEmpresa_ElAuthMiddlewareNoExigeTenant: la comprobación de la suposición, en la capa donde
// habría estado si estuviera. Un token con `tenant_id` vacío es VÁLIDO; si el middleware lo tratara
// como sesión rota, todo lo de arriba sería inalcanzable y el usuario nuevo no podría ni entrar.
func TestSinEmpresa_ElAuthMiddlewareNoExigeTenant(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, map[string]stubResponse{})

	rec := getConCookie(adminRouter(api), "/mi-identificador", sessionCookieFor(t, testUserID, ""))
	if rec.Code == http.StatusSeeOther {
		t.Fatalf("el middleware expulsó una sesión sin empresa a %q: el token es válido",
			rec.Header().Get("Location"))
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// Y la cookie no se borra: expirar la sesión de quien acaba de registrarse lo dejaría fuera.
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == sessionCookieName && ck.MaxAge < 0 {
			t.Error("se borró la cookie de sesión de un usuario sin empresa")
		}
	}
}
