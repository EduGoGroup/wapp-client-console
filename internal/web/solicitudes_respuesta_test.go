package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// solicitudes_respuesta_test.go vigila LAS DOS ACCIONES QUE SÍ LE HABLAN A UN CLIENTE REAL (Plan 047
// · T7.5): aprobar y responder, y pedir más información. Van en un fichero aparte de las otras cuatro
// por lo mismo que van en una casilla aparte: no por lo que cuestan, sino por lo que hacen — cada una
// manda un WhatsApp a una persona, y eso no se deshace.
//
// 🔒 LO QUE ESTE FICHERO AFIRMA:
//
//  1. que EL TEXTO QUE VIAJA AL CLOUD ES EL DEL FORMULARIO y no la propuesta que esta consola
//     redactó. Es el corazón de la casilla y el defecto que de verdad duele: aprobar lo que nadie
//     leyó. El aserto mira el CUERPO que sale hacia la API — mirar la ruta o el HTML pasa con las
//     dos confundidas;
//  2. que el reparto de desenlaces distingue lo que PUDO haber mandado un mensaje de lo que no
//     (D-047.16 ampliada). Un repintado sobre un envío ya hecho invita a un F5, y un F5 le deja la
//     misma cotización DOS VECES al cliente;
//  3. que ningún aviso promete la entrega, y que el desenlace que no se sabe NO manda a reintentar.
//
// 🔴 LO QUE ESTE FICHERO **NO** PUEDE AFIRMAR, y hay que decirlo con estas palabras: que el mensaje
// que le llega al teléfono al cliente sea el que la pantalla enseñaba. Aquí el otro lado del cable es
// un doble en memoria, así que lo que se demuestra es que el CUERPO que sale de esta consola lleva lo
// tecleado — no que el cloud lo mande tal cual, ni que WhatsApp lo entregue. Eso es campo (T7.8), con
// UAT y un teléfono delante.

// --- El arnés ---

// respuestaRouter monta el router con las DOS puertas que envían contestando que sí, y con el detalle
// servido para que el repintado tenga qué releer.
func respuestaRouter(t *testing.T, cambios map[string]stubResponse) (http.Handler, *stubAPI) {
	t.Helper()
	rutas := rutasSolicitudes()
	rutas["GET /api/v1/intakes/{id}"] = stubResponse{http.StatusOK, solicitudDeCampo()}
	rutas["POST /api/v1/intakes/{id}/approve"] = stubResponse{http.StatusOK, solicitudDeCampo()}
	rutas["POST /api/v1/intakes/{id}/request-info"] = stubResponse{http.StatusOK, solicitudDeCampo()}
	for ruta, resp := range cambios {
		rutas[ruta] = resp
	}
	api := newStubAPI(t, rutas)
	return adminRouter(api), api
}

// Las dos rutas de esta casilla y las dos puertas de la API, con el identificador RESUELTO.
var (
	rutaAprobar   = rutaDetalle + sufijoAprobar
	rutaPedirInfo = rutaDetalle + sufijoPedirInfo

	puertaAprobar   = "POST /api/v1/intakes/" + testIntakeID + "/approve"
	puertaPedirInfo = "POST /api/v1/intakes/" + testIntakeID + "/request-info"
)

// laPropuestaDeLaPantalla es un trozo de lo que redacta propuestaDeRespuesta con la solicitud de
// campo. Sirve para el aserto NEGATIVO: si esto viaja, se está mandando la propuesta en vez de lo
// tecleado.
const laPropuestaDeLaPantalla = "Tu pedido:"

// laPreguntaDelSistema es la `suggested_question` de la interpretación de campo, la que precarga el
// formulario. Mismo papel: si viaja, salió sola (INV-1).
const laPreguntaDelSistema = "¿Para cuántas personas es la torta?"

// --- Criterio 1: lo que viaja es lo que la pantalla enseñaba ---

