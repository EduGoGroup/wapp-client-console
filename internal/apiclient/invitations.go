package apiclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// invitations.go es el plano de INVITACIONES: el camino por el que alguien entra en la empresa sin
// que quien administra tenga que teclear el identificador de nadie (Plan 047 · Ola A, D-047.11).
//
// Son DOS audiencias y por eso hay DOS traductores de estado más abajo:
//   - quien EMITE, lista y revoca es la dueña, con su Context Token y sus scopes `members.*`;
//   - quien CANJEA acaba de registrarse: su Context Token va SIN empresa y SIN un solo grant, así
//     que el canje es la única llamada de este paquete que no exige ningún permiso (la plataforma la
//     monta detrás de `Authenticate` y sin `RequirePermission`).
//
// 🔴 EL TOKEN EN CLARO EXISTE UNA SOLA VEZ: en la respuesta del POST de emisión. Ni el listado ni
// ninguna otra respuesta lo devuelven, y en la tabla vive solo su SHA-256. Si se pierde, se revoca
// esa invitación y se emite otra; no hay forma de volver a consultarlo, y esta consola no inventa
// ninguna (ver Invitation, que NO tiene campo para él).

// Invitation es la proyección pública de una invitación (GET /api/v1/invitations).
//
// 🔴 LO QUE NO ESTÁ AQUÍ ES EL CONTRATO: no hay `Token` ni `TokenHash`, y su ausencia no es un hueco
// por rellenar. El digest es la clave de acceso del canje —quien lo tuviera podría buscar la fila por
// él— y el token en claro ya no existe en ninguna parte del servidor. Un campo `Token` en este struct
// no serviría de nada salvo el día que alguien hiciera que el servidor lo devolviera: entonces
// pasaría a la pantalla sin que nadie tuviera que tocar la plantilla.
//
// `Status` es DERIVADO en el servidor y no una columna: pending | redeemed | revoked | expired. La
// caducidad no tiene escritura que la anuncie —ocurre por el paso del tiempo—, así que deducirla aquí
// significaría reescribir la regla de precedencia (canje > revocación > caducidad) en un segundo
// sitio, con dos relojes distintos además (el de Postgres y el de esta máquina).
type Invitation struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	ExpiresAt  string `json:"expires_at"`
	RoleID     string `json:"role_id,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	RedeemedAt string `json:"redeemed_at,omitempty"`
	RevokedAt  string `json:"revoked_at,omitempty"`
}

// IssuedInvitation es la respuesta del POST: la invitación MÁS el token en claro. Es el ÚNICO tipo de
// este paquete que lo lleva, y por eso es un tipo aparte y no un campo opcional de Invitation: el
// listado no puede devolverlo ni por descuido, porque su tipo no tiene dónde ponerlo.
type IssuedInvitation struct {
	Invitation
	Token string `json:"token"`
}

// issueInvitationRequest es el cuerpo de POST /api/v1/invitations. Los DOS campos son opcionales y un
// cuerpo `{}` es una petición válida: invitación sin rol y con la caducidad por defecto.
//
// 🔴 LA CLAVE ES `ttl`, NO `ttl_seconds`, Y EQUIVOCARLA NO DA ERROR. `encoding/json` ignora en
// silencio las claves que el servidor no conoce, así que con el nombre mal escrito el TTL elegido
// NUNCA llega y todo vive el default de 24 h sin un solo aviso: la pantalla diría «1 hora», la
// invitación viviría un día y ningún gate se pondría rojo. Esa trampa ya costó un test dedicado en la
// consola de operadores; aquí hay otro (invitations_test.go) que mira el cuerpo POR EL CABLE.
//
// 🔴 NO HAY CAMPO DE CORREO, NI DE NOMBRE, NI DE TELÉFONO, y su ausencia también es contrato
// (D-047.11): quien emite no teclea el correo de nadie. Reparte el código por WhatsApp y la nube
// nunca sabe a quién se lo mandó.
//
// El `omitempty` de las dos es lo que hace que «sin rol» y «con la caducidad de siempre» se digan NO
// MANDANDO el campo, en vez de mandando un cero que el servidor tendría que interpretar.
type issueInvitationRequest struct {
	RoleID     string `json:"role_id,omitempty"`
	TTLSeconds int    `json:"ttl,omitempty"`
}

// redeemInvitationRequest es el cuerpo de POST /api/v1/invitations/accept.
//
// 🔴 EL TOKEN VIAJA EN EL CUERPO Y NO EN LA RUTA, y es una decisión del 2026-08-28 que sustituye a la
// forma `{token}/accept` de los borradores: un secreto en la URL acaba en el log de acceso de
// cualquier proxy, en la cabecera `Referer` de lo que se cargue después y en el historial del
// navegador de quien lo pegó. El cuerpo de un POST no aparece en ninguno de los tres.
//
// Tiene UN SOLO CAMPO, y eso es INV-04 escrito en un tipo: la empresa a la que entra esta persona
// sale de la FILA de la invitación —la eligió quien la emitió, con su propio token— y no puede salir
// de ningún otro sitio, porque no hay ningún otro sitio donde ponerla.
type redeemInvitationRequest struct {
	Token string `json:"token"`
}

// Los desenlaces PROPIOS de este plano. Los tres del canje existen porque son la única información
// que la persona que acaba de pegar un código va a recibir, y el traductor genérico los fundiría en
// dos textos inservibles («conflicto» y «no pertenece a tu empresa»).
var (
	// ErrInvitationRedeemed es el 409 de REVOCAR: esa invitación ya se canjeó.
	//
	// 🔴 NO es «ya estaba revocada» —eso responde 204, porque el estado que se pedía ya es el que
	// hay— y no se puede fingir que sí: revocar una invitación consumida NO deshace la membresía que
	// el canje escribió, así que contestar «hecho» le diría a quien administra que acaba de retirarle
	// el acceso a alguien que sigue dentro. La baja de esa persona es otra puerta y otra pantalla.
	ErrInvitationRedeemed = fmt.Errorf("apiclient: esa invitación ya fue canjeada: %w", ErrConflict)

	// ErrInvitationUnknown es el 404 del CANJE: no hay ninguna invitación con ese código.
	//
	// Envuelve a ErrNotFound como ErrPersonUnknown, y por lo mismo: quien solo quiera saber «hubo un
	// 404» sigue funcionando con errors.Is. Y como aquel, su significado es «no existe» de verdad y
	// no una frontera de empresa — quien canjea todavía no tiene ninguna empresa que proteger.
	ErrInvitationUnknown = fmt.Errorf("apiclient: esa invitación no existe: %w", ErrNotFound)

	// ErrInvitationExpired es el 410 del CANJE: existía y venció.
	//
	// No envuelve a ningún sentinela genérico porque no hay ninguno para el 410: statusError lo
	// dejaría caer a un *APIError y de ahí al aviso de «inténtalo de nuevo en un momento», que es el
	// peor consejo posible — reintentar con un código caducado falla igual las veces que haga falta.
	// Lo que hay que hacer es pedir otra invitación, y eso solo se puede decir si el desenlace llega
	// distinguido hasta la pantalla.
	//
	// ⚠️ El servidor devuelve el MISMO CUERPO para el 410 y el 404 a propósito (requisito
	// anti-oráculo: quien tuviera una lista de códigos sospechosos podría averiguar cuáles
	// existieron). Lo que sigue distinguiéndolos es el CÓDIGO DE ESTADO, y por eso esta consola los
	// separa por status y no por texto.
	ErrInvitationExpired = errors.New("apiclient: esa invitación caducó")

	// ErrInvitationUnusable es el 409 del CANJE, y funde DOS causas que el servidor tampoco separa:
	// «esa invitación ya se usó o se anuló» y «tú ya perteneces a otra empresa». Separarlas le diría a
	// quien pregunta algo sobre el estado interno de una empresa a la que todavía no pertenece.
	ErrInvitationUnusable = fmt.Errorf("apiclient: esa invitación ya no está disponible: %w", ErrConflict)
)

// InvitationsClient sirve el plano de invitaciones de la empresa del token.
//
// Va aparte de MembersClient aunque comparta los scopes `members.*`: una invitación es una membresía
// EN DIFERIDO —por eso no estrena vocabulario de permisos— pero es otro recurso, con otro ciclo de
// vida, otra tabla y, en el canje, otro perímetro de autorización.
type InvitationsClient struct {
	t *Transport
}

// NewInvitationsClient construye el cliente de invitaciones.
func NewInvitationsClient(t *Transport) *InvitationsClient { return &InvitationsClient{t: t} }

// revokeStatusError es el traductor de la REVOCACIÓN: idéntico al general salvo en el 409, que aquí
// significa «ya se canjeó» y no «ya existe algo con ese nombre».
//
// El 404 sí cae al genérico a propósito: en revocar, un identificador que no existe y uno que es de
// OTRA empresa comparten código porque la plataforma no los distingue, y ese es exactamente el
// significado de frontera que ErrNotFound ya lleva.
func revokeStatusError(op string, status int) error {
	if status == http.StatusConflict {
		return ErrInvitationRedeemed
	}
	return statusError(op, status)
}

// redeemStatusError es el traductor del CANJE, y es el que reparte los CUATRO desenlaces del criterio
// de la ola. Es el único traductor de esta consola que mira el 410, porque es el único endpoint que
// lo devuelve.
//
// Los tres códigos que no son 204 tienen sentinela propio porque los tres llevan a una acción
// distinta de quien canjea: volver a copiar el código (404), pedir otro (410) o hablar con quien se
// lo mandó (409). Un solo «no se pudo» los dejaría a los tres sin salida.
func redeemStatusError(op string, status int) error {
	switch status {
	case http.StatusNotFound:
		return ErrInvitationUnknown
	case http.StatusGone:
		return ErrInvitationExpired
	case http.StatusConflict:
		return ErrInvitationUnusable
	default:
		return statusError(op, status)
	}
}

// Issue emite una invitación de un solo uso para la empresa del token (POST /api/v1/invitations,
// scope `members.write`) y devuelve el código EN CLARO por única vez.
//
// `roleID` vacío = invitación sin rol. `ttlSeconds` <= 0 = la caducidad por defecto del servidor
// (24 h): el campo no viaja, así que el default vive en UN solo sitio y esta consola no lo repite.
// El recorte a [60 s, 30 días] también es del servidor; aquí solo se ofrecen valores que caen dentro,
// y por eso no hay una segunda validación que un día diga otra cosa.
//
// 🔴 Quien llame a esto tiene el token en la mano y es responsable de que no acabe en un log. En esta
// consola no se registra nunca (ver internal/web/invitations_handler.go).
func (c *InvitationsClient) Issue(ctx context.Context, accessToken, roleID string, ttlSeconds int) (*IssuedInvitation, error) {
	const op = "invitations.issue"
	payload := issueInvitationRequest{RoleID: roleID}
	if ttlSeconds > 0 {
		payload.TTLSeconds = ttlSeconds
	}
	req, err := c.t.newAuthedRequest(ctx, http.MethodPost, "/api/v1/invitations", payload, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.do(req, op, statusError)
	if err != nil {
		return nil, err
	}
	var out IssuedInvitation
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// List lee las invitaciones de la empresa DEL TOKEN, las más recientes primero
// (GET /api/v1/invitations, scope `members.read`).
//
// El tenant no se pasa: no hay parámetro donde ponerlo (INV-04). La API devuelve siempre un arreglo
// —vacío si la empresa no ha emitido ninguna—, nunca null.
func (c *InvitationsClient) List(ctx context.Context, accessToken string) ([]Invitation, error) {
	const op = "invitations.list"
	req, err := c.t.newAuthedRequest(ctx, http.MethodGet, "/api/v1/invitations", nil, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.do(req, op, statusError)
	if err != nil {
		return nil, err
	}
	var out []Invitation
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Invitation{}
	}
	return out, nil
}

// Revoke anula una invitación VIVA de la empresa del token (DELETE /api/v1/invitations/{id}, scope
// `members.write`), de modo que un canje posterior de ese código falle y no escriba ninguna
// membresía.
//
// Es idempotente sobre lo ya revocado: 204 aunque ya lo estuviera. NO lo es sobre lo ya canjeado:
// ahí devuelve 409 (ver ErrInvitationRedeemed).
func (c *InvitationsClient) Revoke(ctx context.Context, accessToken, id string) error {
	const op = "invitations.revoke"
	req, err := c.t.newAuthedRequest(ctx, http.MethodDelete, "/api/v1/invitations/"+pathSegment(id), nil, accessToken)
	if err != nil {
		return err
	}
	resp, err := c.t.do(req, op, revokeStatusError)
	if err != nil {
		return err
	}
	discard(resp)
	return nil
}

// Redeem canjea una invitación con el token que la persona pegó (POST /api/v1/invitations/accept).
//
// 🔴 ES LA ÚNICA LLAMADA DE ESTE PAQUETE QUE NO EXIGE NINGÚN PERMISO, y no es un descuido del
// servidor: quien canjea acaba de registrarse, tiene cero membresías y su Context Token sale SIN
// empresa y SIN un solo grant (D-056.12), así que cualquier `RequirePermission` le contestaría 403 a
// todos, siempre. Aun así viaja autenticada: la plataforma necesita saber A QUIÉN incorpora, y ese
// dato sale del token, no del cuerpo.
//
// ⚠️ Tras un 204 la persona YA es miembro, pero el token que tiene en la mano SIGUE SIN EMPRESA: se
// emitió antes de existir la membresía. Hay que refrescar la sesión para que el canje del Identity
// Token vuelva a resolver el tenant, y de eso se ocupa el handler.
func (c *InvitationsClient) Redeem(ctx context.Context, accessToken, token string) error {
	const op = "invitations.redeem"
	req, err := c.t.newAuthedRequest(ctx, http.MethodPost, "/api/v1/invitations/accept",
		redeemInvitationRequest{Token: token}, accessToken)
	if err != nil {
		return err
	}
	resp, err := c.t.do(req, op, redeemStatusError)
	if err != nil {
		return err
	}
	discard(resp)
	return nil
}
