// Package apiclient habla con la API PÚBLICA de la plataforma (:8103 /api/v1) en nombre del
// administrador del tenant que tiene la sesión abierta en la consola.
//
// 🔴 INV-04 — el tenant NO viaja. Ni en el cuerpo, ni en la query, ni en la ruta: la empresa sale del
// Context Token que la plataforma verifica en cada llamada, y por eso los métodos de este paquete no
// aceptan un `tenantID` — no hay dónde ponerlo por error, porque no existe el parámetro. Hay un test
// que lo afirma POR EL CABLE, inspeccionando la petición saliente.
//
// 🆕 Y TIENE EXACTAMENTE UNA EXCEPCIÓN, declarada aquí para que no envejezca escondida:
// `TenantsClient.SetActive` (POST /api/v1/auth/active-tenant), que sí acepta un `tenantID` y lo manda
// en el cuerpo. Es la elección de empresa de quien pertenece a varias, y es lo que permite que el
// CANJE no tenga que aceptar ninguna (INV-8): el porqué entero está en tenants.go, y la excepción va
// con test HERMANO y aserto POSITIVO en internal/web/inv04_test.go. Una segunda excepción no se añade
// sin pasar por ahí.
//
// 🔴 No hay —ni debe haber— un cliente del plano admin (:8100). Este paquete es el del perímetro del
// CLIENTE (ver internal/config).
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// defaultTimeout acota cada llamada si quien construye el cliente no da plazo. En la consola el
// valor efectivo sale de la config (WAPP_CONSOLE_UPSTREAM_TIMEOUT_SECS); esto es solo el suelo para
// que un cliente mal construido no se cuelgue para siempre.
const defaultTimeout = 15 * time.Second

// DefaultInferenceTimeout es el plazo del cliente de INFERENCIA (ver Transport.inference).
//
// 🔴 55s, y el número no es holgura por si acaso. Medido contra UAT el 2026-08-28 desde el BFF, la
// sugerencia de cotización tardó 24,8 / 28,4 / 29,7 / 35,5 segundos con el modelo ya cargado, y el
// cloud se da a sí mismo 48s para redactar (`pipeline.PlazoPorLlamadaSuelo`), así que el techo
// realista de la respuesta es ~48-50s: 55s = eso más el viaje y el cierre. El plazo general de esta
// consola son 15s (o los 20s que pone la config), y por ahí esa llamada no cabe: muere sin
// respuesta.
//
// ⚠️ Este plazo arregla UN plazo de los tres, y solo el de este cliente HTTP. Los otros dos son del
// servidor de la consola —`RequestDeadline(UpstreamTimeout)` (20s) en el router y `WriteTimeout`
// (30s)— y siguen cortando por debajo: quien monte la PANTALLA de la sugerencia tendrá que darle su
// propio plazo a esa ruta, como hizo el BFF en el Plan 047 · T2.4. Aquí no se toca nada de eso: esta
// casilla es el cliente.
const DefaultInferenceTimeout = 55 * time.Second

// maxErrorBody acota lo que se lee del cuerpo de un no-2xx. El detalle del upstream NO se pinta al
// usuario (el código HTTP y el catálogo de flash deciden el texto), pero sí se registra, y un
// upstream que responda megabytes no debe poder llenar el log.
const maxErrorBody = 4 << 10

// Los cinco desenlaces que las pantallas distinguen. Son SENTINELAS y no códigos sueltos porque el
// handler decide el mensaje con errors.Is: un `switch` sobre números repartido por los handlers es
// exactamente lo que se desincroniza cuando se añade una pantalla.
var (
	// ErrUnauthorized es el 401: el Context Token no vale (caducó o lo revocaron). Quien lo recibe
	// reintenta UNA vez tras refrescar (AuthHandler.withAuthRetry) y, si persiste, expulsa a /login.
	ErrUnauthorized = errors.New("apiclient: no autorizado")

	// ErrForbidden es el 403: el token es válido pero al usuario le falta el scope
	// (members.read/write, roles.read/write, entitlements.read) o no trae empresa.
	ErrForbidden = errors.New("apiclient: sin permiso")

	// ErrNotFound es el 404.
	//
	// 🔴 AQUÍ UN 404 NO SIGNIFICA «no existe». La plataforma responde 404 —y no 403— cuando el UUID
	// pertenece a OTRA empresa, y lo hace a propósito: distinguir los dos casos le diría al llamante
	// que ese identificador existe en algún sitio. Por eso el texto que ve el usuario no puede ser
	// «no encontrado» a secas; ver el catálogo de flash de internal/web.
	ErrNotFound = errors.New("apiclient: no existe en tu empresa")

	// ErrConflict es el 409 genérico: el recurso ya existe (por ejemplo, un rol con ese nombre).
	ErrConflict = errors.New("apiclient: conflicto")

	// ErrInvalidInput es el 400: la plataforma rechazó los datos (campo vacío, formato inválido).
	ErrInvalidInput = errors.New("apiclient: entrada inválida")
)

