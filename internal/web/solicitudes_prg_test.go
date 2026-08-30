package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
)

// solicitudes_prg_test.go vigila LAS CUATRO ACCIONES QUE NO LE HABLAN AL CLIENTE (Plan 047 · T7.4):
// mover el estado, guardar las líneas facturables, corregir la interpretación y regenerarla.
//
// 🔒 LO QUE ESTE FICHERO AFIRMA, Y LO AFIRMA EN LOS DOS SENTIDOS (D-047.16):
//
//	el desenlace malo de la API ...... llega como CÓDIGO DE FLASH en un 303, y NO como un 4xx;
//	el rechazo de validación LOCAL ... repinta con 400 y con lo tecleado DENTRO.
//
// Un test que solo comprobara la primera mitad pasaría con una consola que respondiera 303 a todo y
// le borrara a la dueña la tabla que acaba de rellenar; uno que solo comprobara la segunda pasaría
// con una que devolviera 4xx ante cualquier cosa. Hacen falta las dos.
//
// 🔑 Y hay un tercer eje que no es el código de respuesta: `/lineas` y `/corregir` son DOS
// formularios sobre DOS listas distintas de líneas, y lo tecleado en uno tiene que volver al suyo.
// Cruzarlos pondría los precios en filas ajenas sin que nada fallara.

// --- El arnés: el doble con las cuatro puertas que escriben ---

// intakeMovido es lo que devuelve POST /api/v1/intakes/{id}/status: la solicitud, ya movida.
const intakeMovido = `{"id":"` + testIntakeID + `","contact_id":"` + testContactID + `",
	"session_id":"` + testIntakeSesio + `","status":"confirmed","total":21000,
	"customer_note":"","overdue":false,
	"created_at":"2026-08-20T10:00:00Z","updated_at":"2026-08-22T09:00:00Z"}`

// regeneracionEncargada es el 200 de POST …/reanalyze. `status` vale «processing» SIEMPRE y
// `revision_no` anuncia una revisión que todavía no existe: es justo lo que la pantalla no puede
// pintar como «listo».
const regeneracionEncargada = `{"intake_id":"` + testIntakeID + `","revision_no":4,
	"job_id":"job-9","via":"local","status":"processing"}`

// accionesRouter monta el router con las CUATRO puertas de escritura contestando que sí, y con el
// detalle servido para que el repintado tenga qué releer.
func accionesRouter(t *testing.T, cambios map[string]stubResponse) (http.Handler, *stubAPI) {
	t.Helper()
	rutas := rutasSolicitudes()
	rutas["GET /api/v1/intakes/{id}"] = stubResponse{http.StatusOK, solicitudDeCampo()}
	rutas["POST /api/v1/intakes/{id}/status"] = stubResponse{http.StatusOK, intakeMovido}
	rutas["PUT /api/v1/intakes/{id}/items"] = stubResponse{http.StatusOK, solicitudDeCampo()}
	rutas["POST /api/v1/intakes/{id}/reanalyze"] = stubResponse{http.StatusOK, regeneracionEncargada}
	for ruta, resp := range cambios {
		rutas[ruta] = resp
	}
	api := newStubAPI(t, rutas)
	return adminRouter(api), api
}

// Las cuatro rutas de esta casilla, ya compuestas sobre la solicitud de campo.
var (
	rutaEstado    = rutaDetalle + sufijoEstado
	rutaLineas    = rutaDetalle + sufijoLineas
	rutaCorregir  = rutaDetalle + sufijoCorregir
	rutaRegenerar = rutaDetalle + sufijoRegenerar
)

// Las puertas de la API tal como las NOMBRA lo capturado: con el identificador RESUELTO, no con el
// patrón `{id}` con el que se registran en el mux del doble. Son dos cosas distintas y confundirlas
// da un «el upstream nunca recibió …» sobre una llamada que sí ocurrió.
var (
	puertaEstado    = "POST /api/v1/intakes/" + testIntakeID + "/status"
	puertaItems     = "PUT /api/v1/intakes/" + testIntakeID + "/items"
	puertaRegenerar = "POST /api/v1/intakes/" + testIntakeID + "/reanalyze"
)

// tresFilas arma el cuerpo de un formulario de líneas con las tres filas que pinta la pantalla. Los
// cinco campos van como ARRAYS PARALELOS, que es como los manda un formulario HTML y como los lee el
// handler: si uno se desemparejara, el handler no podría saber qué precio va con qué artículo.
func tresFilas(skus, etiquetas, cantidades, precios [3]string) url.Values {
	return url.Values{
		"item_sku":           {skus[0], skus[1], skus[2]},
		"item_label":         {etiquetas[0], etiquetas[1], etiquetas[2]},
		"item_customization": {"", "", ""},
		"item_qty":           {cantidades[0], cantidades[1], cantidades[2]},
		"item_price":         {precios[0], precios[1], precios[2]},
	}
}

// filasBuenas es un envío que la validación local acepta entero.
func filasBuenas() url.Values {
	return tresFilas(
		[3]string{"TRT-CHO", "TEQ-30", ""},
		[3]string{"Torta de chocolate", "Tequeños", ""},
		[3]string{"1", "30", ""},
		[3]string{"21000.00", "0,50", ""})
}

// --- Criterio 2, PRIMERA mitad: el desenlace malo de la API llega como FLASH y NO como 4xx ---

