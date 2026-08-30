package web

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	sharedweb "github.com/EduGoGroup/wapp-shared/web"
	webgin "github.com/EduGoGroup/wapp-shared/web/gin"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
	"github.com/EduGoGroup/wapp-client-console/internal/config"
)

// solicitudes_sugerencia.go es LA RESPUESTA REDACTADA CON LA VOZ DE LA DUEÑA (Plan 044 · T5.1, Plan
// 047 · T2.4), mudada de `intakes_quote.go` del BFF en el Plan 047 · T7.6.
//
// Es la DÉCIMA y última ruta de la bandeja, y la única acción de la pantalla sin cuerpo tecleado: un
// botón. La llamada al cloud no lleva cuerpo —todo va en el token y en la ruta—, y lo que devuelve es
// un texto que PRECARGA el campo de aprobar.
//
// 🔴 ESTO NO APRUEBA NI ENVÍA NADA. La sugerencia es una propuesta; quien le responde al cliente
// sigue siendo la dueña, pulsando «Aprobar y responder» después de leerla. Que sean dos actos y no
// uno es lo que sostiene INV-1, y por eso este handler no llama a ApproveIntake por ningún camino.
//
// 🔴 Y UN MODELO CAÍDO NO ES UN ERROR DE ESTA PUERTA: el cloud responde 200 con el texto determinista
// y su motivo. Lo que cambia entonces es la línea del ORIGEN, no el aviso — ver origenText.
//
// ════════════════════════════════════════════════════════════════════════════
// 🔑 EL PRG DE ESTA RUTA: EL MECANISMO ES GRATIS, EL TOPE Y LAS CERRADURAS NO
// ════════════════════════════════════════════════════════════════════════════
//
// El POST-Redirect-GET aquí no es la doctrina de la casa (D-047.16 lo pide para toda mutación): es
// que ADEMÁS aquí el F5 cuesta caro. Encima de las otras nueve rutas, recargar cuesta una llamada
// barata; encima de ésta cuesta 20-40 s de modelo, medidos en campo. El mecanismo —la cookie de un
// solo uso que la Ola A construyó para el código de invitación— ya estaba en esta casa.
//
// 🔴 LO QUE **NO** ESTABA, y es la mitad del trabajo de esta casilla:
//
//  1. EL TOPE DE TAMAÑO. El mecanismo de `wapp-shared/web` v0.2.0 nació para un token de 43
//     caracteres y NO tiene guarda de tamaño (verificado por diff contra el TAG PUBLICADO, no contra
//     el árbol de al lado). Una cotización no tiene largo acotado, y el navegador DESCARTA EN
//     SILENCIO una cookie que pase de ~4 KB: sin tope, el desenlace de una cotización larga sería el
//     peor posible —la dueña espera 40 s, la página redirige y el texto NO ESTÁ—. Ver
//     maxCookieSugerencia.
//  2. LAS DOS CERRADURAS del identificador: el Path de la cookie (sugerenciaCookieOptions) y el
//     identificador dentro del sobre (tomaSugerenciaFlash). Sin la segunda, pedir la sugerencia de A
//     y abrir B en otra pestaña pinta los precios de A delante de quien va a responderle a B.

// maxCookieSugerencia es cuánto se admite meter en la cookie de la cotización, en bytes del valor ya
// codificado.
//
// 🔴 EXISTE PORQUE EL MECANISMO QUE SE REUTILIZA AQUÍ NO SE ESCRIBIÓ PARA ESTO. La cookie de un solo
// uso de `wapp-shared/web` nació para el token de una invitación —43 caracteres— y no tiene ninguna
// guarda de tamaño: nada corta el valor y nada avisa. El navegador descarta en silencio una cookie
// que pase de unos 4 KB, sin error y sin nada que falle.
//
// El presupuesto: ~4096 B por cookie contando nombre y atributos, y el valor va en base64 (crece un
// tercio). 3.000 B de valor codificado dejan sitio de sobra para lo demás y admiten un texto de unos
// 2 KB, que es varias veces la cotización más larga que se ha visto en campo.
const maxCookieSugerencia = 3000

