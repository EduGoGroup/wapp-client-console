package web

import (
	"log/slog"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
)

// sessions_handler.go sirve la pantalla de SESIONES: los teléfonos vinculados de la empresa, su
// perfil (activa/pasiva), la salud del clasificador y el envío de un mensaje de prueba.
//
// 📌 Es la casa nueva de lo que hoy vive en wapp-guardian-bff (`dashboard_handler.go` +
// `dashboard.html`). Lo que se porta es la PANTALLA; el BFF se retira en otra tanda, así que durante
// un tiempo las dos existen. Dos cosas se portaron a propósito CON su sentido y no con su letra:
//
//  1. las mutaciones van POST-redirect-GET, como el resto de esta consola, y no re-renderizando como
//     el BFF. La consecuencia visible es que un envío fallido NO conserva lo escrito en el
//     formulario: es el mismo precio que ya paga el alta de un miembro, y a cambio un F5 no repite
//     la operación — que en un envío a un cliente real significa no mandarle el mensaje dos veces;
//  2. el aviso sobre la privacidad de la sesión pasiva se ESCRIBIÓ DE NUEVO, porque el del BFF ya no
//     es verdad. Ver el comentario de sesiones.html.
//
// Lo que NO se porta, y no por olvido: la tarjeta «Plan y capacidades» (la portada de esta consola ya
// pinta los chips del plan; duplicarla sería crear la segunda casa de algo que ya tiene una) y los
// bloques del final gateados por `cart_basic` y `llm_intent`, que no son del dashboard de sesiones y
// cuyas pantallas siguen viviendo en el BFF.

// chipView es un chip ya resuelto: texto, variante de color y el tooltip que lo explica.
//
// Los chips se resuelven en Go y no en la plantilla —a diferencia del BFF, que los decide con una
// cadena de `{{ if eq }}`— por dos motivos: los tooltips son párrafos largos que en la plantilla se
// vuelven ilegibles, y así el mapeo se puede afirmar en un test de tabla sin pasar por el HTML.
type chipView struct {
	Text  string
	Class string
	Title string
}

// sessionView es una fila de la tabla de sesiones, con todo ya decidido: la plantilla no calcula.
type sessionView struct {
	// SessionID es el identificador que viaja en la ruta del formulario de perfil.
	SessionID string
	// Label es lo que se lee en la primera celda: el número propio si la plataforma lo sirve, y el
	// identificador de sesión si no.
	Label  string
	EdgeID string
	Estado chipView
	// Circuito y CPU son la columna «Clasificador»: el breaker del clasificador de intenciones y el
	// reparto de CPU entre el cajero y Ollama.
	Circuito chipView
	CPU      chipView
	// Profile es "active", "passive" o "" (DESCONOCIDO). Sale de EffectiveProfile, nunca del campo
	// crudo, y el "" decide que el <select> pinte su opción «sin dato».
	Profile string
}

// avisoCPUDelArranque viaja en el tooltip de los TRES chips de reparto de CPU, y no en uno solo.
//
// El veredicto del `taskset` NO se recalcula: el Edge lo mide UNA VEZ, al arrancar el worker-cajero
// (`veredictoTaskset` se escribe en `Run`, antes del bucle) y lo republica igual en cada parte. Un
// cambio de afinidad en caliente deja esta pantalla enseñando un reparto que ya no existe, y la regla
// de rancidez del parte no puede cazarlo porque el parte SÍ se refresca: es un valor obsoleto, no
// rancio.
//
// 🔴 Habiendo elegido DECLARAR el límite en vez de arreglarlo, la declaración tiene que estar en los
// tres valores conocidos y no solo en el bonito: quien mira «CPU solapada» para decidir si toca un
// taskset es justo quien más necesita saber que tendrá que reiniciar el cajero para ver el resultado.
// El chip del breaker NO lo lleva: ese sí es continuo, y ponérselo diría una mentira distinta.
const avisoCPUDelArranque = "Se calcula UNA SOLA VEZ, al arrancar el cajero, y NO se recalcula: " +
	"si cambias la afinidad en caliente, reinicia el cajero para que este valor vuelva a ser cierto."

