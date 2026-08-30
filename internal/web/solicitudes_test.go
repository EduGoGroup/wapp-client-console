package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
)

// solicitudes_test.go vigila la BANDEJA (Plan 047 · T7.2): el listado con sus filtros y su
// paginador, y el descarte por lotes con sus dos pasos.
//
// 🔴 CUATRO de sus asertos vienen del BFF y no los vigila nadie más en el ecosistema; su fichero de
// origen (wapp-guardian-bff/internal/web/intakes_test.go + intakes_discard_test.go) se borra en
// T7.7, así que si aquí no estuvieran desaparecerían con él sin que ningún gate se pusiera rojo:
//
//  1. que el orden pedido a la API sea SIEMPRE `oldest` (D-044.47 §2);
//  2. que los filtros SOBREVIVAN al cambio de página, porque viajan en la query;
//  3. que la casilla maestra seleccione la PÁGINA y colapse repetidos ANTES de medir el lote;
//  4. que el desglose de un lote mixto se pinte ENTERO, y que solo sea «éxito» si no se saltó nada.

// --- Cuerpos del doble de la API ---

const (
	testIntakeID    = "in-a1b2c3d4"
	testOtroIntake  = "in-e5f6a7b8"
	testTercerIntak = "in-c9d0e1f2"
	testContactID   = "ct-77aa11"
	testIntakeSesio = "33333333-3333-4333-8333-333333333333"
)

// solicitudJSON arma UNA fila del listado. `overdue` va explícito porque hay asertos sobre la marca
// y sobre su ausencia, y un helper que la omitiera dejaría el caso negativo sin material.
func solicitudJSON(id, estado string, total float64, overdue bool, creada string) string {
	return fmt.Sprintf(`{"id":%q,"contact_id":%q,"session_id":%q,"status":%q,"total":%g,`+
		`"customer_note":"","overdue":%t,"created_at":%q,"updated_at":%q}`,
		id, testContactID, testIntakeSesio, estado, total, overdue, creada, creada)
}

// solicitudesBody arma la respuesta de GET /api/v1/intakes. `total` es el de TODA la bandeja que
// cumple el filtro y no el de la página: es lo que alimenta el paginador, y confundirlos es
// exactamente el fallo que estos tests tienen que poder ver.
func solicitudesBody(pagina, porPagina, total int, filas ...string) string {
	return fmt.Sprintf(`{"intakes":[%s],"page":%d,"page_size":%d,"total":%d}`,
		strings.Join(filas, ","), pagina, porPagina, total)
}

// descarteBody arma la respuesta de POST /api/v1/intakes/discard.
func descarteBody(descartadas []string, saltadas map[string]string) string {
	ids := make([]string, 0, len(descartadas))
	for _, id := range descartadas {
		ids = append(ids, strconv.Quote(id))
	}
	// El orden de las saltadas se fija para que los asertos no dependan del recorrido de un mapa.
	filas := make([]string, 0, len(saltadas))
	for _, id := range []string{testIntakeID, testOtroIntake, testTercerIntak} {
		if razon, ok := saltadas[id]; ok {
			filas = append(filas, fmt.Sprintf(`{"intake_id":%q,"reason":%q}`, id, razon))
		}
	}
	return fmt.Sprintf(`{"discarded":[%s],"skipped":[%s]}`,
		strings.Join(ids, ","), strings.Join(filas, ","))
}

// laBandejaDeCampo reproduce lo que hay en UAT: siete solicitudes del mismo tenant, pedidas de cinco
// en cinco. Con ese tamaño la bandeja tiene DOS páginas, que es la condición para que el paginador
// se pueda probar sin fabricar nada.
func laBandejaDeCampo() string {
	return solicitudesBody(1, 5, 7,
		solicitudJSON(testIntakeID, "pending_approval", 12.5, true, "2026-08-20T10:00:00Z"),
		solicitudJSON(testOtroIntake, "open", 8, false, "2026-08-21T11:00:00Z"),
		solicitudJSON(testTercerIntak, "needs_info", 30.25, false, "2026-08-22T12:00:00Z"),
		solicitudJSON("in-44444444", "abandoned", 0, false, "2026-08-23T13:00:00Z"),
		solicitudJSON("in-55555555", "confirmed", 99.9, false, "2026-08-24T14:00:00Z"),
	)
}

