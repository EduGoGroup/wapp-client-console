package web

import (
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
)

// solicitudes_comparacion.go es EL ORIGINAL DEL CLIENTE AL LADO DE LO QUE SE ENTENDIÓ (design §7.6),
// mudado de `intakes_compare.go` del BFF (Plan 047 · T7.3).
//
// 🔴 EL LITERAL LLEGA YA DESCIFRADO del cloud —lo descifra la lectura del detalle en el borde y esta
// consola no tiene KEK ni la quiere—, así que aquí no se descifra nada. Lo que SÍ es responsabilidad
// de esta capa es que ese texto no se quede escrito en ningún sitio del camino: la respuesta va
// `Cache-Control: no-store` (ver renderSolicitudDetalle) y ninguna rama lo mete en el log.

// featureLLMIntake es la capacidad que abre el RE-ANÁLISIS y la sugerencia de la respuesta. NO es la
// misma que abre la bandeja: las pantallas van tras `cart_basic` (D-044.47 §1) y `/reanalyze` exige
// además ésta DENTRO del servicio, no en el middleware.
//
// 🔴 De ahí sale el estado de borde que además es el plan REAL de UAT: una empresa con `cart_basic` y
// sin `llm_intake` abre la solicitud, la lee entera… y el botón Regenerar le devolvería un 403. Por
// eso el botón se pinta DESHABILITADO con la razón a la vista, nunca escondido.
const featureLLMIntake = "llm_intake"

// featureAPILLM es el add-on de la vía EXTERNA, y aquí solo sirve para NOMBRARLO en el aviso.
//
// 🔴 No se comprueba antes de llamar, y no es un hueco: para saber si la vía efectiva de la empresa
// es `api` habría que leer su configuración de LLM, y esa lectura exige justamente el add-on que
// falta. Llega siempre como RECHAZO de la regeneración, nunca como gate previo (ver
// flashCodeForRegeneracion).
const featureAPILLM = "api_llm"

// viaDesconocida es lo que dice el encabezado de una revisión cuando `analysis.provider` viene vacío
// (D-044.52 (b)).
//
// 🔴 Y viene vacío en el caso COMÚN: la plataforma solo rellena `provider` en el re-análisis, así que
// la revisión 1 —la que nace del pipeline— no tiene vía que enseñar. Escribir aquí «LLM local» sería
// inventarse un hecho que nadie registró, y esta consola no lo hace ni cuando la suposición parece
// segura.
const viaDesconocida = "vía no registrada"

// maxRunasRegeneracion es el tope del material EXTRA que se puede pegar para regenerar (contrato
// §8.1). Se cuenta en RUNAS y no en bytes porque el contrato habla de runas y porque un texto con
// acentos —el caso normal en español— cabría de sobra en runas y se pasaría en bytes.
const maxRunasRegeneracion = 280

// origenView es la columna ORIGINAL DEL CLIENTE: el literal, o por qué no está.
type origenView struct {
	// Texto es el literal del cliente. Vacío cuando no hay.
	Texto string
	// Presente es si hay literal que enseñar.
	Presente bool
	// Razon es `purged` o `never_stored` cuando no lo hay (vacío si lo hay). Sale de DOS claves de la
	// revisión y no de un 422 de `/reanalyze`, que es una escritura auditada: preguntar por la razón
	// no puede costar una escritura.
	Razon string
	// PodadoEl es cuándo se podó (solo en `purged`), ya formateado.
	PodadoEl string
}

// RazonText redacta por qué no hay original. Las dos razones se dicen DISTINTO a propósito: una es
// una pérdida —existió y venció— y la otra nunca fue una promesa.
func (s origenView) RazonText() string {
	switch s.Razon {
	case apiclient.SourcePurged:
		text := "El texto original de esta conversación ya venció por la política de retención y se " +
			"podó, así que no hay de qué regenerar."
		if s.PodadoEl != "" {
			text += " Se podó el " + s.PodadoEl + "."
		}
		return text
	case apiclient.SourceNeverStored:
		return "No hay original guardado de esta conversación: cuando ocurrió, el plan de esta empresa " +
			"no guardaba el texto del cliente. No se borró — nunca se guardó."
	}
	return ""
}