// TestAprobar_ELTEXTOQUEVIAJAEsELTECLEADOYNoLaPropuestaDeLaPantalla.
//
// 🔑 ES EL TEST DE LA MUTACIÓN DECLARADA de esta casilla, y el aserto tiene que mirar el CUERPO que
// sale hacia el cloud. Comprobar «llamó a /approve» pasa igual mandando la propuesta; comprobar el
// HTML pasa igual, porque el textarea trae la propuesta ANTES de que nadie la edite. Lo único que
// distingue «aprobó lo que leyó» de «aprobó lo que le pusieron delante» es qué `rendered_text` viajó.
//
// El aserto es DOBLE —está lo tecleado Y no está la propuesta— porque el positivo solo pasaría
// también con un cuerpo que llevara las dos cosas concatenadas.
func TestAprobar_ELTEXTOQUEVIAJAEsELTECLEADOYNoLaPropuestaDeLaPantalla(t *testing.T) {
	t.Parallel()
	router, api := respuestaRouter(t, nil)

	// Lo que la dueña dejó escrito tras leer y corregir la propuesta: otro total, otra forma de
	// decirlo. Nada de esto lo redactaría propuestaDeRespuesta.
	editado := "Hola Ana! Te confirmo: torta de chocolate sin gluten para el sábado y 30 tequeños.\n" +
		"Queda en 24.500 con el envío incluido. ¿Te lo dejo en portería?"

	rec := postFormWithCSRF(router, rutaAprobar,
		url.Values{campoRespuesta: {editado}}, clientSessionCookie(t))

	destino := redirectTarget(t, rec)
	if !strings.Contains(destino, "?success="+flashSolicitudAprobada) {
		t.Fatalf("la aprobación no salió bien: fue a %q", destino)
	}

	cuerpo := api.Last(t, puertaAprobar).Body
	if !strings.Contains(cuerpo, "24.500") || !strings.Contains(cuerpo, "portería") {
		t.Errorf("el cuerpo que viajó al cloud NO lleva lo que la dueña escribió: %s", cuerpo)
	}
	if strings.Contains(cuerpo, laPropuestaDeLaPantalla) {
		t.Errorf("viajó la PROPUESTA que redactó esta consola en vez del texto editado: eso es "+
			"aprobar lo que nadie leyó (D-044.19). Cuerpo: %s", cuerpo)
	}
	// Y lo que viaja es `rendered_text` y nada más: ni las líneas ni el total, que ya están escritos
	// al otro lado. Mandarlos abriría la puerta a aprobar una cotización distinta de la de la ficha.
	if strings.Contains(cuerpo, `"total"`) || strings.Contains(cuerpo, `"items"`) {
		t.Errorf("la aprobación mandó líneas o total además del texto: %s", cuerpo)
	}
}

// TestPedirInfo_LAPREGUNTAQUEVIAJAEsLaTecleadaYNuncaLaQuePreparoElSistema.
//
// 🔒 INV-1 EN SU SITIO MÁS FRÁGIL: las `suggested_questions` precargan el campo, así que la forma de
// romper la invariante no es un cambio grande — basta con que el handler mande `Preguntas[0]` cuando
// el POST no traiga nada, o que se lea el campo equivocado. El aserto mira el cuerpo por lo mismo que
// arriba: por la ruta las dos son idénticas.
func TestPedirInfo_LAPREGUNTAQUEVIAJAEsLaTecleadaYNuncaLaQuePreparoElSistema(t *testing.T) {
	t.Parallel()
	router, api := respuestaRouter(t, nil)

	editada := "¿Los tequeños los quieres de queso blanco o amarillo?"

	rec := postFormWithCSRF(router, rutaPedirInfo,
		url.Values{campoPregunta: {editada}}, clientSessionCookie(t))

	destino := redirectTarget(t, rec)
	if !strings.Contains(destino, "?success="+flashSolicitudInfoPedida) {
		t.Fatalf("la petición de información no salió bien: fue a %q", destino)
	}

	cuerpo := api.Last(t, puertaPedirInfo).Body
	if !strings.Contains(cuerpo, "queso blanco o amarillo") {
		t.Errorf("el cuerpo que viajó NO lleva la pregunta que se escribió: %s", cuerpo)
	}
	if strings.Contains(cuerpo, "cuántas personas") {
		t.Errorf("viajó la pregunta que preparó el sistema en vez de la editada: las propuestas no "+
			"salen solas (INV-1). Cuerpo: %s", cuerpo)
	}
}

