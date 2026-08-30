package web

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
)

// solicitudes_acciones.go son LAS ACCIONES QUE LE HABLAN AL CLIENTE (Plan 044 · T4.2/T4.3/T4.4 y
// Plan 047 · T2.4), mudadas de las VISTAS de `intakes_actions.go` e `intakes_quote.go` del BFF
// (Plan 047 · T7.3).
//
// 📌 Desde T7.5 están aquí también LOS DOS HANDLERS QUE MANDAN UN MENSAJE: aprobar y pedir más
// información. «Sugerir la respuesta» llegó en T7.6 y vive en solicitudes_sugerencia.go —su VISTA se
// arma aquí, junto al campo que precarga, pero su handler se lleva el PRG, el tope de la cookie y los
// plazos propios, que no son de este fichero—. Corregir la interpretación llegó en T7.4 y vive con el
// formulario que guarda (solicitudes_lineas.go), porque comparte con él el aparato entero.
//
// ════════════════════════════════════════════════════════════════════════════
// 🔒 EL REPARTO DE DESENLACES DE LAS DOS PUERTAS QUE ENVÍAN (D-047.16, AMPLIADA EL 2026-08-30)
// ════════════════════════════════════════════════════════════════════════════
//
// La pregunta que decide sigue siendo la misma —«¿pudo escribir algo al otro lado? Si no, repinta;
// si sí, o si no se puede saber, 303»—, pero aquí hay que hacerle una segunda a cada desenlace,
// porque un repintado sobre un envío ya hecho invita a un F5 y un F5 le deja el mismo mensaje DOS
// VECES a una persona:
//
//	                                        ¿mutó?  ¿salió el WhatsApp?  respuesta
//	texto/pregunta en blanco (local) ......  no      no                   400 repintando
//	400 `lines_without_price` (aprobar) ...  NO      NO                   400 repintando + las líneas
//	400/422 sin clave conocida ............  no      no                   303 + flash
//	422 `not_approvable` ..................  no      no                   303 + flash
//	422 `invalid_transition` / 409 ........  no (perdió el CAS)  no       303 + flash
//	403 del plan / 404 ....................  no      no                   303 + flash
//	5xx, conexión cortada, lo desconocido .  SE IGNORA  SE IGNORA         303 + flash INCIERTO
//	éxito .................................  sí      se intentó           303 + flash
//
// 🔑 POR QUÉ EL `lines_without_price` REPINTA, decidido CON EL DATO DEL CLOUD y no por analogía con
// el `invalid_items` de las líneas: `intakes.Service.Approve` (approve.go:373) comprueba el texto, el
// estado, las líneas sin precio y que haya algo que cotizar ANTES de la primera escritura, y el envío
// al cliente es el paso (4), después de la transición y de la revisión. Un `lines_without_price` no
// escribió nada y no mandó nada, así que repintarlo no crea el problema que el PRG resuelve; y lo
// que se salva es la cotización entera que la dueña acababa de escribir.
//
// 🔴 POR QUÉ NO SE EXTIENDE AL RESTO DE LOS 400, aunque el mismo orden del cloud diga que tampoco
// mutaron: porque esa afirmación sale de leer el código de la plataforma, y aquí el precio de que
// algún día deje de ser cierta es un segundo WhatsApp a un cliente. Un cuerpo con clave conocida es
// un contrato que se puede citar; un 400 que no dice qué es, no. Es el mismo límite que fijó T7.4
// para `/items` (solicitudes_lineas.go), sostenido sobre algo que se deshace todavía menos.
//
// 🔴 Y POR QUÉ EL 422 Y EL 409 NO REPINTAN aunque tampoco escribieran: tras cualquiera de los dos la
// solicitud ya no está en `pending_approval`, así que accionesDe devuelve nil y el formulario NI
// SIQUIERA SE EMITE. Repintar ahí sería servir una página sin el campo y perder lo tecleado
// igualmente, con un 4xx encima. El 303 relee la ficha y enseña dónde está de verdad.
//
// Es una vista PROPIA y no una fila más del bloque de estado a propósito: estas acciones LE HABLAN AL
// CLIENTE por WhatsApp y dejan revisión; el desplegable de estado solo mueve la etiqueta del ciclo de
// vida y no le escribe a nadie. Confundirlos sería ofrecer «responderle al cliente» donde no se
// responde.