// TestAcciones_ElDesenlaceMaloDeLaAPIVaPor303ConCodigoDeFlashYNuncaComo4xx.
//
// 🔒 Es la mitad del criterio que el origen NO cumplía: el BFF contestaba 422, 409 y 502 con la
// página pintada, así que un F5 sobre un rechazo reenviaba la escritura. Aquí las cuatro acciones
// mandan el desenlace de la API a la URL como `?error=<código>` y responden 303.
//
// El aserto de que NO es 4xx no es redundante con el 303: son dos formas distintas de romperlo.
// Contestar 422 con la página es lo que hacía el origen; contestar 303 con el código genérico es lo
// que pasaría si alguien borrara el traductor de plano, y el usuario leería «revisa las fechas
// (AAAA-MM-DD)» tras un cambio de estado.
func TestAcciones_ElDesenlaceMaloDeLaAPIVaPor303ConCodigoDeFlashYNuncaComo4xx(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre string
		ruta   string
		puerta string
		fallo  stubResponse
		form   url.Values
		code   string
	}{
		{
			nombre: "estado · 422 transición inválida",
			ruta:   rutaEstado,
			puerta: "POST /api/v1/intakes/{id}/status",
			fallo: stubResponse{http.StatusUnprocessableEntity, `{"error":"invalid_transition",
				"status":"pending_approval","requested":"settled","allowed":["confirmed","rejected"]}`},
			form: url.Values{"status": {"settled"}},
			code: flashSolicitudTransicionInvalida,
		},
		{
			nombre: "estado · 409 otra persona la movió",
			ruta:   rutaEstado,
			puerta: "POST /api/v1/intakes/{id}/status",
			fallo:  stubResponse{http.StatusConflict, `{"error":"intake_changed"}`},
			form:   url.Values{"status": {"confirmed"}},
			code:   flashSolicitudCambiadaPorOtro,
		},
		{
			nombre: "líneas · 422 no editable",
			ruta:   rutaLineas,
			puerta: "PUT /api/v1/intakes/{id}/items",
			fallo: stubResponse{http.StatusUnprocessableEntity,
				`{"error":"not_editable","status":"confirmed","editable_in":["pending_approval"]}`},
			form: filasBuenas(),
			code: flashSolicitudNoEditable,
		},
		{
			// 🔑 El LÍMITE de la extensión de D-047.16: un 400 SIN cuerpo nombrado sigue yendo por
			// 303. `invalid_items` repinta porque su contrato garantiza que no escribió nada y trae
			// con qué corregir; un 400 que no dice qué es no garantiza ni lo uno ni lo otro, y ante
			// la duda manda el PRG. Su gemelo —el que sí repinta— está más abajo, en su propio test.
			nombre: "líneas · 400 sin cuerpo nombrado, que NO es invalid_items",
			ruta:   rutaLineas,
			puerta: "PUT /api/v1/intakes/{id}/items",
			fallo:  stubResponse{http.StatusBadRequest, `{"error":"algo_que_esta_consola_no_conoce"}`},
			form:   filasBuenas(),
			code:   flashInvalidInput,
		},
		{
			nombre: "corregir · 502 de la plataforma",
			ruta:   rutaCorregir,
			puerta: "PUT /api/v1/intakes/{id}/items",
			fallo:  stubResponse{http.StatusBadGateway, `{"error":"upstream"}`},
			form:   filasBuenas(),
			code:   flashUpstreamUnavailable,
		},
		{
			nombre: "regenerar · 422 ya hay una en curso",
			ruta:   rutaRegenerar,
			puerta: "POST /api/v1/intakes/{id}/reanalyze",
			fallo:  stubResponse{http.StatusUnprocessableEntity, `{"error":"reanalysis_in_progress","job_id":"job-1"}`},
			form:   url.Values{"reanalyze_text": {"me llamó y añadió dos gaseosas"}},
			code:   flashRegeneracionEnCurso,
		},
		{
			nombre: "regenerar · 422 sin credencial, que NO es un paywall",
			ruta:   rutaRegenerar,
			puerta: "POST /api/v1/intakes/{id}/reanalyze",
			fallo:  stubResponse{http.StatusUnprocessableEntity, `{"error":"llm_credentials_missing","via":"api"}`},
			form:   url.Values{},
			code:   flashRegeneracionSinCredencial,
		},
		{
			nombre: "regenerar · 403 del add-on de la vía externa",
			ruta:   rutaRegenerar,
			puerta: "POST /api/v1/intakes/{id}/reanalyze",
			fallo:  stubResponse{http.StatusForbidden, `{"error":"feature_not_enabled","feature":"api_llm"}`},
			form:   url.Values{},
			code:   flashRegeneracionSinAddon,
		},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			router, _ := accionesRouter(t, map[string]stubResponse{caso.puerta: caso.fallo})

			rec := postFormWithCSRF(router, caso.ruta, caso.form, clientSessionCookie(t))

			// 🔴 El aserto que el origen no pasaba: NADA de 4xx. redirectTarget ya exige el 303, y
			// esta comprobación previa deja el fallo legible cuando alguien repinte un rechazo.
			if rec.Code >= 400 {
				t.Fatalf("status = %d: el desenlace de la API salió como código HTTP en vez de como "+
					"flash. Body: %s", rec.Code, rec.Body.String())
			}
			destino := redirectTarget(t, rec)
			if !strings.Contains(destino, "?error="+caso.code) {
				t.Errorf("redirigió a %q, y se esperaba el código %q", destino, caso.code)
			}
			// Y el 303 va al DETALLE, no a la bandeja: la solicitud sigue siendo la que se estaba
			// mirando y mandar a la lista obligaría a buscarla otra vez.
			if !strings.HasPrefix(destino, rutaDetalle) {
				t.Errorf("redirigió a %q, fuera del detalle de la solicitud", destino)
			}
			// El texto existe: un código sin entrada en el catálogo cae al genérico EN SILENCIO.
			if !flashErrors.Known(caso.code) {
				t.Errorf("el código %q no tiene texto en el catálogo", caso.code)
			}
		})
	}
}

