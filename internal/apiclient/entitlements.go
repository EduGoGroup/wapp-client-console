package apiclient

import (
	"context"
	"net/http"
)

// Entitlements es la respuesta de GET /api/v1/entitlements: el plan del tenant y sus features
// EFECTIVAS (las del plan con los overrides del tenant ya aplicados), en el orden estable del
// servidor.
//
// La lista trae SOLO las habilitadas —no un mapa clave→bool—, así que la UI decide por PERTENENCIA:
// lo que no está en la lista, no está contratado. Esa forma es la que hace que el gate sea
// fail-closed sin escribir una sola condición extra: una respuesta que no llega es una lista vacía,
// y una lista vacía no habilita nada.
type Entitlements struct {
	Plan            string   `json:"plan"`
	Features        []string `json:"features"`
	CacheTTLSeconds int      `json:"cache_ttl_seconds"`
}

// EntitlementsClient lee el plan efectivo del tenant.
type EntitlementsClient struct {
	t *Transport
}

// NewEntitlementsClient construye el cliente de entitlements.
func NewEntitlementsClient(t *Transport) *EntitlementsClient { return &EntitlementsClient{t: t} }

// Get lee el plan y las features efectivas del tenant DEL TOKEN (scope `entitlements.read`). Un
// token válido sin ese scope devuelve 403, que el llamante distingue con errors.Is(err, ErrForbidden)
// para dar un aviso distinto del «no se pudo consultar».
//
// 🔴 SIN CACHÉ PROPIA, y es deliberado (D-040.6): se pide UNA vez por petición. `cache_ttl_seconds`
// lo publica el servidor para quien quiera cachear, pero una consola que guardara el plan seguiría
// enseñando una sección durante minutos después de que el operador la cortara desde la consola de
// plataforma, y eso convierte el kill-switch comercial en una sugerencia.
func (c *EntitlementsClient) Get(ctx context.Context, accessToken string) (*Entitlements, error) {
	const op = "entitlements.get"
	req, err := c.t.newAuthedRequest(ctx, http.MethodGet, "/api/v1/entitlements", nil, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.do(req, op, statusError)
	if err != nil {
		return nil, err
	}
	var out Entitlements
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