// Nombres de los campos de los DOS formularios que envían. Van como constantes por lo mismo que los
// de las líneas y el del desplegable de estado: los lee el handler y los escribe la plantilla, y un
// desajuste entre los dos no lo detecta el compilador — el POST llegaría con el campo vacío y la
// pantalla contestaría «escribe la respuesta» sobre un campo lleno.
//
// En inglés porque son EXACTAMENTE los nombres con los que el cuerpo viaja al cloud (`rendered_text`,
// `question`): la ruta se traduce porque es superficie, esto no.
const (
	campoRespuesta = "rendered_text"
	campoPregunta  = "question"
)

// paywallSugerencia es lo que se lee cuando el plan no incluye la redacción automática. Va aparte del
// de Regenerar aunque lleve a contratar LO MISMO: son dos acciones distintas, y decirlo con las
// palabras de la otra dejaría a la dueña buscando un botón que no es el que tiene delante.
const paywallSugerencia = "El plan de tu empresa no incluye el análisis con IA (`" + featureLLMIntake +
	"`), así que la plataforma no puede redactar la respuesta. La solicitud se responde igual: el " +
	"campo de abajo trae la propuesta que arma esta consola con las líneas, y se edita a mano."

// sugerenciaView es el botón «Sugerir la respuesta» y POR QUÉ no se puede pulsar.
//
// El botón NUNCA se esconde, igual que Regenerar y por lo mismo: sin `llm_intake` sale DESHABILITADO
// con la razón delante. El gate DURO de esta pantalla sigue siendo `cart_basic`, que corta la ruta
// entera antes de renderizar (solicitudes_gate.go).
type sugerenciaView struct {
	Habilitado bool
	// Razon es por qué NO se puede pulsar (vacío cuando Habilitado).
	Razon string
	// Paywall es si el motivo es del PLAN, que decide si el aviso lleva a contratar o a otro sitio.
	Paywall bool
	// Sugerida es si esta página viene de una sugerencia RECIÉN pedida. Solo entonces se pinta el
	// origen: fuera de ese caso el campo de aprobar lleva la propuesta de siempre —la que arma esta
	// consola con las líneas— y decir «lo redactó el modelo» sería mentir sobre un texto que el modelo
	// no ha visto.
	Sugerida bool
	// OrigenText es QUIÉN redactó lo que hay en el campo, ya redactado para la dueña.
	//
	// 🔴 NO ES UN ADORNO: es lo único que distingue «la voz de la dueña funciona» de «lleva semanas
	// apagada y le están sirviendo el texto sobrio». La puerta NUNCA da 502 —con el modelo caído el
	// cloud contesta 200 con el determinista y su motivo—, así que sin esta línea las dos pantallas
	// serían idénticas. Ver origenText.
	OrigenText string
	// MaxEspera es CUÁNTO espera esta página antes de rendirse, en palabras y ya redondeado.
	//
	// 🔴 SALE DEL PLAZO CONFIGURADO, no de un número escrito en la plantilla, y esa es toda la razón
	// de que el campo exista. En el BFF el aviso decía «unos segundos» porque se redactó cuando la
	// espera moría a los 15 s; en cuanto la ruta pasó a esperar de verdad, la frase se quedó
	// mintiendo —lo medido fueron 24,8-35,5 s— sin que nada fallara. Colgado del plazo, mover el
	// plazo mueve el texto.
	MaxEspera string
}

