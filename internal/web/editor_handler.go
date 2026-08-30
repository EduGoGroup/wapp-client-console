package web

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
)

// editor_handler.go sirve las dos pantallas del EDITOR: los FLUJOS que la conversación recorre y los
// DISPARADORES que deciden cuándo se entra en ellos (Plan 047 · T6.3 y T6.4).
//
// 📌 Es la casa nueva de lo que hoy vive en wapp-guardian-bff (`editor_handler.go` + `flows.html`,
// `flow_detail.html` y `triggers.html`). Lo que se muda es lo que aquellas pantallas HACEN, medido
// contra el código y no supuesto, con cuatro diferencias declaradas y ninguna accidental:
//
//  1. 🔒 EL PRG NO ES UNIVERSAL AQUÍ, y es la decisión más importante de estas dos pantallas
//     (D-047.16). En el BFF ninguno de los tres POST redirige: los tres REPINTAN con el código del
//     desenlace y devuelven lo tecleado. En esta consola el PRG era universal, y un 303 se lleva por
//     delante el 400 y lo escrito con él. La regla que se aplica —y que está escrita también junto a
//     redirectWith— es: validación que falla ANTES de llamar a la API ⇒ 400 REPINTANDO con el
//     formulario intacto; error de la API o éxito ⇒ 303 + flash. El PRG existe para que recargar no
//     reenvíe una MUTACIÓN, y un rechazo local no mutó nada.
//  2. el DESENLACE MALO DEL DETALLE se simplifica. El BFF, ante un 404/502 de `GetFlow`, renderiza
//     `flows.html` —la lista, no el detalle— con una llamada EXTRA a `ListFlows`. Aquí va por 303 a
//     la lista con su flash: el usuario acaba en la misma pantalla, la explicación sale del catálogo
//     y no hay una segunda llamada dentro del camino de error.
//  3. el DISTINTIVO «PROVISIONAL — migra a la consola de administración» NO se muda. Aquí ya no son
//     provisionales: están en su casa.
//  4. los textos salen del CATÁLOGO DE FLASH y nunca del upstream. El BFF llevaba el cuerpo del
//     error de la plataforma hasta la pantalla («La plataforma rechazó la definición: node "x" …»);
//     eso enseña a la dueña un texto escrito para desarrolladores y deja que la plataforma decida qué
//     dice esta consola. Ver el docstring de EditorClient.
//
// Lo que se muda TAL CUAL, porque es contrato y no descuido: el valor mágico del detalle, el modo
// degradado de los dos listados, el catálogo local de tipos de evento SIN `cart_llm`, y que el
// `event_kind` no viaje salvo en `event_start`.

const (
	rutaFlujos       = "/flujos"
	rutaDisparadores = "/disparadores"

	// flujoNuevo es el VALOR MÁGICO del detalle: `/flujos/nuevo` pinta el formulario de alta con la
	// definición de arranque y NO llama a la API. Se conserva del BFF (allí es `new`) porque publicar
	// es siempre una versión nueva y no hay endpoint de «crear»: el alta y la edición son la misma
	// pantalla y el mismo POST.
	//
	// ⚠️ El precio, dicho: un flujo que se llamara literalmente «nuevo» no se podría abrir desde
	// aquí. Es el mismo precio que paga el BFF con `new` y el mismo que /sesiones/enviar paga con una
	// sesión llamada «enviar»; a diferencia de aquella, el `flow_id` sí lo escribe el cliente, así que
	// esto es una limitación real y no una imposible.
	flujoNuevo = "nuevo"
)

// definicionDeArranque es lo que trae el textarea de un flujo nuevo: un menú mínimo que publica bien
// tal cual, para que la primera pantalla no sea un cuadro vacío.
//
// 🔴 Tiene que ser VÁLIDA contra el esquema del Motor. Un ejemplo que su propio validador rechaza
// enseña a copiar lo que no funciona —el mismo defecto que dejó a P4 en 0 de 14 en campo por un
// `package_size: 0` en la plantilla de un prompt—, y aquí además el primer intento de la dueña
// acabaría en un 400 que no es culpa suya.
const definicionDeArranque = `{
  "flow_id": "mi-flujo",
  "version": 1,
  "initial": "inicio",
  "nodes": {
    "inicio": {
      "type": "menu",
      "prompt": "Hola, ¿qué necesitas?",
      "options": { "1": "info", "2": "fin" }
    },
    "info": { "type": "message", "text": "Aquí va la información.", "next": "fin" },
    "fin": { "type": "message", "text": "¡Hasta luego!", "next": null }
  }
}`