// TestRespuesta_CadaFormularioMandaLoSuyoYNoLoDelDeAlLado.
//
// Los dos formularios viven en la MISMA tarjeta y los dos campos se llaman como el cuerpo de su
// puerta. Un handler que leyera el campo del vecino seguiría respondiendo 303 y éxito: lo único que
// cambiaría es qué le llega al cliente.
func TestRespuesta_CadaFormularioMandaLoSuyoYNoLoDelDeAlLado(t *testing.T) {
	t.Parallel()
	router, api := respuestaRouter(t, nil)

	// Un envío con LOS DOS campos, que es lo que llegaría si alguien juntara los formularios: cada
	// puerta tiene que quedarse con el suyo.
	form := url.Values{
		campoRespuesta: {"ESTO ES LA COTIZACIÓN"},
		campoPregunta:  {"ESTO ES LA PREGUNTA"},
	}

	redirectTarget(t, postFormWithCSRF(router, rutaAprobar, form, clientSessionCookie(t)))
	aprobado := api.Last(t, puertaAprobar).Body
	if !strings.Contains(aprobado, "ESTO ES LA COTIZACIÓN") || strings.Contains(aprobado, "ESTO ES LA PREGUNTA") {
		t.Errorf("aprobar mandó el campo del formulario de al lado: %s", aprobado)
	}

	redirectTarget(t, postFormWithCSRF(router, rutaPedirInfo, form, clientSessionCookie(t)))
	preguntado := api.Last(t, puertaPedirInfo).Body
	if !strings.Contains(preguntado, "ESTO ES LA PREGUNTA") || strings.Contains(preguntado, "ESTO ES LA COTIZACIÓN") {
		t.Errorf("pedir información mandó el campo del formulario de al lado: %s", preguntado)
	}
}

// --- El reparto de desenlaces: qué repinta y qué va por 303 ---

// TestRespuesta_ElCampoEnBlancoRepintaCon400YNoLLEGAALaAPI.
//
// Las dos mitades importan y la tercera es la que sostiene el 400: la API NO se llama. Sin viaje no
// hay mutación ni mensaje, que es lo único que hace seguro repintar sobre una puerta que envía.
//
// 🔑 Y el repintado NO borra la propuesta. Un campo en blanco no es «lo tecleado»: devolverlo tal cual
// dejaría a la dueña con el aviso «escribe la respuesta» y sin la respuesta que tenía delante hace un
// segundo. Ver conLoTecleado.
func TestRespuesta_ElCampoEnBlancoRepintaCon400YNoLLEGAALaAPI(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre string
		ruta   string
		puerta string
		form   url.Values
		code   string
	}{
		{"aprobar sin texto", rutaAprobar, puertaAprobar, url.Values{campoRespuesta: {""}}, flashSolicitudSinRespuesta},
		{"aprobar con solo espacios", rutaAprobar, puertaAprobar, url.Values{campoRespuesta: {"   \n  "}}, flashSolicitudSinRespuesta},
		{"pedir info sin pregunta", rutaPedirInfo, puertaPedirInfo, url.Values{campoPregunta: {""}}, flashSolicitudSinPregunta},
		{"pedir info con solo espacios", rutaPedirInfo, puertaPedirInfo, url.Values{campoPregunta: {"  "}}, flashSolicitudSinPregunta},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			router, api := respuestaRouter(t, nil)

			rec := postFormWithCSRF(router, caso.ruta, caso.form, clientSessionCookie(t))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (rechazo local repintando). Body: %s", rec.Code, rec.Body.String())
			}
			if api.Called(caso.puerta) {
				t.Errorf("un rechazo local llegó a la API: %v", api.Requests())
			}
			out := rec.Body.String()
			if !strings.Contains(out, flashError(caso.code)) {
				t.Errorf("el aviso del rechazo no es el del catálogo. Body: %s", out)
			}
			// La propuesta sigue en su sitio: el repintado no puede cobrarse lo que la pantalla ya
			// ofrecía.
			if !strings.Contains(out, laPropuestaDeLaPantalla) {
				t.Errorf("el repintado del campo en blanco borró la propuesta de la cotización. Acciones: %s",
					bloque(t, out, `id="form-solicitud-aprobar"`, "</form>"))
			}
			if !strings.Contains(out, laPreguntaDelSistema) {
				t.Errorf("el repintado del campo en blanco borró la pregunta propuesta. Acciones: %s",
					bloque(t, out, `id="form-solicitud-pedir-info"`, "</form>"))
			}
		})
	}
}

