package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// inv04_test.go es el contract test del perímetro: qué sale de esta consola hacia la API pública.
//
// Los criterios que fija no se pueden ver en el HTML ni en un doble en memoria —son sobre la petición
// que viaja por el cable—, y son dos:
//   - INV-04: el `tenant_id` NUNCA lo manda la UI. Sale del Context Token, que la plataforma verifica.
//   - Cross-tenant ⇒ 404: la plataforma responde 404 y no 403 ante un identificador de otra empresa,
//     y la consola lo trata como frontera, no como inexistencia.

// ejerceTodasLasPantallas recorre TODA la superficie de la consola contra el doble: las tres páginas
// y las cinco operaciones. Devuelve el doble con todo lo capturado.
//
// Que sea exhaustivo es el punto: un aserto sobre «las llamadas» que solo recorriera una pantalla
// dejaría fuera justo la que un día mande el tenant de más.
func ejerceTodasLasPantallas(t *testing.T) *stubAPI {
	t.Helper()

	api := newStubAPI(t, map[string]stubResponse{
		"GET /api/v1/entitlements":         {http.StatusOK, entitlementsBody("commerce", "catalog_import", "menu")},
		"GET /api/v1/members":              {http.StatusOK, membersBody(testUserID, testOtherUserID)},
		"GET /api/v1/roles":                {http.StatusOK, rolesBody},
		"DELETE /api/v1/members/{user_id}": {http.StatusNoContent, ""},
		"POST /api/v1/roles": {http.StatusCreated,
			`{"role_id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","name":"Cajera","global":false}`},
		"POST /api/v1/members":                             {http.StatusNoContent, ""},
		"POST /api/v1/members/{user_id}/roles":             {http.StatusNoContent, ""},
		"DELETE /api/v1/members/{user_id}/roles/{role_id}": {http.StatusNoContent, ""},
		// Sesiones (T2.1): la pantalla y sus dos mutaciones.
		"GET /api/v1/sessions": {http.StatusOK, sesionesBody(
			`{"session_id":"` + testSessionID + `","edge_id":"edge-alpha","state":"online","profile":"active"}`)},
		"POST /api/v1/messages":              {http.StatusOK, `{"acked_command_id":"cmd-1","ok":true}`},
		"POST /api/v1/sessions/{id}/profile": {http.StatusOK, `{"session_id":"` + testSessionID + `","profile":"passive"}`},
	})
	router := adminRouter(api)
	sess := clientSessionCookie(t)

	for _, ruta := range []string{"/", "/sesiones", "/miembros", "/roles"} {
		if rec := getWithSession(t, router, ruta); rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", ruta, rec.Code)
		}
	}
	// El alta con rol, que son DOS llamadas: ninguna de las dos puede llevar la empresa.
	postFormWithCSRF(router, "/miembros",
		url.Values{"user_id": {testOtherUserID}, "role_id": {testTenantRoleID}}, sess)
	postFormWithCSRF(router, "/miembros/"+testOtherUserID+"/baja", url.Values{}, sess)
	postFormWithCSRF(router, "/roles", url.Values{"name": {"Cajera"}, "parent_role_id": {testGlobalRoleID}}, sess)
	postFormWithCSRF(router, "/roles/asignar",
		url.Values{"user_id": {testOtherUserID}, "role_id": {testTenantRoleID}}, sess)
	postFormWithCSRF(router, "/roles/retirar",
		url.Values{"user_id": {testOtherUserID}, "role_id": {testTenantRoleID}}, sess)
	// Las dos mutaciones de sesiones. El envío es la única llamada de esta consola cuyo cuerpo lleva
	// texto escrito por el usuario, así que es por donde un `tenant_id` se colaría sin que se note.
	postFormWithCSRF(router, "/sesiones/enviar",
		url.Values{"session_id": {testSessionID}, "to": {"+593990000002"}, "text": {"hola"}}, sess)
	postFormWithCSRF(router, "/sesiones/"+testSessionID+"/perfil", url.Values{"profile": {"passive"}}, sess)

	if len(api.Requests()) < 13 {
		t.Fatalf("solo se capturaron %d peticiones: el recorrido no ejercitó la superficie completa (%v)",
			len(api.Requests()), routesOf(api.Requests()))
	}
	return api
}