// flowView es una fila del listado de flujos. La plantilla no calcula: el «—» de una fecha ausente lo
// pone el HTML, que es donde se ve.
type flowView struct {
	FlowID    string
	Version   int
	CreatedAt string
}

// triggerView es una fila del listado de disparadores.
//
// `Sombreado` es la marca DERIVADA que calcula la plataforma (D-043.20 / REQ-27b): un `fallback` al
// que la lista de eventos del despachador se ofrece antes y que por tanto no llega a sonar. La
// pantalla la PINTA y no la calcula — y pintarla es lo único que impide que la marca exista en el
// servidor sin que nadie la vea nunca.
type triggerView struct {
	TriggerID string
	Kind      string
	EventKind string
	Keyword   string
	MatchType string
	FlowID    string
	Priority  int
	Enabled   bool
	SessionID string
	Sombreado bool
}

// triggerFormView son los OCHO campos del formulario de alta, tal como el usuario los dejó.
//
// Existe para D-047.16: cuando la validación LOCAL rechaza el alta, la pantalla repinta con estos
// ocho valores dentro. Es un struct y no un `gin.H` suelto a propósito — con el mapa, un campo que se
// dejara de rellenar se pintaría vacío sin que nada fallara, que es exactamente la regresión contra
// la que la decisión existe.
//
// `Priority` es CADENA y no entero: lo que se devuelve al formulario es lo que se tecleó, y si lo que
// se tecleó no era un número («dos»), un int no puede representarlo — se pintaría un 0 que el usuario
// no escribió.
type triggerFormView struct {
	Kind      string
	Keyword   string
	EventKind string
	MatchType string
	FlowID    string
	SessionID string
	Message   string
	Priority  string
}

// validEventKinds son los tipos de evento de fábrica del despachador (D-043.3, INV-07).
//
// 🔴 `cart_llm` NO está, y no es un olvido: se ofrece cuando la empresa tiene el plan que lo incluye,
// y esta pantalla no gatea por plan. Se muda tal cual del BFF.
var validEventKinds = map[string]bool{"menu": true, "cart": true, "survey": true, "media": true}

// ShowFlows pinta el listado de flujos del tenant (T6.3).
//
// DEGRADA en vez de fallar, igual que el BFF: si el listado no se puede leer, la pantalla responde
// 200 con el aviso arriba y el botón de «Nuevo flujo» intacto — que es lo único que se puede hacer
// sin catálogo, y justo lo que alguien necesita cuando algo va mal. Un 502 aquí dejaría a la dueña
// sin manera de publicar nada.
func (h *AdminHandler) ShowFlows(c *gin.Context) {
	if sinEmpresa(c) {
		renderer.HTML(c, http.StatusOK, "flujos.html", h.pageData(c, "Flujos"))
		return
	}

	var flujos []apiclient.FlowSummary
	var listErr error
	code := flashCodeForEditor(h.auth.withAuthRetry(c, func(accessToken string) error {
		var err error
		flujos, err = h.api.Editor.ListFlows(c.Request.Context(), accessToken)
		listErr = err
		return err
	}))
	if sessionIsDead(listErr) {
		h.auth.expireSession(c)
		return
	}

	data := h.pageData(c, "Flujos")
	if code != "" {
		slog.Warn("no se pudieron listar los flujos (modo degradado)", "codigo", code, "error", listErr)
		data["Error"] = flashError(code)
		data["FlowsError"] = true
	}
	data["Flows"] = flowsView(flujos)
	renderer.HTML(c, http.StatusOK, "flujos.html", data)
}

