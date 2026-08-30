package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// FlowSummary es una fila del listado GET /api/v1/flows: un flujo del tenant del token con su
// ÚLTIMA versión. No trae la definición — para eso está GetFlow.
type FlowSummary struct {
	FlowID    string `json:"flow_id"`
	Version   int    `json:"version"`
	CreatedAt string `json:"created_at,omitempty"`
}

// publishFlowRequest es el cuerpo de POST /api/v1/flows. NO lleva `tenant_id` (INV-04): la empresa
// dueña de la versión que se publica es la del Context Token.
type publishFlowRequest struct {
	Definition json.RawMessage `json:"definition"`
}

// PublishFlowResult es la respuesta 201 de POST /api/v1/flows: qué flujo quedó publicado y en qué
// versión. El `flow_id` lo decide la DEFINICIÓN (viaja dentro del JSON), no la ruta.
type PublishFlowResult struct {
	FlowID  string `json:"flow_id"`
	Version int    `json:"version"`
}

// Trigger es una regla de disparo tal como la sirve GET /api/v1/triggers.
type Trigger struct {
	TriggerID string `json:"trigger_id"`
	Kind      string `json:"kind"`
	Keyword   string `json:"keyword,omitempty"`
	// EventKind solo viaja en reglas kind=event_start (Plan 043 · T2.1): qué tipo de evento
	// arranca o conmuta (menu|cart|survey|media). Vacío en el resto de los kinds.
	EventKind string `json:"event_kind,omitempty"`
	MatchType string `json:"match_type"`
	FlowID    string `json:"flow_id,omitempty"`
	Priority  int    `json:"priority"`
	Enabled   bool   `json:"enabled"`
	Message   string `json:"message,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	// ShadowedByEventList es una marca DERIVADA en el servidor (D-043.20/REQ-27b, MD-043.11): la
	// plataforma la calcula para las reglas kind=fallback a las que la lista de eventos del
	// despachador ya se ofrece antes, así que en la práctica no suenan. La consola la PINTA; no la
	// calcula ni la persiste.
	ShadowedByEventList bool `json:"shadowed_by_event_list,omitempty"`
}

// CreateTriggerRequest es el cuerpo de POST /api/v1/triggers. NO lleva `tenant_id` (INV-04).
type CreateTriggerRequest struct {
	Kind    string `json:"kind"`
	Keyword string `json:"keyword,omitempty"`
	// EventKind solo aplica —y solo se envía— cuando Kind es event_start. La plataforma también lo
	// exige, así que esto es defensa en profundidad, no la única guarda.
	EventKind string `json:"event_kind,omitempty"`
	MatchType string `json:"match_type,omitempty"`
	FlowID    string `json:"flow_id,omitempty"`
	Priority  int    `json:"priority"`
	Message   string `json:"message,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// Los desenlaces PROPIOS del editor. Existen por la misma razón que ErrMemberOfAnotherTenant: sin
// ellos, un conflicto del cloud llega al handler como ErrConflict a secas y la pantalla pinta el
// aviso genérico. La Ola 5 encontró exactamente eso en campo —un 409 sin sentinela enseñaba
// «Verifica tus credenciales», que era FALSO— y por eso aquí se traduce por operación.
//
// 🔴 Los tres ENVUELVEN a su sentinela general, así que quien solo quiera saber «hubo conflicto»
// sigue funcionando con errors.Is(err, ErrConflict). La consecuencia es que el ORDEN manda en
// cualquier switch que los distinga: preguntar antes por el genérico se come el significado.
var (
	// ErrFlowVersionConflict es el 409 de PUBLICAR un flujo: la definición que se envía no puede
	// convertirse en la versión siguiente porque alguien publicó entre medias.
	//
	// ⚠️ HOY LA PLATAFORMA NO LO EMITE. POST /api/v1/flows responde 201/400/401/500
	// (flujos/admin/handlers.go: InsertDefinition versiona siempre N+1, sin comprobar contra qué
	// versión se editaba). El sentinela se declara igual y no es adorno: sin él, el día que la
	// plataforma añada el control de versión —o lo añada un proxy por delante— el conflicto caería
	// en la rama por defecto del traductor de la pantalla y la dueña leería un aviso falso. Es la
	// misma trampa de la Ola 5, adelantada.
	ErrFlowVersionConflict = fmt.Errorf("apiclient: ese flujo cambió mientras lo editabas: %w", ErrConflict)

	// ErrTriggerDuplicate es el 409 de CREAR una regla de disparo: ya existe una regla equivalente.
	//
	// ⚠️ Con el mismo matiz que el de arriba: POST /api/v1/triggers responde hoy
	// 201/400/401/422/500 y no tiene unicidad en el store (flujos/admin/triggers.go: Insert no
	// distingue duplicado de fallo). Se declara por la misma razón.
	ErrTriggerDuplicate = fmt.Errorf("apiclient: ya existe una regla de disparo igual: %w", ErrConflict)

	// ErrTriggerWithoutEventStart es el 422 de CREAR una regla, y es el ÚNICO de los tres que la
	// plataforma sí devuelve hoy (Plan 054 · T2.7, D-054.8, MD-054.2): la regla está bien formada
	// —pasó las validaciones de REQ-D5— pero guardarla dejaría al contacto sin respuesta, porque un
	// `fallback` o un `keyword` durable sin una red de `event_start` detrás no lleva a ninguna
	// parte.
	//
	// 🔴 Necesita sentinela propio porque statusError mete 400 y 422 en el MISMO cajón
	// (ErrInvalidInput). Sin esto, la dueña leería «revisa los datos» ante un formulario cuyos datos
	// están todos bien: lo que falta no está en el formulario, está en el flujo. Envuelve a
	// ErrInvalidInput para que quien solo mire «lo rechazaron» siga funcionando.
	ErrTriggerWithoutEventStart = fmt.Errorf("apiclient: esa regla dejaría la conversación sin salida: %w", ErrInvalidInput)
)

// EditorClient sirve el plano del EDITOR de la empresa del token: los flujos que la conversación
// recorre y las reglas que deciden cuándo se entra en ellos.
//
// 🔴 Ninguno de sus seis métodos acepta un `tenantID`, y no es una omisión que haya que recordar: no
// existe el parámetro donde ponerlo (INV-04). La empresa sale del Context Token; el contrato entero
// está en transport.go.
//
// 🔴 Lo que este cliente NO trae del BFF, a propósito: su `RejectionError`, que llevaba el CUERPO
// del upstream hasta la pantalla para pintarlo tal cual. Eso hacía dos cosas malas a la vez —
// enseñaba a la dueña un texto escrito para desarrolladores («definición de flujo inválida:
// node "x" …»), y dejaba que la plataforma decidiera qué dice esta consola—. Aquí el desenlace lo
// deciden el sentinela y el catálogo de flash; el cuerpo del no-2xx se drena acotado a 4 KB
// (maxErrorBody, en transport.go) y no viaja a ningún sitio.
type EditorClient struct {
	t *Transport
}

// NewEditorClient construye el cliente del editor.
func NewEditorClient(t *Transport) *EditorClient { return &EditorClient{t: t} }

// publishFlowStatusError es el traductor de PUBLICAR: idéntico al general salvo en el 409, que aquí
// significa «alguien publicó otra versión mientras editabas» y no «ya existe».
func publishFlowStatusError(op string, status int) error {
	if status == http.StatusConflict {
		return ErrFlowVersionConflict
	}
	return statusError(op, status)
}

// createTriggerStatusError es el traductor de CREAR una regla, y separa DOS códigos que el general
// confunde:
//
//   - el 409, que aquí es «ya existe una igual» y no un conflicto cualquiera;
//   - el 422, que NO es un 400 con otro número: el cuerpo se entiende y aun así no se puede guardar
//     (MD-054.2). statusError los mete a los dos en ErrInvalidInput, y ahí se pierde justo la
//     diferencia que la pantalla necesita para decir qué hacer.
func createTriggerStatusError(op string, status int) error {
	switch status {
	case http.StatusConflict:
		return ErrTriggerDuplicate
	case http.StatusUnprocessableEntity:
		return ErrTriggerWithoutEventStart
	default:
		return statusError(op, status)
	}
}

// ListFlows lee los flujos de la empresa DEL TOKEN, cada uno con su última versión
// (GET /api/v1/flows, scope `flows.read`).
//
// El tenant no se pasa: no hay parámetro donde ponerlo (INV-04). La API devuelve siempre un arreglo
// —vacío si la empresa aún no ha publicado ninguno—, nunca null.
func (c *EditorClient) ListFlows(ctx context.Context, accessToken string) ([]FlowSummary, error) {
	const op = "flows.list"
	req, err := c.t.newAuthedRequest(ctx, http.MethodGet, "/api/v1/flows", nil, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.do(req, op, statusError)
	if err != nil {
		return nil, err
	}
	var out []FlowSummary
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []FlowSummary{}
	}
	return out, nil
}

// GetFlow devuelve la definición VIGENTE (la última versión) del flujo `flowID` tal cual la sirve la
// plataforma (GET /api/v1/flows/{id}, scope `flows.read`).
//
// Se devuelve JSON CRUDO y no un struct a propósito: la consola no modela flujos —los pinta y los
// reenvía—, y modelarlos aquí obligaría a perseguir cada nodo nuevo del Motor con una release de
// esta consola.
//
// 🔴 Su 404 es FRONTERA DE EMPRESA, no «no existe»: el store filtra por tenant, así que un flow_id
// de otra empresa responde 404 igual que uno inventado. Ver ErrNotFound.
func (c *EditorClient) GetFlow(ctx context.Context, accessToken, flowID string) (json.RawMessage, error) {
	const op = "flows.get"
	req, err := c.t.newAuthedRequest(ctx, http.MethodGet, "/api/v1/flows/"+pathSegment(flowID), nil, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.do(req, op, statusError)
	if err != nil {
		return nil, err
	}
	var out json.RawMessage
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// PublishFlow publica `definition` como una versión NUEVA (POST /api/v1/flows, scope `flows.write`).
//
// No hay «guardar sobre»: cada publicación es la versión N+1 y las anteriores se conservan. El
// `flow_id` va DENTRO de la definición, no en la ruta — por eso este método no lo recibe aparte.
//
// Un 400 aquí es lo más frecuente y significa que el JSON no valida contra el esquema del Motor;
// llega como ErrInvalidInput, y el detalle del validador NO se propaga (ver el docstring del tipo).
func (c *EditorClient) PublishFlow(ctx context.Context, accessToken string, definition []byte) (*PublishFlowResult, error) {
	const op = "flows.publish"
	payload := publishFlowRequest{Definition: json.RawMessage(definition)}
	req, err := c.t.newAuthedRequest(ctx, http.MethodPost, "/api/v1/flows", payload, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.do(req, op, publishFlowStatusError)
	if err != nil {
		return nil, err
	}
	var out PublishFlowResult
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListTriggers lee las reglas de disparo de la empresa DEL TOKEN (GET /api/v1/triggers, scope
// `triggers.read`). Arreglo siempre, nunca null.
func (c *EditorClient) ListTriggers(ctx context.Context, accessToken string) ([]Trigger, error) {
	const op = "triggers.list"
	req, err := c.t.newAuthedRequest(ctx, http.MethodGet, "/api/v1/triggers", nil, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.do(req, op, statusError)
	if err != nil {
		return nil, err
	}
	var out []Trigger
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Trigger{}
	}
	return out, nil
}

// CreateTrigger crea una regla de disparo en la empresa del token (POST /api/v1/triggers, scope
// `triggers.create`). Devuelve 201 con la regla creada, ya con su `trigger_id`.
func (c *EditorClient) CreateTrigger(ctx context.Context, accessToken string, tr CreateTriggerRequest) (*Trigger, error) {
	const op = "triggers.create"
	req, err := c.t.newAuthedRequest(ctx, http.MethodPost, "/api/v1/triggers", tr, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.do(req, op, createTriggerStatusError)
	if err != nil {
		return nil, err
	}
	var out Trigger
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteTrigger borra la regla `triggerID` de la empresa del token (DELETE /api/v1/triggers/{id},
// scope `triggers.delete`). 204 si se borró; 404 si esa regla no es de esta empresa.
//
// ⚠️ La pantalla lo dispara por POST —la casa es SSR pura, sin fetch(), ver server.go— y es AQUÍ
// donde el verbo se convierte en DELETE. El navegador nunca emite un DELETE.
func (c *EditorClient) DeleteTrigger(ctx context.Context, accessToken, triggerID string) error {
	const op = "triggers.delete"
	req, err := c.t.newAuthedRequest(ctx, http.MethodDelete, "/api/v1/triggers/"+pathSegment(triggerID), nil, accessToken)
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
