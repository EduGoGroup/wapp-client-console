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

// MembersClient sirve el plano de MEMBRESÍA: quién entra en la empresa, quién está y quién deja de
// estar.
//
// El alta EXISTE (Add), y la vía de hoy es que la persona aporte su propio identificador: se
// registra, lo copia de «Mi identificador» (T1.6) y se lo pasa a quien administra, que lo pega en el
// formulario de /miembros. No es la vía definitiva, pero es una vía real y sin oráculos.
//
// 🔴 Lo que sigue SIN existir, y no por olvido, es la búsqueda por correo. Las dos formas de tenerla
// están descartadas a conciencia:
//   - `EnsureUser` de identity resolvería el correo, pero es ESCRITURA: si el correo no existe, crea
//     la cuenta. Eso deja cuentas fantasma que nadie ha reclamado y que un tercero podría adoptar al
//     registrarse con ese correo — heredando de golpe la membresía y los roles que se le pusieran.
//   - un buscador de solo lectura sobre el padrón sería un ORÁCULO DE ENUMERACIÓN: cualquier dueña
//     de cualquier empresa podría preguntar «¿está fulano en wApp?» sobre correos ajenos.
//
// La vía buena es la INVITACIÓN —quien administra la emite, la persona la canjea y se incorpora
// sola—, está aprobada como Ola A / D-047.11 y todavía no se ha empezado. Cuando llegue, este pegar
// un UUID se retira; hasta entonces es lo que hay, y funciona.
type MembersClient struct {
	t *Transport
}

// addMemberRequest es el cuerpo de POST /api/v1/members. NO lleva `tenant_id` (INV-04): la empresa
// que recibe a la persona es la del Context Token.
type addMemberRequest struct {
	UserID string `json:"user_id"`
}

// NewMembersClient construye el cliente de membresía.
func NewMembersClient(t *Transport) *MembersClient { return &MembersClient{t: t} }

// memberStatusError es el traductor del plano de miembros: idéntico al general salvo en el 409, que
// aquí significa «esa persona ya pertenece a otra empresa» (MD-055.2) y no «ya existe».
//
// Lo usan el listado y la baja, y no solo la que hoy más lo dispararía: la baja pasa por el mismo
// writeDomainError del servidor, así que un ErrConflict del dominio saldría por ahí igual. Un
// traductor que solo cubriera una de las dos sería una asimetría a la espera de morder.
//
// El ALTA no pasa por aquí: tiene el suyo (memberAddStatusError), porque su 404 significa otra cosa.
func memberStatusError(op string, status int) error {
	if status == http.StatusConflict {
		return ErrMemberOfAnotherTenant
	}
	return statusError(op, status)
}

// memberAddStatusError es el traductor del ALTA, y es distinto del de las demás operaciones del
// plano por UN código: el 404.
//
// En el resto de la consola un 404 es una frontera de tenant («ese identificador no es de tu
// empresa») y memberStatusError lo deja caer a statusError, que lo traduce a ErrNotFound. En el alta
// significa lo contrario —esa persona no está en el padrón de identity— y traducirlo igual daría un
// diagnóstico FALSO justo en el fallo más frecuente de la pantalla: el UUID mal pegado. Por eso hay
// dos traductores y no uno con un parámetro: la diferencia es de significado, no de configuración.
func memberAddStatusError(op string, status int) error {
	switch status {
	case http.StatusNotFound:
		return ErrPersonUnknown
	case http.StatusConflict:
		return ErrMemberOfAnotherTenant
	default:
		return statusError(op, status)
	}
}

// Add incorpora a la empresa del token a alguien que YA TIENE CUENTA, por su identificador de
// identity (POST /api/v1/members, scope `members.write`). Es idempotente: 204 aunque ya fuera
// miembro.
//
// Lo que hace la plataforma por debajo, porque explica los desenlaces raros: consulta identity con
// su credencial M2M para comprobar que esa persona existe y que está acreditada en `wapp.bff`. De
// ahí salen dos códigos que ninguna otra operación de esta consola devuelve —502 si identity no la
// acredita y 503 si identity no responde o no hay cliente M2M configurado—, y ninguno de los dos es
// culpa de lo que la dueña escribió. Los dos caen al aviso genérico de «inténtalo de nuevo», que es
// exactamente lo que toca hacer.
//
// El 204 NO distingue «la acabo de incorporar» de «ya estaba»: la plataforma lo hace a propósito y
// aquí no se inventa la diferencia.
func (c *MembersClient) Add(ctx context.Context, accessToken, userID string) error {
	const op = "members.add"
	req, err := c.t.newAuthedRequest(ctx, http.MethodPost, "/api/v1/members", addMemberRequest{UserID: userID}, accessToken)
	if err != nil {
		return err
	}
	resp, err := c.t.do(req, op, memberAddStatusError)
	if err != nil {
		return err
	}
	discard(resp)
	return nil
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