// enlaceRevision es UNA entrada de la navegación entre interpretaciones.
//
// 🔴 SIN JAVASCRIPT (ADR-0035): saltar de una revisión a otra es un enlace normal a la misma página
// con un parámetro de query, y el «después» lo pinta el servidor. No hay pestañas ni acordeones,
// porque no hay quien los mueva en el navegador.
type enlaceRevision struct {
	Revision int
	URL      string
	// ViaText es la vía REGISTRADA en esa revisión, ya redactada.
	ViaText string
	// Actual marca la que se está mirando: se pinta sin enlace, para que el enlace no prometa un
	// salto a donde ya se está.
	Actual bool
}

// regenerarView es el botón Regenerar y, sobre todo, POR QUÉ no se puede pulsar.
//
// El botón nunca se esconde: los estados de borde lo dejan DESHABILITADO con la razón delante.
// Esconderlo dejaría a la dueña sin saber que existe una regeneración —ni por qué no la tiene—, que
// es peor que un botón apagado con su motivo.
type regenerarView struct {
	Habilitado bool
	// Razon es por qué NO se puede (vacío cuando Habilitado).
	Razon string
	// Paywall es si el motivo es del PLAN, que es lo que decide si el aviso lleva a contratar o a
	// otro sitio. El 403 de capacidad y el 422 de credencial no se dicen igual a propósito.
	Paywall bool
	// Texto es el material extra tecleado, para repintarlo tras un rechazo (lo rellena
	// RegenerarSolicitud).
	Texto string
	// MaxRunas es el tope del material extra, para poder decirlo en la pantalla.
	MaxRunas int
	// Runas es cuántas trae lo tecleado, y SOLO se rellena cuando se pasó del tope (0 ⇒ no se pasó).
	//
	// 🔴 Vive en la vista y no en el texto del flash porque el catálogo traduce códigos a textos
	// FIJOS y no interpola datos. Y el tope tampoco se escribe a mano en la plantilla: sale de
	// MaxRunas, que es la constante. Un número copiado a mano en un texto no se entera el día que
	// alguien mueva la constante — que es exactamente cómo el aviso de la espera del BFF acabó
	// mintiendo.
	Runas int
}

// comparacionView es el §7.6 entero: el original del cliente AL LADO de lo que se entendió, la
// navegación por las interpretaciones y el botón de regenerar.
//
// Se pinta sobre la revisión SELECCIONADA, que no tiene por qué ser la última. El borrador editable
// del §7.5, en cambio, sale SIEMPRE de la última: navegar por el histórico es leer, no cambiar lo que
// se corrige, y confundir las dos cosas dejaría a la dueña guardando precios sobre una lectura vieja.
type comparacionView struct {
	SolicitudID string
	Revision    int
	// CreadaEl es cuándo se guardó la revisión, YA FORMATEADA (ver fecha).
	CreadaEl string
	// RolText es QUIÉN dejó la revisión, y es un ROL —no una persona—: la plataforma publica
	// `system`/`owner`/`crm` y esta consola no puede convertir eso en un nombre (cero PII).
	RolText string
	// ViaText es la vía registrada en esta revisión, ya redactada.
	ViaText string
	// Modelo es el modelo que consta (vacío si no consta).
	Modelo string
	// ReanalisisDe es la revisión de la que salió este re-análisis (0 ⇒ es una primera lectura).
	ReanalisisDe int

	Origen origenView
	// Lineas es lo interpretado en la columna de al lado, en el mismo formato que el §7.5 para que
	// las dos digan lo mismo del mismo dato.
	Lineas []borradorLinea
	// Unidades son las unidades TOTALES interpretadas y NumLineas las líneas. Es lo que hace legible
	// la discrepancia del caso de las hamburguesas —1 pedida, 3 interpretadas— sin abrir nada más.
	Unidades  int
	NumLineas int

	Revisiones []enlaceRevision
	Regenerar  regenerarView

	// RevisionInexistente es la revisión que se pidió por query y no existe (0 si no pasó). Se dice
	// en vez de redirigir en silencio: un enlace viejo o tecleado a mano tiene que enterarse.
	RevisionInexistente int
}