// ErrMemberOfAnotherTenant es el 409 del plano de MIEMBROS, y no es «ya está»: significa que esa
// persona pertenece a otra empresa.
//
// La guarda es de MD-055.2 y nació sin ser una regla de administración: mientras el canje no supo
// elegir empresa, una segunda membresía le ROMPÍA el login a esa persona (ErrMultipleTenants), así
// que añadirla no le daba una empresa más — se la quitaba.
//
// 🆕 Ese motivo ya no vale: el canje sabe elegir (Plan 047 · Ola 5, D-047.14) y esta consola tiene
// selector. Lo que queda del 409 es una decisión COMERCIAL —el alta de una segunda empresa la honra
// el entitlement `multi_empresa` (T5.2)—, así que el sentinela sigue existiendo y su texto sigue
// siendo el mismo: lo que cambia es que ahora significa «tu plan no lo incluye», no «le romperías
// el acceso».
//
// Envuelve a ErrConflict, así que quien solo quiera saber «hubo conflicto» sigue funcionando con
// errors.Is(err, ErrConflict); quien quiera dar el mensaje exacto pregunta por este.
var ErrMemberOfAnotherTenant = fmt.Errorf("apiclient: la persona ya pertenece a otra empresa: %w", ErrConflict)

// ErrPersonUnknown es el 404 del ALTA de un miembro, y es la ÚNICA operación de esta consola donde
// un 404 sí significa «no existe».
//
// El motivo es que el alta no pregunta por un recurso de la empresa: pregunta por una persona del
// PADRÓN. La plataforma consulta identity con su credencial M2M y, si ese UUID no está allí,
// responde 404 — sin frontera de tenant que proteger, porque no hay nada al otro lado. Y es el
// desenlace más probable de esa pantalla: quien pega un identificador se equivoca de carácter.
//
// 🔴 ENVUELVE a ErrNotFound —igual que ErrMemberOfAnotherTenant envuelve a ErrConflict—, así que
// quien solo quiera saber «hubo un 404» sigue funcionando con errors.Is(err, ErrNotFound). La
// consecuencia es que el ORDEN manda en cualquier switch que los distinga: preguntar antes por el
// genérico se come este significado y el usuario lee «no pertenece a tu empresa» ante un UUID que
// no existe en ninguna. Ver flashCodeFor en internal/web.
var ErrPersonUnknown = fmt.Errorf("apiclient: esa persona no existe en el proveedor de identidad: %w", ErrNotFound)

// APIError es un fallo con el status del upstream, para los códigos que no tienen sentinela (5xx y
// cualquier otro inesperado). StatusCodeOf lo extrae.
type APIError struct {
	Op         string
	StatusCode int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("apiclient: %s devolvió status %d", e.Op, e.StatusCode)
}

// StatusCodeOf devuelve el status HTTP del upstream si el error lo lleva (0 si no).
func StatusCodeOf(err error) int {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	return 0
}

// Transport es la base HTTP compartida por los clientes de dominio: URL base, http.Client con plazo
// y la construcción de peticiones autenticadas.
type Transport struct {
	baseURL string
	http    *http.Client

	// inference es el cliente de las llamadas que ESPERAN A UN MODELO. Hoy hay UNA sola
	// —IntakesClient.SuggestIntakeQuote— y un test estructural vigila que siga siendo una sola.
	//
	// 🔴 EXISTE PORQUE http.Client.Timeout NO SE PUEDE SOBRESCRIBIR POR PETICIÓN: es un campo del
	// cliente, no del request, y entre el plazo del cliente y el del contexto gana SIEMPRE el menor.
	// Un ctx de 58s sobre un cliente de 15s se sigue cortando a los 15s, así que el único modo de
	// darle a UNA llamada un plazo mayor sin dárselo a todas es que esa llamada use OTRO cliente.
	//
	// Comparte el RoundTripper (los dos van con Transport nil == http.DefaultTransport), así que
	// comparte el pool de conexiones con el general: lo que cambia es el plazo, no el cable.
	inference *http.Client
}

