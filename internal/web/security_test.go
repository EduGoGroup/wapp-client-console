package web

import (
	"bytes"
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
	rutasAdmin := map[string]stubResponse{
		// `cart_basic` va en el plan del doble desde la Ola 7: sin ella el gate por ruta corta con
		// 403 y la bandeja —que es la superficie de HTML más grande de esta consola— quedaría
		// fuera de esta familia sin que nadie lo notara. Su rama SIN la feature se captura aparte,
		// más abajo.
		"GET /api/v1/entitlements": {http.StatusOK, entitlementsBody("commerce", "catalog_import", "menu", featureCartBasic)},
		"GET /api/v1/members":      {http.StatusOK, membersBody(testUserID, testOtherUserID)},
		"GET /api/v1/roles":        {http.StatusOK, rolesBody},
		// La pantalla de sesiones se pinta ENTERA —con su tabla, sus chips y su formulario—, que es
		// donde se colaría un `style=` o un `<script>`. Con el listado vacío no se vería ninguna de
		// las tres cosas.
		"GET /api/v1/sessions": {http.StatusOK, sesionesBody(
			`{"session_id":"` + testSessionID + `","edge_id":"edge-alpha","state":"online",` +
				`"profile":"active","self_pn":"+593990000001",` +
				`"intent_circuit":"open","worker_taskset":"solapada"}`)},
		// La pantalla de invitaciones, también entera: con las cuatro filas hace falta que salgan sus
		// CUATRO chips de estado y el botón de anular, que solo pinta la fila pendiente.
		"GET /api/v1/invitations":  {http.StatusOK, invitacionesBody()},
		"POST /api/v1/invitations": {http.StatusCreated, invitacionEmitidaBody(testInviteToken)},
		// Las dos pantallas del EDITOR (T6.3/T6.4), enteras: la de flujos con su tabla y su detalle
		// —que lleva el único <textarea> con contenido de la consola—, y la de disparadores con sus
		// nueve columnas, sus chips y el formulario de OCHO campos, que es la superficie más grande
		// de HTML de todo el repo y por tanto donde más fácil se cuela un `style=` o un `onchange=`.
		"GET /api/v1/flows":      {http.StatusOK, flowsBody(flowJSON(testFlowID, 3, "2026-08-01T10:00:00Z"))},
		"GET /api/v1/flows/{id}": {http.StatusOK, flowDefinitionBody},
		"GET /api/v1/triggers":   {http.StatusOK, triggersBody(disparadorSombreado, disparadorNormal)},
		// La BANDEJA (T7.2): la lista con su tabla, su paginador y su formulario de descarte, y las
		// DOS pantallas del descarte, que son HTML que ningún GET sirve.
		"GET /api/v1/intakes": {http.StatusOK, laBandejaDeCampo()},
		// EL DETALLE (T7.3), que es la superficie de HTML más grande del repo: seis tarjetas, cuatro
		// tablas y SIETE formularios, dos de ellos con `<textarea>` y uno con un `<select>`. Es donde
		// más fácil se cuela un `style=` o un `onchange=`, y además la única pantalla que pinta texto
		// que escribió una persona por WhatsApp.
		"GET /api/v1/intakes/{id}": {http.StatusOK, solicitudDeCampo()},
		"POST /api/v1/intakes/discard": {http.StatusOK, descarteBody(
			[]string{testIntakeID}, map[string]string{testOtroIntake: "live_event"})},
		// LA SUGERENCIA (T7.6), para poder capturar la rama de la pantalla que solo existe tras
		// pedirla: el párrafo del ORIGEN, que es HTML que ningún GET sirve por su cuenta.
		"POST /api/v1/intakes/{id}/quote-suggestion": {http.StatusOK, sugerenciaDelRespaldo},
		// LA IMPORTACIÓN DE CATÁLOGO (T8.2). `catalog_import` ya va en el plan del doble de arriba,
		// así que la pantalla se pinta ENTERA: el selector de ref, el <textarea> del documento, el
		// <input type=file> y el <textarea> del prompt, que es texto largo servido por el upstream.
		// Sin las refs y sin el prompt, dos de esas cuatro cosas no llegarían al HTML.
		rutaRefsDeContenido: {http.StatusOK, refsBody("catalogo", "promociones")},
		rutaPromptCatalogo: {http.StatusOK,
			`{"format":"wapp.catalog_import","version":1,"prompt":"` + promptDeLaPlataforma + `"}`},
		rutaImportJSON: {http.StatusOK, diffDeCampo},
	}
	api := newStubAPI(t, rutasAdmin)
	routerAdmin := adminRouter(api)
	for nombre, ruta := range map[string]string{
		"home_con_plan":    "/",
		"sesiones":         "/sesiones",
		"miembros":         "/miembros",
		"roles":            "/roles",
		"invitaciones":     "/invitaciones",
		"mi_identificador": "/mi-identificador",
		"flujos":           rutaFlujos,
		"flujo_nuevo":      rutaFlujos + "/" + flujoNuevo,
		"flujo_detalle":    rutaFlujos + "/" + testFlowID,
		"disparadores":     rutaDisparadores,
		"solicitudes":      rutaSolicitudes + "?page_size=5",
		// El DETALLE (T7.3) se recorre sobre la solicitud de campo, que trae las cuatro clases de
		// línea y por tanto pinta las cuatro tablas y los siete formularios.
		"solicitud": rutaSolicitudes + "/" + testIntakeID,
		// LA IMPORTACIÓN (T8.2): el paso 1, con el único <input type=file> de la consola y dos
		// <textarea>, uno de ellos con texto largo que escribió el upstream.
		"catalogo": rutaCatalogo,
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

	// 🔴 La pantalla del CÓDIGO recién emitido es otro HTML —la caja del secreto— y solo existe cuando
	// el GET trae la cookie efímera que puso la emisión. Sin este par POST+GET, el único bloque de
	// esta consola que pinta material sensible quedaría fuera de los tests de CSP, de estilo inline y
	// de JavaScript, y nadie lo notaría: todo lo demás seguiría verde.
	renders["invitaciones_con_token"] = getConCookies(routerAdmin, "/invitaciones",
		clientSessionCookie(t), cookieDeInvitacion(t, routerAdmin))

	// 🔴 LA PANTALLA CON LA SUGERENCIA RECIÉN PEDIDA (T7.6) es otra rama que ningún GET sirve solo: el
	// párrafo del ORIGEN únicamente se emite cuando el GET llega con la cookie efímera que puso el
	// POST. Sin este par POST+GET, el único bloque de esta consola que pinta un texto redactado por un
	// modelo quedaría fuera de los tests de CSP, de estilo inline y de JavaScript, y nadie lo notaría:
	// todo lo demás seguiría verde.
	//
	// 🔑 VA CON UN ROUTER APARTE y no cambiando el de arriba, por lo mismo que el selector de empresas:
	// necesita un plan CON `llm_intake`, y el principal no lo tiene A PROPÓSITO. Sin esa capacidad el
	// detalle pinta TRES bloques de paywall —el de sugerir, el de regenerar y el de las preguntas
	// preparadas— que también son HTML de esta pantalla; añadírsela al router principal los sacaría del
	// recorrido sin que nadie lo notara. Con los dos routers se recorren las dos mitades.
	rutasConIA := make(map[string]stubResponse, len(rutasAdmin))
	for ruta, resp := range rutasAdmin {
		rutasConIA[ruta] = resp
	}
	rutasConIA["GET /api/v1/entitlements"] = stubResponse{http.StatusOK,
		entitlementsBody("commerce", "catalog_import", "menu", featureCartBasic, featureLLMIntake)}
	routerConIA := adminRouter(newStubAPI(t, rutasConIA))
	renders["solicitud_con_ia"] = getWithSession(t, routerConIA, rutaSolicitudes+"/"+testIntakeID)
	renders["solicitud_con_sugerencia"] = getConCookies(routerConIA, rutaSolicitudes+"/"+testIntakeID,
		clientSessionCookie(t), cookieDeSugerenciaRecienPedida(t, routerConIA))

	// 🔴 LOS DOS REPINTADOS DE D-047.16 son HTML que NO se sirve en ningún GET: solo aparecen cuando
	// la validación local rechaza una publicación o un alta, y llevan dentro justo lo que el usuario
	// tecleó. Sin este par, el único HTML de esta consola que devuelve texto del usuario a la página
	// quedaría fuera de los tests de CSP, de estilo inline y de JavaScript, y nadie lo notaría.
	renders["flujo_repintado_400"] = postFormWithCSRF(routerAdmin, rutaFlujos, url.Values{
		"flow_id": {testFlowID}, "is_new": {"0"}, "definition": {"esto no es json"},
	}, clientSessionCookie(t))
	renders["disparador_repintado_400"] = postFormWithCSRF(routerAdmin, rutaDisparadores, url.Values{
		"kind": {"keyword"}, "keyword": {"hola"}, "flow_id": {""},
	}, clientSessionCookie(t))
	for _, nombre := range []string{"flujo_repintado_400", "disparador_repintado_400"} {
		if rec := renders[nombre]; rec.Code != http.StatusBadRequest {
			t.Fatalf("el repintado %q respondió %d, want 400 (D-047.16). Body: %s", nombre, rec.Code, rec.Body.String())
		}
	}

	// 🔴 LAS TRES PANTALLAS DE LA BANDEJA QUE NINGÚN GET SIRVE (T7.2). Las tres son HTML propio y
	// ninguna se alcanza tecleando una URL: el 403 del gate por feature —la rama `sin_plan` de la
	// plantilla— y los dos pasos del descarte, que llevan dentro los ids que se marcaron. Sin estas
	// capturas quedarían fuera de los tests de CSP, de estilo inline y de JavaScript.
	sesion := clientSessionCookie(t)
	renders["solicitudes_descarte_confirmar"] = postFormWithCSRF(routerAdmin, rutaSolicitudesDescartar, url.Values{
		"intake_id": {testIntakeID, testOtroIntake},
	}, sesion)
	renders["solicitudes_descarte_resultado"] = postFormWithCSRF(routerAdmin, rutaSolicitudesDescartar, url.Values{
		"intake_id": {testIntakeID, testOtroIntake}, "action": {"discard"},
	}, sesion)
	for _, nombre := range []string{"solicitudes_descarte_confirmar", "solicitudes_descarte_resultado"} {
		if rec := renders[nombre]; rec.Code != http.StatusOK {
			t.Fatalf("el paso %q del descarte respondió %d, want 200. Body: %s", nombre, rec.Code, rec.Body.String())
		}
	}

	apiSinPlan := newStubAPI(t, map[string]stubResponse{
		rutaListadoDeEmpresas:      {http.StatusOK, unaEmpresa()},
		"GET /api/v1/entitlements": {http.StatusOK, entitlementsBody("basic", "menu")},
	})
	routerSinPlan := adminRouter(apiSinPlan)
	renders["solicitudes_sin_plan"] = getWithSession(t, routerSinPlan, rutaSolicitudes)
	if rec := renders["solicitudes_sin_plan"]; rec.Code != http.StatusForbidden {
		t.Fatalf("la bandeja sin cart_basic respondió %d, want 403. Body: %s", rec.Code, rec.Body.String())
	}
	// 🆕 La rama VACÍA de la importación (T8.2), por el mismo motivo: el plan de arriba SÍ trae
	// `catalog_import`, así que sin esta captura el HTML del 403 de esa pantalla quedaría fuera de la
	// familia. El mismo router sirve las dos porque a este plan le faltan las dos capacidades.
	renders["catalogo_sin_plan"] = getWithSession(t, routerSinPlan, rutaCatalogo)
	if rec := renders["catalogo_sin_plan"]; rec.Code != http.StatusForbidden {
		t.Fatalf("la importación sin catalog_import respondió %d, want 403. Body: %s", rec.Code, rec.Body.String())
	}

	// 🔴 EL PASO 2 DE LA IMPORTACIÓN (T8.2) es HTML que NINGÚN GET sirve: el diff con sus tres tablas
	// y el formulario que ESCRIBE el catálogo, con el documento entero dentro de un `hidden`. Sin esta
	// captura, el único bloque de esta consola que devuelve a la página un documento que subió el
	// usuario quedaría fuera de los tests de CSP, de estilo inline y de JavaScript.
	renders["catalogo_paso2"] = postMultipartConCSRF(t, routerAdmin, rutaCatalogo, url.Values{
		campoModoCatalogo:      {"validate"},
		campoRefCatalogo:       {"catalogo"},
		campoDocumentoCatalogo: {documentoPegado},
	}, nil, clientSessionCookie(t))
	if rec := renders["catalogo_paso2"]; rec.Code != http.StatusOK {
		t.Fatalf("el paso 2 de la importación respondió %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}

	// 🔴 Y LA PÁGINA DEL 413 (T8.4), que tampoco la sirve ningún GET y que además se pinta ANTES del
	// AuthMiddleware: es la única página de esta consola que sale sin barra de navegación, así que es
	// la única cuyo HTML no se parece a ninguno de los de arriba.
	renders["catalogo_cuerpo_grande"] = postMultipartConCSRF(t, routerAdmin, rutaCatalogo,
		url.Values{campoModoCatalogo: {"validate"}},
		&ficheroSubido{nombre: "catalogo.csv", contenido: bytes.Repeat([]byte("a"), 5<<20)},
		clientSessionCookie(t))
	if rec := renders["catalogo_cuerpo_grande"]; rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("la página del techo de cuerpo respondió %d, want 413. Body: %s", rec.Code, rec.Body.String())
	}

	// Y las MISMAS pantallas en el estado «sin empresa», que es otra rama de plantilla —el parcial
	// sin_empresa— y por tanto otro HTML que puede traer un style= o un <script> sin que ninguna de
	// las capturas de arriba lo vea.
	sinTenant := sessionCookieFor(t, testUserID, "")
	for nombre, ruta := range map[string]string{
		"home_sin_empresa":             "/",
		"sesiones_sin_empresa":         "/sesiones",
		"miembros_sin_empresa":         "/miembros",
		"roles_sin_empresa":            "/roles",
		"invitaciones_sin_empresa":     "/invitaciones",
		"mi_identificador_sin_empresa": "/mi-identificador",
		"flujos_sin_empresa":           rutaFlujos,
		"disparadores_sin_empresa":     rutaDisparadores,
		"solicitudes_sin_empresa":      rutaSolicitudes,
		"solicitud_sin_empresa":        rutaSolicitudes + "/" + testIntakeID,
		"catalogo_sin_empresa":         rutaCatalogo,
	} {
		rec := getConCookie(routerAdmin, ruta, sinTenant)
		if rec.Code != http.StatusOK {
			t.Fatalf("la página %q respondió %d, want 200. Body: %s", nombre, rec.Code, rec.Body.String())
		}
		renders[nombre] = rec
	}

	// 🆕 El SELECTOR DE EMPRESAS (T5.3) es HTML nuevo —dos bloques, con `<select>` y formulario— y
	// tiene que pasar por esta familia o quedaría fuera de los tests de CSP, de estilo inline y de
	// JavaScript, que es exactamente donde se cuela un `onchange=` en un desplegable.
	//
	// Van con un router APARTE y no cambiando el de arriba: los dos bloques son EXCLUYENTES entre sí
	// y excluyentes con el parcial `sin_empresa`, así que servir el listado en el router principal
	// habría sacado del recorrido las seis capturas de «sin empresa» sin que nadie lo notara.
	rutasDosEmpresas := make(map[string]stubResponse, len(rutasAdmin)+1)
	for ruta, resp := range rutasAdmin {
		rutasDosEmpresas[ruta] = resp
	}
	rutasDosEmpresas[rutaListadoDeEmpresas] = stubResponse{http.StatusOK, dosEmpresas()}
	routerDosEmpresas := adminRouter(newStubAPI(t, rutasDosEmpresas))

	// Con empresa activa: el selector de la BARRA, que sale en las seis pantallas.
	renders["miembros_con_selector"] = getWithSession(t, routerDosEmpresas, "/miembros")
	// Sin empresa activa y con dos entre las que elegir: el bloque `elegir_empresa`.
	renders["home_elegir_empresa"] = getConCookie(routerDosEmpresas, "/", sessionCookieFor(t, testUserID, ""))
	for _, nombre := range []string{"miembros_con_selector", "home_elegir_empresa"} {
		if rec := renders[nombre]; rec.Code != http.StatusOK {
			t.Fatalf("la página %q respondió %d, want 200. Body: %s", nombre, rec.Code, rec.Body.String())
		}
		if !strings.Contains(renders[nombre].Body.String(), `id="tenant-switcher"`) {
			t.Fatalf("la página %q no trae el selector de empresas: esta familia no lo estaría mirando", nombre)
		}
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
		"/": true, "/sesiones": true, "/miembros": true, "/roles": true, "/invitaciones": true,
		"/mi-identificador": true,
		// El editor (T6.3/T6.4). El detalle entra por su patrón —`/flujos/:id`—, que es como lo
		// registra el router, y se recorre con DOS peticiones: el valor mágico `nuevo` y un flujo de
		// verdad, que son dos ramas distintas de la misma plantilla.
		rutaFlujos: true, rutaFlujos + "/:id": true, rutaDisparadores: true,
		// La bandeja (T7.2). Su GET se recorre con `?page_size=5`, que es lo que hace que el
		// paginador se pinte: con el tamaño por defecto las siete solicitudes de campo caben en una
		// página y el bloque del paginador no se emitiría.
		rutaSolicitudes: true,
		// El detalle (T7.3) entra por su patrón —`/solicitudes/:id`—, que es como lo registra el
		// router.
		rutaSolicitudes + rutaSolicitudDetalle: true,
		// La importación (T8.2). Se recorre en sus DOS ramas —el paso 1 por GET y el paso 2 por el
		// POST que lo produce—, además de la del 403 y la del techo de cuerpo.
		rutaCatalogo: true,
	}

	// 🔴 EL GET QUE NO ES UNA PÁGINA (T8.3), declarado aparte y no metido en el mapa de arriba. La
	// descarga de la plantilla responde BYTES —un CSV, un XLSX— sin pasar por el renderizador, así que
	// no hay HTML que escanear y meterla entre las «cubiertas» sería decir que esta familia la mira
	// cuando no puede. Va en su propio conjunto para que el conteo de abajo siga cuadrando sin mentir,
	// y para que una ruta nueva que tampoco sea una página tenga que declararse aquí a propósito.
	//
	// Lo que sí la vigila —tipo, nombre de fichero y que no lleve layout— está en catalogo_test.go.
	noSonPaginas := map[string]bool{
		rutaPlantillaCatalogo: true,
	}

	for ruta := range rutasGET {
		if !cubiertas[ruta] && !noSonPaginas[ruta] {
			t.Errorf("la pantalla %q no está en paginasRenderizadas: los tests de CSP no la miran", ruta)
		}
	}
	if len(rutasGET) != len(cubiertas)+len(noSonPaginas) {
		t.Errorf("el router sirve %d GET autenticados y los mapas cubren %d páginas + %d que no lo son",
			len(rutasGET), len(cubiertas), len(noSonPaginas))
	}
}

// TestPaginas_LaFamiliaMiraLaRamaCOMPLETADeLaBandeja cierra el ÚNICO punto ciego que el gate por
// ruta (T7.2) pudo abrir en esta familia, y lo cierra por escrito en vez de por suerte.
//
// 🔴 EL RIESGO, dicho entero: `/solicitudes` es la primera pantalla de esta consola cuya rama
// PRINCIPAL solo se pinta si el plan trae una feature. Los candados de CSP, estilo inline y
// JavaScript miran `rec.Body`, o sea el HTML que salió por el cable; si la captura de esta pantalla
// fuera un 403 —su rama VACÍA—, los tres seguirían verdes midiendo una página sin tabla, sin
// formularios y sin filas, que es justo donde se cuela un `style=` o un `<script>`.
//
// Hoy no pasa porque `rutasAdmin` mete `featureCartBasic` en el plan del doble, pero eso es un
// ACOPLAMIENTO IMPLÍCITO entre dos sitios del mismo fichero: quien quitara esa feature de ahí —para
// probar otra cosa— dejaría a la bandeja fuera del recorrido sin que nada fallara. Este test lo hace
// explícito: exige que la captura `solicitudes` traiga las anclas de la rama completa y que la
// captura `solicitudes_sin_plan` traiga las de la vacía. Las dos direcciones, para que ni una rama
// se caiga del recorrido ni la otra deje de ser la otra.
func TestPaginas_LaFamiliaMiraLaRamaCOMPLETADeLaBandeja(t *testing.T) {
	t.Parallel()

	renders := paginasRenderizadas(t, NewRouter(offlineConfig()))

	completa, ok := renders["solicitudes"]
	if !ok {
		t.Fatal("la familia no captura `solicitudes`: la bandeja quedó fuera de los tests de CSP")
	}
	// Las cuatro anclas son de la rama que SOLO existe con `cart_basic`: la tarjeta del listado, la
	// tabla, el formulario de descarte y el paginador. Ninguna se emite en la rama vacía.
	for _, ancla := range []string{
		`id="section-listado"`, `id="table-solicitudes"`, `id="form-descarte"`, `id="paginador"`,
	} {
		if !strings.Contains(completa.Body.String(), ancla) {
			t.Errorf("la captura `solicitudes` no trae %s: la familia está escaneando la pantalla "+
				"VACÍA y un style= o un <script> en la rama completa pasaría inadvertido", ancla)
		}
	}

	// 🆕 EL DETALLE (T7.3) cuelga del MISMO gate de grupo, así que corre exactamente el mismo riesgo
	// —y con más superficie: es la pantalla con más HTML del repo—. Sus anclas son de la rama que solo
	// existe con `cart_basic` y con la solicitud servida: la ficha, la comparación (que pinta texto de
	// una persona), el borrador editable y los dos formularios con `<textarea>`.
	detalle, ok := renders["solicitud"]
	if !ok {
		t.Fatal("la familia no captura `solicitud`: el detalle quedó fuera de los tests de CSP")
	}
	for _, ancla := range []string{
		`id="section-solicitud"`, `id="section-solicitud-comparacion"`,
		`id="table-solicitud-borrador"`, `id="form-solicitud-aprobar"`, `id="form-solicitud-lineas"`,
	} {
		if !strings.Contains(detalle.Body.String(), ancla) {
			t.Errorf("la captura `solicitud` no trae %s: la familia está escaneando una pantalla "+
				"incompleta y un style= o un <script> en la rama completa pasaría inadvertido", ancla)
		}
	}

	vacia, ok := renders["solicitudes_sin_plan"]
	if !ok {
		t.Fatal("la familia no captura `solicitudes_sin_plan`: la rama del 403 quedó sin vigilar")
	}
	if !strings.Contains(vacia.Body.String(), `id="section-sin-plan"`) {
		t.Error("la captura `solicitudes_sin_plan` no es la pantalla vacía: el gate dejó de cortar")
	}
	if strings.Contains(vacia.Body.String(), `id="section-listado"`) {
		t.Error("la pantalla del 403 sirvió el listado: las dos capturas son la misma rama y una de " +
			"las dos dejó de probar algo")
	}
}

// TestPaginas_LaFamiliaMiraLaRamaCOMPLETADeLaImportacion es el HERMANO del de arriba para la
// pantalla de catálogo (T8.2), y existe por la misma razón exacta: es la SEGUNDA pantalla de esta
// consola cuya rama principal solo se pinta si el plan trae una feature.
//
// 🔴 EL PUNTO CIEGO QUE CIERRA: los candados de CSP, estilo inline y JavaScript miran `rec.Body`, o
// sea el HTML que salió por el cable. Si la captura `catalogo` cayera en su rama 403 —la pantalla
// VACÍA—, los tres seguirían verdes midiendo una página sin formulario, sin `<input type=file>` y sin
// los dos `<textarea>`, que es justo donde se cuela un `style=` o un `<script>`. Hoy no pasa porque
// `rutasAdmin` mete `catalog_import` en el plan del doble, pero eso es un ACOPLAMIENTO IMPLÍCITO
// entre dos sitios del mismo fichero: quien quitara esa feature de ahí dejaría esta pantalla fuera
// del recorrido sin que nada fallara.
//
// 🔑 Y ADEMÁS EL PASO 2, que ninguna otra captura puede sustituir: es la única rama de esta consola
// donde un documento que subió el usuario vuelve a la página dentro de un `hidden`, pegado al
// formulario que ESCRIBE el catálogo entero.
func TestPaginas_LaFamiliaMiraLaRamaCOMPLETADeLaImportacion(t *testing.T) {
	t.Parallel()

	renders := paginasRenderizadas(t, NewRouter(offlineConfig()))

	completa, ok := renders["catalogo"]
	if !ok {
		t.Fatal("la familia no captura `catalogo`: la importación quedó fuera de los tests de CSP")
	}
	// Las cuatro anclas son de la rama que SOLO existe con `catalog_import`: la tarjeta del
	// formulario, el único campo de fichero de la consola, el selector de ref y el textarea del
	// prompt, que es texto largo escrito por el upstream. Ninguna se emite en la rama vacía.
	for _, ancla := range []string{
		`id="section-catalogo-subir"`, `id="file"`, `id="ref"`, `id="prompt"`,
	} {
		if !strings.Contains(completa.Body.String(), ancla) {
			t.Errorf("la captura `catalogo` no trae %s: la familia está escaneando la pantalla VACÍA "+
				"y un style= o un <script> en la rama completa pasaría inadvertido", ancla)
		}
	}

	paso2, ok := renders["catalogo_paso2"]
	if !ok {
		t.Fatal("la familia no captura `catalogo_paso2`: el diff y el formulario que escribe quedaron sin vigilar")
	}
	for _, ancla := range []string{
		`id="section-catalogo-diff"`, `id="section-catalogo-confirmar"`,
		`id="table-catalogo-precios"`, `name="document"`,
	} {
		if !strings.Contains(paso2.Body.String(), ancla) {
			t.Errorf("la captura `catalogo_paso2` no trae %s: no es el segundo paso", ancla)
		}
	}

	vacia, ok := renders["catalogo_sin_plan"]
	if !ok {
		t.Fatal("la familia no captura `catalogo_sin_plan`: la rama del 403 quedó sin vigilar")
	}
	if !strings.Contains(vacia.Body.String(), `id="section-catalogo-sin-plan"`) {
		t.Error("la captura `catalogo_sin_plan` no es la pantalla vacía: el gate dejó de cortar")
	}
	if strings.Contains(vacia.Body.String(), `id="section-catalogo-subir"`) {
		t.Error("la pantalla del 403 sirvió el formulario: las dos capturas son la misma rama y una " +
			"de las dos dejó de probar algo")
	}

	// Y la página del techo de cuerpo (T8.4), que se pinta ANTES del AuthMiddleware y por tanto SIN
	// barra: es el único HTML de esta consola que sale así, y por eso no lo cubre ninguna otra captura.
	grande, ok := renders["catalogo_cuerpo_grande"]
	if !ok {
		t.Fatal("la familia no captura `catalogo_cuerpo_grande`: la página del 413 quedó sin vigilar")
	}
	if !strings.Contains(grande.Body.String(), `id="section-cuerpo-grande"`) {
		t.Error("la captura `catalogo_cuerpo_grande` no es la página del 413")
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