// CabeceraText redacta el encabezado de la interpretación que se está mirando.
func (v *comparacionView) CabeceraText() string {
	text := "Interpretación · revisión " + strconv.Itoa(v.Revision) + " · " + v.ViaText
	if v.Modelo != "" {
		text += " · modelo " + v.Modelo
	}
	if v.ReanalisisDe > 0 {
		text += " · re-análisis de la revisión " + strconv.Itoa(v.ReanalisisDe)
	}
	return text
}

// UnidadesText redacta cuántas unidades se interpretaron. Es la mitad de la comparación: al lado del
// texto del cliente, «3 unidades interpretadas» es lo que hace saltar a la vista que se pidió una.
func (v *comparacionView) UnidadesText() string {
	return cuenta(v.Unidades, "unidad interpretada", "unidades interpretadas") + " en " +
		cuenta(v.NumLineas, "línea", "líneas")
}

// HayHistorico responde si hay más de una interpretación que comparar. Con una sola no se pinta una
// navegación de un elemento, que solo diría «estás donde estás».
func (v *comparacionView) HayHistorico() bool { return len(v.Revisiones) > 1 }

// comparacionDe arma el §7.6 desde el detalle. Devuelve nil cuando no hay ninguna revisión
// `interpreted` (o su payload no se puede leer): la comparación no se pinta a medias, igual que el
// §7.5, y la página ya dice en ese caso que no hay interpretación.
//
// `pedida` es el `?revision=N` de la query: sin él —o con uno que no existe— se mira la ÚLTIMA.
func comparacionDe(detalle *apiclient.IntakeDetail, ent entitlementsView, pedida int) *comparacionView {
	revisiones := detalle.RevisionsOf(apiclient.RevisionKindInterpreted)
	if len(revisiones) == 0 {
		return nil
	}

	actual := revisiones[len(revisiones)-1]
	inexistente := 0
	if pedida > 0 && pedida != actual.RevisionNo {
		if hallada := revisionNumerada(revisiones, pedida); hallada != nil {
			actual = hallada
		} else {
			inexistente = pedida
		}
	}

	payload, err := apiclient.DecodeInterpretation(actual.Payload)
	if err != nil {
		return nil
	}

	view := &comparacionView{
		SolicitudID:         detalle.ID,
		Revision:            actual.RevisionNo,
		CreadaEl:            fecha(actual.CreatedAt),
		RolText:             rolDeRevisionText(actual.CreatedBy),
		ViaText:             viaText(payload.Analysis.Provider),
		Modelo:              payload.Analysis.Model,
		Origen:              origenDe(actual, payload),
		RevisionInexistente: inexistente,
	}
	if payload.Analysis.ReanalyzedFrom != nil {
		view.ReanalisisDe = *payload.Analysis.ReanalyzedFrom
	}
	for _, line := range payload.Lines {
		// El envío lo pone wApp y no es algo que el cliente pidiera: contarlo entre «lo que se
		// entendió» inflaría la discrepancia con una línea que no sale de su texto.
		if line.Kind == apiclient.LineKindShipping {
			continue
		}
		view.Lineas = append(view.Lineas, lineaDe(line))
		view.Unidades += line.Qty
		view.NumLineas++
	}
	view.Revisiones = enlacesDeRevision(detalle.ID, revisiones, actual.RevisionNo)
	view.Regenerar = regenerarDe(ent, view.Origen)
	return view
}

// revisionNumerada busca una interpretación por su NÚMERO (nil si no está). Va por `revision_no` y no
// por índice por lo mismo que LastRevisionOf: el orden del histórico no es contrato.
func revisionNumerada(revisiones []*apiclient.IntakeRevision, no int) *apiclient.IntakeRevision {
	for _, rev := range revisiones {
		if rev.RevisionNo == no {
			return rev
		}
	}
	return nil
}