// rutasSolicitudes son las respuestas de fábrica del doble: el plan trae `cart_basic` y la bandeja
// contesta. Los tests del gate cambian la primera; los del listado, la segunda.
func rutasSolicitudes() map[string]stubResponse {
	return map[string]stubResponse{
		rutaListadoDeEmpresas:      {http.StatusOK, unaEmpresa()},
		"GET /api/v1/entitlements": {http.StatusOK, entitlementsBody("pro", featureCartBasic, "llm_intake")},
		"GET /api/v1/intakes":      {http.StatusOK, laBandejaDeCampo()},
		"POST /api/v1/intakes/discard": {http.StatusOK, descarteBody(
			[]string{testIntakeID}, map[string]string{testOtroIntake: "live_event"})},
	}
}

// solicitudesRouter monta el router con las respuestas de fábrica, sustituyendo las que se le pasen.
func solicitudesRouter(t *testing.T, cambios map[string]stubResponse) (http.Handler, *stubAPI) {
	t.Helper()
	rutas := rutasSolicitudes()
	for ruta, resp := range cambios {
		rutas[ruta] = resp
	}
	api := newStubAPI(t, rutas)
	return adminRouter(api), api
}

// --- El listado ---

// TestSolicitudes_LaBandejaPaginaYDiceDondeEsta.
//
// Es el criterio 1 de la casilla, con los números de campo: siete solicitudes y `page_size=5` dan
// DOS páginas. El aserto no es solo que salga un enlace: es que el paginador diga el rango, el
// total y de cuántas páginas, porque un paginador que no dice dónde estás no sirve para volver.
func TestSolicitudes_LaBandejaPaginaYDiceDondeEsta(t *testing.T) {
	t.Parallel()
	router, api := solicitudesRouter(t, nil)

	out := getWithSession(t, router, rutaSolicitudes+"?page_size=5").Body.String()

	if !strings.Contains(out, "1–5 de 7 · página 1 de 2") {
		t.Errorf("el paginador no dice el rango, el total ni la página. Body: %s", bloque(t, out, `id="paginador"`, "</p>"))
	}
	if !strings.Contains(out, `href="/solicitudes?page=2&amp;page_size=5"`) {
		t.Errorf("no hay enlace a la página siguiente con el tamaño vigente")
	}
	if strings.Contains(out, ">Anterior<") {
		t.Error("la primera página ofrece «Anterior»")
	}

	// Y lo que se le pidió a la plataforma: el tamaño explícito y el orden más antiguo primero.
	pedido := api.Last(t, "GET /api/v1/intakes").Query
	if pedido.Get("page_size") != "5" || pedido.Get("page") != "1" {
		t.Errorf("la petición no lleva la paginación explícita: %v", pedido)
	}
	if pedido.Get("sort") != "oldest" {
		t.Errorf("sort = %q, want «oldest»: lo que lleva más esperando es lo que hay que atender (D-044.47 §2)",
			pedido.Get("sort"))
	}
}