// TestAcciones_LosCuatroExitosVanPor303ConSuPropioCodigo.
//
// Cuatro códigos y no uno: mover el estado, guardar lo facturable, corregir la interpretación y
// encargar una regeneración significan cosas distintas. El que más lo necesita es el último — se
// comprueba aparte, abajo.
func TestAcciones_LosCuatroExitosVanPor303ConSuPropioCodigo(t *testing.T) {
	t.Parallel()

	casos := []struct {
		ruta string
		form url.Values
		code string
	}{
		{rutaEstado, url.Values{"status": {"confirmed"}}, flashSolicitudEstadoCambiado},
		{rutaLineas, filasBuenas(), flashSolicitudLineasGuardadas},
		{rutaCorregir, filasBuenas(), flashSolicitudCorreccionGuardada},
		{rutaRegenerar, url.Values{"reanalyze_text": {""}}, flashRegeneracionEncargada},
	}

	vistos := make(map[string]bool, len(casos))
	for _, caso := range casos {
		router, _ := accionesRouter(t, nil)
		destino := redirectTarget(t, postFormWithCSRF(router, caso.ruta, caso.form, clientSessionCookie(t)))
		if !strings.Contains(destino, "?success="+caso.code) {
			t.Errorf("%s redirigió a %q, y se esperaba el éxito %q", caso.ruta, destino, caso.code)
		}
		if !flashSuccesses.Known(caso.code) {
			t.Errorf("el código de éxito %q no tiene texto en el catálogo", caso.code)
		}
		if vistos[caso.code] {
			t.Errorf("%s reutiliza el código de éxito %q de otra acción: son desenlaces distintos",
				caso.ruta, caso.code)
		}
		vistos[caso.code] = true
	}
}

// TestRegenerar_ElExitoNoPrometeQueLaInterpretacionEsteLista.
//
// 🔴 La plataforma responde «processing» SIEMPRE y anuncia una revisión que todavía no existe. Si
// esta pantalla dijera «listo», la dueña recargaría, vería la interpretación anterior y creería que
// falló. Este test vigila el texto, no el código: es lo único que sostiene esa honestidad.
func TestRegenerar_ElExitoNoPrometeQueLaInterpretacionEsteLista(t *testing.T) {
	t.Parallel()

	texto := flashSuccess(flashRegeneracionEncargada)
	if !strings.Contains(texto, "TODAVÍA NO ESTÁ LISTA") {
		t.Errorf("el éxito de regenerar no avisa de que no está lista: %q", texto)
	}
	for _, prohibido := range []string{"ya está", "Listo", "completada"} {
		if strings.Contains(texto, prohibido) {
			t.Errorf("el éxito de regenerar dice %q: %q", prohibido, texto)
		}
	}
}

// --- Criterio 2, SEGUNDA mitad: la validación local repinta con 400 y lo tecleado dentro ---

// TestLineas_ElPrecioIlegibleRepintaCon400YLoTecleadoVuelveEntero.
//
// 🔒 Las DOS cosas juntas son la decisión, y ninguna vale sola: con 200 el navegador no distinguiría
// el rechazo, y con el formulario repuesto desde la plataforma el 400 se cumpliría «de palabra»
// mientras la dueña pierde lo que acababa de escribir.
//
// 🔴 Y la tercera: la API NO se llama. Es lo que hace que el 400 no necesite el PRG — no hubo
// mutación de la que proteger.
func TestLineas_ElPrecioIlegibleRepintaCon400YLoTecleadoVuelveEntero(t *testing.T) {
	t.Parallel()
	router, api := accionesRouter(t, nil)

	// «1.234» es el caso que se rechaza aunque parezca inofensivo: no hay forma de saber si son mil
	// doscientos treinta y cuatro o uno con doscientos treinta y cuatro milésimas.
	form := tresFilas(
		[3]string{"TRT-CHO", "EXTRA-QUESO", ""},
		[3]string{"Torta de chocolate", "Queso extra tecleado", ""},
		[3]string{"1", "2", ""},
		[3]string{"21000.00", "1.234", ""})

	rec := postFormWithCSRF(router, rutaLineas, form, clientSessionCookie(t))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (validación local repinta). Body: %s", rec.Code, rec.Body.String())
	}
	if api.Called(puertaItems) {
		t.Errorf("se llamó a la API con un rechazo local: %v", api.Requests())
	}

	out := rec.Body.String()
	// Lo TECLEADO vuelve: el artículo que no estaba en el catálogo y el precio tal como se escribió.
	if !strings.Contains(out, `value="Queso extra tecleado"`) || !strings.Contains(out, `value="1.234"`) {
		t.Errorf("el repintado perdió lo tecleado. Tabla: %s",
			bloque(t, out, `id="table-solicitud-lineas"`, "</table>"))
	}
	// El DEFECTO señala la fila que se ve (1-based) y dice cómo escribirlo.
	if !strings.Contains(out, "Línea 2 · precio:") || !strings.Contains(out, "sin separador de miles") {
		t.Errorf("el rechazo no señala la fila ni dice cómo corregirla. Defectos: %s",
			bloque(t, out, `id="solicitud-lineas-defectos"`, "</ul>"))
	}
	// Y el encabezado sale del CATÁLOGO, no de una cadena escrita en el handler.
	if !strings.Contains(out, flashError(flashSolicitudLineasIlegibles)) {
		t.Errorf("el aviso del rechazo no es el del catálogo. Body: %s", out)
	}
}