// NewTransport construye el transporte contra la API pública. Un timeout <= 0 cae a defaultTimeout:
// un cliente sin plazo es un cuelgue esperando a ocurrir.
func NewTransport(baseURL string, timeout time.Duration) *Transport {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Transport{
		baseURL:   strings.TrimRight(baseURL, "/"),
		http:      &http.Client{Timeout: timeout},
		inference: &http.Client{Timeout: DefaultInferenceTimeout},
	}
}

// statusError traduce un status no-2xx al sentinela que le corresponde.
//
// El 409 NO se traduce aquí a ErrMemberOfAnotherTenant: ese significado es del plano de miembros y
// lo pone su propio traductor (members.go). Aquí el 409 es «conflicto» a secas.
func statusError(op string, status int) error {
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("%s: %w", op, ErrUnauthorized)
	case http.StatusForbidden:
		return fmt.Errorf("%s: %w", op, ErrForbidden)
	case http.StatusNotFound:
		return fmt.Errorf("%s: %w", op, ErrNotFound)
	case http.StatusConflict:
		return fmt.Errorf("%s: %w", op, ErrConflict)
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return fmt.Errorf("%s: %w", op, ErrInvalidInput)
	default:
		return &APIError{Op: op, StatusCode: status}
	}
}

// do ejecuta la petición y devuelve la respuesta solo si es 2xx; en otro caso drena, cierra y
// traduce el status con `translate` (statusError salvo que el dominio tenga uno propio).
func (t *Transport) do(req *http.Request, op string, translate func(string, int) error) (*http.Response, error) {
	resp, err := t.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("apiclient: %s: %w", op, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status := resp.StatusCode
		drainClose(resp.Body)
		return nil, translate(op, status)
	}
	return resp, nil
}

// doTyped es `do` para los planos cuyo no-2xx trae un CUERPO que hay que leer para saber QUÉ pasó.
//
// La diferencia con `do` es una sola y es la razón de que existan los dos: `do` drena el cuerpo y le
// pasa al traductor solo el status, porque en el plano de administración el código HTTP es toda la
// información. En la bandeja NO lo es —el 400 de aprobar puede ser «faltan precios» (con la lista
// entera) o «falta el texto», y el 422 puede ser «este estado no aprueba» o «otro operador la
// movió», y a los cuatro solo los separa la clave `error` del cuerpo—, así que el traductor tiene
// que verlo.
//
// El traductor recibe la respuesta VIVA y este método la cierra después: quien traduzca lee lo que
// necesite (acotado) y no se ocupa del cierre.
func (t *Transport) doTyped(req *http.Request, op string, translate func(string, *http.Response) error) (*http.Response, error) {
	return t.doWith(t.http, req, op, translate)
}

// doInference es doTyped por el cliente de plazo largo. ÚNICO llamante permitido:
// IntakesClient.SuggestIntakeQuote (ver Transport.inference); hay un test estructural que lo cuenta.
func (t *Transport) doInference(req *http.Request, op string, translate func(string, *http.Response) error) (*http.Response, error) {
	return t.doWith(t.inference, req, op, translate)
}

// doWith es el viaje compartido por doTyped y doInference: lo único que cambia entre los dos es el
// http.Client, y por tanto el plazo.
func (t *Transport) doWith(client *http.Client, req *http.Request, op string, translate func(string, *http.Response) error) (*http.Response, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("apiclient: %s: %w", op, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer drainClose(resp.Body)
		return nil, translate(op, resp)
	}
	return resp, nil
}

// newAuthedRequest arma la petición con el Context Token de la sesión.
//
// `path` ya viene con sus segmentos escapados por quien lo compone (ver pathSegment): este método no
// re-escapa nada, porque hacerlo dos veces rompe los UUID con guiones tanto como no hacerlo ninguna.
func (t *Transport) newAuthedRequest(ctx context.Context, method, path string, payload any, accessToken string) (*http.Request, error) {
	var body io.Reader
	var raw []byte
	if payload != nil {
		var err error
		raw, err = json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("apiclient: serializar %s: %w", path, err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("apiclient: construir petición %s: %w", path, err)
	}
	if raw != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	return req, nil
}

// decodeJSON lee el cuerpo 2xx en `out` y cierra siempre.
func decodeJSON(resp *http.Response, op string, out any) error {
	defer drainClose(resp.Body)
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("apiclient: %s: decodificar respuesta: %w", op, err)
	}
	return nil
}

// discard consume y cierra el cuerpo de una respuesta que no trae datos (204).
func discard(resp *http.Response) {
	drainClose(resp.Body)
}

// drainClose vacía y cierra el cuerpo para que la conexión vuelva al pool en vez de quedar colgada.
func drainClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxErrorBody))
	_ = body.Close()
}