// TestSolicitudes_LosFiltrosSOBREVIVENAlCambioDePagina es el criterio 2.
//
// 🔴 El aserto es sobre las URL que la pantalla EMITE, no sobre lo que el handler recuerda: los
// filtros viajan en la query y por eso siguen ahí en la página siguiente, en el «Limpiar» y en el
// `action` del descarte. Si algún día se guardaran en el servidor, dos pestañas verían la misma
// bandeja y este test seguiría en verde solo si se escribiera así.
func TestSolicitudes_LosFiltrosSOBREVIVENAlCambioDePagina(t *testing.T) {
	t.Parallel()
	router, api := solicitudesRouter(t, nil)

	consulta := "?from=2026-08-01&to=2026-08-31&status=pending_approval&session=" + testIntakeSesio + "&page_size=5"
	out := getWithSession(t, router, rutaSolicitudes+consulta).Body.String()

	siguiente := "/solicitudes?from=2026-08-01&amp;page=2&amp;page_size=5&amp;session=" +
		testIntakeSesio + "&amp;status=pending_approval&amp;to=2026-08-31"
	if !strings.Contains(out, `href="`+siguiente+`"`) {
		t.Errorf("la página siguiente pierde filtros. Body: %s", bloque(t, out, `id="paginador"`, "</p>"))
	}
	// El formulario del descarte apunta a la MISMA bandeja: descartar no puede devolver a otra.
	if !strings.Contains(out, `action="/solicitudes/descartar?from=2026-08-01&amp;page=1&amp;page_size=5&amp;session=`+
		testIntakeSesio+`&amp;status=pending_approval&amp;to=2026-08-31"`) {
		t.Error("el formulario de descarte no arrastra los filtros vigentes")
	}
	// Y los filtros vuelven al formulario, que es lo que dice con qué se está mirando.
	for _, valor := range []string{`value="2026-08-01"`, `value="2026-08-31"`,
		`value="` + testIntakeSesio + `"`, `<option value="pending_approval" selected>`} {
		if !strings.Contains(out, valor) {
			t.Errorf("el formulario de filtros no devuelve %s", valor)
		}
	}

	// Lo que viajó a la plataforma es lo mismo que se tecleó: los filtros no se reinterpretan aquí.
	pedido := api.Last(t, "GET /api/v1/intakes").Query
	for clave, want := range map[string]string{
		"from": "2026-08-01", "to": "2026-08-31", "status": "pending_approval", "session": testIntakeSesio,
	} {
		if got := pedido.Get(clave); got != want {
			t.Errorf("la petición llevó %s=%q, want %q", clave, got, want)
		}
	}
}

// TestSolicitudes_ElTamanoDePaginaSeSaturaAlTechoDeLaAPI.
//
// La plataforma rechaza con 400 cualquier `page_size` por encima de 200. Se satura aquí para no
// gastar un viaje en una petición que va a ser rechazada — y el aserto es sobre lo que SALE, porque
// es lo único que lo demuestra.
func TestSolicitudes_ElTamanoDePaginaSeSaturaAlTechoDeLaAPI(t *testing.T) {
	t.Parallel()
	router, api := solicitudesRouter(t, nil)

	getWithSession(t, router, rutaSolicitudes+"?page_size=5000")

	if got := api.Last(t, "GET /api/v1/intakes").Query.Get("page_size"); got != "200" {
		t.Errorf("page_size = %q, want «200» (el techo de la API)", got)
	}
}

// TestSolicitudes_UnaPaginaIlegibleCaeALaPrimera: `page` y `page_size` basura no rompen la pantalla
// ni viajan tal cual. Es el único saneo que hace esta consola sobre los filtros; el resto lo valida
// la API a propósito, para no mantener dos criterios que acabarían discrepando.
func TestSolicitudes_UnaPaginaIlegibleCaeALaPrimera(t *testing.T) {
	t.Parallel()
	router, api := solicitudesRouter(t, nil)

	rec := getWithSession(t, router, rutaSolicitudes+"?page=dos&page_size=-4")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	pedido := api.Last(t, "GET /api/v1/intakes").Query
	if pedido.Get("page") != "1" || pedido.Get("page_size") != "50" {
		t.Errorf("una paginación ilegible viajó como %v, want page=1 y page_size=50", pedido)
	}
}

// TestSolicitudes_LaMarcaOverdueVaPEGADAAlEstadoYNoEnSuLugar.
//
// 🔴 `overdue` NO es un estado: es un aviso sobre una solicitud que sigue VIVA. Pintarlo en lugar
// del estado le diría a la dueña que la solicitud ya no sirve, que es exactamente lo contrario. El
// par positivo/negativo va junto: sin él, un gate que se llevara la marca por delante saldría verde.
func TestSolicitudes_LaMarcaOverdueVaPEGADAAlEstadoYNoEnSuLugar(t *testing.T) {
	t.Parallel()
	router, _ := solicitudesRouter(t, nil)

	out := getWithSession(t, router, rutaSolicitudes).Body.String()

	if !strings.Contains(out, `<span class="wapp-chip wapp-chip--neutral">por aprobar</span><span class="wapp-chip wapp-chip--danger">sin responder hace más de 24 h</span>`) {
		t.Error("la marca de retraso no acompaña al estado, o lo sustituye")
	}
	// La que NO está retrasada no la lleva: la marca sale del dato y no del estado.
	if strings.Count(out, "sin responder hace más de") != 1 {
		t.Errorf("la marca de retraso salió %d veces; solo una solicitud la trae",
			strings.Count(out, "sin responder hace más de"))
	}
	// Y en ningún caso se pinta la palabra que confundiría los dos conceptos.
	if strings.Contains(out, ">vencido<") {
		t.Error("la marca de retraso se pintó como «vencido», que es un ESTADO terminal y distinto")
	}
}