// accionesView son las acciones tal como las pinta la plantilla.
type accionesView struct {
	// RespuestaPropuesta es la cotización PROPUESTA, que la dueña edita antes de mandarla. Es una
	// propuesta y nada más: lo que la revisión guarda es byte a byte lo que se envió, y su autora es
	// ella (D-044.19).
	RespuestaPropuesta string
	// Pregunta es la propuesta, tomada de las que preparó el LLM. Nunca sale sola (INV-1): esto es lo
	// que aparece en el formulario, no lo que se manda.
	Pregunta string
	// Preguntas son todas las preparadas, para poder copiar otra.
	Preguntas []string
	// PreguntasConocidas es falso cuando la plataforma NO publicó la clave, o sea cuando el plan no
	// incluye `llm_intake`. No es lo mismo que «no había nada que preguntar», y la pantalla lo dice
	// distinto.
	PreguntasConocidas bool
	// PendientesPrecio es cuántas líneas siguen sin precio. Con una sola, la aprobación va a salir
	// rechazada, y decirlo ANTES ahorra el viaje y explica por qué.
	PendientesPrecio int
	// HayBorrador es si hay un borrador que corregir. Sin él NO se ofrece el botón «Corregir»: lo
	// manda el formulario del borrador, y un botón que apunta a un formulario que no está en la
	// página no hace nada — que es peor que no ofrecerlo, porque parece que sí.
	HayBorrador bool
	// Sugerencia es la acción de OTRA naturaleza: no le habla al cliente, no escribe en la solicitud
	// y no la mueve de estado. Solo redacta una propuesta y la deja en el campo de aquí al lado. Vive
	// en esta vista —y no en una tarjeta propia— porque su resultado ES este formulario: separarla
	// dejaría un botón lejos del campo que precarga.
	Sugerencia sugerenciaView
	// SinPrecio son las líneas que la PLATAFORMA señaló sin precio al rechazar una aprobación. Vacío
	// mientras no haya habido rechazo: no es el conteo de arriba —ése lo calcula esta pantalla con el
	// borrador que tiene delante—, es la lista que vino en el `lines_without_price`.
	//
	// Va como vista y no en el texto del catálogo por lo mismo que los defectos de las líneas y los
	// avisos con números del descarte: el catálogo traduce códigos a textos FIJOS y no interpola, y
	// «quedan líneas sin precio» sin decir cuáles deja a la dueña buscándolas a ojo.
	SinPrecio []lineaSinPrecio
}

// lineaSinPrecio es UNA línea que la plataforma devolvió sin precio, ya redactada.
//
// 🔑 `Posicion` es 1-based y sale de sumarle uno al `index` del contrato. Ese `index` es la posición
// dentro de `lines` DE LA ÚLTIMA REVISIÓN, y está verificado en la plataforma: `LinesWithoutPrice`
// recorre el payload con el índice del bucle (cloud/wapp-cloud-platform/internal/intakes/
// approve.go:283) y `PendingPriceLines` mira solo la revisión de número más alto. Se deja escrito con
// fichero y línea porque es una asunción sobre un contrato AJENO.
//
// 🔴 LO QUE ESA POSICIÓN **NO** ES, y por eso el defecto NO se ancla a una fila del formulario del
// borrador como hacen los `invalid_items`: esta consola reparte las líneas de la interpretación en
// dos listas —las del pedido y las de envío (borradorView.anade)— y renumera solo las primeras. Con
// una línea de envío por delante, la fila que la dueña ve numerada como 3 es el índice 4 del payload.
// Anclarla marcaría una fila ajena y NADA FALLARÍA. Lo que identifica la línea aquí es la ETIQUETA; el
// número acompaña porque una etiqueta vacía es posible y entonces es lo único que queda.
type lineaSinPrecio struct {
	Posicion int
	Etiqueta string
}

// lineasSinPrecioDe traduce lo que vino en el rechazo a lo que la pantalla pinta.
func lineasSinPrecioDe(lineas []apiclient.IntakeLineRef) []lineaSinPrecio {
	out := make([]lineaSinPrecio, 0, len(lineas))
	for _, linea := range lineas {
		out = append(out, lineaSinPrecio{
			Posicion: linea.Index + 1,
			Etiqueta: strings.TrimSpace(linea.Label),
		})
	}
	return out
}

// conLoTecleado devuelve a los dos formularios lo que la dueña escribió, y a la aprobación las líneas
// que la plataforma le objetó. Nil ⇒ no hubo repintado y los campos se quedan con la propuesta.
//
// 🔴 UN CAMPO EN BLANCO NO ES «LO TECLEADO», y por eso las dos ramas preguntan por el vacío antes de
// pisar nada: el rechazo local es precisamente el del campo vacío, y devolverlo tal cual borraría la
// propuesta que la pantalla ofrecía —la dueña se quedaría con el aviso «escribe la respuesta» y sin
// la respuesta que tenía delante hace un segundo—. Es la misma regla que conLoTecleadoEnRegenerar.
//
// Y son campos SEPARADOS aunque los dos formularios estén en la misma tarjeta: el navegador manda
// solo el que se envía, así que en un repintado uno de los dos viene siempre vacío y el otro tiene
// que conservar su propuesta. Cruzarlos pondría la cotización dentro del campo de la pregunta.
func (v *accionesView) conLoTecleado(respuesta, pregunta string, sinPrecio []lineaSinPrecio) {
	if v == nil {
		return
	}
	if respuesta != "" {
		v.RespuestaPropuesta = respuesta
	}
	if pregunta != "" {
		v.Pregunta = pregunta
	}
	v.SinPrecio = sinPrecio
}