// ShowFlowDetail pinta el editor de UN flujo (T6.3).
//
// `/flujos/nuevo` no sale a la red: pinta el formulario con la definición de arranque. Ver flujoNuevo.
func (h *AdminHandler) ShowFlowDetail(c *gin.Context) {
	if sinEmpresa(c) {
		renderer.HTML(c, http.StatusOK, "flujo.html", h.pageData(c, "Flujo"))
		return
	}

	id := c.Param("id")
	if id == flujoNuevo {
		h.renderFlowDetail(c, "", true, definicionDeArranque)
		return
	}

	var crudo json.RawMessage
	var getErr error
	code := flashCodeForEditor(h.auth.withAuthRetry(c, func(accessToken string) error {
		var err error
		crudo, err = h.api.Editor.GetFlow(c.Request.Context(), accessToken, id)
		getErr = err
		return err
	}))
	if sessionIsDead(getErr) {
		h.auth.expireSession(c)
		return
	}
	if code != "" {
		// Un flujo que no se puede abrir manda a la LISTA, no a un detalle vacío: ahí están los que sí
		// existen, y el aviso explica cuál de los dos casos fue (frontera de empresa o upstream).
		slog.Warn("no se pudo cargar el flujo", "codigo", code, "error", getErr)
		redirectWith(c, rutaFlujos, code, "")
		return
	}
	h.renderFlowDetail(c, id, false, prettyJSON(crudo))
}

// renderFlowDetail pinta el detalle con la definición dada. `definicion` es SIEMPRE lo que se va a
// ver en el textarea: la de la plataforma al abrir, y la TECLEADA cuando se repinta un rechazo local.
func (h *AdminHandler) renderFlowDetail(c *gin.Context, flowID string, esNuevo bool, definicion string) {
	data := h.pageData(c, "Flujo")
	data["FlowID"] = flowID
	data["IsNew"] = esNuevo
	data["Definition"] = definicion
	renderer.HTML(c, http.StatusOK, "flujo.html", data)
}

// PublishFlow publica la definición del textarea como la versión N+1 (T6.3).
//
// 🔒 Los TRES desenlaces, y su reparto es D-047.16:
//
//	JSON inválido ....... 400 REPINTANDO, con el textarea intacto. Es validación local: no se llamó a
//	                      la plataforma, así que no hay mutación de la que el PRG proteja, y un 303
//	                      aquí le borraría a la dueña la definición entera que acaba de escribir.
//	error de la API ..... 303 + flash. Pudo mutar (la petición salió), que es justo el caso del PRG.
//	éxito ............... 303 + flash a la lista, donde se ve la versión nueva.
func (h *AdminHandler) PublishFlow(c *gin.Context) {
	flowID := formValue(c, "flow_id")
	esNuevo := c.PostForm("is_new") == "1"
	// La definición NO se recorta con formValue: lo que el usuario tecleó vuelve tal cual al textarea
	// si hay que repintar. El TrimSpace se aplica solo para juzgar si es JSON.
	definicion := c.PostForm("definition")

	if !json.Valid([]byte(strings.TrimSpace(definicion))) {
		slog.Warn("se rechazó una definición que no es JSON antes de salir a la red")
		h.repintarFlowDetail(c, flowID, esNuevo, definicion, flashFlowInvalidJSON)
		return
	}

	var publErr error
	code := flashCodeForEditor(h.auth.withAuthRetry(c, func(accessToken string) error {
		_, err := h.api.Editor.PublishFlow(c.Request.Context(), accessToken, []byte(definicion))
		publErr = err
		return err
	}))
	if sessionIsDead(publErr) {
		h.auth.expireSession(c)
		return
	}
	if code != "" {
		slog.Warn("no se pudo publicar el flujo", "codigo", code, "error", publErr)
	}
	redirectWith(c, rutaFlujos, code, flashFlowPublished)
}

// repintarFlowDetail es el 400 de D-047.16: la MISMA pantalla, con lo tecleado dentro y el aviso
// arriba.
//
// 🔴 El status es 400 y no 200, y el textarea lleva `definicion` y no la de la plataforma. Las dos
// cosas juntas son la decisión: con 200 el navegador no distingue el rechazo, y con el textarea
// repuesto de otra fuente el 400 se cumpliría «de palabra» y el usuario habría perdido lo escrito
// igual, que es lo que la decisión existe para impedir.
func (h *AdminHandler) repintarFlowDetail(c *gin.Context, flowID string, esNuevo bool, definicion, code string) {
	data := h.pageData(c, "Flujo")
	data["FlowID"] = flowID
	data["IsNew"] = esNuevo
	data["Definition"] = definicion
	data["Error"] = flashError(code)
	renderer.HTML(c, http.StatusBadRequest, "flujo.html", data)
}

// ShowTriggers pinta el listado de disparadores y el formulario de alta (T6.4).
func (h *AdminHandler) ShowTriggers(c *gin.Context) {
	h.renderTriggers(c, http.StatusOK, "", triggerFormView{})
}