// TestSolicitudes_StatusLabelTraduceYDevuelveCrudoLoQueNoConoce.
//
// El helper vive en el FuncMap, o sea en una CADENA de plantilla que no compila nadie: si la clave
// se renombrara, `vet` y el linter seguirían verdes y la pantalla fallaría al renderizar. Este test
// lo ejercita POR EL CABLE —sobre el HTML servido— y no llamando a la función, que es lo único que
// demuestra que la clave del FuncMap sigue siendo la que la plantilla invoca.
func TestSolicitudes_StatusLabelTraduceYDevuelveCrudoLoQueNoConoce(t *testing.T) {
	t.Parallel()
	router, _ := solicitudesRouter(t, map[string]stubResponse{
		"GET /api/v1/intakes": {http.StatusOK, solicitudesBody(1, 50, 2,
			solicitudJSON(testIntakeID, "deposit_requested", 5, false, "2026-08-20T10:00:00Z"),
			solicitudJSON(testOtroIntake, "deposit_refunded", 5, false, "2026-08-20T10:00:00Z"))},
	})

	out := getWithSession(t, router, rutaSolicitudes).Body.String()

	if !strings.Contains(out, ">seña solicitada<") {
		t.Error("el estado conocido no se tradujo al nombre de negocio")
	}
	// 🔴 La clave desconocida se pinta TAL CUAL: es preferible que la dueña vea `deposit_refunded` a
	// que la pantalla se invente una traducción o esconda un estado que existe.
	if !strings.Contains(out, ">deposit_refunded<") {
		t.Error("un estado que el diccionario no conoce no se pintó crudo")
	}
	if statusLabel("deposit_refunded") != "deposit_refunded" {
		t.Error("statusLabel inventó una traducción para una clave desconocida")
	}
	// El `closed` legado sigue traducido aunque la API lo normalice: una fila vieja que se colara no
	// debe verse como un estado desconocido.
	if statusLabel("closed") != "confirmado" {
		t.Errorf("statusLabel(closed) = %q, want «confirmado»", statusLabel("closed"))
	}
}

// TestSolicitudes_LaBandejaCaidaDEGRADAYNoTumbaLaPantalla.
//
// Es el criterio de esta casa (ShowFlows, ShowSessions) y una diferencia declarada contra el BFF,
// que respondía 502 con la página pintada. Lo que importa es que los filtros sigan sirviendo:
// alguien que llega con la plataforma caída necesita poder volver a intentarlo.
func TestSolicitudes_LaBandejaCaidaDEGRADAYNoTumbaLaPantalla(t *testing.T) {
	t.Parallel()
	router, _ := solicitudesRouter(t, map[string]stubResponse{
		"GET /api/v1/intakes": {http.StatusBadGateway, `{"error":"upstream"}`},
	})

	rec := getWithSession(t, router, rutaSolicitudes)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: la bandeja caída no tumba la pantalla", rec.Code)
	}
	out := rec.Body.String()
	if !strings.Contains(out, `id="solicitudes-degradado"`) {
		t.Error("no se avisa de que el listado no se pudo leer")
	}
	if !strings.Contains(out, flashError(flashUpstreamUnavailable)) {
		t.Error("el aviso no sale del catálogo de flash")
	}
	if !strings.Contains(out, `id="form-filtros"`) {
		t.Error("la pantalla perdió el formulario de filtros, que es lo único que se puede hacer sin catálogo")
	}
}