// sugerenciaFlash es lo que viaja del POST al GET dentro de la cookie efímera. Las claves son de una
// letra a propósito: el presupuesto de una cookie es de bytes, y el nombre del campo se paga tantas
// veces como el valor.
type sugerenciaFlash struct {
	// ID es la solicitud a la que pertenece este texto. Ver tomaSugerenciaFlash: es la SEGUNDA
	// cerradura.
	ID string `json:"i"`
	// Texto es la cotización redactada.
	Texto string `json:"t"`
	// Origen y Respaldo son de dónde salió y por qué, para que el GET pinte la MISMA línea de origen
	// que pintaría el POST. Sin ellos el redirect perdería justo lo que distingue «la voz de la dueña
	// funciona» de «lleva semanas apagada».
	Origen   string `json:"s"`
	Respaldo string `json:"f,omitempty"`
}

// ponSugerenciaFlash guarda la cotización para el GET siguiente. Devuelve si CUPO: cuando no cupo,
// quien llama tiene que pintar sobre el POST.
//
// 🔑 NO PERDER EL TEXTO MANDA SOBRE HACER EL PRG. Son dos cosas buenas y solo una es imprescindible:
// el PRG ahorra una tecla, pero perder una cotización que costó 40 s de modelo es un daño de verdad.
func ponSugerenciaFlash(c *gin.Context, cfg *config.Config, id string,
	out *apiclient.IntakeQuoteSuggestion) bool {
	valor, err := sharedweb.EncodeCookiePayload(sugerenciaFlash{
		ID: id, Texto: out.RenderedText, Origen: out.Source, Respaldo: out.FallbackReason,
	})
	if err != nil {
		// Sin el texto en el log: es contenido de negocio del cliente, la misma razón del `no-store`
		// de la pantalla y del silencio de aprobar.
		slog.Warn("no se pudo empaquetar la cotización para el redirect", "error", err, "solicitud", id)
		return false
	}
	if len(valor) > maxCookieSugerencia {
		slog.Info("cotización demasiado larga para la cookie: se pinta sobre el POST",
			"solicitud", id, "bytes", len(valor), "tope", maxCookieSugerencia)
		return false
	}
	webgin.SetOneTimeCookie(c, sugerenciaCookieOptions(cfg, id), valor)
	return true
}

// tomaSugerenciaFlash lee la cotización que dejó el POST y BORRA la cookie en el mismo gesto, que es
// lo que hace que el texto sobreviva UNA vez: el F5 siguiente ya no encuentra nada.
//
// 🔴 COMPRUEBA QUE EL TEXTO ES DE ESTA SOLICITUD, y no es una comprobación de adorno: es la SEGUNDA
// de las dos cerraduras. Si por lo que sea la cookie llegara a la pantalla de otra solicitud, aquí se
// descarta. Pintar la cotización de A en la pantalla de B pondría los precios de A delante de quien
// está a punto de responderle a B.
func tomaSugerenciaFlash(c *gin.Context, cfg *config.Config, id string) *apiclient.IntakeQuoteSuggestion {
	raw := webgin.TakeOneTimeCookie(c, sugerenciaCookieOptions(cfg, id))
	if raw == "" {
		return nil
	}
	var flash sugerenciaFlash
	if err := sharedweb.DecodeCookiePayload(raw, &flash); err != nil {
		slog.Warn("la cookie de la cotización llegó ilegible", "error", err, "solicitud", id)
		return nil
	}
	if flash.ID != id || strings.TrimSpace(flash.Texto) == "" {
		slog.Warn("la cookie de la cotización no es de esta solicitud, o venía vacía",
			"solicitud", id, "solicitud_de_la_cookie", flash.ID)
		return nil
	}
	return &apiclient.IntakeQuoteSuggestion{
		RenderedText: flash.Texto, Source: flash.Origen, FallbackReason: flash.Respaldo,
	}
}