// TestCorregir_LoTecleadoVUELVEALBORRADORYNoAlFormularioDeLineas.
//
// 🔒 EL MATIZ CARO DE ESTA PANTALLA. Son dos formularios sobre dos representaciones distintas de las
// líneas: el de abajo edita `items` —lo que la plataforma factura— y éste edita la interpretación,
// que es la única que tiene la línea `unmatched`, o sea justo la que hay que poner a precio.
//
// Volcar lo tecleado en uno dentro del otro pondría los precios en filas ajenas Y NADA FALLARÍA: la
// página se pintaría entera, con los números cambiados de sitio. Por eso el aserto es doble —está en
// el suyo Y no está en el otro—: comprobar solo lo primero pasa con las dos vistas rellenadas.
func TestCorregir_LoTecleadoVUELVEALBORRADORYNoAlFormularioDeLineas(t *testing.T) {
	t.Parallel()
	router, _ := accionesRouter(t, nil)

	form := tresFilas(
		[3]string{"TRT-CHO", "TEQ-30", "GAS-1L"},
		[3]string{"Torta", "Tequeños del borrador", "Gaseosa"},
		[3]string{"1", "30", "2"},
		[3]string{"21000.00", "ocho", "1500.00"})

	rec := postFormWithCSRF(router, rutaCorregir, form, clientSessionCookie(t))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400. Body: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()

	borrador := bloque(t, out, `id="table-solicitud-borrador"`, "</table>")
	lineas := bloque(t, out, `id="table-solicitud-lineas"`, "</table>")

	if !strings.Contains(borrador, "Tequeños del borrador") {
		t.Errorf("lo tecleado en «Corregir» no volvió a su formulario. Borrador: %s", borrador)
	}
	if strings.Contains(lineas, "Tequeños del borrador") {
		t.Errorf("lo tecleado en «Corregir» se volcó en el formulario de líneas FACTURABLES: los "+
			"precios acabarían en filas ajenas. Líneas: %s", lineas)
	}
	// El formulario de líneas facturables sigue enseñando lo que dice la plataforma.
	if !strings.Contains(lineas, `value="Torta de chocolate"`) {
		t.Errorf("el formulario de líneas perdió lo que dice la plataforma. Líneas: %s", lineas)
	}
	// Y los defectos, igual: en la lista del borrador y no en la de líneas.
	if !strings.Contains(bloque(t, out, `id="solicitud-borrador-defectos"`, "</ul>"), "Línea 2 · precio:") {
		t.Errorf("el defecto no se pintó junto al formulario del borrador. Body: %s", out)
	}
	if strings.Contains(out, `id="solicitud-lineas-defectos"`) {
		t.Error("el rechazo del borrador marcó también el formulario de líneas facturables")
	}
}

// TestLineas_UnFormularioDesemparejadoNoAdivinaQuePrecioVaConQueArticulo.
//
// Los cinco campos por fila viajan como arrays paralelos. Si no vienen emparejados —envío truncado o
// manipulado—, no hay forma de saber qué precio va con qué artículo, y adivinar significa guardar
// una mezcla. Se rechaza SIN repintar lo tecleado, porque «lo tecleado» es justamente lo que no se
// sabe reconstruir.
func TestLineas_UnFormularioDesemparejadoNoAdivinaQuePrecioVaConQueArticulo(t *testing.T) {
	t.Parallel()
	router, api := accionesRouter(t, nil)

	rec := postFormWithCSRF(router, rutaLineas, url.Values{
		"item_sku":           {"A", "B"},
		"item_label":         {"Uno", "Dos"},
		"item_customization": {"", ""},
		"item_qty":           {"1", "2"},
		"item_price":         {"1.00"}, // falta uno
	}, clientSessionCookie(t))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400. Body: %s", rec.Code, rec.Body.String())
	}
	if api.Called(puertaItems) {
		t.Error("un formulario desemparejado llegó a la API")
	}
	if !strings.Contains(rec.Body.String(), flashError(flashSolicitudFormularioIncompleto)) {
		t.Error("el rechazo no dice que el formulario llegó incompleto")
	}
}

// TestLineas_UnQuitarQueNoSenalaNingunaFilaSeRechazaConservandoLoTecleado.
func TestLineas_UnQuitarQueNoSenalaNingunaFilaSeRechazaConservandoLoTecleado(t *testing.T) {
	t.Parallel()
	router, api := accionesRouter(t, nil)

	form := filasBuenas()
	form.Set("remove", "9")

	rec := postFormWithCSRF(router, rutaLineas, form, clientSessionCookie(t))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400. Body: %s", rec.Code, rec.Body.String())
	}
	if api.Called(puertaItems) {
		t.Error("un «Quitar» ilocalizable llegó a la API")
	}
	if !strings.Contains(rec.Body.String(), `value="Tequeños"`) {
		t.Error("el repintado del «Quitar» ilocalizable perdió lo tecleado")
	}
}

// TestEstado_SinEstadoVaPor303PorqueNoHayNADAQuePerder.
//
// 🔒 ES VALIDACIÓN LOCAL Y AUN ASÍ NO REPINTA, y merece un test propio porque parece una excepción a
// D-047.16 y no lo es: la excepción del 400 existe para conservar lo TECLEADO, y aquí el control es
// un `<select>` sobre una lista cerrada que arma el servidor. Sin nada escrito no hay nada que
// perder — es la misma regla con la que el borrado de un disparador va por 303.
//
// El gemelo va incluido: si alguien «unificara» esto con el repintado de las líneas, el aserto de
// que NO es 400 lo caza.
func TestEstado_SinEstadoVaPor303PorqueNoHayNADAQuePerder(t *testing.T) {
	t.Parallel()
	router, api := accionesRouter(t, nil)

	rec := postFormWithCSRF(router, rutaEstado, url.Values{}, clientSessionCookie(t))

	if rec.Code == http.StatusBadRequest {
		t.Fatalf("el envío sin estado repintó con 400: no hay formulario tecleado que conservar. Body: %s",
			rec.Body.String())
	}
	destino := redirectTarget(t, rec)
	if !strings.Contains(destino, "?error="+flashSolicitudSinEstado) {
		t.Errorf("redirigió a %q, y se esperaba %q", destino, flashSolicitudSinEstado)
	}
	if api.Called(puertaEstado) {
		t.Error("un envío sin estado llegó a la API")
	}
}

