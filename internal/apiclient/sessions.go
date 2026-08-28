package apiclient

import (
	"context"
	"net/http"
)

// Los dos PERFILES DE NEGOCIO de una sesión (ADR-0027, Plan 046 · T1.2). Son los identificadores que
// viajan por el cable; «activa»/«pasiva» es solo lo que ve la dueña en la pantalla, y esa asimetría es
// deliberada: el vocabulario del dueño se traduce, el dato no se renombra.
//
// 🔴 NO CONFUNDIR con `devices.role` del Edge (`primary`/`standby`, failover multi-dispositivo,
// ADR-0018): son dominios sin relación y el Edge no se renombra.
//
// Van como constantes —y no como literales sueltos— porque los usan TRES sitios que tienen que decir
// lo mismo: EffectiveProfile, la validación del formulario (internal/web) y el cuerpo que se manda.
const (
	// ProfileActive: la sesión conversa sola, contesta por su cuenta a quien le escribe.
	ProfileActive = "active"
	// ProfilePassive: la sesión SOLO envía. Sus entrantes se descartan en el equipo del cliente.
	ProfilePassive = "passive"
)

// ValidProfile dice si un perfil es uno de los dos que la plataforma acepta.
//
// Existe para que la consola pueda rechazar un valor imposible ANTES de salir a la red —la plataforma
// respondería 400 y el usuario esperaría un viaje entero para leer lo mismo—, y sobre todo para que
// el `value=""` del `<option>` placeholder de «sin dato» rebote aquí en vez de convertirse en un
// cambio de perfil que nadie pidió.
func ValidProfile(profile string) bool {
	return profile == ProfileActive || profile == ProfilePassive
}

// Session es una fila del listado GET /api/v1/sessions: los metadatos de OPERACIÓN de un teléfono
// vinculado. Cero credenciales y cero PII (la plataforma no las sirve por esta ruta).
//
// Solo están los campos que la pantalla PINTA. La API sirve además `last_connected_at`,
// `last_seen_at`, salud del Edge, profundidad de outbox y una decena más de contadores del Plan 051;
// no se declaran aquí porque un campo que nadie lee es una promesa de que alguien lo mantiene.
type Session struct {
	SessionID string `json:"session_id"`
	EdgeID    string `json:"edge_id"`
	State     string `json:"state"`

	// Profile es el PERFIL DE NEGOCIO de la sesión: "active" | "passive" (ADR-0027). Es el campo
	// vivo: lo que la consola pinta y lo que el formulario escribe. Se lee SIEMPRE por
	// EffectiveProfile, nunca crudo.
	Profile string `json:"profile,omitempty"`

	// SelfPn es el número propio de la sesión, cuando la plataforma lo sirve. Es la etiqueta legible
	// de la fila; sin él se pinta el identificador de sesión.
	SelfPn string `json:"self_pn,omitempty"`

	// Salud del clasificador de intenciones (Plan 051 · Ola 4 · T4.3). Son los dos únicos campos de
	// salud que esta consola lee: responden «¿está clasificando?» y «¿se estorban el cajero y
	// Ollama?» SIN ENTRAR EN LA MÁQUINA, que es el criterio de aquella tarea.
	//
	// 🔴 LOS DOS LLEGAN AUSENTES CUANDO EL EDGE NO LO SABE, y ausente NO es un valor por defecto: la
	// API los marca `omitempty` precisamente porque el Edge manda su cero a propósito cuando el parte
	// del worker-cajero lleva más de 90 s sin refrescarse (cajero muerto, o Edge que no es Linux).
	// Pintar "" como `closed` o como `disjunta` publicaría una salud INVENTADA sobre un clasificador
	// que puede estar apagado. En la vista, vacío se pinta «desconocido» y nunca otra cosa.

	// IntentCircuit es el breaker del clasificador: "closed" | "open" | "half_open".
	IntentCircuit string `json:"intent_circuit,omitempty"`
	// WorkerTaskset es el veredicto del reparto de CPU entre el cajero y Ollama:
	// "disjunta" | "solapada" | "cajero_sin_confinar".
	WorkerTaskset string `json:"worker_taskset,omitempty"`
}

// EffectiveProfile es el perfil que la vista debe pintar.
//
// Devuelve EXACTAMENTE uno de tres valores: "active", "passive" o "". Cualquier otra cosa que llegue
// —un `profile` desconocido, el campo ausente— cae a "", que significa DESCONOCIDO y nunca un valor
// por defecto: no se inventa un perfil que la plataforma no dijo, y mucho menos "active", que es el
// que hace hablar sola a la sesión.
//
// 🔴 Quien consuma este "" tiene que pintarlo como desconocido A PROPÓSITO. Un <select> sin ninguna
// opción `selected` NO sale vacío: el navegador enseña la primera. Por eso sesiones.html emite un
// <option> «sin dato» selected+disabled cuando esto devuelve "".
func (s Session) EffectiveProfile() string {
	if ValidProfile(s.Profile) {
		return s.Profile
	}
	return ""
}