// ShowSessions pinta la pantalla de sesiones (T2.1).
//
// Hace UNA llamada —el listado— y degrada: si falla, la pantalla sigue en pie con el aviso arriba y
// el formulario de envío pidiendo el identificador de sesión a mano, que es lo único que se puede
// hacer sin catálogo. Es el mismo comportamiento del BFF y no es un adorno: el envío es la operación
// que alguien necesita justo cuando algo va mal.
//
// La excepción es el 401 que sobrevivió al refresco: ahí la sesión ya no vale y lo que toca es
// expulsar a /login, no pintar una pantalla que el usuario no puede usar.
func (h *AdminHandler) ShowSessions(c *gin.Context) {
	// Sin empresa no hay sesiones que listar, y la API respondería 403 —«no tienes permiso»—, que es
	// un diagnóstico falso: no le falta un permiso, le falta una empresa. Ver sinEmpresa().
	if sinEmpresa(c) {
		renderer.HTML(c, http.StatusOK, "sesiones.html", h.pageData(c, "Sesiones"))
		return
	}

	var sesiones []apiclient.Session
	var listErr error
	code := flashCodeForSessions(h.auth.withAuthRetry(c, func(accessToken string) error {
		var err error
		sesiones, err = h.api.Sessions.List(c.Request.Context(), accessToken)
		listErr = err
		return err
	}))
	if sessionIsDead(listErr) {
		h.auth.expireSession(c)
		return
	}

	data := h.pageData(c, "Sesiones")
	if code != "" {
		slog.Warn("no se pudieron listar las sesiones de la empresa (modo degradado)", "codigo", code, "error", listErr)
		data["Error"] = flashError(code)
		data["SessionsError"] = true
	}
	data["Sessions"] = sessionsView(sesiones)
	data["Ack"] = ackSeguro(c.Query(ackParam))
	renderer.HTML(c, http.StatusOK, "sesiones.html", data)
}

// SendTestMessage manda un mensaje de prueba por una de las sesiones de la empresa (T2.1).
//
// 🔴 Un 200 de la plataforma NO es un envío entregado: es el ACUSE del Edge, y el acuse puede traer
// `ok:false` —el equipo recibió el comando y su ejecución falló—. Dar eso por éxito le diría a la
// dueña que el mensaje salió cuando no salió, así que se distingue (flashSendNotDelivered).
func (h *AdminHandler) SendTestMessage(c *gin.Context) {
	sessionID := formValue(c, "session_id")
	to := formValue(c, "to")
	text := formValue(c, "text")

	// Los tres campos son obligatorios y se comprueban ANTES de salir a la red: la plataforma
	// respondería 400 y el usuario habría esperado un viaje entero para leer lo mismo.
	if sessionID == "" || to == "" || text == "" {
		redirectWith(c, "/sesiones", flashMissingField, "")
		return
	}

	var result *apiclient.SendResult
	code := flashCodeForSessions(h.auth.withAuthRetry(c, func(accessToken string) error {
		var err error
		result, err = h.api.Sessions.SendMessage(c.Request.Context(), accessToken, sessionID, to, text)
		return err
	}))
	if code != "" {
		// Sin el destino ni el texto: en el log de esta consola no entra ni el número de un tercero
		// ni lo que se le escribió.
		slog.Warn("no se pudo enviar el mensaje de prueba", "codigo", code)
		redirectWith(c, "/sesiones", code, "")
		return
	}
	if result == nil || !result.OK {
		// El detalle que da el equipo se queda en el LOG: en pantalla importa el estado en que
		// quedaron las cosas —recibido y no entregado—, y el texto sale del catálogo.
		detalle := ""
		if result != nil {
			detalle = result.Error
		}
		slog.Warn("el equipo aceptó el comando y no pudo entregarlo", "detalle", detalle)
		redirectWith(c, "/sesiones", flashSendNotDelivered, "")
		return
	}
	redirectWithAck(c, "/sesiones", flashMessageSent, result.AckedCommandID)
}