// TestAprobar_LineasSinPrecioRepintaCon400ConLaCotizacionYLasLineasObjetadas.
//
// 🔒 ES EL DESENLACE QUE SE DECIDIÓ CON EL DATO DEL CLOUD Y NO POR ANALOGÍA. Que un rechazo de la API
// repinte es la excepción de la excepción, y aquí se sostiene sobre un hecho verificable en
// `intakes/approve.go`: la comprobación de las líneas sin precio corre ANTES de la primera escritura,
// y el envío al cliente es el último paso. No mutó y no mandó nada, así que repintar no crea el
// problema que el PRG resuelve — y lo que se salva es la cotización entera recién escrita.
//
// Los TRES asertos son la decisión: 400, la cotización de vuelta, y QUÉ líneas. Con solo el primero
// pasaría una consola que contesta 400 y borra el campo; con solo el segundo, una que repinta y no
// dice qué corregir.
func TestAprobar_LineasSinPrecioRepintaCon400ConLaCotizacionYLasLineasObjetadas(t *testing.T) {
	t.Parallel()
	router, api := respuestaRouter(t, map[string]stubResponse{
		"POST /api/v1/intakes/{id}/approve": {http.StatusBadRequest, `{"error":"lines_without_price",
			"lines":[{"index":1,"label":"Tequeños"},{"index":2,"label":"Gaseosa"}]}`},
	})

	escrito := "Te confirmo el pedido, quedaría en 24.500 con envío."
	rec := postFormWithCSRF(router, rutaAprobar,
		url.Values{campoRespuesta: {escrito}}, clientSessionCookie(t))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: el `lines_without_price` no mutó ni mandó nada, así que "+
			"repinta. Body: %s", rec.Code, rec.Body.String())
	}
	// Sí hubo viaje —a diferencia del rechazo local—: la lista de líneas la manda la plataforma.
	if !api.Called(puertaAprobar) {
		t.Error("el rechazo se produjo sin llamar a la API: entonces no puede traer las líneas")
	}

	out := rec.Body.String()
	if !strings.Contains(out, "24.500") {
		t.Errorf("el repintado perdió la cotización recién escrita, que es justo lo que la excepción "+
			"existe para conservar. Formulario: %s", bloque(t, out, `id="form-solicitud-aprobar"`, "</form>"))
	}
	// QUÉ líneas, con su posición 1-based y su etiqueta. Un encabezado sin la lista dejaría a la
	// dueña buscándolas a ojo.
	lista := bloque(t, out, `id="solicitud-aprobar-lineas-sin-precio"`, "</ul>")
	if !strings.Contains(lista, "Línea 2") || !strings.Contains(lista, "Tequeños") {
		t.Errorf("el rechazo no dice qué línea falta. Lista: %s", lista)
	}
	if !strings.Contains(lista, "Línea 3") || !strings.Contains(lista, "Gaseosa") {
		t.Errorf("el rechazo se dejó una de las líneas objetadas. Lista: %s", lista)
	}
	// Y el encabezado sale del CATÁLOGO, no de una cadena escrita en el handler.
	if !strings.Contains(out, flashError(flashSolicitudSinPrecio)) {
		t.Errorf("el aviso del rechazo no es el del catálogo. Body: %s", out)
	}
}