// TestSolicitudes_ElRechazoDeLosFiltrosNoSeExplicaComoUnErrorDeTecleo.
//
// El 400 del listado es su único rechazo accionable, y el genérico —«revisa lo que escribiste»— no
// dice cuál de los cuatro filtros está mal. El gemelo va al lado: si alguien «simplificara» el
// traductor, este test cae en vez de dejar un texto peor en pantalla.
func TestSolicitudes_ElRechazoDeLosFiltrosNoSeExplicaComoUnErrorDeTecleo(t *testing.T) {
	t.Parallel()
	router, _ := solicitudesRouter(t, map[string]stubResponse{
		"GET /api/v1/intakes": {http.StatusBadRequest, `{"error":"invalid_filter"}`},
	})

	out := getWithSession(t, router, rutaSolicitudes+"?from=ayer").Body.String()
	if !strings.Contains(out, flashError(flashSolicitudesFiltrosInvalidos)) {
		t.Error("el 400 del listado no da el aviso de los filtros")
	}
	if strings.Contains(out, flashError(flashInvalidInput)) {
		t.Error("el 400 cayó en el genérico: el aviso de los filtros dejó de comprobarse")
	}
}

// TestSolicitudes_LaBandejaVaciaLoDICE en vez de dejar un hueco. Es la diferencia entre «no hay
// nada» y «no cargó», que son dos cosas que la dueña necesita separar.
func TestSolicitudes_LaBandejaVaciaLoDICE(t *testing.T) {
	t.Parallel()
	router, _ := solicitudesRouter(t, map[string]stubResponse{
		"GET /api/v1/intakes": {http.StatusOK, solicitudesBody(1, 50, 0)},
	})

	out := getWithSession(t, router, rutaSolicitudes).Body.String()
	if !strings.Contains(out, `id="solicitudes-vacio"`) {
		t.Error("una bandeja vacía no lo dice")
	}
	if strings.Contains(out, `id="form-descarte"`) {
		t.Error("sin filas se ofrece igualmente el formulario de descarte")
	}
}

// --- El descarte ---

// TestDescarte_SinMarcarNadaSEREPINTACon400.
//
// D-047.16 exacto: la validación es LOCAL —la petición no llegó a salir—, así que no hubo mutación
// de la que el PRG proteja y el 400 conserva la bandeja delante. El aserto de que la API NO se llamó
// es la otra mitad: sin él, esto pasaría igual con un viaje gastado.
func TestDescarte_SinMarcarNadaSEREPINTACon400(t *testing.T) {
	t.Parallel()
	router, api := solicitudesRouter(t, nil)

	rec := postFormWithCSRF(router, rutaSolicitudesDescartar, url.Values{}, clientSessionCookie(t))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (D-047.16: validación local repinta)", rec.Code)
	}
	out := rec.Body.String()
	if !strings.Contains(out, "Marca al menos una solicitud") || !strings.Contains(out, "No se ha tocado nada") {
		t.Errorf("el aviso no dice qué falta ni que no se tocó nada. Body: %s", out)
	}
	if !strings.Contains(out, `id="section-listado"`) {
		t.Error("el repintado se quedó sin la bandeja: quien recibe el rechazo pierde su selección")
	}
	if api.Called("POST /api/v1/intakes/discard") {
		t.Error("se llamó a la API con cero solicitudes marcadas")
	}
}

// TestDescarte_UnLoteMayorQueElTopeSeRechazaANTESDeSalir.
//
// El tope (200) es un ESPEJO del de la plataforma, no la autoridad. Vive aquí para poder decirlo en
// español —un 400 crudo no lo está— y para no gastar un viaje que va a ser rechazado entero.
func TestDescarte_UnLoteMayorQueElTopeSeRechazaANTESDeSalir(t *testing.T) {
	t.Parallel()
	router, api := solicitudesRouter(t, nil)

	form := url.Values{}
	for i := 0; i < 201; i++ {
		form.Add("intake_id", fmt.Sprintf("in-%04d", i))
	}
	rec := postFormWithCSRF(router, rutaSolicitudesDescartar, form, clientSessionCookie(t))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	out := rec.Body.String()
	if !strings.Contains(out, "Marcaste 201 solicitudes") || !strings.Contains(out, "como mucho 200") {
		t.Errorf("el aviso no dice cuántas se marcaron ni cuántas caben. Body: %s",
			bloque(t, out, `id="aviso-solicitudes"`, "</div>"))
	}
	if api.Called("POST /api/v1/intakes/discard") {
		t.Error("un lote que no cabe llegó a salir a la red")
	}
}