// conLaSugerencia deja en el campo de aprobar la cotización que se acaba de pedir, y enciende la
// línea del ORIGEN.
//
// Llega por DOS caminos que aquí se ven iguales: la cookie efímera que consume el GET tras el 303, y
// el repinte de reserva cuando la cotización no cupo en esa cookie. Que los dos entren por el MISMO
// carril es deliberado: en cuanto el texto está en el campo es un borrador de la dueña, y se edita
// como tal.
//
// 🔑 SE APLICA ANTES QUE conLoTecleado a propósito. Hoy no pueden coincidir —el repintado de un
// rechazo no trae sugerencia y viceversa—, pero si algún día coincidieran, lo que la dueña escribió
// tiene que ganar sobre lo que redactó la máquina. El orden lo garantiza sin depender de que nadie se
// acuerde.
func (v *accionesView) conLaSugerencia(sugerencia *apiclient.IntakeQuoteSuggestion) {
	if v == nil || sugerencia == nil {
		return
	}
	v.RespuestaPropuesta = sugerencia.RenderedText
	v.Sugerencia.Sugerida = true
	v.Sugerencia.OrigenText = origenText(sugerencia)
}

// accionesDe arma las acciones. Devuelve nil cuando el estado no las admite: un botón que la
// plataforma va a rechazar no se ofrece (misma regla que el desplegable de un estado terminal y que
// el formulario de líneas).
func accionesDe(detalle *apiclient.IntakeDetail, borrador *borradorView, ent entitlementsView,
	espera time.Duration) *accionesView {
	if detalle.Status != estadoEditable {
		return nil
	}
	view := &accionesView{
		RespuestaPropuesta: propuestaDeRespuesta(detalle, borrador),
		HayBorrador:        borrador != nil,
		Sugerencia:         sugerenciaDe(ent, espera),
	}
	if borrador != nil {
		view.Preguntas = borrador.Preguntas
		view.PreguntasConocidas = borrador.PreguntasConocidas
		view.PendientesPrecio = borrador.PendientesPrecio
		if len(borrador.Preguntas) > 0 {
			view.Pregunta = borrador.Preguntas[0]
		}
	}
	return view
}

// sugerenciaDe decide si el botón se puede pulsar, y si no, POR QUÉ.
//
// Solo hay UN motivo que se pueda anticipar aquí —la capacidad—, y no es un hueco: los otros dos
// desenlaces malos (líneas sin precio y solicitud sin líneas) dependen del estado del borrador EN LA
// PLATAFORMA, y adivinarlos desde el espejo local sería apagar el botón por una foto vieja. Llegan
// como RECHAZO, y su traducción es de T7.6.
func sugerenciaDe(ent entitlementsView, espera time.Duration) sugerenciaView {
	view := sugerenciaView{MaxEspera: esperaText(espera)}
	if !ent.Has(featureLLMIntake) {
		view.Paywall = true
		view.Razon = paywallSugerencia
		return view
	}
	view.Habilitado = true
	return view
}

// esperaText pone en palabras lo que la página espera antes de rendirse.
//
// Redondea a propósito: el aviso lo lee una persona que está decidiendo si quedarse mirando la
// pantalla, y «57 segundos» no le sirve mejor que «un minuto» — pero «unos segundos» sí le sirve
// PEOR, porque describe una espera que no es la que va a tener. El corte del minuto es donde la
// unidad deja de ayudar.
func esperaText(espera time.Duration) string {
	if espera <= 0 {
		// Sin plazo conocido no se inventa una magnitud: se dice lo único cierto, que espera a que
		// llegue. Es el caso de una Config armada a mano (los tests), no el de producción.
		return "que la plataforma conteste"
	}
	if espera < time.Minute {
		return strconv.Itoa(int(espera.Round(time.Second)/time.Second)) + " segundos"
	}
	minutos := int(espera.Round(time.Minute) / time.Minute)
	if minutos <= 1 {
		return "un minuto"
	}
	return strconv.Itoa(minutos) + " minutos"
}