// origenText redacta DE DÓNDE salió el texto que quedó en el campo.
//
// 🔴 EL ORIGEN NO ES UN ADORNO: es lo único que distingue «la voz de la dueña funciona» de «la voz de
// la dueña está apagada y le están sirviendo el texto sobrio desde hace semanas». Sin él las dos
// pantallas serían idénticas, y esta puerta NUNCA da 502 —con el modelo caído contesta 200 con el
// determinista y su motivo—, así que este párrafo es la única señal que existe.
func origenText(s *apiclient.IntakeQuoteSuggestion) string {
	switch s.Source {
	case apiclient.QuoteSourceLLM:
		return "Origen: LLM. Lo redactó el modelo imitando el estilo de tus cotizaciones aprobadas. " +
			"Léelo entero antes de enviarlo: quien responde sigues siendo tú."
	case apiclient.QuoteSourceDeterministic:
		return "Origen: texto determinista (NO lo redactó el modelo). Lo compuso la plataforma con el " +
			"formato sobrio, y se puede enviar igual. " + respaldoText(s.FallbackReason)
	}
	if strings.TrimSpace(s.Source) == "" {
		return "Origen: la plataforma no dijo quién redactó este texto."
	}
	// Un origen que esta consola no conoce se pinta TAL CUAL, misma doctrina que `viaText` y que
	// `estadoLabel`: antes una clave cruda que una procedencia inventada sobre un texto que se le va a
	// mandar a un cliente.
	return "Origen: `" + s.Source + "` (esta consola no conoce ese origen)."
}

// respaldoText traduce POR QUÉ no lo redactó el modelo.
//
// 🔴 SON TRECE MOTIVOS, no seis: cuatro los emite el generador y NUEVE el verificador de precios del
// cloud, y los nueve viajan por este mismo campo. La lista está enumerada entera en el test hermano,
// que falla si alguno cae en el genérico — porque un motivo sin traducir no rompe nada visible: se
// cuela como clave cruda en una pantalla que lee una persona que no programa.
//
// Los trece se agrupan en TRES historias, y esa agrupación es el trabajo de esta función: la dueña no
// necesita saber cuál de las cinco comprobaciones de importes falló, necesita saber si esto se arregla
// contratando algo, esperando, o mirando los precios del borrador.
func respaldoText(motivo string) string {
	switch motivo {
	// (1) NO SE LLAMÓ AL MODELO, y no es un fallo de nadie.
	case apiclient.QuoteFallbackNoExamples:
		return "Motivo: todavía no hay cotizaciones aprobadas de las que aprender tu estilo, así que " +
			"no se llamó al modelo. En cuanto apruebes unas cuantas, empezará a sonar como tú."
	case apiclient.QuoteFallbackDraftWithoutAmounts:
		return "Motivo: el borrador no tiene ni un importe cerrado, así que no había precios que " +
			"escribir. Pon precios a las líneas de arriba y vuelve a pedirla."

	// (2) EL MODELO NO CONTESTÓ, o contestó algo inservible. Se reintenta pulsando otra vez.
	case apiclient.QuoteFallbackProviderDown:
		return "Motivo: el modelo no estaba disponible en este momento. Puedes volver a pedirla en un rato."
	case apiclient.QuoteFallbackLLMFailed:
		return "Motivo: el modelo falló al redactar (se cayó, tardó demasiado o devolvió algo que no " +
			"servía). Puedes volver a pedirla."
	case apiclient.QuoteFallbackBadOutput, apiclient.QuoteFallbackUnreadableText:
		return "Motivo: el modelo contestó algo que no se puede usar como texto. Puedes volver a pedirla."

	// (3) EL MODELO CONTESTÓ Y SUS NÚMEROS NO CUADRABAN CON EL PEDIDO. Este grupo es el importante: el
	// texto se descartó para que a nadie se le mande un precio que la plataforma no respalda.
	case apiclient.QuoteFallbackUnreadableNumber:
		return "Motivo: el texto del modelo traía un número que no se puede leer como precio, y no se " +
			"manda una cotización con un importe dudoso."
	case apiclient.QuoteFallbackTextWithoutAmounts:
		return "Motivo: el texto del modelo no decía ni un precio, así que no era una cotización."
	case apiclient.QuoteFallbackMissingUnitPrice:
		return "Motivo: al texto del modelo le faltaba el precio de alguna línea."
	case apiclient.QuoteFallbackMissingTotal:
		return "Motivo: al texto del modelo le faltaba el total."
	case apiclient.QuoteFallbackForeignAmount:
		return "Motivo: el texto del modelo traía un importe que no sale de ninguna línea de este " +
			"pedido — un precio inventado, y eso no se le manda a un cliente."
	case apiclient.QuoteFallbackForeignNumber:
		return "Motivo: el texto del modelo traía un número grande que no sale de este pedido y que se " +
			"podría leer como un precio."
	case apiclient.QuoteFallbackAmountsOutOfPlace:
		return "Motivo: los precios del texto del modelo eran los del pedido, pero mal colocados " +
			"(cambiados de línea, repetidos o de más), así que la cuenta no cuadraba."
	}
	if strings.TrimSpace(motivo) == "" {
		return "La plataforma no dijo por qué."
	}
	// Un motivo que esta consola no conoce se nombra TAL CUAL. Es feo A PROPÓSITO: significa que el
	// cloud publicó uno nuevo y que aquí falta traducirlo, y una frase amable lo escondería.
	return "Motivo (sin traducir en esta consola): `" + motivo + "`."
}

