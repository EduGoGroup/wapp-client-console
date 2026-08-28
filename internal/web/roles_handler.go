package web

import (
	"log/slog"
	"net/http"

	webgin "github.com/EduGoGroup/wapp-shared/web/gin"
	"github.com/gin-gonic/gin"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
)

// roleView es una fila del catálogo de roles.
//
// `Origen` y `Parent` son texto ya resuelto: la plantilla no calcula. `Global` se conserva aparte
// porque decide algo más que el texto —un rol global es una PLANTILLA del ecosistema y no se puede
// editar desde una empresa (la API responde 422)—, así que la pantalla no debe ofrecerlo como padre
// de… bueno, sí puede serlo; lo que no puede es tocarse. Ver ShowRoles.
type roleView struct {
	RoleID string
	Short  string
	Name   string
	Origen string
	Parent string
	Global bool
}

// ShowRoles pinta el plano de roles del tenant (T1.3): el catálogo, el alta de un rol propio y la
// asignación/retirada de un rol a un miembro.
//
// Hace DOS llamadas —roles y miembros— porque el formulario de asignación necesita las dos listas, y
// cada una degrada por su lado: que no se pueda listar a la gente no debe borrar el catálogo de
// roles de la pantalla.
func (h *AdminHandler) ShowRoles(c *gin.Context) {
	// Mismo motivo que en ShowMembers: sin empresa la API contesta 403 y ese texto miente sobre la
	// causa.
	if sinEmpresa(c) {
		renderer.HTML(c, http.StatusOK, "roles.html", h.pageData(c, "Roles"))
		return
	}

	var roles []apiclient.Role
	var miembros []apiclient.Member
	var rolesErr, membersErr error

	rolesCode := flashCodeFor(h.auth.withAuthRetry(c, func(accessToken string) error {
		var err error
		roles, err = h.api.Roles.List(c.Request.Context(), accessToken)
		rolesErr = err
		return err
	}))
	membersCode := flashCodeFor(h.auth.withAuthRetry(c, func(accessToken string) error {
		var err error
		miembros, err = h.api.Members.List(c.Request.Context(), accessToken)
		membersErr = err
		return err
	}))
	if sessionIsDead(rolesErr) || sessionIsDead(membersErr) {
		h.auth.expireSession(c)
		return
	}

	data := h.pageData(c, "Roles")
	if rolesCode != "" {
		slog.Warn("no se pudo listar el catálogo de roles", "codigo", rolesCode, "error", rolesErr)
		data["Error"] = flashError(rolesCode)
	}
	if membersCode != "" {
		slog.Warn("no se pudo listar los miembros para el formulario de asignación",
			"codigo", membersCode, "error", membersErr)
		if rolesCode == "" {
			data["Error"] = flashError(membersCode)
		}
	}

	data["Roles"] = rolesView(roles)
	data["Members"] = membersView(miembros, webgin.UserIDFromContext(c))
	renderer.HTML(c, http.StatusOK, "roles.html", data)
}

// rolesView proyecta el catálogo a filas, resolviendo el NOMBRE del rol padre.
//
// El padre llega como UUID y no como nombre, así que se resuelve contra el propio catálogo (que trae
// tanto los roles de la empresa como las plantillas globales). Si el padre no está en la lista, se
// deja el identificador abreviado en vez de una celda vacía: un rol cuyo padre no es visible es
// información, no un hueco.
func rolesView(roles []apiclient.Role) []roleView {
	nombres := make(map[string]string, len(roles))
	for _, r := range roles {
		nombres[r.RoleID] = r.Name
	}

	out := make([]roleView, 0, len(roles))
	for _, r := range roles {
		v := roleView{
			RoleID: r.RoleID,
			Short:  shortID(r.RoleID),
			Name:   r.Name,
			Global: r.Global,
			Origen: "De tu empresa",
		}
		if r.Global {
			v.Origen = "Plantilla del ecosistema"
		}
		if r.ParentRoleID != "" {
			if nombre, ok := nombres[r.ParentRoleID]; ok {
				v.Parent = nombre
			} else {
				v.Parent = shortID(r.ParentRoleID)
			}
		}
		out = append(out, v)
	}
	return out
}

// CreateRole crea un rol CUSTOM de la empresa del token (T1.3). `parent_role_id` vacío = rol raíz.
func (h *AdminHandler) CreateRole(c *gin.Context) {
	nombre := formValue(c, "name")
	if nombre == "" {
		redirectWith(c, "/roles", flashMissingField, "")
		return
	}
	padre := formValue(c, "parent_role_id")

	code := h.call(c, func(accessToken string) error {
		_, err := h.api.Roles.Create(c.Request.Context(), accessToken, nombre, padre)
		return err
	})
	if code != "" {
		slog.Warn("no se pudo crear el rol", "codigo", code)
	}
	redirectWith(c, "/roles", code, flashRoleCreated)
}

// AssignRole asigna un rol a un miembro (T1.3).
//
// 🔴 Consume `roles.write`, NO `members.write`, aunque la ruta de la API empiece por
// `/api/v1/members/{user_id}/roles`: el prefijo es el SUJETO y el scope lo decide la OPERACIÓN. Lo
// que se mueve aquí es un permiso —quien puede asignar roles puede asignarse `tenant_admin`—, no la
// pertenencia a la empresa. Por eso llama a h.api.Roles y no a h.api.Members, y hay un test que fija
// la ruta y el verbo por el cable.
func (h *AdminHandler) AssignRole(c *gin.Context) {
	h.moverRol(c, true)
}

// UnassignRole retira la asignación de un rol a un miembro (T1.3). Mismo scope que la asignación
// —`roles.write`— por el mismo motivo.
func (h *AdminHandler) UnassignRole(c *gin.Context) {
	h.moverRol(c, false)
}

// moverRol comparte lo que asignar y retirar tienen idéntico: los mismos dos campos, la misma
// validación y el mismo destino. Lo único que cambia es la llamada y el mensaje de éxito, y
// duplicarlo entero solo daría dos sitios donde olvidar la validación.
func (h *AdminHandler) moverRol(c *gin.Context, asignar bool) {
	userID := formValue(c, "user_id")
	roleID := formValue(c, "role_id")
	if userID == "" || roleID == "" {
		redirectWith(c, "/roles", flashMissingField, "")
		return
	}

	code := h.call(c, func(accessToken string) error {
		if asignar {
			return h.api.Roles.Assign(c.Request.Context(), accessToken, userID, roleID)
		}
		return h.api.Roles.Unassign(c.Request.Context(), accessToken, userID, roleID)
	})
	if code != "" {
		slog.Warn("no se pudo mover el rol", "codigo", code, "asignar", asignar)
	}

	exito := flashRoleAssigned
	if !asignar {
		exito = flashRoleRemoved
	}
	redirectWith(c, "/roles", code, exito)
}
