package apiclient

import (
	"context"
	"net/http"
	"net/url"
)

// Member es una membresía del tenant tal como la sirve GET /api/v1/members: EXACTAMENTE lo que
// guarda `tenant_members`.
//
// 🔴 NO HAY `name` NI `email`, y su ausencia es el CONTRATO, no un hueco por rellenar. La persona
// vive en el proveedor de identidad (INV-02) y wApp no guarda PII de personas: la migración de
// `tenant_members` lo dice con todas sus letras («CERO PII»). Quien venga a "arreglarlo" añadiendo
// una consulta al padrón que devuelva nombres estará cambiando una decisión de producto, no
// completando un DTO.
//
// La consola pinta el UUID de forma legible y dice de dónde sale la identidad; ver internal/web.
type Member struct {
	UserID    string `json:"user_id"`
	TenantID  string `json:"tenant_id"`
	CreatedAt string `json:"created_at,omitempty"`
}

// MembersClient sirve el plano de MEMBRESÍA: quién está en la empresa y quién deja de estarlo.
//
// 🔴 Aquí NO hay alta (POST /api/v1/members). No es un olvido: el alta necesita el UUID de identity
// de la persona, y hoy la consola no tiene forma de resolverlo desde un correo. Añadir un formulario
// que pida un UUID a la dueña del negocio sería trasladarle un hueco del cloud.
type MembersClient struct {
	t *Transport
}

// NewMembersClient construye el cliente de membresía.
func NewMembersClient(t *Transport) *MembersClient { return &MembersClient{t: t} }

// memberStatusError es el traductor del plano de miembros: idéntico al general salvo en el 409, que
// aquí significa «esa persona ya pertenece a otra empresa» (MD-055.2) y no «ya existe».
//
// Lo usan las dos operaciones del plano —también la baja—, y no solo la que hoy más lo dispararía:
// la baja pasa por el mismo writeDomainError del servidor, así que un ErrConflict del dominio
// saldría por ahí igual. Un traductor que solo cubriera una de las dos sería una asimetría a la
// espera de morder.
func memberStatusError(op string, status int) error {
	if status == http.StatusConflict {
		return ErrMemberOfAnotherTenant
	}
	return statusError(op, status)
}

// List lee los miembros de la empresa DEL TOKEN (GET /api/v1/members, scope `members.read`).
//
// El tenant no se pasa: no hay parámetro donde ponerlo (INV-04). La API devuelve siempre un arreglo
// —vacío si la empresa aún no tiene a nadie—, nunca null.
func (c *MembersClient) List(ctx context.Context, accessToken string) ([]Member, error) {
	const op = "members.list"
	req, err := c.t.newAuthedRequest(ctx, http.MethodGet, "/api/v1/members", nil, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.do(req, op, memberStatusError)
	if err != nil {
		return nil, err
	}
	var out []Member
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Member{}
	}
	return out, nil
}

// Remove da de baja a la persona de la empresa del token (DELETE /api/v1/members/{user_id}, scope
// `members.write`). Es idempotente en el servidor: 204 aunque ya no fuera miembro.
//
// ⚠️ La baja NO retira los roles ni los grants de esa persona (out.MembershipRepo.Remove): sin
// membresía el canje ya no le resuelve permisos, y readmitirla no obliga a reconstruir su rol. La
// pantalla lo dice, para que nadie lea «dar de baja» como «borrar sus permisos».
func (c *MembersClient) Remove(ctx context.Context, accessToken, userID string) error {
	const op = "members.remove"
	req, err := c.t.newAuthedRequest(ctx, http.MethodDelete, "/api/v1/members/"+pathSegment(userID), nil, accessToken)
	if err != nil {
		return err
	}
	resp, err := c.t.do(req, op, memberStatusError)
	if err != nil {
		return err
	}
	discard(resp)
	return nil
}

// pathSegment escapa un valor que va a un SEGMENTO de la ruta.
//
// No es cosmética: el `user_id` y el `role_id` llegan de un formulario, y sin escapar, un valor con
// `/` o `?` reescribiría la ruta y podría acabar llamando a OTRO endpoint de la API pública con el
// token del usuario. url.PathEscape deja intactos los UUID (letras, dígitos y guiones) y neutraliza
// todo lo demás.
func pathSegment(v string) string { return url.PathEscape(v) }