// renderTriggers pinta la pantalla de disparadores con el formulario en el estado que se le dé.
//
// LLAMA AL LISTADO SIEMPRE, incluido el repintado de un rechazo local, y eso se mudó a propósito del
// BFF: la tabla es la mitad de esta pantalla, y repintar sin ella le enseñaría a la dueña un listado
// vacío justo después de un error — que se lee como «se han borrado».
func (h *AdminHandler) renderTriggers(c *gin.Context, status int, code string, form triggerFormView) {
	if sinEmpresa(c) {
		renderer.HTML(c, http.StatusOK, "disparadores.html", h.pageData(c, "Disparadores"))
		return
	}

	var reglas []apiclient.Trigger
	var listErr error
	listCode := flashCodeForEditor(h.auth.withAuthRetry(c, func(accessToken string) error {
		var err error
		reglas, err = h.api.Editor.ListTriggers(c.Request.Context(), accessToken)
		listErr = err
		return err
	}))
	if sessionIsDead(listErr) {
		h.auth.expireSession(c)
		return
	}

	data := h.pageData(c, "Disparadores")
	if listCode != "" {
		slog.Warn("no se pudieron listar los disparadores (modo degradado)", "codigo", listCode, "error", listErr)
		data["TriggersError"] = true
		// El aviso del listado NO pisa al del formulario: quien acaba de recibir un rechazo de su alta
		// necesita leer ESE, y que la tabla no cargara ya se ve en su propio hueco.
		if code == "" {
			data["Error"] = flashError(listCode)
		}
	}
	if code != "" {
		data["Error"] = flashError(code)
	}
	data["Triggers"] = triggersView(reglas)
	data["Form"] = form
	renderer.HTML(c, status, "disparadores.html", data)
}

// CreateTrigger crea un disparador desde el formulario (T6.4).
//
// 🔒 Mismo reparto que PublishFlow (D-047.16): la prioridad no entera y `validateTriggerForm` son
// validación LOCAL y repintan con 400 conservando los OCHO campos; el desenlace de la API —incluido
// el 422 que sí existe— va por 303 + flash.
func (h *AdminHandler) CreateTrigger(c *gin.Context) {
	form := triggerFormView{
		Kind:      formValue(c, "kind"),
		Keyword:   formValue(c, "keyword"),
		EventKind: formValue(c, "event_kind"),
		MatchType: formValue(c, "match_type"),
		FlowID:    formValue(c, "flow_id"),
		SessionID: formValue(c, "session_id"),
		Message:   formValue(c, "message"),
		Priority:  formValue(c, "priority"),
	}

	prioridad := 0
	if form.Priority != "" {
		p, err := strconv.Atoi(form.Priority)
		if err != nil {
			h.renderTriggers(c, http.StatusBadRequest, flashTriggerPriorityNotInteger, form)
			return
		}
		prioridad = p
	}

	if code := validateTriggerForm(form.Kind, form.Keyword, form.FlowID, form.EventKind); code != "" {
		h.renderTriggers(c, http.StatusBadRequest, code, form)
		return
	}

	// 🔴 `event_kind` NO VIAJA salvo en `event_start`, y es una invariante de FORMA DEL CUERPO, no una
	// comodidad: el `<select>` conserva el valor de un envío anterior, así que el navegador manda un
	// `event_kind` residual cuando el usuario cambia el tipo a `keyword` después de haber probado un
	// `event_start`. Mandarlo le pediría a la plataforma que guardara un tipo de evento en una regla
	// que no arranca ninguno.
	eventKind := ""
	if form.Kind == "event_start" {
		eventKind = form.EventKind
	}

	var crearErr error
	code := flashCodeForEditor(h.auth.withAuthRetry(c, func(accessToken string) error {
		_, err := h.api.Editor.CreateTrigger(c.Request.Context(), accessToken, apiclient.CreateTriggerRequest{
			Kind:      form.Kind,
			Keyword:   form.Keyword,
			EventKind: eventKind,
			MatchType: form.MatchType,
			FlowID:    form.FlowID,
			Priority:  prioridad,
			Message:   form.Message,
			SessionID: form.SessionID,
		})
		crearErr = err
		return err
	}))
	if sessionIsDead(crearErr) {
		h.auth.expireSession(c)
		return
	}
	if code != "" {
		slog.Warn("no se pudo crear el disparador", "codigo", code, "error", crearErr)
	}
	redirectWith(c, rutaDisparadores, code, flashTriggerCreated)
}