// setSessionProfileRequest es el cuerpo de POST /api/v1/sessions/{id}/profile. La sesión va en la
// RUTA y el tenant NO viaja (INV-04): sale del Context Token.
type setSessionProfileRequest struct {
	Profile string `json:"profile"`
}

// sendMessageRequest es el cuerpo de POST /api/v1/messages. Tampoco lleva `tenant_id` (INV-04).
type sendMessageRequest struct {
	SessionID string `json:"session_id"`
	To        string `json:"to"`
	Text      string `json:"text"`
}

// SendResult es la respuesta 200 de POST /api/v1/messages: el ACUSE del Edge.
//
// 🔴 Un 200 NO significa «entregado». Significa que llegó el Ack, y el Ack puede traer `ok:false`:
// el Edge recibió el comando y su ejecución falló. Quien trate este 200 como éxito sin mirar `OK`
// le estará diciendo a la dueña que el mensaje salió cuando no salió.
type SendResult struct {
	// AckedCommandID es el identificador del comando que la nube empujó al Edge. Es el hilo que
	// correlaciona lo que la nube intentó con el outbox del Edge, así que la pantalla lo enseña.
	AckedCommandID string `json:"acked_command_id"`
	OK             bool   `json:"ok"`
	// Error es el detalle que da el Edge cuando OK es false. Va al LOG y nunca a la pantalla: el
	// texto que ve el usuario sale del catálogo de flash, como en el resto de esta consola.
	Error string `json:"error,omitempty"`
}

// SessionsClient sirve el plano de SESIONES del tenant del token: los teléfonos vinculados, su perfil
// y el envío de un mensaje de prueba por uno de ellos.
//
// Las tres operaciones consumen scopes distintos en la plataforma —`sessions.read`, `sessions.write`
// y `messages.send`—, así que un 403 en una no dice nada de las otras.
//
// 🔴 Lo que este cliente NO PUEDE hacer, y no por olvido: vincular un teléfono. El emparejamiento
// vive en el Edge (por QR, contra el plano de control local) y no hay endpoint en la API pública que
// lo haga desde aquí. La pantalla lo dice en vez de ofrecer un botón que no existe.
type SessionsClient struct {
	t *Transport
}

// NewSessionsClient construye el cliente de sesiones.
func NewSessionsClient(t *Transport) *SessionsClient { return &SessionsClient{t: t} }

// List lee las sesiones vinculadas de la empresa DEL TOKEN (GET /api/v1/sessions, scope
// `sessions.read`).
//
// El tenant no se pasa: no hay parámetro donde ponerlo (INV-04). La plataforma filtra por el tenant
// del token, así que una sesión ajena nunca aparece.
func (c *SessionsClient) List(ctx context.Context, accessToken string) ([]Session, error) {
	const op = "sessions.list"
	req, err := c.t.newAuthedRequest(ctx, http.MethodGet, "/api/v1/sessions", nil, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.do(req, op, statusError)
	if err != nil {
		return nil, err
	}
	var out []Session
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Session{}
	}
	return out, nil
}

// SetProfile fija el PERFIL de negocio de una sesión (POST /api/v1/sessions/{id}/profile, scope
// `sessions.write`). Responde 200 con el estado ya fijado; aquí el cuerpo se descarta, porque quien
// manda es la relectura del listado tras el redirect.
//
// El `profile` que se le pase tiene que ser uno de los dos válidos: la plataforma responde 400 a
// cualquier otra cosa, y la consola ya lo rechaza antes de llegar aquí (ver ValidProfile).
//
// 🔴 El identificador va ESCAPADO. Llega de un formulario, y sin escapar un valor con `/` o `?`
// reescribiría la ruta y podría acabar llamando a OTRO endpoint de la API pública con el token del
// usuario. Es la misma razón por la que la baja de un miembro usa pathSegment.
func (c *SessionsClient) SetProfile(ctx context.Context, accessToken, sessionID, profile string) error {
	const op = "sessions.set_profile"
	req, err := c.t.newAuthedRequest(ctx, http.MethodPost,
		"/api/v1/sessions/"+pathSegment(sessionID)+"/profile",
		setSessionProfileRequest{Profile: profile}, accessToken)
	if err != nil {
		return err
	}
	resp, err := c.t.do(req, op, statusError)
	if err != nil {
		return err
	}
	discard(resp)
	return nil
}

// SendMessage manda un texto por una sesión del Edge (POST /api/v1/messages, scope `messages.send`).
//
// Los desenlaces que NO tienen sentinela y sí significan algo para la dueña llegan como *APIError y
// se distinguen con StatusCodeOf: 502 es «el teléfono está desconectado» y 504 es «el acuse no llegó
// a tiempo». Traducir los dos al genérico «no se pudo completar» perdería justo la parte accionable.
func (c *SessionsClient) SendMessage(ctx context.Context, accessToken, sessionID, to, text string) (*SendResult, error) {
	const op = "messages.send"
	req, err := c.t.newAuthedRequest(ctx, http.MethodPost, "/api/v1/messages",
		sendMessageRequest{SessionID: sessionID, To: to, Text: text}, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.do(req, op, statusError)
	if err != nil {
		return nil, err
	}
	var out SendResult
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