// origenDe decide cuál de los TRES casos del literal es, y es el corazón de D-044.52 §3.
//
// 🔴 La pregunta es de PRESENCIA DE CLAVE, no de valor:
//
//   - `source_text` con texto              ⇒ hay literal;
//   - sin texto y SIN `literal_pruned_at`  ⇒ nunca lo hubo (`never_stored`);
//   - sin texto y CON `literal_pruned_at`  ⇒ se podó (`purged`), y trae cuándo.
//
// Un `source_text` vacío cuenta como ausente y no es una licencia: la plataforma lo emite con
// `omitempty`, y un literal vacío tampoco sería un literal. Lo que NO se puede colapsar es la otra
// clave, y por eso viaja como puntero.
func origenDe(rev *apiclient.IntakeRevision, payload *apiclient.IntakeInterpretation) origenView {
	if texto := strings.TrimSpace(payload.SourceText); texto != "" {
		return origenView{Texto: payload.SourceText, Presente: true}
	}
	if rev.LiteralPruned() {
		return origenView{Razon: apiclient.SourcePurged, PodadoEl: fecha(rev.PrunedAt())}
	}
	return origenView{Razon: apiclient.SourceNeverStored}
}

// viaText redacta la vía de una revisión. El vacío se dice «vía no registrada» y JAMÁS «LLM local»:
// ver el comentario de viaDesconocida. Una vía desconocida se pinta TAL CUAL, misma doctrina que
// statusLabel — antes una clave cruda que una traducción inventada.
func viaText(provider string) string {
	if strings.TrimSpace(provider) == "" {
		return viaDesconocida
	}
	return "vía " + provider
}

// rolDeRevisionText redacta QUIÉN dejó la revisión. Es un ROL y se dice como tal: la plataforma
// publica `system`/`owner`/`crm` y no publica personas, así que esta consola no puede pintar un
// nombre ni insinuarlo (cero PII). Un rol desconocido se pinta tal cual.
func rolDeRevisionText(createdBy string) string {
	switch createdBy {
	case apiclient.RevisionBySystem:
		return "la dejó el sistema (rol `system`)"
	case apiclient.RevisionByOwner:
		return "la dejó la dueña (rol `owner`)"
	case apiclient.RevisionByCRM:
		return "la dejó el CRM (rol `crm`)"
	}
	if strings.TrimSpace(createdBy) == "" {
		return "no consta qué rol la dejó"
	}
	return "la dejó el rol `" + createdBy + "`"
}

// enlacesDeRevision arma la navegación. Cada entrada enseña SU vía —que es el punto de comparar— y la
// actual va sin URL.
func enlacesDeRevision(solicitudID string, revisiones []*apiclient.IntakeRevision, actual int) []enlaceRevision {
	enlaces := make([]enlaceRevision, 0, len(revisiones))
	for _, rev := range revisiones {
		e := enlaceRevision{
			Revision: rev.RevisionNo,
			ViaText:  viaDesconocida,
			Actual:   rev.RevisionNo == actual,
		}
		// La vía vive DENTRO del payload, así que cada entrada hay que abrirla. Un payload ilegible
		// no tumba la navegación: esa revisión se lista con la vía sin registrar, que es lo mismo que
		// dice cuando el campo viene vacío.
		if payload, err := apiclient.DecodeInterpretation(rev.Payload); err == nil {
			e.ViaText = viaText(payload.Analysis.Provider)
		}
		if !e.Actual {
			e.URL = revisionURL(solicitudID, rev.RevisionNo)
		}
		enlaces = append(enlaces, e)
	}
	return enlaces
}

// solicitudURL es la pantalla de UNA solicitud. Se compone en UN solo sitio porque lo van a usar el
// enlace de vuelta, la navegación por revisiones y —cuando lleguen las acciones— el destino de sus
// redirecciones: dos literales iguales escritos aparte se desalinean el día que alguien mueva la ruta.
func solicitudURL(solicitudID string) string {
	return rutaSolicitudes + "/" + url.PathEscape(solicitudID)
}