// TestRegenerar_LLEVAFormularioTecleadoYSuRechazoLocalRepintaCon400.
//
// 🔴 EL PLAN DE LA CASILLA DABA ESTA ACCIÓN POR «BOTÓN SIN CUERPO TECLEADO», y por tanto entera del
// lado del 303. Se comprobó en el origen y es FALSO: lleva un `<textarea name="reanalyze_text">`
// —material extra OPCIONAL, que SUMA al literal del cliente en vez de sustituirlo— con validación
// local de longitud (`intakeReanalyzeMaxRunes = 280`, intakes_reanalyze.go:57) que responde 400
// diciendo cuántas runas van.
//
// Opcional no es inexistente: quien pega la transcripción de una llamada y se pasa por veinte
// caracteres tiene mucho que perder. Por eso entra en la excepción del 400-repintando, y este test
// es la prueba de que se decidió con el dato y no con el enunciado.
//
// 🔴 Se cuenta en RUNAS y no en bytes, y el texto de prueba es de acentos a propósito: 200 «á» son
// 200 runas y 400 bytes. Contar bytes rechazaría un texto que cabe.
func TestRegenerar_LLEVAFormularioTecleadoYSuRechazoLocalRepintaCon400(t *testing.T) {
	t.Parallel()

	t.Run("dentro del tope pasa, aunque en bytes no quepa", func(t *testing.T) {
		t.Parallel()
		router, api := accionesRouter(t, nil)

		cabe := strings.Repeat("á", maxRunasRegeneracion)
		rec := postFormWithCSRF(router, rutaRegenerar,
			url.Values{"reanalyze_text": {cabe}}, clientSessionCookie(t))

		destino := redirectTarget(t, rec)
		if !strings.Contains(destino, "?success="+flashRegeneracionEncargada) {
			t.Fatalf("un texto de %d runas (y %d bytes) se rechazó: se está contando en bytes. Fue a %q",
				maxRunasRegeneracion, len(cabe), destino)
		}
		// Y lo que viajó es el material extra, sin ninguna `via`: cambiar de vía es configuración de
		// la empresa, no un botón de la bandeja (D-044.51).
		cuerpo := api.Last(t, puertaRegenerar).Body
		if strings.Contains(cuerpo, `"via"`) {
			t.Errorf("la regeneración mandó una vía: %s", cuerpo)
		}
	})

	t.Run("pasarse repinta con 400, con lo tecleado y con cuántas van", func(t *testing.T) {
		t.Parallel()
		router, api := accionesRouter(t, nil)

		largo := strings.Repeat("á", maxRunasRegeneracion+1)
		rec := postFormWithCSRF(router, rutaRegenerar,
			url.Values{"reanalyze_text": {largo}}, clientSessionCookie(t))

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: la regeneración SÍ tiene formulario tecleado y su rechazo "+
				"local entra en la excepción de D-047.16", rec.Code)
		}
		if api.Called(puertaRegenerar) {
			t.Error("se llamó a la API con un rechazo local")
		}
		out := rec.Body.String()
		if !strings.Contains(out, largo) {
			t.Error("el repintado perdió el material extra tecleado, que es justo lo que la excepción " +
				"existe para conservar")
		}
		// El NÚMERO no puede salir del catálogo de flash, así que viaja en la vista; y el tope no se
		// escribe a mano en la plantilla, sale de la constante.
		if !strings.Contains(out, "Ahí van 281 caracteres") || !strings.Contains(out, "tope son 280") {
			t.Errorf("el rechazo no dice cuántas runas van ni cuál es el tope. Aviso: %s",
				bloque(t, out, `id="solicitud-regenerar-largo"`, "</p>"))
		}
		if !strings.Contains(out, flashError(flashRegeneracionTextoLargo)) {
			t.Error("el aviso del rechazo no es el del catálogo")
		}
	})
}

// TestRegenerar_SinElPlanResponde403YCONSERVALoTecleado.
//
// 🔒 EL 403 Y EL REPINTADO SON DOS DECISIONES INDEPENDIENTES, y este test afirma las dos juntas
// porque separarlas es justo el error que hay que impedir:
//
//   - EL CÓDIGO ES 403 Y NO 400. D-047.16 acota el 400 a la VALIDACIÓN, y una denegación por plan no
//     es una validación: la petición está perfecta y lo que falta es lo contratado. Además es el
//     MISMO hecho que corta el gate de `cart_basic`, que responde 403 (solicitudes_gate.go) — si uno
//     dijera 403 y el otro 400, la consola diría dos cosas distintas sobre «tu plan no lo incluye».
//     Por eso el aserto de que NO es 400 va explícito: es la forma en que esto se rompería.
//   - Y AUN ASÍ REPINTA, con el textarea intacto. El 403 del gate sirve una pantalla vacía porque
//     allí no hay nada que conservar; aquí hay material extra tecleado, y perderlo es exactamente lo
//     que la excepción existe para impedir.
//
// El gate por ruta de esta consola solo cubre `cart_basic`: `llm_intake` vive DENTRO del servicio de
// la plataforma, así que la bandeja abre y esta acción no. Se corta aquí para no gastar el viaje,
// sobre la MISMA vista que sembró el gate —no se resuelve el plan una segunda vez—.
//
// 🔴 El botón sale deshabilitado en la pantalla, pero eso NO es una guarda: un POST hecho a mano no
// tiene `disabled`. Este test entra justo por ahí.
func TestRegenerar_SinElPlanResponde403YCONSERVALoTecleado(t *testing.T) {
	t.Parallel()
	router, api := accionesRouter(t, map[string]stubResponse{
		"GET /api/v1/entitlements": {http.StatusOK, entitlementsBody("basico", featureCartBasic)},
	})

	rec := postFormWithCSRF(router, rutaRegenerar,
		url.Values{"reanalyze_text": {"lo dijo por teléfono"}}, clientSessionCookie(t))

	if rec.Code == http.StatusBadRequest {
		t.Fatalf("la falta de plan respondió 400: eso dice «tu petición está mal formada», y es falso " +
			"— la petición está perfecta y lo que falta es el plan")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (lo mismo que responde el gate de cart_basic ante el mismo "+
			"hecho). Body: %s", rec.Code, rec.Body.String())
	}
	if api.Called(puertaRegenerar) {
		t.Error("se salió a la red sabiendo que el plan no incluye la capacidad")
	}
	out := rec.Body.String()
	if !strings.Contains(out, "lo dijo por teléfono") {
		t.Error("el repintado del paywall perdió el material extra tecleado")
	}
	if !strings.Contains(out, flashError(flashRegeneracionSinPlan)) {
		t.Error("el aviso no manda a la contratación")
	}
	// Y el plan se resolvió UNA sola vez: el gate lo siembra en el contexto y el handler lo reutiliza.
	var veces int
	for _, r := range api.Requests() {
		if r.Route() == "GET /api/v1/entitlements" {
			veces++
		}
	}
	if veces != 1 {
		t.Errorf("se preguntó por el plan %d veces en una petición, want 1", veces)
	}
}

