// Package apiclient habla con la API PÚBLICA de la plataforma (:8103 /api/v1) en nombre del
// administrador del tenant que tiene la sesión abierta en la consola.
//
// 🔴 INV-04 — el tenant NO viaja nunca. Ni en el cuerpo, ni en la query, ni en la ruta: la empresa
// sale del Context Token que la plataforma verifica en cada llamada. Ese es el motivo de que ningún
// método de este paquete acepte un `tenantID`: no hay dónde ponerlo por error, porque no existe el
// parámetro. Hay un test que lo afirma POR EL CABLE, inspeccionando la petición saliente.
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
// La guarda es de MD-055.2 y no es una regla de administración: una segunda membresía rompe el canje
// del token de esa persona (ErrMultipleTenants), así que añadirla no le daría una empresa más — le
// quitaría el login. Se levanta cuando el canje sepa elegir empresa, no antes.
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
}

// NewTransport construye el transporte contra la API pública. Un timeout <= 0 cae a defaultTimeout:
// un cliente sin plazo es un cuelgue esperando a ocurrir.
func NewTransport(baseURL string, timeout time.Duration) *Transport {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Transport{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: timeout},
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