// TestRespuesta_LosDesenlacesQuePUDIERONMANDARUNMENSAJEVanPor303YNuncaComo4xx.
//
// 🔒 LA MITAD DEL CRITERIO QUE EL ORIGEN NO CUMPLÍA: el BFF contestaba 422, 409 y 502 con la página
// pintada, así que un F5 sobre un rechazo REENVIABA la aprobación — y aquí eso significa una segunda
// cotización en el teléfono de una persona.
//
// El aserto de que NO es 4xx no es redundante con el 303: son dos formas distintas de romperlo.
// Contestar 4xx con la página es lo que hacía el origen; contestar 303 con el código genérico es lo
// que pasaría si alguien borrara el traductor de plano, y entonces un 502 diría «inténtalo de nuevo
// en un momento» sobre un mensaje que quizá ya salió.
func TestRespuesta_LosDesenlacesQuePUDIERONMANDARUNMENSAJEVanPor303YNuncaComo4xx(t *testing.T) {
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
			// No mutó ni mandó nada, y aun así 303: tras el 422 la solicitud ya no está en
			// `pending_approval`, así que el formulario NI SIQUIERA SE EMITE. Repintar sería servir
			// una página sin el campo y perder lo tecleado igual, con un 4xx encima.
			nombre: "aprobar · 422 no aprobable desde ese estado",
			ruta:   rutaAprobar,
			puerta: "POST /api/v1/intakes/{id}/approve",
			fallo: stubResponse{http.StatusUnprocessableEntity,
				`{"error":"not_approvable","status":"needs_info","approvable_in":["pending_approval"]}`},
			form: url.Values{campoRespuesta: {"la cotización"}},
			code: flashSolicitudNoAprobable,
		},
		{
			nombre: "aprobar · 422 otra persona la movió entre la lectura y la escritura",
			ruta:   rutaAprobar,
			puerta: "POST /api/v1/intakes/{id}/approve",
			fallo: stubResponse{http.StatusUnprocessableEntity, `{"error":"invalid_transition",
				"status":"confirmed","requested":"confirmed","allowed":["settled"]}`},
			form: url.Values{campoRespuesta: {"la cotización"}},
			code: flashSolicitudMovidaSinEnviar,
		},
		{
			nombre: "aprobar · 409 de carrera",
			ruta:   rutaAprobar,
			puerta: "POST /api/v1/intakes/{id}/approve",
			fallo:  stubResponse{http.StatusConflict, `{"error":"intake_changed"}`},
			form:   url.Values{campoRespuesta: {"la cotización"}},
			code:   flashSolicitudMovidaSinEnviar,
		},
		{
			// 🔑 EL LÍMITE de la excepción: un 400 SIN clave conocida sigue yendo por 303 aunque el
			// orden del cloud diga que tampoco escribió. `lines_without_price` repinta porque su
			// cuerpo es un contrato que se puede citar; un 400 que no dice qué es, no — y el precio
			// de equivocarse aquí es un segundo WhatsApp.
			nombre: "aprobar · 400 sin cuerpo nombrado, que NO es lines_without_price",
			ruta:   rutaAprobar,
			puerta: "POST /api/v1/intakes/{id}/approve",
			fallo: stubResponse{http.StatusBadRequest,
				`{"error":"la solicitud no tiene líneas que cotizar"}`},
			form: url.Values{campoRespuesta: {"la cotización"}},
			code: flashSolicitudRechazadaSinEnviar,
		},
		{
			nombre: "aprobar · 502 de la plataforma",
			ruta:   rutaAprobar,
			puerta: "POST /api/v1/intakes/{id}/approve",
			fallo:  stubResponse{http.StatusBadGateway, `{"error":"upstream"}`},
			form:   url.Values{campoRespuesta: {"la cotización"}},
			code:   flashSolicitudEnvioIncierto,
		},
		{
			nombre: "aprobar · 403 porque el plan cambió a mitad de camino",
			ruta:   rutaAprobar,
			puerta: "POST /api/v1/intakes/{id}/approve",
			fallo:  stubResponse{http.StatusForbidden, `{"error":"feature_not_enabled","feature":"cart_basic"}`},
			form:   url.Values{campoRespuesta: {"la cotización"}},
			code:   flashSolicitudesSinPlan,
		},
		{
			nombre: "aprobar · 404, que no dice si existe",
			ruta:   rutaAprobar,
			puerta: "POST /api/v1/intakes/{id}/approve",
			fallo:  stubResponse{http.StatusNotFound, `{"error":"solicitud no encontrada"}`},
			form:   url.Values{campoRespuesta: {"la cotización"}},
			code:   flashNotInYourTenant,
		},
		{
			nombre: "pedir info · 422 del ciclo de vida",
			ruta:   rutaPedirInfo,
			puerta: "POST /api/v1/intakes/{id}/request-info",
			fallo: stubResponse{http.StatusUnprocessableEntity, `{"error":"invalid_transition",
				"status":"confirmed","requested":"needs_info","allowed":["settled"]}`},
			form: url.Values{campoPregunta: {"¿de qué sabor?"}},
			code: flashSolicitudMovidaSinEnviar,
		},
		{
			nombre: "pedir info · 409 de carrera",
			ruta:   rutaPedirInfo,
			puerta: "POST /api/v1/intakes/{id}/request-info",
			fallo:  stubResponse{http.StatusConflict, `{"error":"intake_changed"}`},
			form:   url.Values{campoPregunta: {"¿de qué sabor?"}},
			code:   flashSolicitudMovidaSinEnviar,
		},
		{
			// Esta puerta NO emite `lines_without_price`: no cotiza nada. Si llegara —porque alguien
			// delegara su traductor en el de aprobar— se leería como un rechazo por precios sobre una
			// acción que no los mira.
			nombre: "pedir info · 400 sin cuerpo nombrado",
			ruta:   rutaPedirInfo,
			puerta: "POST /api/v1/intakes/{id}/request-info",
			fallo:  stubResponse{http.StatusBadRequest, `{"error":"question es obligatoria"}`},
			form:   url.Values{campoPregunta: {"¿de qué sabor?"}},
			code:   flashSolicitudRechazadaSinEnviar,
		},
		{
			nombre: "pedir info · 502 de la plataforma",
			ruta:   rutaPedirInfo,
			puerta: "POST /api/v1/intakes/{id}/request-info",
			fallo:  stubResponse{http.StatusBadGateway, `{"error":"upstream"}`},
			form:   url.Values{campoPregunta: {"¿de qué sabor?"}},
			code:   flashSolicitudEnvioIncierto,
		},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			router, _ := respuestaRouter(t, map[string]stubResponse{caso.puerta: caso.fallo})

			rec := postFormWithCSRF(router, caso.ruta, caso.form, clientSessionCookie(t))

			if rec.Code >= 400 {
				t.Fatalf("status = %d: un desenlace que pudo haber mandado el mensaje repintó, y un "+
					"F5 sobre eso lo enviaría dos veces. Body: %s", rec.Code, rec.Body.String())
			}
			destino := redirectTarget(t, rec)
			if !strings.Contains(destino, "?error="+caso.code) {
				t.Errorf("redirigió a %q, y se esperaba el código %q", destino, caso.code)
			}
			// El 303 va al DETALLE: la solicitud sigue siendo la que se estaba mirando.
			if !strings.HasPrefix(destino, rutaDetalle) {
				t.Errorf("redirigió a %q, fuera del detalle de la solicitud", destino)
			}
			if !flashErrors.Known(caso.code) {
				t.Errorf("el código %q no tiene texto en el catálogo", caso.code)
			}
		})
	}
}

