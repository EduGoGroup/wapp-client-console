package apiclient

import (
	"context"
	"net/http"
)

// Role es la proyección pública de un rol (GET /api/v1/roles).
//
// `Global` es DERIVADA en el servidor (no hay columna): un rol sin `tenant_id` es una PLANTILLA del
// ecosistema, visible desde cualquier empresa pero no editable desde ninguna. Viaja explícita —y no
// se deja inferir de la ausencia de `tenant_id`— porque es justo la diferencia que la pantalla
// necesita para no ofrecer «editar» sobre algo que responderá 422.
type Role struct {
	RoleID       string `json:"role_id"`
	Name         string `json:"name"`
	TenantID     string `json:"tenant_id,omitempty"`
	ParentRoleID string `json:"parent_role_id,omitempty"`
	Global       bool   `json:"global"`
	CreatedAt    string `json:"created_at,omitempty"`
}

// createRoleRequest es el cuerpo de POST /api/v1/roles. NO lleva `tenant_id` (INV-04) y
// `parent_role_id` vacío significa rol raíz.
type createRoleRequest struct {
	Name         string `json:"name"`
	ParentRoleID string `json:"parent_role_id"`
}

// assignRoleRequest es el cuerpo de POST /api/v1/members/{user_id}/roles: la persona va en la RUTA y
// el rol en el cuerpo.
type assignRoleRequest struct {
	RoleID string `json:"role_id"`
}

// RolesClient sirve el plano de ROLES de la empresa del token.
//
// 🔴 El scope lo decide la OPERACIÓN, no el prefijo de la ruta. Asignar o retirar un rol a un
// miembro cuelga de `/api/v1/members/{user_id}/roles` pero consume `roles.write`, NO
// `members.write`: lo que se mueve es un PERMISO —quien puede asignar roles puede asignarse
// tenant_admin—, no la pertenencia. Por eso esas dos operaciones viven en ESTE cliente y no en
// MembersClient, aunque su ruta empiece por /members.
//
// 🔴 Lo que este cliente NO PUEDE hacer: leer qué roles tiene una persona. La API pública no expone
// `GET /api/v1/members/{user_id}/roles` (verificado contra el mux del plano de roles: solo hay POST
// y DELETE). La pantalla asigna y retira; el estado actual de cada miembro no es consultable, y eso
// se dice en la pantalla en vez de fingirse.
type RolesClient struct {
	t *Transport
}

// NewRolesClient construye el cliente de roles.
func NewRolesClient(t *Transport) *RolesClient { return &RolesClient{t: t} }

// List lee el catálogo VISIBLE para la empresa del token —sus roles propios más las plantillas
// globales— (GET /api/v1/roles, scope `roles.read`).
func (c *RolesClient) List(ctx context.Context, accessToken string) ([]Role, error) {
	const op = "roles.list"
	req, err := c.t.newAuthedRequest(ctx, http.MethodGet, "/api/v1/roles", nil, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.do(req, op, statusError)
	if err != nil {
		return nil, err
	}
	var out []Role
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Role{}
	}
	return out, nil
}

// Create crea un rol CUSTOM de la empresa del token (POST /api/v1/roles, scope `roles.write`).
// `parentRoleID` vacío = rol raíz. Devuelve 201 con el rol creado; 409 si ya hay uno con ese nombre.
func (c *RolesClient) Create(ctx context.Context, accessToken, name, parentRoleID string) (*Role, error) {
	const op = "roles.create"
	payload := createRoleRequest{Name: name, ParentRoleID: parentRoleID}
	req, err := c.t.newAuthedRequest(ctx, http.MethodPost, "/api/v1/roles", payload, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.do(req, op, statusError)
	if err != nil {
		return nil, err
	}
	var out Role
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Assign asigna un rol visible a un MIEMBRO de la empresa del token
// (POST /api/v1/members/{user_id}/roles, scope `roles.write`). Idempotente: 204.
//
// El 404 cubre DOS casos a la vez —la persona no es miembro de esta empresa, o el rol no es visible
// para ella— y los comparte a propósito: distinguirlos diría si ese UUID pertenece a otra empresa.
func (c *RolesClient) Assign(ctx context.Context, accessToken, userID, roleID string) error {
	const op = "roles.assign"
	path := "/api/v1/members/" + pathSegment(userID) + "/roles"
	req, err := c.t.newAuthedRequest(ctx, http.MethodPost, path, assignRoleRequest{RoleID: roleID}, accessToken)
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

// Unassign retira la asignación ACOTADA a la empresa del token
// (DELETE /api/v1/members/{user_id}/roles/{role_id}, scope `roles.write`).
//
// La asignación GLOBAL que esa persona pudiera tener no se toca desde aquí: el borrado es simétrico
// al alta, que también fue acotada.
func (c *RolesClient) Unassign(ctx context.Context, accessToken, userID, roleID string) error {
	const op = "roles.unassign"
	path := "/api/v1/members/" + pathSegment(userID) + "/roles/" + pathSegment(roleID)
	req, err := c.t.newAuthedRequest(ctx, http.MethodDelete, path, nil, accessToken)
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