// TestDescarte_ElPasoDeREVISARNoEscribeNada.
//
// 🔒 El descarte es irreversible y no hay papelera (D-041.22): el primer POST solo ENSEÑA qué se va
// a descartar, con sus estados y sus totales. El botón que escribe existe únicamente en esa segunda
// pantalla.
func TestDescarte_ElPasoDeREVISARNoEscribeNada(t *testing.T) {
	t.Parallel()
	router, api := solicitudesRouter(t, nil)

	rec := postFormWithCSRF(router, rutaSolicitudesDescartar, url.Values{
		"intake_id": {testIntakeID, testOtroIntake},
	}, clientSessionCookie(t))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if api.Called("POST /api/v1/intakes/discard") {
		t.Fatal("🔴 el paso de REVISAR llamó a la API: mirar no puede escribir")
	}
	out := rec.Body.String()
	if !strings.Contains(out, `id="section-descarte-confirmar"`) {
		t.Fatalf("no se pintó la pantalla de confirmación. Body: %s", out)
	}
	// Las dos filas se describen con la bandeja que se está mirando: estado en nombre de negocio y
	// total con sus dos decimales. Una confirmación con celdas vacías no informa de nada.
	if !strings.Contains(out, ">por aprobar<") || !strings.Contains(out, ">12.50<") {
		t.Error("la confirmación no describe lo seleccionado con lo que ya está en la bandeja")
	}
	if !strings.Contains(out, "Vas a descartar 2 solicitudes") {
		t.Error("la advertencia no dice cuántas se van a descartar")
	}
	// Las dos mitades de D-044.47 §3, que se contradecían por separado.
	if !strings.Contains(out, "no se borra nada") || !strings.Contains(out, "no se puede deshacer") {
		t.Error("la advertencia se calla una de sus dos mitades")
	}
	// La salida segura es la primera parada del tabulador (REQ-32f(a)).
	if !strings.Contains(out, `<a href="/solicitudes?page=1" class="btn btn--text" autofocus>Cancelar</a>`) {
		t.Error("«Cancelar» no es el foco inicial del formulario que descarta")
	}
}

// TestDescarte_UnaFilaQueYaNoEstaEnLaBandejaSEDICE.
//
// La confirmación se describe con la página que se está mirando y NO pidiendo cada solicitud a la
// API (son hasta 200). Un id que no esté en ella se conserva —es la selección de quien descarta—
// pero se marca, en vez de pintar celdas vacías que parecerían datos.
func TestDescarte_UnaFilaQueYaNoEstaEnLaBandejaSEDICE(t *testing.T) {
	t.Parallel()
	router, _ := solicitudesRouter(t, nil)

	out := postFormWithCSRF(router, rutaSolicitudesDescartar, url.Values{
		"intake_id": {"in-que-ya-no-esta"},
	}, clientSessionCookie(t)).Body.String()

	if !strings.Contains(out, "ya no aparece en la bandeja que estás mirando") {
		t.Errorf("no se avisa de la fila que no se pudo describir. Body: %s",
			bloque(t, out, `id="section-descarte-confirmar"`, "</div>"))
	}
}

// TestDescarte_LaMaestraSeleccionaLaPAGINAYColapsaRepetidosANTESDeMedir.
//
// Dos invariantes en una, porque viajan juntas: la maestra GANA sobre las casillas sueltas (los
// ocultos de la página son un superconjunto de lo que se pueda haber marcado a mano), y los
// repetidos se colapsan CONSERVANDO EL ORDEN y antes de medir el lote — así lo que se mide es lo
// que se manda, y no hay forma de que esta consola acepte un lote que la plataforma rechace por
// tamaño.
func TestDescarte_LaMaestraSeleccionaLaPAGINAYColapsaRepetidosANTESDeMedir(t *testing.T) {
	t.Parallel()
	router, api := solicitudesRouter(t, nil)

	rec := postFormWithCSRF(router, rutaSolicitudesDescartar, url.Values{
		"select_visible":    {"1"},
		"intake_id":         {testTercerIntak},
		"visible_intake_id": {testIntakeID, testOtroIntake, testIntakeID},
		"action":            {"discard"},
	}, clientSessionCookie(t))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	cuerpo := api.Last(t, "POST /api/v1/intakes/discard").Body
	want := `{"intake_ids":["` + testIntakeID + `","` + testOtroIntake + `"]}`
	if cuerpo != want {
		t.Errorf("el lote que viajó fue %s, want %s (maestra ⇒ los VISIBLES, sin repetidos y en orden)",
			cuerpo, want)
	}
}