// TestINV04_LaConsolaNoMandaNuncaElTenant.
//
// La empresa sale del token. Si la UI la mandara, un usuario podría escribir la de OTRA empresa en el
// formulario y la plataforma tendría que decidir a quién creer — que es exactamente la decisión que
// INV-04 elimina no dándole al cliente dónde ponerla.
//
// El aserto barre las TRES posiciones donde podría colarse: query, cuerpo y ruta.
func TestINV04_LaConsolaNoMandaNuncaElTenant(t *testing.T) {
	t.Parallel()
	api := ejerceTodasLasPantallas(t)

	for _, req := range api.Requests() {
		if req.Query.Has("tenant_id") {
			t.Errorf("%s manda tenant_id en la query: %s", req.Route(), req.Query.Encode())
		}
		if strings.Contains(req.Body, "tenant_id") {
			t.Errorf("%s manda tenant_id en el cuerpo: %s", req.Route(), req.Body)
		}
		if strings.Contains(req.Path, testTenantID) {
			t.Errorf("%s lleva la empresa en la RUTA: %s", req.Route(), req.Path)
		}
		// Y el token, que es de donde la plataforma lo saca, sí viaja siempre.
		if !strings.HasPrefix(req.Auth, "Bearer ") {
			t.Errorf("%s salió sin Context Token: Authorization = %q", req.Route(), req.Auth)
		}
	}
}

// TestINV04_TodoLoQueSaleVaAlPlanoPublico: ninguna llamada al perímetro de OPERADORES (:8100).
//
// No hay `AdminAPIBaseURL` en la config precisamente para que ningún handler pueda apuntar ahí «sin
// querer» (INV-10), pero eso es una propiedad de la config; esto lo afirma sobre lo que sale.
func TestINV04_TodoLoQueSaleVaAlPlanoPublico(t *testing.T) {
	t.Parallel()
	api := ejerceTodasLasPantallas(t)

	for _, req := range api.Requests() {
		if !strings.HasPrefix(req.Path, "/api/v1/") {
			t.Errorf("%s no va al plano público /api/v1: %s", req.Route(), req.Path)
		}
		// Las rutas del perímetro de plataforma que esta consola JAMÁS debe tocar.
		for _, prohibida := range []string{"/api/v1/tenants", "/api/v1/installations", "/api/v1/plans", "/admin/"} {
			if strings.HasPrefix(req.Path, prohibida) {
				t.Errorf("%s toca el perímetro de operadores: %s", req.Route(), req.Path)
			}
		}
	}
}