// propuestaDeRespuesta redacta la cotización que se le propone a la dueña.
//
// Si la revisión ya trae un texto compuesto, ése; si no, se arma con las líneas del borrador. Y se
// arma SIN hacer una sola cuenta: los precios se copian tal cual y el total es el que manda la
// plataforma (INV-13). Multiplicar aquí crearía una segunda autoridad sobre el dinero, y el día que
// las dos discreparan el cliente tendría delante la de esta pantalla.
func propuestaDeRespuesta(detalle *apiclient.IntakeDetail, borrador *borradorView) string {
	if borrador == nil {
		return ""
	}
	if rev := detalle.LastRevisionOf(apiclient.RevisionKindInterpreted); rev != nil {
		if texto := strings.TrimSpace(rev.RenderedText); texto != "" {
			return texto
		}
	}

	var b strings.Builder
	b.WriteString("Tu pedido:\n")
	for _, linea := range borrador.Lineas {
		b.WriteString("- " + linea.Cantidad + " × " + linea.Etiqueta)
		if linea.Tamano != "" {
			b.WriteString(" (" + linea.Tamano + ")")
		}
		if linea.Personalizacion != "" {
			b.WriteString(" · " + linea.Personalizacion)
		}
		if linea.TienePrecio {
			b.WriteString(" — " + linea.PrecioUnitario + " c/u")
		} else {
			b.WriteString(" — pendiente de precio")
		}
		b.WriteString("\n")
	}
	for _, linea := range borrador.Envio {
		b.WriteString("- " + linea.Etiqueta)
		if linea.TienePrecio {
			b.WriteString(" — " + linea.PrecioUnitario)
		} else if linea.Nota != "" {
			b.WriteString(" — " + linea.Nota)
		}
		b.WriteString("\n")
	}
	if borrador.FechaEntrega != "" {
		b.WriteString("Entrega: " + borrador.FechaEntrega + "\n")
	}
	if detalle.CustomerNote != "" {
		b.WriteString("Indicación: " + detalle.CustomerNote + "\n")
	}
	b.WriteString("Total: " + strconv.FormatFloat(detalle.Total, 'f', 2, 64))
	return b.String()
}