// SugerirRespuestaSolicitud pide la cotización redactada con la voz de la dueña y la deja EN EL CAMPO
// de aprobar, editable y sin enviar nada (Plan 047 · T7.6).
//
// ════════════════════════════════════════════════════════════════════════════
// EL REPARTO DE DESENLACES, Y EN QUÉ SE SEPARA DEL ORIGEN
// ════════════════════════════════════════════════════════════════════════════
//
//	                                             ¿mutó?  respuesta
//	sin `llm_intake` (corte local) ............   no      403 REPINTANDO
//	400 `lines_without_price` .................   NO      400 REPINTANDO + las líneas
//	el resto de la API (400 sin clave, 403, …).   no      303 + flash
//	éxito y la cotización CUPO ................   no      303 + flash (el PRG)
//	éxito y NO cupo ...........................   no      200 REPINTANDO con el texto
//
// 🔴 EN EL BFF **TODOS** LOS DESENLACES MALOS REPINTABAN, y aquí no: aquella casa repintaba entera
// —26 llamadas a renderIntakeDetail— y ésta hace PRG. La regla de esta casa (D-047.16, ampliada en
// T7.4/T7.5) es «¿pudo escribir algo al otro lado? Si no, repinta; si sí, o si no se puede saber,
// 303», con una condición añadida: se repinta cuando hay algo que devolver que un 303 no puede
// llevarse. Aquí eso es exactamente UN caso —el `lines_without_price`, que trae la lista de líneas a
// arreglar y que el cloud decide antes de llamar al modelo—, y es el mismo trato que le da la
// aprobación. Los demás no traen nada que un `?error=` no diga igual.
//
// 🔴 Y EL ÉXITO QUE NO CUPO TAMBIÉN REPINTA, que es el caso raro de esta consola: un 200 con aviso de
// ÉXITO sobre el POST. No es una excepción a D-047.16 —no hubo mutación por ningún lado—, es que el
// PRG es lo prescindible y el texto no. Ver ponSugerenciaFlash.
func (h *AdminHandler) SugerirRespuestaSolicitud(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	// Sin empresa no hay solicitud que cotizar y la API respondería 403 sobre una causa que no es esa.
	// Un id vacío no lo produce el router: es una guarda para no gastar el viaje.
	if sinEmpresa(c) || id == "" {
		c.Redirect(http.StatusSeeOther, rutaSolicitudes)
		return
	}

	// DEFENSA EN PROFUNDIDAD. El botón ya sale deshabilitado sin la capacidad, pero un `disabled` es
	// del navegador y un POST a mano no lo tiene: sin `llm_intake` aquí no se llama al cloud.
	//
	// 🔑 SE LEE LA VISTA QUE SEMBRÓ EL GATE y NO se encadena un segundo `requiereFeature` sobre la
	// ruta, que sería lo que parece: ese middleware resuelve el plan por su cuenta, así que apilarlo
	// pagaría DOS llamadas a /entitlements por petición en una consola que resuelve el plan SIN CACHÉ.
	// El gate del grupo es `cart_basic` —la puerta dura, que se lleva la pantalla entera—; ésta es la
	// segunda capacidad y solo la necesita esta ruta.
	if !entitlementsFromContext(c).Has(featureLLMIntake) {
		h.renderSolicitudDetalle(c, detalleRender{
			id: id, status: http.StatusForbidden, code: flashSugerenciaSinPlan,
		})
		return
	}

	var out *apiclient.IntakeQuoteSuggestion
	var sugerirErr error
	code := flashCodeForSugerencia(h.auth.withAuthRetry(c, func(accessToken string) error {
		var err error
		out, err = h.api.Intakes.SuggestIntakeQuote(c.Request.Context(), accessToken, id)
		sugerirErr = err
		return err
	}))
	if sessionIsDead(sugerirErr) {
		h.auth.expireSession(c)
		return
	}
	if code != "" {
		// 🔴 EL TEXTO DE LA COTIZACIÓN NO ENTRA EN EL LOG, y aquí tampoco el error del upstream lleva
		// nada de él: es lo que se le dice a un cliente y esta consola no lo escribe en ningún sitio
		// del camino (misma regla que el `no-store` de la pantalla y que aprobar).
		slog.Warn("no se pudo sugerir la respuesta", "codigo", code, "error", sugerirErr)
		// El `lines_without_price` es el ÚNICO desenlace de la API de esta puerta que repinta, y es
		// además el MÁS PROBABLE en campo: un borrador recién interpretado no tiene precios. Trae la
		// lista con la que se corrige, y un 303 no puede llevársela.
		if sinPrecio, ok := apiclient.LinesWithoutPriceOf(sugerirErr); ok {
			h.renderSolicitudDetalle(c, detalleRender{
				id: id, status: http.StatusBadRequest, code: code,
				sinPrecio: lineasSinPrecioDe(sinPrecio.Lines),
			})
			return
		}
		redirectWith(c, solicitudURL(id), code, "")
		return
	}

	if ponSugerenciaFlash(c, h.cfg, id, out) {
		// 🔑 EL DESTINO DEL 303 Y EL PATH DE LA COOKIE SALEN DE LA MISMA FUNCIÓN. Tienen que coincidir
		// EXACTAMENTE —el navegador identifica una cookie por la terna (dominio, ruta, nombre)— y dos
		// literales iguales escritos aparte se desalinean el día que alguien mueva la ruta.
		redirectWith(c, solicitudURL(id), "", flashSugerenciaLista)
		return
	}

	// NO CUPO EN LA COOKIE: se pinta sobre el POST. El texto no se pierde; lo que se pierde es el PRG,
	// y ésa es la mitad prescindible. La degradación es DECLARADA, no un fallo: el aviso que se lee es
	// el mismo éxito de arriba.
	h.renderSolicitudDetalle(c, detalleRender{
		id: id, status: http.StatusOK, exito: flashSugerenciaLista, sugerencia: out,
	})
}
