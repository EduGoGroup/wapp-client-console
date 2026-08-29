package apiclient

import (
	"context"
	"net/http"
)

// tenants.go es el plano de LA EMPRESA DEL SUJETO: entre cuáles puede elegir quien tiene la sesión
// abierta, y cuál elige (Plan 047 · Ola 5 · T5.3).
//
// 🔴 ES LA ÚNICA EXCEPCIÓN A INV-04 DE TODO EL PAQUETE, y está aquí y no en otro sitio a propósito.
// El resto de métodos no acepta un `tenantID` porque no hay dónde ponerlo por error; `SetActive` sí,
// y lo manda en el cuerpo. No es una grieta: es la puerta que permite que INV-04 siga entero en todas
// las demás llamadas y que INV-8 siga entero en el CANJE, que es donde de verdad importa —los tres
// consumidores web re-canjean solos cada ~13 min, sin nadie delante, así que un `tenant_id` aceptado
// por el canje viajaría en cada refresco desatendido. Aquí viaja UNA vez, en una acción deliberada de
// una persona, y lo que el canje lee después está en el SERVIDOR.
//
// La excepción va vigilada por un test HERMANO con aserto POSITIVO (internal/web/inv04_test.go):
// esta ruta SÍ lleva `tenant_id` en el cuerpo, y es la única de toda la superficie que lo lleva.
// Meterla en la tabla del invariante lo rompería; dejarla fuera en silencio lo envejecería.

// TenantOption es UNA empresa del sujeto en la respuesta de GET /api/v1/auth/tenants.
//
// Tiene TRES campos y ninguno sobra: sin `id` el selector no puede mandar la elección de vuelta, sin
// `display_name` pinta UUIDs (que es lo que esta consola hacía hasta T5.3) y sin `active` no sabe
// cuál marcar al cargar.
//
// 🔴 `Active` LO CALCULA EL SERVIDOR con la MISMA regla que acota el token en el canje, y por eso la
// consola no lo deduce por su cuenta: si lo dedujera —«la primera», «la del token»— tendría una
// segunda fuente del mismo hecho, y dos fuentes del mismo hecho se desincronizan. Como mucho una es
// `true`, y puede que ninguna: «ninguna elegida todavía» es un estado legítimo y se expresa sin
// marcar nada.
type TenantOption struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Active      bool   `json:"active"`
}

// tenantListResponse es el sobre de GET /api/v1/auth/tenants.
//
// La API sirve un OBJETO y no un array pelado —aunque hoy solo tenga una clave— para poder crecer sin
// romper a los clientes; el tipo lo refleja en vez de decodificar contra `[]TenantOption` y depender
// de que nunca cambie.
type tenantListResponse struct {
	Tenants []TenantOption `json:"tenants"`
}

// selectActiveTenantRequest es el cuerpo de POST /api/v1/auth/active-tenant. Ver la cabecera: es el
// único cuerpo de este paquete con un `tenant_id` dentro.
type selectActiveTenantRequest struct {
	TenantID string `json:"tenant_id"`
}

// TenantsClient lee las empresas del sujeto y escribe cuál es la activa.
type TenantsClient struct {
	t *Transport
}

// NewTenantsClient construye el cliente del plano de empresas del sujeto.
func NewTenantsClient(t *Transport) *TenantsClient { return &TenantsClient{t: t} }

// List devuelve las empresas de quien tiene la sesión, con la activa marcada.
//
// 🔴 SE PUEDE LLAMAR CON UN TOKEN SIN EMPRESA, y es justo para lo que existe: el Context Token de
// quien tiene CERO empresas y el de quien tiene DOS y no ha elegido son IDÉNTICOS —los dos sin tenant
// y sin un solo grant—, así que sin este listado la consola no puede distinguir si toca pintar la
// pantalla de espera o el selector. Esa es la diferencia con `GET /api/v1/entitlements`, que responde
// 401 sin empresa y por eso la portada ni lo llama (ver sinEmpresa en internal/web).
//
// NUNCA devuelve nil: cero empresas es `[]`, no `null`. Quien lo consume cuenta elementos, y un nil
// que hubiera que distinguir de un vacío para el mismo hecho es un defecto, no una economía.
func (c *TenantsClient) List(ctx context.Context, accessToken string) ([]TenantOption, error) {
	const op = "tenants.list"
	req, err := c.t.newAuthedRequest(ctx, http.MethodGet, "/api/v1/auth/tenants", nil, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.do(req, op, statusError)
	if err != nil {
		return nil, err
	}
	var out tenantListResponse
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	if out.Tenants == nil {
		return []TenantOption{}, nil
	}
	return out.Tenants, nil
}

// SetActive fija la empresa con la que se entra a partir del SIGUIENTE canje. Responde 204 y no
// devuelve token: lo que la persona necesita después —su empresa y sus grants— no está en esta
// respuesta, está en su próximo Context Token, y el que tiene en la mano se emitió antes y sigue
// diciendo lo que decía. Por eso quien llama tiene que REFRESCAR la sesión después (ver
// SelectTenant en internal/web).
//
// 🔴 EL RECHAZO ES 404 Y NO 403, Y ESO NO SE TRADUCE A «no existe». La plataforma contesta lo mismo a
// «no eres miembro de esa empresa» y a «esa empresa no existe», a propósito: distinguirlas dejaría
// sondear UUIDs y levantar el censo de empresas de la plataforma. El texto que ve el usuario tiene
// que conservar esa ambigüedad (ver flashTenantNotYours en internal/web).
func (c *TenantsClient) SetActive(ctx context.Context, accessToken, tenantID string) error {
	const op = "tenants.set_active"
	req, err := c.t.newAuthedRequest(ctx, http.MethodPost, "/api/v1/auth/active-tenant",
		selectActiveTenantRequest{TenantID: tenantID}, accessToken)
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