// --- Los textos: lo único que sostiene la honestidad de esta pantalla ---

// TestRespuesta_NingunExitoPROMETEQueElClienteLoTengaDelante.
//
// 🔴 El 200 de la plataforma significa «se aplicó y quedó registrado», NUNCA «el cliente lo recibió»:
// el envío es el último paso del cloud y NO devuelve error a propósito, así que con la sesión de
// WhatsApp caída esta pantalla ve exactamente el mismo 200. Un «enviado» a secas convertiría un dato
// que la consola no tiene en una promesa que el negocio va a creer.
func TestRespuesta_NingunExitoPROMETEQueElClienteLoTengaDelante(t *testing.T) {
	t.Parallel()

	for _, code := range []string{flashSolicitudAprobada, flashSolicitudInfoPedida} {
		texto := flashSuccess(code)
		if !strings.Contains(texto, "NO garantiza que el cliente ya la tenga delante") {
			t.Errorf("el éxito %q no avisa de que registrar no es entregar: %q", code, texto)
		}
	}
}

// TestRespuesta_ElDesenlaceQueNoSeSabeNoMandaAREINTENTARAciegas.
//
// 🔒 ES EL TEXTO MÁS CARO DE ESTA CASILLA. Un 5xx o una conexión cortada dejan a esta consola sin
// saber por dónde se cortó: pudo ser antes de escribir, o después de escribir Y de mandar el mensaje.
// El genérico de la casa dice «Inténtalo de nuevo en un momento», y sobre esta puerta eso es una
// invitación a que al cliente le llegue la misma cotización dos veces.
//
// El gemelo negativo va incluido: sin él, el test pasaría con un texto que dijera las dos cosas.
func TestRespuesta_ElDesenlaceQueNoSeSabeNoMandaAREINTENTARAciegas(t *testing.T) {
	t.Parallel()

	texto := flashError(flashSolicitudEnvioIncierto)
	if !strings.Contains(texto, "DOS VECES") {
		t.Errorf("el aviso del desenlace incierto no dice qué pasa si se repite: %q", texto)
	}
	if !strings.Contains(texto, "ANTES") {
		t.Errorf("el aviso del desenlace incierto no manda a mirar antes de repetir: %q", texto)
	}
	if strings.Contains(texto, flashError(flashUpstreamUnavailable)) {
		t.Errorf("el desenlace incierto cayó en el genérico, que invita a reintentar a ciegas: %q", texto)
	}
}

// TestRespuesta_LosAvisosDicenQueNOSeLeEnvioNadaAlCliente.
//
// 🔴 POR QUÉ ESTOS CÓDIGOS SON PROPIOS Y NO LOS DE T7.4, dicho como aserto y no solo como comentario:
// los de aquellas cuatro acciones dicen «no se ha guardado nada», que es verdad y no es lo que hace
// falta leer cuando la acción que se acaba de intentar manda un WhatsApp. Quien reutilice aquel
// catálogo deja a la dueña deduciendo, sobre un mensaje que no puede desenviar.
func TestRespuesta_LosAvisosDicenQueNOSeLeEnvioNadaAlCliente(t *testing.T) {
	t.Parallel()

	for _, code := range []string{
		flashSolicitudSinRespuesta, flashSolicitudSinPregunta, flashSolicitudSinPrecio,
		flashSolicitudNoAprobable, flashSolicitudMovidaSinEnviar, flashSolicitudRechazadaSinEnviar,
	} {
		texto := flashError(code)
		if !strings.Contains(texto, "NO se") || !strings.Contains(texto, "envi") {
			t.Errorf("el aviso %q no dice que NO se envió nada: %q", code, texto)
		}
	}
}