// TestDescarte_ElDesgloseSeVeENTEROYUnLoteMixtoNoEsUnExito.
//
// 🔴 Un lote mixto es el caso NORMAL, no el excepcional. El verde de arriba es lo único que mucha
// gente lee: dárselo a un lote en el que una solicitud sigue en la bandeja es enseñarle a no leer la
// tabla. Y la razón se traduce a la voz de la dueña: `live_event` es «el cliente está a media
// compra», no «evento vivo».
func TestDescarte_ElDesgloseSeVeENTEROYUnLoteMixtoNoEsUnExito(t *testing.T) {
	t.Parallel()
	router, _ := solicitudesRouter(t, nil)

	rec := postFormWithCSRF(router, rutaSolicitudesDescartar, url.Values{
		"intake_id": {testIntakeID, testOtroIntake},
		"action":    {"discard"},
	}, clientSessionCookie(t))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	out := rec.Body.String()

	if !strings.Contains(out, `id="section-descarte-resultado"`) {
		t.Fatalf("no se pintó el desglose. Body: %s", out)
	}
	if !strings.Contains(out, "Se descartó 1 de 2 solicitudes. La otra sigue en tu bandeja") {
		t.Errorf("el encabezado no cuenta el lote mixto. Body: %s",
			bloque(t, out, `id="aviso-solicitudes"`, "</div>"))
	}
	if strings.Contains(out, "wapp-snackbar--success") {
		t.Error("🔴 un lote mixto se anunció como ÉXITO: una solicitud sigue en la bandeja")
	}
	if !strings.Contains(out, "El cliente sigue en plena conversación") {
		t.Error("la razón del salto no se tradujo a la voz de la dueña")
	}
	// Las dos listas enteras: qué cayó y qué no.
	if !strings.Contains(out, testIntakeID) || !strings.Contains(out, testOtroIntake) {
		t.Error("el desglose no nombra las dos solicitudes del lote")
	}
}

// TestDescarte_UnLoteLIMPIOSiEsUnExito. Es el par del anterior: sin él, el negativo de arriba
// saldría verde con una pantalla que nunca celebra nada.
func TestDescarte_UnLoteLIMPIOSiEsUnExito(t *testing.T) {
	t.Parallel()
	router, _ := solicitudesRouter(t, map[string]stubResponse{
		"POST /api/v1/intakes/discard": {http.StatusOK, descarteBody([]string{testIntakeID, testOtroIntake}, nil)},
	})

	out := postFormWithCSRF(router, rutaSolicitudesDescartar, url.Values{
		"intake_id": {testIntakeID, testOtroIntake},
		"action":    {"discard"},
	}, clientSessionCookie(t)).Body.String()

	if !strings.Contains(out, "wapp-snackbar--success") {
		t.Error("un lote sin saltadas no se anunció como éxito")
	}
	if !strings.Contains(out, "Descartadas 2 solicitudes") {
		t.Errorf("el aviso no dice cuántas cayeron. Body: %s", bloque(t, out, `id="aviso-solicitudes"`, "</div>"))
	}
}

// TestDescarte_ElFalloDeLaAPIVaPor303ConservandoLaBandeja.
//
// D-047.16: la petición SALIÓ y pudo mutar a medio lote, que es justo el caso del PRG. El destino
// conserva los filtros —descartar no puede devolver a otra bandeja— y el aviso es el incómodo: mirar
// antes de repetir, porque lo ya escrito queda escrito.
func TestDescarte_ElFalloDeLaAPIVaPor303ConservandoLaBandeja(t *testing.T) {
	t.Parallel()
	router, _ := solicitudesRouter(t, map[string]stubResponse{
		"POST /api/v1/intakes/discard": {http.StatusBadGateway, `{"error":"upstream"}`},
	})

	rec := postFormWithCSRF(router, rutaSolicitudesDescartar+"?status=open&page=2", url.Values{
		"intake_id": {testIntakeID},
		"action":    {"discard"},
	}, clientSessionCookie(t))

	destino := redirectTarget(t, rec)
	want := "/solicitudes?page=2&status=open&error=" + flashDescarteIncierto
	if destino != want {
		t.Errorf("Location = %q, want %q", destino, want)
	}
	if !strings.Contains(flashError(flashDescarteIncierto), "ANTES de repetirlo") {
		t.Error("el aviso del descarte incierto dejó de mandar a mirar antes de repetir")
	}
	if flashCodeForDescarte(nil) != "" {
		t.Error("el traductor del descarte inventa un desenlace para el error nulo")
	}
}