// AprobarSolicitud aprueba la solicitud y le responde al cliente con el texto que dejó escrito la
// dueña (Plan 044 · T4.3, mudada en Plan 047 · T7.5).
//
// 🔴 EL ÉXITO DE ESTA PUERTA SIGNIFICA «se aplicó y quedó registrado», NUNCA «el cliente lo recibió»:
// el envío cuelga de una sesión de WhatsApp que puede estar caída y el cloud no lo deja tumbar la
// aprobación. El aviso que se pinta dice exactamente eso y no debe prometer la entrega.
//
// 🔑 LO QUE VIAJA ES LO QUE LA PANTALLA ENSEÑABA, y ése es el criterio de la casilla: el cuerpo lleva
// el `rendered_text` DEL FORMULARIO. La propuesta que esta consola redacta con las líneas
// (propuestaDeRespuesta) es solo el valor inicial del campo; mandar esa propuesta en vez del texto
// del POST sería aprobar lo que nadie leyó, y ningún test que mire la ruta o el HTML lo vería —el que
// lo ve mira el CUERPO que sale hacia el cloud—.
func (h *AdminHandler) AprobarSolicitud(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	// Sin empresa no hay solicitud que aprobar y la API respondería 403 sobre una causa que no es
	// esa. Un id vacío no lo produce el router: es una guarda para no gastar el viaje.
	if sinEmpresa(c) || id == "" {
		c.Redirect(http.StatusSeeOther, rutaSolicitudes)
		return
	}

	// El texto NO se recorta con formValue: lo que se tecleó vuelve tal cual al textarea si hay que
	// repintar. El TrimSpace se aplica para DECIDIR si hay algo que mandar y para componer el cuerpo,
	// que es lo que hacía el origen; el cloud no vuelve a recortarlo —guarda byte a byte lo que
	// recibe (approve.go)—, así que el recorte de aquí es la única mano que se le pone al texto y por
	// eso se queda en los extremos.
	crudo := c.PostForm(campoRespuesta)
	texto := strings.TrimSpace(crudo)
	if texto == "" {
		h.renderSolicitudDetalle(c, detalleRender{
			id: id, status: http.StatusBadRequest, code: flashSolicitudSinRespuesta,
		})
		return
	}

	var aprobarErr error
	code := flashCodeForAprobar(h.auth.withAuthRetry(c, func(accessToken string) error {
		_, err := h.api.Intakes.ApproveIntake(c.Request.Context(), accessToken, id, texto)
		aprobarErr = err
		return err
	}))
	if sessionIsDead(aprobarErr) {
		h.auth.expireSession(c)
		return
	}
	if code != "" {
		// 🔴 EL TEXTO DE LA COTIZACIÓN NO ENTRA EN EL LOG. Es lo que se le dice a un cliente —precios,
		// dirección, lo que la dueña haya escrito— y esta consola no lo escribe en ningún sitio del
		// camino (misma regla que el `Cache-Control: no-store` de la pantalla y que el material extra
		// de la regeneración).
		slog.Warn("no se pudo aprobar la solicitud", "codigo", code, "error", aprobarErr)
	}
	// El `lines_without_price` es el ÚNICO desenlace de la API de esta puerta que no sale por el PRG:
	// corre antes de la primera escritura, así que no mutó ni mandó nada, y trae la lista con la que
	// se corrige. Ver el reparto entero en la cabecera del fichero.
	if sinPrecio, ok := apiclient.LinesWithoutPriceOf(aprobarErr); ok {
		h.renderSolicitudDetalle(c, detalleRender{
			id: id, status: http.StatusBadRequest, code: code,
			textoRespuesta: crudo, sinPrecio: lineasSinPrecioDe(sinPrecio.Lines),
		})
		return
	}
	redirectWith(c, solicitudURL(id), code, flashSolicitudAprobada)
}

// PedirInfoSolicitud le manda al cliente la pregunta de la dueña y deja la solicitud esperando su
// respuesta (Plan 044 · T4.4, mudada en Plan 047 · T7.5).
//
// La pregunta va SIEMPRE editada por una persona: las que prepara el LLM son una propuesta del
// formulario y jamás salen solas (INV-1). Esta puerta es el sitio donde esa invariante se puede
// romper sin que nada falle —bastaría con mandar `Preguntas[0]` cuando el campo llega vacío en vez
// de rechazar—, y por eso el rechazo del campo en blanco no es una formalidad de formulario.
//
// 🔴 NO REPINTA NINGÚN DESENLACE DE LA API, y no es una asimetría con aprobar: es que esta puerta no
// tiene ninguno que repintar. Su único 400 nombrado no existe —el cloud contesta el 400 de la
// pregunta vacía en prosa, sin clave— y su 422 es el del ciclo de vida, tras el cual el formulario ni
// se emite. Lo que repinta es lo mismo que en aprobar: el rechazo LOCAL.
func (h *AdminHandler) PedirInfoSolicitud(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if sinEmpresa(c) || id == "" {
		c.Redirect(http.StatusSeeOther, rutaSolicitudes)
		return
	}

	crudo := c.PostForm(campoPregunta)
	pregunta := strings.TrimSpace(crudo)
	if pregunta == "" {
		h.renderSolicitudDetalle(c, detalleRender{
			id: id, status: http.StatusBadRequest, code: flashSolicitudSinPregunta,
		})
		return
	}

	var pedirErr error
	code := flashCodeForPedirInfo(h.auth.withAuthRetry(c, func(accessToken string) error {
		_, err := h.api.Intakes.RequestIntakeInfo(c.Request.Context(), accessToken, id, pregunta)
		pedirErr = err
		return err
	}))
	if sessionIsDead(pedirErr) {
		h.auth.expireSession(c)
		return
	}
	if code != "" {
		// La pregunta tampoco entra en el log, por lo mismo que la cotización: es texto que va a salir
		// hacia un cliente y puede nombrarlo.
		slog.Warn("no se pudo pedir más información sobre la solicitud", "codigo", code, "error", pedirErr)
	}
	redirectWith(c, solicitudURL(id), code, flashSolicitudInfoPedida)
}