// validateTriggerForm comprueba los campos que cada tipo de disparador necesita y devuelve el CÓDIGO
// de flash del primer fallo, o cadena vacía si el formulario está completo.
//
// Devuelve un CÓDIGO y no un texto porque el vocabulario de esta consola es cerrado (ver flash.go):
// un mensaje escrito aquí sería el único texto de la consola que no sale del catálogo, y el primero
// en decir otra cosa que sus vecinos ante el mismo desenlace.
//
// Son OCHO desenlaces distintos y no uno genérico porque cada uno dice QUÉ CAMPO falta: ante ocho
// campos, un «revisa el formulario» deja a quien administra probando a ciegas.
func validateTriggerForm(kind, keyword, flowID, eventKind string) string {
	switch kind {
	case "keyword":
		if keyword == "" || flowID == "" {
			return flashTriggerKeywordIncomplete
		}
	case "fallback":
		if flowID == "" {
			return flashTriggerFallbackWithoutFlow
		}
	case "escape":
		if keyword == "" {
			return flashTriggerEscapeWithoutKeyword
		}
	case "event_start":
		if keyword == "" {
			return flashTriggerEventStartNoKeyword
		}
		if eventKind == "" {
			return flashTriggerEventStartNoKind
		}
		if !validEventKinds[eventKind] {
			return flashTriggerEventKindUnknown
		}
	case "event_stop":
		if keyword == "" {
			return flashTriggerEventStopWithoutKey
		}
	default:
		return flashTriggerKindUnknown
	}
	return ""
}

// DeleteTrigger borra un disparador (T6.4).
//
// 🔴 SIEMPRE 303 + flash, también cuando falla, y eso NO contradice a D-047.16: la excepción del
// repintado es para lo que el usuario TECLEA, y aquí no hay nada que perder —el borrado es un
// `<form>` de un solo botón dentro de la fila—. Repintarlo solo cambiaría el código de estado.
//
// El navegador manda POST; el DELETE que la plataforma espera lo pone el cliente de la API. Esta
// consola es SSR pura, sin una línea de JavaScript (ver server.go).
func (h *AdminHandler) DeleteTrigger(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		redirectWith(c, rutaDisparadores, flashMissingField, "")
		return
	}

	var delErr error
	code := flashCodeForEditor(h.auth.withAuthRetry(c, func(accessToken string) error {
		err := h.api.Editor.DeleteTrigger(c.Request.Context(), accessToken, id)
		delErr = err
		return err
	}))
	if sessionIsDead(delErr) {
		h.auth.expireSession(c)
		return
	}
	if code != "" {
		slog.Warn("no se pudo borrar el disparador", "codigo", code, "error", delErr)
	}
	redirectWith(c, rutaDisparadores, code, flashTriggerDeleted)
}

// flowsView proyecta el listado de la API a filas de la tabla.
func flowsView(flujos []apiclient.FlowSummary) []flowView {
	out := make([]flowView, 0, len(flujos))
	for _, f := range flujos {
		out = append(out, flowView{FlowID: f.FlowID, Version: f.Version, CreatedAt: f.CreatedAt})
	}
	return out
}

// triggersView proyecta los disparadores a filas de la tabla, conservando la marca de sombreado.
func triggersView(reglas []apiclient.Trigger) []triggerView {
	out := make([]triggerView, 0, len(reglas))
	for _, t := range reglas {
		out = append(out, triggerView{
			TriggerID: t.TriggerID,
			Kind:      t.Kind,
			EventKind: t.EventKind,
			Keyword:   t.Keyword,
			MatchType: t.MatchType,
			FlowID:    t.FlowID,
			Priority:  t.Priority,
			Enabled:   t.Enabled,
			SessionID: t.SessionID,
			Sombreado: t.ShadowedByEventList,
		})
	}
	return out
}

// prettyJSON sangra la definición para que se pueda leer y editar en el textarea.
//
// Si no se puede sangrar se devuelve el original: la pantalla enseña lo que la plataforma tiene, no
// un hueco. Un flujo que llegara con un JSON raro se sigue pudiendo ver y corregir a mano, que es
// justo cuando más falta hace.
func prettyJSON(crudo json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, crudo, "", "  "); err != nil {
		return string(crudo)
	}
	return buf.String()
}