// revisionURL arma el enlace a una revisión del detalle.
//
// 🔴 El literal del cliente NUNCA entra en esta URL: lo que viaja es un número. El log de acceso
// escribe el `path` de cada petición, así que meter texto de negocio en una URL sería escribirlo en
// el log sin que ninguna revisión de código lo viera venir.
func revisionURL(solicitudID string, revision int) string {
	return solicitudURL(solicitudID) + "?revision=" + strconv.Itoa(revision)
}

// regenerarDe decide si el botón Regenerar se puede pulsar, y si no, POR QUÉ.
//
// El orden de los motivos es el del contrato §8.1 y no es cosmético: el gate de capacidad corta ANTES
// que la comprobación de la fuente, así que cuando faltan las dos cosas lo que se ve es lo que la
// plataforma diría — «tu plan no lo incluye», no «no hay original».
//
// Los otros dos bordes —el add-on `api_llm` y la credencial— NO se pueden anticipar aquí y no es un
// hueco: para saber que la vía efectiva es `api` habría que leer la configuración de la empresa, y
// esa lectura exige justamente el add-on que falta. Llegan como RECHAZO del re-análisis (T7.4).
func regenerarDe(ent entitlementsView, origen origenView) regenerarView {
	view := regenerarView{MaxRunas: maxRunasRegeneracion}
	switch {
	case !ent.Has(featureLLMIntake):
		view.Paywall = true
		view.Razon = "El plan de tu empresa no incluye el análisis con IA (`" + featureLLMIntake +
			"`), así que la plataforma rechazaría la regeneración. La bandeja se lee igual: lo que " +
			"falta es volver a interpretar."
	case !origen.Presente:
		view.Razon = origen.RazonText()
	default:
		view.Habilitado = true
	}
	return view
}

// campoRegenerarTexto es el material EXTRA del formulario de Regenerar. Constante por lo mismo que
// los otros campos: lo lee el handler y lo escribe la plantilla.
const campoRegenerarTexto = "reanalyze_text"