// --- La MUTACIÓN declarada de la casilla ---

// TestCorregir_VIAJACONAsCorrectionYLaEdicionDeLineasNO.
//
// 🔒 ES LA MUTACIÓN DE ESTA CASILLA, y el aserto está escrito para sobrevivirla: hacer que
// `/corregir` llame al mismo método que `/lineas` —sin el flag— tiene que poner esto en rojo.
//
// 🔑 POR QUÉ EL ASERTO MIRA EL CUERPO Y NO LA RUTA. Las dos acciones emiten EL MISMO
// `PUT /api/v1/intakes/{id}/items`: no existe ninguna ruta `/correct` en la API, corregir es este
// mismo PUT con `as_correction` puesto. Un test que comprobara «llamó al endpoint», o incluso «llamó
// con estos ítems», PASARÍA con las dos confundidas — y lo que se perdería es que la plataforma
// registre la corrección de la dueña y deje la señal few-shot con la que lee mejor los pedidos
// parecidos. Nada fallaría, en ninguna capa.
//
// 🔴 Y el gemelo negativo es obligatorio, no un adorno: `as_correction` lleva `omitempty` a
// propósito —con `false` la clave NI SE EMITE, y el cuerpo del camino del Plan 041 sale byte a byte
// igual que antes del 044—. Sin el aserto de ausencia, una implementación que mandara el flag SIEMPRE
// pasaría la mitad de arriba.
func TestCorregir_VIAJACONAsCorrectionYLaEdicionDeLineasNO(t *testing.T) {
	t.Parallel()

	cuerpoDe := func(t *testing.T, ruta string) string {
		t.Helper()
		router, api := accionesRouter(t, nil)
		rec := postFormWithCSRF(router, ruta, filasBuenas(), clientSessionCookie(t))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("%s status = %d, want 303. Body: %s", ruta, rec.Code, rec.Body.String())
		}
		return api.Last(t, puertaItems).Body
	}

	correccion := cuerpoDe(t, rutaCorregir)
	if !strings.Contains(correccion, `"as_correction":true`) {
		t.Errorf("«Corregir» viajó SIN la bandera: la plataforma lo registraría como una edición "+
			"normal y no dejaría la señal de corrección. Cuerpo: %s", correccion)
	}

	edicion := cuerpoDe(t, rutaLineas)
	if strings.Contains(edicion, "as_correction") {
		t.Errorf("guardar las líneas facturables emitió la clave, y `omitempty` existe justamente "+
			"para que no salga: el cuerpo del 041 tiene que salir igual que antes del 044. "+
			"Cuerpo: %s", edicion)
	}

	// Las dos van al MISMO endpoint. Si esto dejara de ser cierto, el test de arriba habría que
	// reescribirlo entero — y ese aviso es la mitad del valor de tenerlo aquí.
	if !strings.Contains(correccion, `"items"`) || !strings.Contains(edicion, `"items"`) {
		t.Error("alguno de los dos cuerpos dejó de llevar `items`: ya no son el mismo PUT")
	}
}