// SetSessionProfile cambia el perfil de negocio de una sesión: activa (conversa sola) o pasiva (solo
// envía). ADR-0027 · Plan 046.
//
// 🔴 El perfil se valida AQUÍ, antes de salir a la red, y el caso que importa es la cadena vacía: es
// el `value` del <option> «sin dato» que la tabla pinta cuando no se conoce el perfil, así que es
// exactamente lo que llega si alguien fuerza ese envío. Sin esta guarda, un perfil DESCONOCIDO
// acabaría en una llamada a la plataforma que nadie pidió.
func (h *AdminHandler) SetSessionProfile(c *gin.Context) {
	sessionID := c.Param("id")
	profile := formValue(c, "profile")

	if sessionID == "" {
		redirectWith(c, "/sesiones", flashMissingField, "")
		return
	}
	if !apiclient.ValidProfile(profile) {
		slog.Warn("se rechazó un perfil inválido antes de salir a la red")
		redirectWith(c, "/sesiones", flashInvalidProfile, "")
		return
	}

	if code := flashCodeForSessions(h.auth.withAuthRetry(c, func(accessToken string) error {
		return h.api.Sessions.SetProfile(c.Request.Context(), accessToken, sessionID, profile)
	})); code != "" {
		slog.Warn("no se pudo cambiar el perfil de la sesión", "codigo", code)
		redirectWith(c, "/sesiones", code, "")
		return
	}
	redirectWith(c, "/sesiones", "", profileSuccessCode(profile))
}

// profileSuccessCode elige el aviso de éxito según el perfil que quedó fijado. Son dos códigos y no
// uno con el nombre interpolado: ver el catálogo (flash.go).
func profileSuccessCode(profile string) string {
	if profile == apiclient.ProfilePassive {
		return flashProfilePassive
	}
	return flashProfileActive
}

// sessionsView proyecta la respuesta de la API a filas de la tabla, resolviendo los chips.
func sessionsView(sesiones []apiclient.Session) []sessionView {
	out := make([]sessionView, 0, len(sesiones))
	for _, s := range sesiones {
		etiqueta := s.SelfPn
		if etiqueta == "" {
			etiqueta = s.SessionID
		}
		out = append(out, sessionView{
			SessionID: s.SessionID,
			Label:     etiqueta,
			EdgeID:    s.EdgeID,
			Estado:    estadoChip(s.State),
			Circuito:  circuitoChip(s.IntentCircuit),
			CPU:       cpuChip(s.WorkerTaskset),
			// 🔴 EffectiveProfile y no `.Profile` a secas: normaliza a active|passive|"" y NUNCA
			// inventa un valor por defecto. Ver su test en internal/apiclient.
			Profile: s.EffectiveProfile(),
		})
	}
	return out
}

// estadoChip traduce el estado de conexión del teléfono.
func estadoChip(state string) chipView {
	switch state {
	case "online":
		return chipView{Text: "online", Class: "wapp-chip--success", Title: "El teléfono está conectado y puede enviar."}
	case "offline":
		return chipView{Text: "offline", Class: "wapp-chip--danger",
			Title: "El teléfono no está conectado ahora mismo: un envío por esta sesión fallará hasta que vuelva."}
	case "":
		return chipView{Text: "desconocido", Class: "wapp-chip--neutral", Title: "La plataforma no reporta el estado de esta sesión."}
	default:
		// Los demás estados de la flota (`loggedout`, `suspended`, …) se pintan tal cual: inventarles
		// una traducción aquí crearía un vocabulario que solo existe en esta pantalla.
		return chipView{Text: state, Class: "wapp-chip--neutral", Title: "Estado reportado por la plataforma para esta sesión."}
	}
}