// TestCrossTenant_El404NoSeCuentaComoInexistencia.
//
// La plataforma responde 404 —y no 403— cuando el identificador es de otra empresa: distinguirlos
// diría que ese UUID existe en algún sitio. La consola tiene que conservar esa ambigüedad en el texto
// que pinta, para las CUATRO operaciones que pueden recibirlo.
//
// 🔴 EL ALTA NO ESTÁ EN ESTA TABLA, y su ausencia es la afirmación, no un olvido: es la única
// operación de la consola donde un 404 significa lo contrario. El alta pregunta por el PADRÓN de
// identity, no por un recurso del tenant, así que ahí no hay frontera de empresa que proteger y
// «no pertenece a tu empresa» sería un diagnóstico falso ante el fallo más frecuente de esa
// pantalla: un UUID mal pegado. Meterla aquí haría caer el test — y haría bien. Su criterio, que es
// el simétrico, está en el test HERMANO justo debajo; se leen juntos a propósito.
func TestCrossTenant_El404NoSeCuentaComoInexistencia(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre  string
		ruta    string
		form    url.Values
		destino string
		rutaAPI string
	}{
		{"baja de miembro", "/miembros/" + testOtherUserID + "/baja", url.Values{}, "/miembros",
			"DELETE /api/v1/members/{user_id}"},
		{"crear rol", "/roles", url.Values{"name": {"Cajera"}}, "/roles",
			"POST /api/v1/roles"},
		{"asignar rol", "/roles/asignar",
			url.Values{"user_id": {testOtherUserID}, "role_id": {testTenantRoleID}}, "/roles",
			"POST /api/v1/members/{user_id}/roles"},
		{"retirar rol", "/roles/retirar",
			url.Values{"user_id": {testOtherUserID}, "role_id": {testTenantRoleID}}, "/roles",
			"DELETE /api/v1/members/{user_id}/roles/{role_id}"},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			rutas := rolesOK()
			rutas["GET /api/v1/members"] = stubResponse{http.StatusOK, membersBody(testUserID, testOtherUserID)}
			rutas[caso.rutaAPI] = stubResponse{http.StatusNotFound, `{"error":"recurso no encontrado"}`}
			api := newStubAPI(t, rutas)
			router := adminRouter(api)

			rec := postFormWithCSRF(router, caso.ruta, caso.form, clientSessionCookie(t))
			destino := redirectTarget(t, rec)
			if destino != caso.destino+"?error="+flashNotInYourTenant {
				t.Fatalf("Location = %q, want %q", destino, caso.destino+"?error="+flashNotInYourTenant)
			}

			out := getWithSession(t, router, destino).Body.String()
			if !strings.Contains(out, "no pertenece a tu empresa") {
				t.Errorf("el 404 no se explica como frontera de empresa. Body: %s", out)
			}
			if strings.Contains(out, "no encontrado") {
				t.Error("el aviso dice «no encontrado»: eso es justo lo que este 404 NO significa")
			}
		})
	}
}

// TestCrossTenant_ElAltaEsLaEXCEPCION_SuS404SiEsInexistencia.
//
// El hermano del de arriba, y el que fija por qué el alta no está en su tabla. Aquí el 404 SÍ es «no
// existe»: la plataforma consultó identity con su credencial M2M y ese UUID no está en el padrón. No
// hay otra empresa al otro lado cuyo secreto haya que guardar.
//
// El aserto NEGATIVO —que jamás se pinte «no pertenece a tu empresa»— importa tanto como el
// positivo: ese es el texto que saldría solo con que alguien tradujera el 404 del alta con el
// traductor genérico o moviera la rama de ErrPersonUnknown detrás de la de ErrNotFound. Las dos
// regresiones dejan todo lo demás en verde.
func TestCrossTenant_ElAltaEsLaEXCEPCION_SuS404SiEsInexistencia(t *testing.T) {
	t.Parallel()

	rutas := rolesOK()
	rutas["GET /api/v1/members"] = stubResponse{http.StatusOK, membersBody(testUserID, testOtherUserID)}
	rutas["POST /api/v1/members"] = stubResponse{http.StatusNotFound, `{"error":"usuario no encontrado"}`}
	api := newStubAPI(t, rutas)
	router := adminRouter(api)

	rec := postFormWithCSRF(router, "/miembros", url.Values{"user_id": {testOtherUserID}}, clientSessionCookie(t))
	destino := redirectTarget(t, rec)
	if destino != "/miembros?error="+flashPersonUnknown {
		t.Fatalf("Location = %q, want %q", destino, "/miembros?error="+flashPersonUnknown)
	}

	out := getWithSession(t, router, destino).Body.String()
	if !strings.Contains(out, "no existe en wApp") {
		t.Errorf("el 404 del alta no se explica como inexistencia. Body: %s", out)
	}
	if strings.Contains(out, "no pertenece a tu empresa") {
		t.Error("el 404 del alta se explicó como frontera de empresa: ahí no hay ninguna empresa al otro lado")
	}
	// Y el detalle del upstream sigue sin pintarse: el texto sale del catálogo.
	if strings.Contains(out, "usuario no encontrado") {
		t.Error("el mensaje del upstream acabó en pantalla")
	}
}