// TestAcciones_LasCuatroRutasExigenTokenCSRF.
//
// Las cuatro escriben, y una escritura que se dispara desde otra pestaña es lo que el double-submit
// impide. El POST se hace a mano: postFormWithCSRF pondría el token.
func TestAcciones_LasCuatroRutasExigenTokenCSRF(t *testing.T) {
	t.Parallel()
	router, api := accionesRouter(t, nil)

	for _, ruta := range []string{rutaEstado, rutaLineas, rutaCorregir, rutaRegenerar} {
		req := httptest.NewRequest(http.MethodPost, ruta, strings.NewReader(url.Values{}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(clientSessionCookie(t))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("POST %s sin token CSRF respondió %d: la defensa no cubre esta ruta", ruta, rec.Code)
		}
	}
	if len(api.Requests()) > 0 {
		t.Errorf("una petición sin CSRF llegó a la API: %v", api.Requests())
	}
}

// --- El ORDEN de las ramas de los tres traductores nuevos ---

// TestFlashDeLasAcciones_ElOrdenDeLasRamasEsContrato.
//
// 🔴 Cada caso lleva su GEMELO: lo que habría dicho el traductor GENÉRICO. Sin el gemelo, un test
// que solo compruebe «da el código bueno» sigue verde el día que alguien mueva la rama detrás de la
// genérica en un traductor y no en el otro — porque el aserto positivo no sabe si acertó por la rama
// correcta o por casualidad.
//
// Los pares que dependen del orden, y por qué duelen:
//   - ErrIntakeChanged desenvuelve a ErrConflict, que en esta consola es «ya existe algo con ese
//     nombre». El consejo que hace falta es RECARGAR, y ése no lo da «ya existe».
//   - InvalidItems / NotEditable / InvalidTransition desenvuelven a ErrInvalidInput, cuyo texto
//     genérico —«revisa lo que escribiste»— se calla lo único que importa: que no se guardó nada.
//   - FeatureNotEnabled desenvuelve a ErrForbidden: al revés, mandaría a pedir permisos en vez de a
//     la contratación.
func TestFlashDeLasAcciones_ElOrdenDeLasRamasEsContrato(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre    string
		err       error
		traductor func(error) string
		want      string
		// generico es lo que habría dicho flashCodeFor: el test exige que sea DISTINTO, o esta rama
		// dejó de probar nada.
		generico string
	}{
		{
			nombre:    "409 de carrera, no «ya existe»",
			err:       fmt.Errorf("intakes.status: %w", apiclient.ErrIntakeChanged),
			traductor: flashCodeForEstado,
			want:      flashSolicitudCambiadaPorOtro,
			generico:  flashConflict,
		},
		{
			nombre:    "422 de transición, no «revisa lo que escribiste»",
			err:       &apiclient.InvalidTransitionError{Status: "settled", Requested: "open"},
			traductor: flashCodeForEstado,
			want:      flashSolicitudTransicionInvalida,
			generico:  flashInvalidInput,
		},
		{
			nombre:    "400 por líneas, no el genérico",
			err:       &apiclient.InvalidItemsError{},
			traductor: flashCodeForLineas,
			want:      flashSolicitudLineasRechazadas,
			generico:  flashInvalidInput,
		},
		{
			nombre:    "422 no editable, no el genérico",
			err:       &apiclient.NotEditableError{Status: "confirmed"},
			traductor: flashCodeForLineas,
			want:      flashSolicitudNoEditable,
			generico:  flashInvalidInput,
		},
		{
			nombre:    "403 de capacidad, no «no tienes permiso»",
			err:       &apiclient.FeatureNotEnabledError{Feature: featureLLMIntake},
			traductor: flashCodeForRegeneracion,
			want:      flashRegeneracionSinPlan,
			generico:  flashForbidden,
		},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			if got := caso.traductor(caso.err); got != caso.want {
				t.Errorf("el traductor de plano dio %q, want %q", got, caso.want)
			}
			if got := flashCodeFor(caso.err); got != caso.generico {
				t.Errorf("el traductor genérico ya distingue este caso (dio %q): esta rama dejó de "+
					"probar nada", got)
			}
		})
	}
}

// TestFlashDeRegenerar_LasDOSCapacidadesQueFaltanNoSeDicenIgual.
//
// 🔒 `llm_intake` se contrata; `api_llm` es un add-on que ADEMÁS tiene alternativa —cambiar la vía a
// la local desde los ajustes—. Un código único mandaría a comprar algo que la empresa quizá ya
// tiene, o a comprar lo que no resuelve nada.
//
// Es la única puerta de la bandeja donde el 403 puede traer las dos, porque es la única SIN el
// middleware de entitlements del cloud: ese 403 lo emite su propio handler.
func TestFlashDeRegenerar_LasDOSCapacidadesQueFaltanNoSeDicenIgual(t *testing.T) {
	t.Parallel()

	plan := flashCodeForRegeneracion(&apiclient.FeatureNotEnabledError{Feature: featureLLMIntake})
	addon := flashCodeForRegeneracion(&apiclient.FeatureNotEnabledError{Feature: featureAPILLM})
	if plan == addon {
		t.Fatalf("las dos capacidades dan el mismo código %q: llevan a sitios distintos", plan)
	}
	if !strings.Contains(flashError(addon), "vía") {
		t.Errorf("el aviso del add-on no menciona la alternativa de la vía: %q", flashError(addon))
	}
	// Y la credencial que falta NO es un paywall: no hay nada que contratar.
	credencial := flashError(flashCodeForRegeneracion(&apiclient.LLMCredentialsMissingError{Via: "api"}))
	if !strings.Contains(credencial, "nada que contratar") {
		t.Errorf("el 422 de credencial se lee como un paywall: %q", credencial)
	}
}

// --- El 400 `invalid_items`: el desenlace de la API que REPINTA (D-047.16 extendida) ---

// filasConLaPrimeraEnBlanco es el envío que hace visible el problema del anclaje: la fila 1 va
// VACÍA —la dueña borró lo que había— y las dos siguientes llevan datos.
//
// 🔑 ES LA FORMA DEL ENVÍO LO QUE PRUEBA ALGO. Las filas en blanco NO viajan en el cuerpo, así que
// lo que la plataforma recibe son DOS ítems y su índice 0-based no coincide con el número de fila:
// el ítem 0 es la fila 2 y el ítem 1 es la fila 3. Con las tres filas rellenas los dos números
// coincidirían por casualidad y el test no probaría nada.
func filasConLaPrimeraEnBlanco() url.Values {
	return tresFilas(
		[3]string{"", "TEQ-30", "GAS-1L"},
		[3]string{"", "Tequeños", "Gaseosa de litro"},
		[3]string{"", "30", "2"},
		[3]string{"", "0.50", "1500.00"})
}

// invalidItemsSobreElSegundoItem es el 400 de la plataforma señalando el ítem 1 del cuerpo que
// recibió, o sea la TERCERA fila del formulario.
const invalidItemsSobreElSegundoItem = `{"error":"invalid_items","errors":[
	{"index":1,"field":"unit_price","message":"el precio no puede ser negativo"}]}`

