package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// Intake es la cabecera de una solicitud tal como la publica la API pública (Plan 041 · T1.1). Los
// campos son EXACTAMENTE los de `publicapi.intakeDTO` de la plataforma; nada se enriquece aquí.
//
// ContactID viaja OPACO: es un identificador sin número ni JID (INV-04/ADR-0010). La consola lo
// pinta tal cual y no intenta resolverlo a un nombre o un teléfono —no puede, y no debe—.
//
// CustomerNote es la indicación del cliente para el PEDIDO ENTERO (D-041.19): el «dejarlo en
// portería, no tocar el timbre». No confundir con IntakeItem.Customization, que es la indicación de
// UNA línea («sin cebolla»): son campos distintos, de niveles distintos, y la pantalla los pinta en
// sitios distintos a propósito.
//
// Overdue es la MARCA de que la solicitud lleva demasiado esperando al dueño (Plan 044 · T4.1): es
// `true` solo cuando está en `pending_approval` y han pasado 24 h desde la última modificación.
//
// 🔴 Es DERIVADO y no tiene ningún efecto: no cambia `Status`, no cambia `AllowedTransitions` y no
// cierra nada. Y NO ES `expired`, que es un ESTADO terminal y legado: esto es un aviso sobre una
// solicitud que sigue viva. Una pantalla que pinte `overdue` como si fuera un estado le está
// enseñando al dueño algo que el sistema no dice.
type Intake struct {
	ID           string  `json:"id"`
	ContactID    string  `json:"contact_id"`
	SessionID    string  `json:"session_id"`
	Status       string  `json:"status"`
	Total        float64 `json:"total"`
	CustomerNote string  `json:"customer_note"`
	Overdue      bool    `json:"overdue"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

// IntakeItem es una línea de la solicitud (código de catálogo del tenant, nunca PII).
//
// Customization es la personalización NO facturable de la línea (D-041.17): el «sin cebolla». Es una
// instrucción de PRODUCCIÓN y por eso viaja hasta aquí — quien recibió el pedido y quien lo prepara
// son personas distintas, y una personalización que no llega a la pantalla se pierde. NUNCA entra en
// ninguna cuenta: el total lo manda la plataforma y esta capa no lo recalcula (INV-13).
//
// La plataforma la publica SIEMPRE, también vacía (sin `omitempty`), así que aquí las dos ausencias
// posibles —clave que no llega y personalización vacía— colapsan en `""`. Es aceptable porque la
// pantalla trata las dos igual: no pinta nada.
type IntakeItem struct {
	SKU           string  `json:"sku"`
	Label         string  `json:"label"`
	Customization string  `json:"customization"`
	Qty           int     `json:"qty"`
	UnitPrice     float64 `json:"unit_price"`
}

// IntakePage es la respuesta de GET /api/v1/intakes: la página más el TOTAL de coincidencias del
// filtro, que es lo que hace falta para pintar el paginador.
//
// `Total` NO es `len(Intakes)`: es cuántas cumplen el filtro en TODA la bandeja. Sin él no se puede
// saber si hay una página siguiente, y una bandeja sin paginador deja lo más antiguo —que es lo que
// lleva más esperando— fuera de la vista para siempre.
type IntakePage struct {
	Intakes  []Intake `json:"intakes"`
	Page     int      `json:"page"`
	PageSize int      `json:"page_size"`
	Total    int      `json:"total"`
}

// IntakeDetail es la respuesta de GET /api/v1/intakes/{id}: cabecera + líneas + histórico.
//
// AllowedTransitions son los destinos válidos desde el estado actual, y es lo que alimenta el
// selector de la pantalla: esta consola NO replica la máquina de estados —que vive en
// `internal/intakes/status.go` de la plataforma— porque un mapa duplicado se desincroniza a la
// primera transición nueva.
//
// El campo es un puntero deliberado, porque hay dos «sin destinos» que no significan lo mismo:
//   - `nil` (la plataforma NO manda el campo): no se sabe, así que la pantalla lo declara en vez de
//     fingir un estado terminal. Es el caso de un servidor anterior a cloud `a804943`.
//   - lista vacía (la plataforma manda `[]`): estado TERMINAL, no admite cambios.
//
// `Revisions` es el histórico auditado (ADR-0031 §3), y es de donde sale el BORRADOR que la pantalla
// pinta: la razón es dura y vale la pena escribirla — `Items` son las líneas RESUELTAS —cinco claves
// planas y un `unit_price` que no sabe decir «sin precio»— y la línea `unmatched`, que es exactamente
// la que el dueño tiene que atender, NI SIQUIERA APARECE ahí. Ver intakes_draft.go.
//
// `Items` NO se retira ni se sustituye: sigue siendo lo que la plataforma factura y lo que sostiene
// `Total`.
type IntakeDetail struct {
	Intake
	Items              []IntakeItem     `json:"items"`
	Revisions          []IntakeRevision `json:"revisions"`
	AllowedTransitions *[]string        `json:"allowed_transitions"`
}

// IntakeFilter son los filtros y la paginación de GET /api/v1/intakes. Los ceros significan «sin
// filtro»: la API aplica sus propios defaults (página 1, tamaño 50, máximo 200).
//
// From/To aceptan `YYYY-MM-DD` (día suelto en UTC) o RFC3339; un `to` con fecha suelta cubre el día
// ENTERO. Status admite las claves del ciclo de vida y el `closed` legado; una clave desconocida la
// rechaza la API con 400, y esa validación no se replica aquí.
//
// Sort es el orden de la página: `newest` (el default de la API) u `oldest`. Vacío ⇒ no se manda y
// decide la API.
//
// 🔴 NO LLEVA `tenant_id`, y no es una omisión que haya que recordar: no existe el campo donde
// ponerlo (INV-04). La empresa sale del Context Token.
//
// ⚠️ Dos cosas que la API pública SÍ acepta y este filtro no expresa, medidas contra
// `publicapi.parseIntakeFilter` y anotadas para que la ausencia no envejezca como si fuera un límite
// del contrato:
//   - `status` se puede REPETIR para pedir varios estados a la vez (`?status=pending_approval&
//     status=needs_info`, D-044.47 §2). Aquí es UN string, así que solo se puede pedir uno. Es lo
//     que el BFF mandaba y se porta tal cual; ensancharlo es una decisión de la pantalla que lo
//     necesite, no un arreglo de paso.
//   - `orphan=true` acota a las solicitudes cuyo evento conversacional ya no está `open` (REQ-21c).
//     No hay campo: nadie lo pide todavía.
type IntakeFilter struct {
	From     string
	To       string
	Status   string
	Session  string
	Sort     string
	Page     int
	PageSize int
}

// Órdenes que acepta el listado. `IntakeSortOldest` es el que pide la bandeja del dueño (D-044.47
// §2): lo que lleva más tiempo esperando es lo que hay que atender primero, y con el default de la
// API —lo más reciente arriba— eso queda justo al final de la última página.
const (
	IntakeSortNewest = "newest"
	IntakeSortOldest = "oldest"
)

// query serializa el filtro a la query string de la API. Lo vacío no se manda: un `status=` vacío y
// no mandar `status` significan lo mismo, y omitirlo deja la URL legible en los logs.
//
// 🔴 `page` y `page_size` son los nombres LITERALES que lee el cloud (`parseIntQuery(r, "page", 1)`
// y `parseIntQuery(r, "page_size", intakes.DefaultPageSize)`), y por eso van con guion bajo y no en
// camelCase: una clave que la API no reconoce no da error, da la PRIMERA PÁGINA en silencio.
func (f IntakeFilter) query() string {
	q := url.Values{}
	for key, value := range map[string]string{
		"from": f.From, "to": f.To, "status": f.Status, "session": f.Session, "sort": f.Sort,
	} {
		if value != "" {
			q.Set(key, value)
		}
	}
	if f.Page > 0 {
		q.Set("page", strconv.Itoa(f.Page))
	}
	if f.PageSize > 0 {
		q.Set("page_size", strconv.Itoa(f.PageSize))
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

// maxIntakeErrorBody acota lo que se lee del cuerpo de un rechazo de la bandeja. Es mucho más alto
// que el maxErrorBody general (4 KB) por una razón concreta: el 400 de la edición manual no trae un
// motivo suelto sino la lista COMPLETA de defectos —uno por línea y campo—, y recortarla dejaría al
// dueño arreglando solo los primeros.
const maxIntakeErrorBody = 64 << 10

// MaxIntakeDiscardBatch es cuántas solicitudes acepta UN POST /api/v1/intakes/discard
// (`intakes.MaxDiscardBatch` de la plataforma). Con una más, la plataforma responde 400 y no
// descarta ninguna.
//
// Es un ESPEJO y no la autoridad: quien mide el lote y lo rechaza es la plataforma. Vive aquí para
// que la pantalla pueda decirlo ANTES de gastar el viaje —y decirlo en español, que un 400 crudo no
// lo está—, no para decidirlo.
const MaxIntakeDiscardBatch = 200

// Claves `error` del vocabulario CERRADO que publica la bandeja del cloud. Van como constantes
// porque los rechazos NO se distinguen por el código: el 400 de aprobar puede ser dos cosas y el 422
// puede ser tres, y a todas las separa esta clave. Una errata en un literal repartido por cuatro
// ficheros no la detecta el compilador.
const (
	errInvalidItems          = "invalid_items"
	errNotEditable           = "not_editable"
	errInvalidTransition     = "invalid_transition"
	errLinesWithoutPrice     = "lines_without_price"
	errNotApprovable         = "not_approvable"
	errFeatureNotEnabled     = "feature_not_enabled"
	errLLMCredentialsMissing = "llm_credentials_missing"
	errSourceUnavailable     = "source_unavailable"
	errReanalysisInProgress  = "reanalysis_in_progress"
	errInvalidVia            = "invalid_via"
	errTextTooLong           = "text_too_long"
)

// ErrIntakeChanged es el 409 de la bandeja: otro operador movió la solicitud entre que esta pantalla
// la leyó y la acción llegó al cloud, y el compare-and-swap la rechazó.
//
// 🔴 Necesita sentinela propio y no es adorno: lo emiten CUATRO puertas —`/status`, `/items`,
// `/approve` y `/request-info`, todas con el mismo cuerpo en prosa («la solicitud cambió de estado;
// recárgala y reintenta»)—, y sin él llegaría como ErrConflict a secas, que en esta consola significa
// «ya existe» (un rol repetido). El consejo que hay que dar es RECARGAR, y ese es un consejo que
// «ya existe» no da.
//
// Envuelve a ErrConflict, así que quien solo quiera saber «hubo conflicto» sigue funcionando con
// errors.Is(err, ErrConflict); la consecuencia es que el ORDEN manda en cualquier switch que los
// distinga —preguntar antes por el genérico se come este significado—.
var ErrIntakeChanged = fmt.Errorf("apiclient: la solicitud cambió mientras la mirabas: %w", ErrConflict)

// ItemDefect localiza UN defecto de UNA línea de la edición manual: en qué posición del cuerpo que
// se mandó, qué campo y qué le pasa. Es la forma EXACTA de `intakes.LineDefect` de la plataforma.
//
// Index es la posición 0-based en la lista ENVIADA, que no tiene por qué coincidir con la fila del
// formulario (las filas en blanco no se mandan). Traducir de una a otra es cosa de quien armó el
// cuerpo, y por eso este tipo no lo intenta.
//
// ⚠️ `Message` es prosa ESCRITA POR EL CLOUD. Se conserva porque es lo único que dice qué le pasa a
// esa línea en concreto, pero la doctrina de esta casa sigue siendo la de editor.go: el texto que ve
// la dueña sale del catálogo de flash, no del upstream. Quien lo pinte tal cual está dejando que la
// plataforma decida qué dice esta consola.
type ItemDefect struct {
	Index   int    `json:"index"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// InvalidItemsError es el 400 `invalid_items` de PUT /api/v1/intakes/{id}/items: TODOS los defectos
// de una vez.
//
// Va como tipo propio y no como un sentinela pelado porque la pantalla no enseña un motivo, enseña
// una lista con la que el dueño corrige sus líneas. Y cuando llega, la plataforma NO escribió nada:
// la edición es todo-o-nada.
type InvalidItemsError struct {
	Defects []ItemDefect
}

func (e *InvalidItemsError) Error() string {
	return fmt.Sprintf("apiclient: la edición tiene %d líneas inválidas", len(e.Defects))
}

// Unwrap devuelve ErrInvalidInput: es un 400, y quien solo quiera saber «lo rechazaron» no tiene por
// qué conocer este tipo. Mismo criterio que ErrMemberOfAnotherTenant sobre ErrConflict.
func (e *InvalidItemsError) Unwrap() error { return ErrInvalidInput }

// InvalidItemsOf extrae el rechazo por líneas inválidas de un error (nil, false si no lo es).
func InvalidItemsOf(err error) (*InvalidItemsError, bool) {
	var invalid *InvalidItemsError
	if errors.As(err, &invalid) {
		return invalid, true
	}
	return nil, false
}

// NotEditableError es el 422 `not_editable`: la solicitud no está en un estado que admita edición
// manual. Trae dónde está AHORA y desde qué estados SÍ se edita.
//
// EditableIn es la razón de que esta pantalla no tenga que saberse el ciclo de vida: igual que
// `allowed` en el 422 del cambio de estado, es la plataforma la que dice qué se puede hacer.
type NotEditableError struct {
	Status     string   `json:"status"`
	EditableIn []string `json:"editable_in"`
}

func (e *NotEditableError) Error() string {
	return fmt.Sprintf("apiclient: una solicitud en %q no admite edición manual", e.Status)
}

// Unwrap devuelve ErrInvalidInput, que es donde statusError mete el 422.
func (e *NotEditableError) Unwrap() error { return ErrInvalidInput }

// NotEditableOf extrae el rechazo por estado no editable de un error (nil, false si no lo es).
func NotEditableOf(err error) (*NotEditableError, bool) {
	var notEditable *NotEditableError
	if errors.As(err, &notEditable) {
		return notEditable, true
	}
	return nil, false
}

// InvalidTransitionError es el 422 `invalid_transition`: la transición pedida no existe en el ciclo
// de vida. Trae dónde está la solicitud AHORA y adónde sí puede ir, que es lo único con lo que el
// operador puede corregir sin adivinar.
//
// Lo emiten TRES puertas: `/status` (el caso normal), `/approve` y `/request-info` (ahí es una
// carrera: alguien movió la solicitud entre la validación y el compare-and-swap).
type InvalidTransitionError struct {
	Status    string   `json:"status"`
	Requested string   `json:"requested"`
	Allowed   []string `json:"allowed"`
}

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("apiclient: transición inválida de %q a %q", e.Status, e.Requested)
}

// Unwrap devuelve ErrInvalidInput, que es donde statusError mete el 422.
func (e *InvalidTransitionError) Unwrap() error { return ErrInvalidInput }

// InvalidTransitionOf extrae el rechazo de transición de un error (nil, false si no lo es).
func InvalidTransitionOf(err error) (*InvalidTransitionError, bool) {
	var invalid *InvalidTransitionError
	if errors.As(err, &invalid) {
		return invalid, true
	}
	return nil, false
}

// FeatureNotEnabledError es el 403 `feature_not_enabled`: al plan del tenant le falta una capacidad.
// Trae CUÁL, porque las que pueden faltar llevan a sitios distintos —`cart_basic` es la bandeja,
// `llm_intake` la bandeja con IA y `api_llm` el add-on de la vía externa— y un aviso genérico dejaría
// al dueño sin saber qué contratar.
//
// 🔴 Lo emite el middleware de entitlements del cloud en OCHO de las nueve rutas de la bandeja
// (`entitlements.RequireFeature`), antes de que el handler se ejecute: no es exclusivo de las rutas
// con LLM. Por eso lo traduce el traductor COMÚN y no el de una operación suelta — con la feature
// apagada, todo lo que esta pantalla intente responde 403, y «sin permiso» sería un diagnóstico
// falso: el permiso está, lo que falta es el plan.
type FeatureNotEnabledError struct {
	Feature string
}

func (e *FeatureNotEnabledError) Error() string {
	return fmt.Sprintf("apiclient: el plan de la empresa no incluye %q", e.Feature)
}

// Unwrap devuelve ErrForbidden: es un 403.
func (e *FeatureNotEnabledError) Unwrap() error { return ErrForbidden }

// FeatureNotEnabledOf extrae el rechazo por capacidad (nil, false si no lo es).
func FeatureNotEnabledOf(err error) (*FeatureNotEnabledError, bool) {
	var missing *FeatureNotEnabledError
	if errors.As(err, &missing) {
		return missing, true
	}
	return nil, false
}

// intakeErrorBody es todo lo que la bandeja puede traer en un no-2xx. Se decodifica UNA vez y con él
// deciden los traductores: los cuerpos son disjuntos por clave `error`, así que un solo struct
// ancho es más honesto que cinco estrechos que habría que elegir antes de saber cuál toca.
type intakeErrorBody struct {
	Error string `json:"error"`
	// invalid_items
	Errors []ItemDefect `json:"errors"`
	// not_editable / not_approvable / invalid_transition
	Status       string   `json:"status"`
	EditableIn   []string `json:"editable_in"`
	ApprovableIn []string `json:"approvable_in"`
	Requested    string   `json:"requested"`
	Allowed      []string `json:"allowed"`
	// lines_without_price
	Lines []IntakeLineRef `json:"lines"`
	// feature_not_enabled
	Feature string `json:"feature"`
	// invalid_via / llm_credentials_missing
	Via string `json:"via"`
	// source_unavailable
	Reason string `json:"reason"`
	// reanalysis_in_progress
	JobID string `json:"job_id"`
	// text_too_long
	Runes int `json:"runes"`
	Max   int `json:"max"`
}

// readIntakeError lee el cuerpo de un no-2xx acotado a maxIntakeErrorBody. Un cuerpo ilegible deja
// todo en blanco: el status sigue siendo la información principal y el llamante tiene su sentinela
// genérico.
func readIntakeError(resp *http.Response) intakeErrorBody {
	var body intakeErrorBody
	_ = json.NewDecoder(io.LimitReader(resp.Body, maxIntakeErrorBody)).Decode(&body)
	return body
}

// intakeCommonError traduce lo que TODAS las puertas de la bandeja comparten y ninguna operación
// necesita repetir: el 403 con capacidad que falta y el 409 de carrera. Devuelve nil cuando el
// código no es de los suyos, para que el traductor de cada operación siga decidiendo.
func intakeCommonError(op string, status int, body intakeErrorBody) error {
	switch status {
	case http.StatusForbidden:
		if body.Error == errFeatureNotEnabled {
			return &FeatureNotEnabledError{Feature: body.Feature}
		}
	case http.StatusConflict:
		return fmt.Errorf("%s: %w", op, ErrIntakeChanged)
	}
	return nil
}

// intakeStatusError es el traductor de las puertas SIN cuerpo tipado propio (el listado, el detalle y
// el descarte): lo común y, para lo demás, el general de la casa.
func intakeStatusError(op string, resp *http.Response) error {
	body := readIntakeError(resp)
	if err := intakeCommonError(op, resp.StatusCode, body); err != nil {
		return err
	}
	return statusError(op, resp.StatusCode)
}

// setIntakeStatusRequest es el cuerpo de POST /api/v1/intakes/{id}/status. NO lleva `tenant_id`
// (INV-04).
type setIntakeStatusRequest struct {
	Status string `json:"status"`
}

// replaceIntakeItemsRequest es el cuerpo de PUT /api/v1/intakes/{id}/items: el conjunto COMPLETO de
// líneas de cliente que debe quedar.
//
// `as_correction` es el campo del 044 (D-044.48 §1) y lleva `omitempty` a propósito: SIN él el cuerpo
// sale byte a byte como el del Plan 041, que es la condición de la cero-regresión. No existe ninguna
// ruta `/correct`: corregir es este mismo PUT con el campo puesto, y dos puertas dejando la misma
// revisión `corrected` era exactamente el duplicado que el 044 ya pagó una vez.
type replaceIntakeItemsRequest struct {
	Items        []IntakeItem `json:"items"`
	AsCorrection bool         `json:"as_correction,omitempty"`
}

// discardIntakesRequest es el cuerpo de POST /api/v1/intakes/discard: la lista EXPLÍCITA de ids que
// el dueño quiere sacar de su bandeja.
//
// Es una lista de ids y no un filtro, y esa es la decisión de fondo de esta puerta: el descarte es
// irreversible y no hay papelera (D-041.22), así que quien descarta NOMBRA lo que descarta. Un filtro
// dejaría el conjunto afectado a merced de lo que hubiera cambiado entre la pantalla y el POST.
type discardIntakesRequest struct {
	IntakeIDs []string `json:"intake_ids"`
}

// IntakeDiscardSkip es UNA solicitud del lote que NO se descartó, con la razón que da la plataforma
// (`not_found`, `already_discarded`, `not_open`, `live_event`).
//
// La razón viaja como CLAVE, no como prosa: es contrato, y traducirla a la voz del dueño del negocio
// es cosa de la pantalla. Una clave que este cliente no conozca se entrega tal cual en vez de
// descartarse — un motivo que no se entiende sigue siendo un motivo, y callarlo dejaría al dueño
// creyendo que esa solicitud sí se descartó.
type IntakeDiscardSkip struct {
	IntakeID string `json:"intake_id"`
	Reason   string `json:"reason"`
}

// Razones por las que una solicitud del lote NO se descartó (`intakes/discard.go` del cloud).
const (
	DiscardSkipNotFound         = "not_found"
	DiscardSkipAlreadyDiscarded = "already_discarded"
	DiscardSkipNotOpen          = "not_open"
	DiscardSkipLiveEvent        = "live_event"
)

// IntakeDiscardResult es la respuesta 200 de POST /api/v1/intakes/discard: el desglose POR ÍTEM del
// lote.
//
// Las dos listas vienen SIEMPRE, también vacías: la plataforma las emite como `[]` y nunca como
// `null` a propósito, porque «no se descartó nada» y «no sé qué pasó» son respuestas distintas. Un
// lote MIXTO —unos descartados y otros no— es el caso NORMAL, no el excepcional: por eso el éxito de
// esta llamada no significa que se haya descartado lo que se pidió, y quien la use tiene que contar
// las dos listas.
type IntakeDiscardResult struct {
	Discarded []string            `json:"discarded"`
	Skipped   []IntakeDiscardSkip `json:"skipped"`
}

// IntakesClient sirve la BANDEJA de solicitudes de la empresa del token.
//
// 🔴 Ninguno de sus diez métodos acepta un `tenantID`, y no es una omisión que haya que recordar: no
// existe el parámetro donde ponerlo (INV-04). La empresa sale del Context Token; el contrato entero
// está en transport.go.
//
// Todas sus rutas exigen además la feature `cart_basic` —y `/quote-suggestion` también `llm_intake`—:
// sin ellas la plataforma corta con 403 y `{"error":"feature_not_enabled"}` desde el middleware,
// antes del handler. Esa es la autoridad real; un gate en la plantilla solo decide qué se pinta.
//
// 🔴 Lo que este cliente NO trae del BFF, a propósito y por la misma razón que EditorClient: su
// `RejectionError`, que llevaba el CUERPO en prosa del upstream hasta la pantalla para pintarlo tal
// cual. Aquí el desenlace lo deciden el sentinela y el catálogo de flash. Lo que sí viaja son los
// cuerpos ESTRUCTURADOS —la lista de defectos, los estados permitidos, las líneas sin precio—, que
// no son prosa del upstream sino contrato con datos que la pantalla necesita para que el dueño
// arregle lo suyo.
type IntakesClient struct {
	t *Transport
}

// NewIntakesClient construye el cliente de la bandeja.
func NewIntakesClient(t *Transport) *IntakesClient { return &IntakesClient{t: t} }

// ListIntakes lista las solicitudes de la empresa DEL TOKEN vía GET /api/v1/intakes.
//
// La paginación viaja en la query (`page`, `page_size`) y el total de coincidencias vuelve en
// IntakePage.Total: los tres juntos son lo que hace falta para pintar el paginador. Un filtro con
// `Page` en cero significa «la que decida la API», que es la primera.
//
// El 400 es el único fallo del listado con un motivo accionable —el filtro venía mal: una fecha
// ilegible, un estado que no existe, un `sort` desconocido— y llega como ErrInvalidInput.
func (c *IntakesClient) ListIntakes(ctx context.Context, accessToken string, f IntakeFilter) (*IntakePage, error) {
	const op = "intakes.list"
	req, err := c.t.newAuthedRequest(ctx, http.MethodGet, "/api/v1/intakes"+f.query(), nil, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.doTyped(req, op, intakeStatusError)
	if err != nil {
		return nil, err
	}
	var out IntakePage
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	if out.Intakes == nil {
		out.Intakes = []Intake{}
	}
	return &out, nil
}

// GetIntake devuelve la solicitud {id} con sus líneas y su histórico vía GET /api/v1/intakes/{id}.
//
// 🔴 Su 404 es FRONTERA DE EMPRESA, no «no existe»: una solicitud de otra empresa responde 404 y no
// 403 a propósito, porque un 403 confirmaría que ese id existe en algún sitio (INV-8). Ver ErrNotFound.
func (c *IntakesClient) GetIntake(ctx context.Context, accessToken, id string) (*IntakeDetail, error) {
	const op = "intakes.get"
	req, err := c.t.newAuthedRequest(ctx, http.MethodGet, "/api/v1/intakes/"+pathSegment(id), nil, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.doTyped(req, op, intakeStatusError)
	if err != nil {
		return nil, err
	}
	var out IntakeDetail
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetIntakeStatus aplica una transición del ciclo de vida vía POST /api/v1/intakes/{id}/status y
// devuelve la solicitud ya transicionada.
//
// El 422 se traduce a *InvalidTransitionError CON los destinos permitidos en vez de a un error
// opaco: es la única respuesta de la API que publica el ciclo de vida, y tirarla dejaría al operador
// probando estados a ciegas. El 409 llega como ErrIntakeChanged —otro operador se adelantó— y el 404
// como ErrNotFound.
func (c *IntakesClient) SetIntakeStatus(ctx context.Context, accessToken, id, status string) (*Intake, error) {
	const op = "intakes.status"
	req, err := c.t.newAuthedRequest(ctx, http.MethodPost,
		"/api/v1/intakes/"+pathSegment(id)+"/status",
		setIntakeStatusRequest{Status: status}, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.doTyped(req, op, setIntakeStatusError)
	if err != nil {
		return nil, err
	}
	var out Intake
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// setIntakeStatusError traduce el no-2xx del cambio de estado: el 422 nombrado, más lo común.
func setIntakeStatusError(op string, resp *http.Response) error {
	body := readIntakeError(resp)
	if err := intakeCommonError(op, resp.StatusCode, body); err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnprocessableEntity && body.Error == errInvalidTransition {
		return &InvalidTransitionError{Status: body.Status, Requested: body.Requested, Allowed: body.Allowed}
	}
	return statusError(op, resp.StatusCode)
}

// ReplaceIntakeItems SUSTITUYE las líneas de cliente de una solicitud por las que manda el dueño, vía
// PUT /api/v1/intakes/{id}/items, y devuelve el detalle YA actualizado (Plan 041 · T4.10).
//
// Es un REEMPLAZO del conjunto entero, no tres operaciones: añadir, quitar y corregir se expresan
// mandando la lista que debe quedar. Así lo publica la plataforma y así se consume; inventar aquí un
// `AddItem`/`RemoveItem` de conveniencia haría creer que existe una operación por línea que el
// contrato no tiene —y no puede tener, porque dos líneas comparten sku y solo se distinguen por la
// personalización—.
//
// La LÍNEA DE ENVÍO (`_shipping`) NO va en `items` y no se toca: es de la plataforma, sobrevive a la
// edición y sigue contando en el total. Un sku con el prefijo reservado en la entrada lo rechaza la
// plataforma con un defecto de línea, así que por esta puerta no se puede duplicar ni pisar.
//
// La respuesta es el detalle COMPLETO —el mismo cuerpo que el GET, con la revisión `corrected` recién
// escrita ya dentro—, y por eso el llamante repinta con lo que devuelve el PUT en vez de encadenar un
// segundo GET: entre los dos viajes cabe la edición de otro operador, y repintar con esa lectura
// enseñaría un estado que nadie pidió.
//
// Errores: *InvalidItemsError (400 `invalid_items`, con la lista entera), *NotEditableError (422
// `not_editable`), ErrIntakeChanged (409), *FeatureNotEnabledError (403) y los sentinelas generales.
func (c *IntakesClient) ReplaceIntakeItems(ctx context.Context, accessToken, id string, items []IntakeItem) (*IntakeDetail, error) {
	return c.putIntakeItems(ctx, accessToken, id, items, false)
}

// CorrectIntakeItems es el MISMO PUT con `as_correction` puesto: la corrección del dueño del Plan 044
// (D-044.48 §1). Deja la misma revisión `corrected` que la edición del 041 y además marca la señal
// few-shot con la que la Ola 5 aprende la voz de la dueña.
//
// Va como método aparte y NO como un parámetro más de ReplaceIntakeItems para no tocar la firma que
// consume el formulario del 041: ese camino tiene que seguir mandando el cuerpo de siempre, y un
// booleano extra en la firma es una invitación permanente a pasarlo mal desde el sitio equivocado.
// Lo que comparten —la ruta, la lista, los rechazos— vive en putIntakeItems y no está duplicado.
func (c *IntakesClient) CorrectIntakeItems(ctx context.Context, accessToken, id string, items []IntakeItem) (*IntakeDetail, error) {
	return c.putIntakeItems(ctx, accessToken, id, items, true)
}

// putIntakeItems es el viaje compartido por la edición del 041 y la corrección del 044.
func (c *IntakesClient) putIntakeItems(ctx context.Context, accessToken, id string, items []IntakeItem, asCorrection bool) (*IntakeDetail, error) {
	const op = "intakes.items"
	// La lista vacía viaja como `[]`, NUNCA como `null`: quitar todas las líneas es una edición
	// legítima, y la plataforma distingue las dos a propósito —`null` es «no mandaste la clave» y lo
	// contesta con un 400—. Un `[]IntakeItem` nil serializaría a `null` y convertiría ese vaciado en
	// un error incomprensible.
	if items == nil {
		items = []IntakeItem{}
	}

	req, err := c.t.newAuthedRequest(ctx, http.MethodPut,
		"/api/v1/intakes/"+pathSegment(id)+"/items",
		replaceIntakeItemsRequest{Items: items, AsCorrection: asCorrection}, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.doTyped(req, op, replaceIntakeItemsError)
	if err != nil {
		return nil, err
	}
	var out IntakeDetail
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// replaceIntakeItemsError traduce un no-2xx de la edición manual.
//
// El 400 se reconoce por la clave `invalid_items` Y, además, por la FORMA (que venga la lista de
// defectos). Los dos criterios y no uno porque son dos versiones del mismo contrato: el cloud de hoy
// publica la clave, y el BFF —que se escribió contra este mismo endpoint— distinguía por la lista.
// Aceptar los dos no cuesta nada y evita que un servidor de una versión distinta convierta la lista
// de defectos en un «revisa los datos» genérico.
func replaceIntakeItemsError(op string, resp *http.Response) error {
	body := readIntakeError(resp)
	if err := intakeCommonError(op, resp.StatusCode, body); err != nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusUnprocessableEntity:
		if body.Error == errNotEditable || len(body.EditableIn) > 0 {
			return &NotEditableError{Status: body.Status, EditableIn: body.EditableIn}
		}
	case http.StatusBadRequest:
		if body.Error == errInvalidItems || len(body.Errors) > 0 {
			return &InvalidItemsError{Defects: body.Errors}
		}
	}
	return statusError(op, resp.StatusCode)
}

// DiscardIntakes DESCARTA a mano un LOTE de solicitudes vía POST /api/v1/intakes/discard y devuelve
// el desglose POR ÍTEM (Plan 041 · T4.8, D-041.18). Las descartadas quedan en `abandoned`, con su
// revisión `discarded`.
//
// ⚠️ Es IRREVERSIBLE y no hay papelera (D-041.22): quien llame a esto tiene que haber enseñado antes
// qué se va a descartar. No borra líneas ni revisiones, y NO avisa al cliente.
//
// Un error de esta función NUNCA significa «no se descartó nada»: significa que no se pudo contestar
// el lote entero. Y al revés, que no haya error tampoco significa que se descartara lo pedido —para
// eso están las dos listas del resultado—. Si la plataforma falla a MEDIO lote, lo ya escrito QUEDA
// escrito y repetir el mismo lote es seguro: lo hecho vuelve como `already_discarded`.
//
// 🔴 NO HAY 404 NI 422 en esta puerta, y está medido contra el handler del cloud: un lote no tiene UN
// recurso ni UN estado. Un id inexistente —o de otra empresa, que es indistinguible (INV-8)— sale
// como `not_found` DENTRO del 200. Los únicos rechazos son el 400 (cuerpo malformado, lista vacía o
// más de MaxIntakeDiscardBatch ids) y el 403 de la feature.
func (c *IntakesClient) DiscardIntakes(ctx context.Context, accessToken string, intakeIDs []string) (*IntakeDiscardResult, error) {
	const op = "intakes.discard"
	// La lista viaja como `[]` y nunca como `null`. Las dos las contesta la plataforma con el mismo
	// 400, así que no cambia el resultado; lo que cambia es lo que lee quien mire el cuerpo después:
	// `[]` dice «no se seleccionó nada» y `null` dice «este cliente no supo armar la petición».
	if intakeIDs == nil {
		intakeIDs = []string{}
	}

	req, err := c.t.newAuthedRequest(ctx, http.MethodPost, "/api/v1/intakes/discard",
		discardIntakesRequest{IntakeIDs: intakeIDs}, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.doTyped(req, op, intakeStatusError)
	if err != nil {
		return nil, err
	}
	var out IntakeDiscardResult
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	if out.Discarded == nil {
		out.Discarded = []string{}
	}
	if out.Skipped == nil {
		out.Skipped = []IntakeDiscardSkip{}
	}
	return &out, nil
}
