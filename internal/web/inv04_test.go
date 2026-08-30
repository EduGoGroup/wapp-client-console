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
//   - INV-04: el `tenant_id` no lo manda la UI. Sale del Context Token, que la plataforma verifica.
//     🆕 Con UNA excepción desde T5.3 —la ELECCIÓN de empresa—, que va declarada abajo y con test
//     HERMANO de aserto POSITIVO, no escondida en la tabla del invariante.
//   - Cross-tenant ⇒ 404: la plataforma responde 404 y no 403 ante un identificador de otra empresa,
//     y la consola lo trata como frontera, no como inexistencia.

// rutaEleccionDeEmpresa es la ÚNICA llamada de esta consola que manda un `tenant_id`, y por eso está
// escrita una sola vez: la nombran el test que la exceptúa y el que la exige.
const rutaEleccionDeEmpresa = "POST /api/v1/auth/active-tenant"

// ejerceTodasLasPantallas recorre TODA la superficie de la consola contra el doble: las páginas y
// TODAS las operaciones. Devuelve el doble con todo lo capturado.
//
// Que sea exhaustivo es el punto: un aserto sobre «las llamadas» que solo recorriera una pantalla
// dejaría fuera justo la que un día mande el tenant de más.
func ejerceTodasLasPantallas(t *testing.T) *stubAPI {
	t.Helper()

	// 🔴 EL PLAN DEL DOBLE LAS TRAE TODAS, Y ES PARTE DEL BARRIDO. Dos de las pantallas de esta
	// consola cuelgan de un gate por feature: sin `cart_basic` la bandeja entera contesta 403 y sin
	// `catalog_import` la importación también, y en los dos casos NO SE LLAMA A LA API — que es
	// exactamente cómo una pantalla se cae de este barrido sin que nada falle. `llm_intake` va por lo
	// mismo un nivel más abajo: sin ella, regenerar y sugerir cortan en el handler y sus dos llamadas
	// nunca salen.
	api := newStubAPI(t, map[string]stubResponse{
		"GET /api/v1/entitlements": {http.StatusOK, entitlementsBody("commerce",
			"catalog_import", "menu", featureCartBasic, featureLLMIntake)},
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
		// Invitaciones (T-A7/T-A8): la pantalla y sus TRES operaciones. El canje es la única llamada
		// de esta consola que sale con un token que el usuario ha TECLEADO, así que es por donde un
		// `tenant_id` de más se colaría con más facilidad.
		"GET /api/v1/invitations":         {http.StatusOK, invitacionesBody()},
		"POST /api/v1/invitations":        {http.StatusCreated, invitacionEmitidaBody(testInviteToken)},
		"DELETE /api/v1/invitations/{id}": {http.StatusNoContent, ""},
		"POST /api/v1/invitations/accept": {http.StatusNoContent, ""},
		// El plano de la empresa DEL SUJETO (T5.3): el listado que pintan las seis pantallas y la
		// elección, que es la excepción a INV-04 y por tanto lo que más falta hace ejercitar aquí.
		rutaListadoDeEmpresas: {http.StatusOK, dosEmpresas()},
		rutaEleccionDeEmpresa: {http.StatusNoContent, ""},
		// El EDITOR (T6.3/T6.4). Publicar es la llamada cuyo cuerpo lleva un JSON ENTERO escrito por
		// el usuario, así que es por donde un `tenant_id` de más se colaría sin que se note: la
		// consola lo envuelve en `{definition}` y no toca lo de dentro.
		"GET /api/v1/flows":            {http.StatusOK, flowsBody(flowJSON(testFlowID, 3, ""))},
		"GET /api/v1/flows/{id}":       {http.StatusOK, flowDefinitionBody},
		"POST /api/v1/flows":           {http.StatusCreated, `{"flow_id":"` + testFlowID + `","version":4}`},
		"GET /api/v1/triggers":         {http.StatusOK, triggersBody(disparadorNormal)},
		"POST /api/v1/triggers":        {http.StatusCreated, disparadorNormal},
		"DELETE /api/v1/triggers/{id}": {http.StatusNoContent, ""},
		// LA BANDEJA (T7.2–T7.6), que llevaba una ola entera sin recorrerse aquí. Son DIEZ puertas y
		// van todas: entre ellas están las dos que le mandan un WhatsApp a una persona y los dos
		// formularios cuyo cuerpo lo escribe la dueña a mano en un `<textarea>`.
		"GET /api/v1/intakes":                        {http.StatusOK, laBandejaDeCampo()},
		"GET /api/v1/intakes/{id}":                   {http.StatusOK, solicitudDeCampo()},
		"POST /api/v1/intakes/discard":               {http.StatusOK, descarteBody([]string{testIntakeID}, nil)},
		"POST /api/v1/intakes/{id}/status":           {http.StatusOK, intakeMovido},
		"PUT /api/v1/intakes/{id}/items":             {http.StatusOK, solicitudDeCampo()},
		"POST /api/v1/intakes/{id}/approve":          {http.StatusOK, solicitudDeCampo()},
		"POST /api/v1/intakes/{id}/request-info":     {http.StatusOK, solicitudDeCampo()},
		"POST /api/v1/intakes/{id}/reanalyze":        {http.StatusOK, regeneracionEncargada},
		"POST /api/v1/intakes/{id}/quote-suggestion": {http.StatusOK, sugerenciaDelRespaldo},
		// LA IMPORTACIÓN DE CATÁLOGO (T8.2/T8.3). 🔴 Es la llamada por la que MÁS falta hacía pasar
		// este barrido: su cuerpo es un DOCUMENTO ENTERO escrito por el usuario y además viaja en
		// MULTIPART, que es una forma que ningún otro caso de aquí ejercita — un `tenant_id` de más
		// dentro de un sobre multipart no se parece a nada de lo de arriba.
		rutaRefsDeContenido: {http.StatusOK, refsBody("catalogo")},
		rutaPromptCatalogo: {http.StatusOK,
			`{"format":"wapp.catalog_import","version":1,"prompt":"pega esto en tu asistente"}`},
		"GET /api/v1/catalog/import/template": {http.StatusOK, `{"format":"wapp.catalog_import"}`},
		rutaImportJSON:                        {http.StatusOK, diffDeCampo},
		rutaImportTabular:                     {http.StatusOK, diffTabularDeCampo},
	})
	router := adminRouter(api)
	sess := clientSessionCookie(t)

	for _, ruta := range []string{"/", "/sesiones", "/miembros", "/roles", "/invitaciones",
		rutaFlujos, rutaFlujos + "/" + testFlowID, rutaDisparadores,
		// La bandeja y su detalle (T7.2/T7.3), y la importación (T8.2), que hasta ahora no entraban.
		rutaSolicitudes, rutaSolicitudes + "/" + testIntakeID, rutaCatalogo,
		// 🔴 Y LA DESCARGA DE LA PLANTILLA (T8.3), que es un GET que NO devuelve una página. Aquí sí
		// entra —y no como una excepción tipo `noSonPaginas`— porque lo que este barrido mira es la
		// PETICIÓN SALIENTE, no el HTML de vuelta: que la respuesta sean bytes no la exime de nada.
		// Se comprueba aparte por eso mismo: su 200 no es HTML y el bucle de abajo no puede pedirle
		// que lo sea.
	} {
		if rec := getWithSession(t, router, ruta); rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", ruta, rec.Code)
		}
	}
	if rec := getWithSession(t, router, rutaPlantillaCatalogo+"?format=json"); rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", rutaPlantillaCatalogo, rec.Code)
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
	// Las tres de invitaciones. El canje va con el token en el cuerpo, que es lo que la API espera.
	postFormWithCSRF(router, "/invitaciones", url.Values{"ttl": {"86400"}, "role_id": {testTenantRoleID}}, sess)
	postFormWithCSRF(router, "/invitaciones/"+testInvitacionPendiente+"/revocar", url.Values{}, sess)
	postFormWithCSRF(router, "/invitaciones/canjear", url.Values{"token": {testInviteToken}}, sess)
	// Las TRES del editor. La publicación va con una definición de verdad porque su cuerpo es lo que
	// el usuario escribió, no un formulario de campos sueltos.
	postFormWithCSRF(router, rutaFlujos, url.Values{
		"flow_id": {testFlowID}, "is_new": {"0"},
		"definition": {`{"flow_id":"` + testFlowID + `","version":4,"initial":"a","nodes":{}}`},
	}, sess)
	postFormWithCSRF(router, rutaDisparadores, url.Values{
		"kind": {"keyword"}, "keyword": {"hola"}, "flow_id": {testFlowID}, "match_type": {"exact"},
	}, sess)
	postFormWithCSRF(router, rutaDisparadores+"/"+testTriggerID+"/borrar", url.Values{}, sess)
	// LAS SIETE ACCIONES DEL DETALLE más el descarte por lotes (T7.2–T7.6). Las dos que le hablan al
	// cliente —aprobar y pedir información— mandan un WhatsApp a una persona, y sus cuerpos llevan
	// TEXTO tecleado: son las que peor se leerían con una empresa de más metida dentro.
	detalle := rutaSolicitudes + "/" + testIntakeID
	postFormWithCSRF(router, rutaSolicitudesDescartar,
		url.Values{campoDescarteID: {testIntakeID}, campoDescarteAccion: {"discard"}}, sess)
	postFormWithCSRF(router, detalle+sufijoEstado, url.Values{campoEstado: {"confirmed"}}, sess)
	// Los DOS formularios de líneas van con una fila de verdad: su cuerpo es una lista de artículos
	// con precios, que es el cuerpo más grande de la bandeja.
	lineas := url.Values{
		campoLineaSKU: {"PAN-01"}, campoLineaEtiqueta: {"Pan de yema"},
		campoLineaPersonalizacion: {""}, campoLineaCantidad: {"2"}, campoLineaPrecio: {"1,50"},
	}
	postFormWithCSRF(router, detalle+sufijoLineas, lineas, sess)
	postFormWithCSRF(router, detalle+sufijoCorregir, lineas, sess)
	postFormWithCSRF(router, detalle+sufijoRegenerar, url.Values{"reanalyze_text": {"añadió dos gaseosas"}}, sess)
	postFormWithCSRF(router, detalle+sufijoAprobar, url.Values{campoRespuesta: {"Le quedan 21000 en total."}}, sess)
	postFormWithCSRF(router, detalle+sufijoPedirInfo, url.Values{campoPregunta: {"¿Para cuándo lo necesita?"}}, sess)
	postFormWithCSRF(router, detalle+sufijoSugerir, url.Values{}, sess)

	// 🔴 LA IMPORTACIÓN, Y VA POR MULTIPART, que es su forma de verdad. Las DOS puertas: la del
	// documento pegado y la de la planilla, y en los dos modos —comprobar y aplicar—, porque aplicar
	// es la única llamada de esta consola que reemplaza el catálogo entero de una empresa y es
	// justamente donde una empresa de más haría el daño mayor.
	postMultipartConCSRF(t, router, rutaCatalogo, url.Values{
		campoModoCatalogo: {"validate"}, campoRefCatalogo: {"catalogo"},
		campoDocumentoCatalogo: {documentoPegado},
	}, nil, sess)
	postMultipartConCSRF(t, router, rutaCatalogo, url.Values{
		campoModoCatalogo: {"apply"}, campoRefCatalogo: {"catalogo"},
		campoDocumentoCatalogo: {documentoNormalizado},
	}, nil, sess)
	postMultipartConCSRF(t, router, rutaCatalogo, url.Values{
		campoModoCatalogo: {"validate"}, campoRefCatalogo: {"catalogo"},
	}, &ficheroSubido{nombre: "catalogo.csv", contenido: []byte("sku;articulo;precio\nPAN-01;Pan;1.50\n")}, sess)

	// Y la elección de empresa, que es la única llamada de toda la superficie que manda un tenant.
	postFormWithCSRF(router, rutaEmpresa, url.Values{"tenant_id": {testOtherTenantID}}, sess)

	// 🔴 EL SUELO SUBE CON EL BARRIDO, y por eso no se queda en 25: este número es lo único que
	// impide que el recorrido de arriba se quede a medias en silencio —un gate que empiece a cortar,
	// un formulario que se rechace en local— y siga saliendo verde sobre las pocas llamadas que sí
	// salieron. Con la bandeja y la importación dentro son 40 y pico; el suelo se deja por debajo
	// para no atarlo al conteo exacto, pero MUY por encima del anterior.
	if len(api.Requests()) < 40 {
		t.Fatalf("solo se capturaron %d peticiones: el recorrido no ejercitó la superficie completa (%v)",
			len(api.Requests()), routesOf(api.Requests()))
	}
	// Y las puertas que este barrido acaba de estrenar tienen que haber salido DE VERDAD: sin esto,
	// un gate por feature que cortara antes de la red dejaría las dos pantallas fuera y el conteo de
	// arriba seguiría cuadrando con las llamadas de las demás.
	for _, ruta := range []string{
		"GET /api/v1/intakes", "POST /api/v1/intakes/discard",
		"POST /api/v1/intakes/" + testIntakeID + "/approve",
		"POST /api/v1/catalog/import", "POST /api/v1/catalog/import/tabular",
		"GET /api/v1/catalog/import/template",
	} {
		if !api.Called(ruta) {
			t.Fatalf("%s no llegó a salir: esa pantalla se cayó del barrido (salieron %v)",
				ruta, routesOf(api.Requests()))
		}
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
//
// 🆕 🔴 Y SALTA EXACTAMENTE UNA RUTA, la elección de empresa. No se mete en la tabla porque la
// rompería —esa llamada SÍ manda el tenant, y tiene que mandarlo—, y no se deja fuera en silencio
// porque entonces el invariante envejecería sin que nadie lo notara: su mitad positiva está en el
// test HERMANO de abajo, que además exige que sea la ÚNICA. La excusa de la excepción está escrita
// en apiclient/tenants.go y es que aceptar el tenant AQUÍ es lo que permite que el CANJE no tenga
// que aceptarlo nunca — y el canje es el que se repite solo cada ~13 minutos sin nadie delante.
func TestINV04_LaConsolaNoMandaNuncaElTenant(t *testing.T) {
	t.Parallel()
	api := ejerceTodasLasPantallas(t)

	for _, req := range api.Requests() {
		if req.Route() == rutaEleccionDeEmpresa {
			continue
		}
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

// TestINV04_LaELECCIONdeEmpresaEsLaUNICAexcepcion — el hermano del de arriba, y se leen juntos.
//
// 🔴 UN ASERTO POSITIVO Y UN CONTEO, porque una excepción sin positivo se apaga sola: el día que
// alguien dejara de mandar el `tenant_id` en esta ruta, el selector no cambiaría de empresa y el test
// de arriba —que solo mira que NADIE lo mande— seguiría en verde. Y el conteo es lo que impide que la
// excepción se ensanche: si mañana una segunda llamada empieza a mandar la empresa, cae aquí y hay
// que decidirlo a propósito en vez de heredarlo.
func TestINV04_LaELECCIONdeEmpresaEsLaUNICAexcepcion(t *testing.T) {
	t.Parallel()
	api := ejerceTodasLasPantallas(t)

	// POSITIVO: la elección SÍ manda la empresa, en el cuerpo y no en la ruta ni en la query.
	req := api.Last(t, rutaEleccionDeEmpresa)
	if !strings.Contains(req.Body, `"tenant_id":"`+testOtherTenantID+`"`) {
		t.Errorf("la elección de empresa no mandó la empresa elegida: %s", req.Body)
	}
	if req.Query.Has("tenant_id") || strings.Contains(req.Path, testOtherTenantID) {
		t.Errorf("la empresa viajó fuera del cuerpo (query %q, ruta %q)", req.Query.Encode(), req.Path)
	}

	// Y es la ÚNICA de toda la superficie que lo hace.
	conTenant := map[string]bool{}
	for _, r := range api.Requests() {
		if strings.Contains(r.Body, "tenant_id") {
			conTenant[r.Route()] = true
		}
	}
	if len(conTenant) != 1 || !conTenant[rutaEleccionDeEmpresa] {
		rutas := make([]string, 0, len(conTenant))
		for ruta := range conTenant {
			rutas = append(rutas, ruta)
		}
		t.Errorf("mandan tenant_id en el cuerpo %v; la única excepción a INV-04 es %q", rutas, rutaEleccionDeEmpresa)
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