// RegenerarSolicitud pide re-interpretar la solicitud desde el literal original del cliente (T7.4,
// sobre el endpoint del Plan 044 · T4.6).
//
// 🔴 NO LE HABLA AL CLIENTE: reinterpreta un texto que ya estaba guardado y el cliente no se entera.
// Por eso vive en esta casilla y no con aprobar o pedir información.
//
// 🔴 Y NO devuelve la interpretación nueva: la plataforma abre un trabajo que corre por detrás y
// responde con el número que la revisión TENDRÁ. Al volver, el detalle se relee y sigue enseñando la
// anterior. El aviso de éxito lo dice con esas palabras, porque un «listo» dejaría a la dueña
// recargando y creyendo que falló.
//
// 🔴 NO se manda `via`: cambiar de vía es un acto de configuración de la empresa, no un botón de la
// bandeja (D-044.51).
//
// 🔒 EL REPARTO DE DESENLACES (D-047.16), Y AQUÍ ESTÁ LA CORRECCIÓN AL ENUNCIADO DE LA CASILLA:
//
//	material extra > maxRunasRegeneracion .... 400 REPINTANDO, con lo tecleado en el textarea.
//	falta `llm_intake` ....................... 403 REPINTANDO, con lo tecleado igual.
//	error de la API (403, 409, 422, 502…) .... 303 + flash.
//	éxito .................................... 303 + flash.
//
// 🔒 LAS DOS PRIMERAS REPINTAN Y NO COMPARTEN CÓDIGO, y esa asimetría es la decisión: el código de
// estado y el repintado son cosas INDEPENDIENTES. D-047.16 acota el 400 a la VALIDACIÓN, y una
// denegación por plan no es una validación — la petición está perfecta, lo que falta es lo
// contratado, y decir 400 sobre eso es falso. El 403 lo fija esta misma ola para la falta de
// feature (solicitudes_gate.go): si `cart_basic` cortara con 403 y `llm_intake` con 400, la consola
// diría dos cosas distintas sobre el mismo hecho sin que nadie lo hubiera decidido.
//
// Lo que las une es lo otro: las dos REPINTAN, porque las dos cortan antes de salir a la red y las
// dos tienen un textarea con algo dentro que se perdería en un 303.
//
// 📌 El plan daba esta acción por «botón sin cuerpo tecleado» y por tanto entera del lado del 303.
// Se comprobó en el código del origen y es FALSO: lleva un `<textarea name="reanalyze_text">` —
// material extra OPCIONAL que SUMA al literal, no lo sustituye— con validación local de longitud
// (`intakeReanalyzeMaxRunes = 280`, intakes_reanalyze.go:57), que responde 400 diciendo cuántas
// runas van. Opcional no es inexistente: quien pega la transcripción de una llamada y se pasa por
// veinte caracteres tiene mucho que perder. Entra en la excepción.
//
// La que SÍ es un botón sin cuerpo es la sugerencia de la respuesta, y es de T7.6.
func (h *AdminHandler) RegenerarSolicitud(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if sinEmpresa(c) || id == "" {
		c.Redirect(http.StatusSeeOther, rutaSolicitudes)
		return
	}

	// El material extra NO se recorta con formValue: lo que se tecleó vuelve tal cual al textarea si
	// hay que repintar. El TrimSpace se aplica solo para MEDIRLO y para decidir si hay algo que
	// mandar — mismo criterio que la definición de un flujo en PublishFlow.
	crudo := c.PostForm(campoRegenerarTexto)
	texto := strings.TrimSpace(crudo)

	// Se cuenta en RUNAS y no en bytes porque el contrato habla de runas, y porque un texto con
	// acentos —el caso normal en español— cabría de sobra en runas y se pasaría en bytes. El
	// `maxlength` de la plantilla no sirve de guarda: es del navegador, y además cuenta unidades
	// UTF-16.
	if runas := utf8.RuneCountInString(texto); runas > maxRunasRegeneracion {
		h.renderSolicitudDetalle(c, detalleRender{
			id: id, status: http.StatusBadRequest, code: flashRegeneracionTextoLargo,
			textoRegenerar: crudo, runasRegenerar: runas,
		})
		return
	}

	// El gate de `llm_intake` vive DENTRO del servicio de la plataforma y no en su middleware, así
	// que la bandeja abre y esta ruta no: el gate por ruta de esta consola solo cubre `cart_basic`.
	// Se comprueba aquí para no gastar el viaje, sobre la MISMA vista que sembró ese gate —no se
	// resuelve el plan una segunda vez—, y sobre el mismo predicado con el que regenerarDe apagó el
	// botón. El botón deshabilitado no es una guarda: un POST hecho a mano no tiene `disabled`.
	//
	// 🔴 EL 403 ES EL SEGUNDO DE ESTA CONSOLA, y el primero fuera del middleware. Está declarado en el
	// candado que lo vigila (TestGate_ElUNICO403DeLaConsolaEsElDeLaFeature): es el MISMO hecho que
	// corta el gate —falta una capacidad del plan— dicho desde el otro lado de la puerta, así que
	// tiene que responder lo mismo. Lo que cambia es que aquí se repinta en vez de servir la pantalla
	// vacía, porque aquí hay un textarea con algo dentro.
	if !entitlementsFromContext(c).Has(featureLLMIntake) {
		h.renderSolicitudDetalle(c, detalleRender{
			id: id, status: http.StatusForbidden, code: flashRegeneracionSinPlan,
			textoRegenerar: crudo,
		})
		return
	}

	var regenerarErr error
	code := flashCodeForRegeneracion(h.auth.withAuthRetry(c, func(accessToken string) error {
		_, err := h.api.Intakes.ReanalyzeIntake(c.Request.Context(), accessToken, id, texto)
		regenerarErr = err
		return err
	}))
	if sessionIsDead(regenerarErr) {
		h.auth.expireSession(c)
		return
	}
	if code != "" {
		// 🔴 EL MATERIAL EXTRA NO ENTRA EN EL LOG. Es texto que puede traer lo que un cliente dijo por
		// otro canal, y esta consola no lo escribe en ningún sitio del camino (misma regla que el
		// `Cache-Control: no-store` de la pantalla).
		slog.Warn("no se pudo encargar la regeneración", "codigo", code, "error", regenerarErr)
	}
	redirectWith(c, solicitudURL(id), code, flashRegeneracionEncargada)
}