// TestLineas_ElInvalidItemsDelCloudREPINTAYAnclaCadaDefectoASuFila.
//
// 🔒 ES LA EXTENSIÓN DE D-047.16 (decidida por Jhoan el 2026-08-30), y el argumento es el de la
// propia regla llevado hasta el final: la frontera NO es «la validación es local», es «NO HUBO
// MUTACIÓN». La edición de líneas es todo-o-nada, así que un `invalid_items` rechaza el cuerpo
// entero ANTES de escribir; repintarlo no crea el problema que el PRG resuelve y sí evita perder la
// tabla recién rellenada Y la lista de defectos, que es con lo que se corrige.
//
// 🔴 EL ASERTO DEL ANCLAJE ES EL QUE VALE, y por eso no basta con buscar el texto del defecto en la
// página: lo que se pierde al portar esto mal NO es el defecto —ése aparece igual, con su mensaje
// correcto— sino la CORRESPONDENCIA línea↔defecto. Un test que solo buscara la cadena pasaría con
// todos los defectos colgados de la fila 1, y la dueña corregiría la línea que no era.
//
// El envío está construido para que los dos números NO coincidan: la fila 1 va en blanco y no viaja,
// así que el ítem 1 del cuerpo es la fila 3. La implementación ingenua —pintar `index + 1`— diría
// «Línea 2» y este test la mata.
func TestLineas_ElInvalidItemsDelCloudREPINTAYAnclaCadaDefectoASuFila(t *testing.T) {
	t.Parallel()
	router, _ := accionesRouter(t, map[string]stubResponse{
		"PUT /api/v1/intakes/{id}/items": {http.StatusBadRequest, invalidItemsSobreElSegundoItem},
	})

	rec := postFormWithCSRF(router, rutaLineas, filasConLaPrimeraEnBlanco(), clientSessionCookie(t))

	// 1. REPINTA, y con el código que devolvió el cloud.
	if rec.Code == http.StatusSeeOther {
		t.Fatalf("el invalid_items salió por 303: se lleva por delante lo tecleado Y la lista de "+
			"defectos, sobre un rechazo que no escribió nada. Location: %s",
			rec.Header().Get("Location"))
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400. Body: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()

	// 2. LO TECLEADO vuelve entero, incluida la fila que se dejó en blanco.
	tabla := bloque(t, out, `id="table-solicitud-lineas"`, "</table>")
	for _, tecleado := range []string{`value="TEQ-30"`, `value="Gaseosa de litro"`, `value="1500.00"`} {
		if !strings.Contains(tabla, tecleado) {
			t.Errorf("el repintado perdió %s. Tabla: %s", tecleado, tabla)
		}
	}

	// 3. EL ANCLAJE: el ítem 1 del cuerpo es la fila 3, no la 2.
	defectos := bloque(t, out, `id="solicitud-lineas-defectos"`, "</ul>")
	if !strings.Contains(defectos, "Línea 3 · precio:") {
		t.Errorf("el defecto no quedó anclado a la fila 3. Defectos: %s", defectos)
	}
	if strings.Contains(defectos, "Línea 2 ·") || strings.Contains(defectos, "Línea 1 ·") {
		t.Errorf("el defecto se ancló a la fila equivocada: la plataforma señala el ítem 1 del cuerpo, "+
			"y el cuerpo NO lleva las filas en blanco. Defectos: %s", defectos)
	}
	// El campo llega traducido al rótulo del formulario, no como la clave del contrato.
	if strings.Contains(defectos, "unit_price") {
		t.Errorf("el campo del defecto se pinta con la clave del contrato: %s", defectos)
	}
	// Y el mensaje del cloud llega entero: es lo único que dice qué le pasa a ESA línea.
	if !strings.Contains(defectos, "el precio no puede ser negativo") {
		t.Errorf("se perdió el motivo que manda la plataforma. Defectos: %s", defectos)
	}
	// El encabezado sigue saliendo del catálogo, no del upstream.
	if !strings.Contains(out, flashError(flashSolicitudLineasRechazadas)) {
		t.Error("el aviso del rechazo no es el del catálogo")
	}
}

// TestCorregir_ElInvalidItemsDelCloudVuelveALBORRADORYNoAlFormularioDeLineas.
//
// El gemelo del anterior sobre la otra puerta. Vale lo mismo que en el rechazo local: son dos
// formularios sobre dos listas distintas de líneas, y un defecto del borrador marcado en la tabla de
// las facturables señalaría una fila que no es la que hay que tocar.
func TestCorregir_ElInvalidItemsDelCloudVuelveALBORRADORYNoAlFormularioDeLineas(t *testing.T) {
	t.Parallel()
	router, _ := accionesRouter(t, map[string]stubResponse{
		"PUT /api/v1/intakes/{id}/items": {http.StatusBadRequest, invalidItemsSobreElSegundoItem},
	})

	rec := postFormWithCSRF(router, rutaCorregir, filasConLaPrimeraEnBlanco(), clientSessionCookie(t))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400. Body: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()

	// El defecto va a la lista del BORRADOR, anclado a la fila 3, y la de líneas ni se emite.
	borrador := bloque(t, out, `id="solicitud-borrador-defectos"`, "</ul>")
	if !strings.Contains(borrador, "Línea 3 · precio:") {
		t.Errorf("el defecto remoto no quedó anclado a la fila 3 del borrador. Defectos: %s", borrador)
	}
	if strings.Contains(out, `id="solicitud-lineas-defectos"`) {
		t.Error("el rechazo del borrador marcó también el formulario de líneas facturables")
	}
	// Y lo tecleado, igual: en el borrador y no en las facturables.
	if !strings.Contains(bloque(t, out, `id="table-solicitud-borrador"`, "</table>"), "Gaseosa de litro") {
		t.Error("lo tecleado en «Corregir» no volvió a su formulario")
	}
	if strings.Contains(bloque(t, out, `id="table-solicitud-lineas"`, "</table>"), "Gaseosa de litro") {
		t.Error("lo tecleado en «Corregir» se volcó en el formulario de líneas FACTURABLES")
	}
}