// TestDescarte_El400DeLaAPINoSeExplicaComoUnFalloIncierto: los dos desenlaces malos del descarte
// dicen cosas OPUESTAS —«no se tocó ninguna» frente a «no se sabe»— y confundirlos sobre una
// operación irreversible es el peor consejo posible.
func TestDescarte_El400DeLaAPINoSeExplicaComoUnFalloIncierto(t *testing.T) {
	t.Parallel()
	router, _ := solicitudesRouter(t, map[string]stubResponse{
		"POST /api/v1/intakes/discard": {http.StatusBadRequest, `{"error":"invalid_body"}`},
	})

	rec := postFormWithCSRF(router, rutaSolicitudesDescartar, url.Values{
		"intake_id": {testIntakeID},
		"action":    {"discard"},
	}, clientSessionCookie(t))

	if destino := redirectTarget(t, rec); !strings.HasSuffix(destino, "error="+flashDescarteRechazado) {
		t.Errorf("Location = %q, want el aviso de descarte rechazado", destino)
	}
	if !strings.Contains(flashError(flashDescarteRechazado), "NO se tocó ninguna") {
		t.Error("el aviso del 400 no afirma que no se descartó ninguna")
	}
}

// TestSolicitudes_ElRechazoPorPLANNoSeExplicaComoUnRECHAZOPorPERMISO.
//
// 🔴 EL ORDEN DE LAS RAMAS ES CONTRATO, y este test es lo único que lo sostiene:
// `*apiclient.FeatureNotEnabledError` DESENVUELVE a ErrForbidden, así que preguntar antes por el
// genérico se comería el único desenlace que manda a la CONTRATACIÓN en vez de a pedirle un permiso
// a alguien — y todo seguiría verde. El gemelo —lo que el genérico habría dicho— va al lado: si
// alguien «simplificara» los dos traductores, este test cae en vez de dejar el texto equivocado en
// pantalla.
//
// Llega en campo cuando el gate por ruta dijo que sí y la plataforma dijo que no: o sea cuando el
// plan cambió entre las dos.
func TestSolicitudes_ElRechazoPorPLANNoSeExplicaComoUnRECHAZOPorPERMISO(t *testing.T) {
	t.Parallel()

	sinPlan := fmt.Errorf("intakes.list: %w", &apiclient.FeatureNotEnabledError{Feature: featureCartBasic})

	for nombre, traducir := range map[string]func(error) string{
		"listado":  flashCodeForSolicitudes,
		"descarte": flashCodeForDescarte,
	} {
		if got := traducir(sinPlan); got != flashSolicitudesSinPlan {
			t.Errorf("el traductor del %s dio %q, want %q", nombre, got, flashSolicitudesSinPlan)
		}
	}
	if flashCodeFor(sinPlan) != flashForbidden {
		t.Error("el traductor genérico ya distingue este 403: estos dos tests dejaron de probar nada")
	}
	if !strings.Contains(flashError(flashSolicitudesSinPlan), "contratación") {
		t.Error("el aviso del plan no dice adónde ir; el genérico manda a pedir permisos")
	}

	// Y el resto delega: dos traductores que copiaran la tabla serían dos tablas esperando a
	// desincronizarse.
	for _, caso := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("x: %w", apiclient.ErrForbidden), flashForbidden},
		{fmt.Errorf("x: %w", apiclient.ErrNotFound), flashNotInYourTenant},
		{nil, ""},
	} {
		if got := flashCodeForSolicitudes(caso.err); got != caso.want {
			t.Errorf("flashCodeForSolicitudes(%v) = %q, want %q", caso.err, got, caso.want)
		}
	}
}