// circuitoChip traduce el breaker del clasificador de intenciones (Plan 051 · Ola 4).
//
// 🔴 El caso por defecto es DESCONOCIDO y no «sano». El Edge manda el campo vacío a propósito cuando
// el parte del worker-cajero lleva más de 90 s sin refrescarse —o sea, cuando el cajero puede estar
// MUERTO—, así que pintar «closed» ahí publicaría la salud de un clasificador apagado.
func circuitoChip(circuit string) chipView {
	switch circuit {
	case "closed":
		return chipView{Text: "closed", Class: "wapp-chip--success",
			Title: "El breaker del clasificador está cerrado: clasificando con normalidad."}
	case "open":
		return chipView{Text: "open", Class: "wapp-chip--danger",
			Title: "El breaker está ABIERTO: el clasificador no está clasificando (Ollama caído o fallos repetidos)."}
	case "half_open":
		return chipView{Text: "half_open", Class: "wapp-chip--info",
			Title: "El breaker está probando si el clasificador se recuperó."}
	default:
		return chipView{Text: "desconocido", Class: "wapp-chip--neutral",
			Title: "Este equipo no reporta el breaker: el worker-cajero puede estar parado, o su parte lleva más " +
				"de 90 s sin refrescarse. NO significa que esté sano."}
	}
}

// cpuChip traduce el reparto de CPU entre el worker-cajero y Ollama (Plan 051 · Ola 4 · T4.6).
//
// Los TRES valores conocidos llevan avisoCPUDelArranque en el tooltip; el desconocido no, porque ahí
// no hay ningún veredicto que pueda haber envejecido.
func cpuChip(taskset string) chipView {
	switch taskset {
	case "disjunta":
		return chipView{Text: "CPU disjunta", Class: "wapp-chip--success",
			Title: "El cajero y Ollama corren en núcleos distintos. " + avisoCPUDelArranque}
	case "solapada":
		return chipView{Text: "CPU solapada", Class: "wapp-chip--danger",
			Title: "El cajero y Ollama comparten núcleos y se estorban; el síntoma que se ve es latencia. " +
				avisoCPUDelArranque}
	case "cajero_sin_confinar":
		return chipView{Text: "CPU sin confinar", Class: "wapp-chip--info",
			Title: "El cajero no tiene afinidad fijada (sin taskset). " + avisoCPUDelArranque}
	default:
		return chipView{Text: "CPU desconocida", Class: "wapp-chip--neutral",
			Title: "Este equipo no reporta el reparto de CPU (no es Linux, o el parte del cajero está rancio)."}
	}
}

// ackParam es el parámetro que transporta el identificador del comando acusado por el equipo desde el
// redirect del envío hasta la pantalla que lo pinta.
//
// Es la ÚNICA cosa de esta consola que viaja por el query string y acaba a la vista, y no es una
// excepción al principio de «el texto sale del catálogo»: no es un texto, es un identificador opaco
// —el hilo que correlaciona lo que la nube intentó con el outbox del equipo—, y por eso mismo pasa
// por ackSeguro en los dos extremos.
const ackParam = "ack"

// ackSeguro devuelve el acuse solo si tiene la forma de un identificador de comando; en cualquier
// otro caso, cadena vacía (la pantalla simplemente no pinta el chip).
//
// Se aplica en los DOS extremos, y cada uno tapa un agujero distinto:
//   - al ESCRIBIR el redirect, porque el valor viene del upstream y un `\r\n` en la cabecera Location
//     es partir una respuesta HTTP en dos;
//   - al LEER la query, porque ahí el valor lo escribe quien quiera: la URL la teclea el usuario.
//     El escapado de la plantilla ya impide inyectar HTML, pero un chip que pinta cualquier frase que
//     alguien ponga en la URL es un cartel gratis para un enlace enviado por correo.
func ackSeguro(v string) string {
	const maxAck = 64
	if v == "" || len(v) > maxAck {
		return ""
	}
	for _, r := range v {
		esAlfanumerico := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !esAlfanumerico && r != '-' && r != '_' {
			return ""
		}
	}
	return v
}

// redirectWithAck es redirectWith más el acuse del equipo (POST-redirect-GET). Un acuse que no pase
// ackSeguro se omite: el aviso de éxito se pinta igual, sin chip.
func redirectWithAck(c *gin.Context, path, okCode, ack string) {
	destino := path + "?success=" + okCode
	if seguro := ackSeguro(ack); seguro != "" {
		destino += "&" + ackParam + "=" + url.QueryEscape(seguro)
	}
	c.Redirect(http.StatusSeeOther, destino)
}
