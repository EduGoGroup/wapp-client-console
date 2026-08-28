package web

import (
	"log/slog"
	"net/http"

	webgin "github.com/EduGoGroup/wapp-shared/web/gin"
	"github.com/gin-gonic/gin"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
)

// memberView es una fila de la tabla de miembros.
//
// `Short` y `UserID` conviven a propósito: la tabla pinta el abreviado y deja el completo en el
// atributo `title`, para que se pueda leer y copiar sin romper la fila. `IsSelf` es lo ÚNICO que la
// consola sabe de la identidad de alguien —porque ese alguien es quien tiene la sesión abierta— y por
// eso es lo único que se marca.
type memberView struct {
	UserID   string
	Short    string
	JoinedAt string
	IsSelf   bool
}

// ShowMembers pinta la pantalla de miembros del tenant (T1.4a · T1.2): el formulario para incorporar
// a alguien, quién está ya en la empresa y el botón para darlo de baja.
//
// Hace DOS llamadas —miembros y roles— porque el alta ofrece asignar un rol en el mismo paso, y cada
// una degrada por su lado. La pantalla sirve 200 aunque alguna falle: el aviso se pinta arriba y el
// marco sigue navegable.
//
// 🔴 Que el catálogo de roles no se pueda leer NO puede tumbar esta pantalla: lo que se pierde es el
// desplegable opcional del formulario —que se omite—, no la tabla ni el alta. La lista de personas es
// para lo que se entra aquí, y un fallo en un accesorio no debe llevársela por delante.
//
// La excepción a todo lo anterior es el 401 que sobrevivió al refresco: ahí la sesión ya no vale y lo
// que toca es expulsar a /login, no enseñar una pantalla vacía con un mensaje que el usuario no puede
// resolver desde ella.
func (h *AdminHandler) ShowMembers(c *gin.Context) {
	// Sin empresa no hay a quién listar, y la API respondería 403 —«no tienes permiso»—, que es un
	// diagnóstico falso: no le falta un permiso, le falta una empresa. Se explica y no se llama.
	if sinEmpresa(c) {
		renderer.HTML(c, http.StatusOK, "miembros.html", h.pageData(c, "Miembros"))
		return
	}

	var miembros []apiclient.Member
	var roles []apiclient.Role
	var membersErr, rolesErr error

	membersCode := flashCodeFor(h.auth.withAuthRetry(c, func(accessToken string) error {
		var err error
		miembros, err = h.api.Members.List(c.Request.Context(), accessToken)
		membersErr = err
		return err
	}))
	rolesCode := flashCodeFor(h.auth.withAuthRetry(c, func(accessToken string) error {
		var err error
		roles, err = h.api.Roles.List(c.Request.Context(), accessToken)
		rolesErr = err
		return err
	}))
	if sessionIsDead(membersErr) || sessionIsDead(rolesErr) {
		h.auth.expireSession(c)
		return
	}

	data := h.pageData(c, "Miembros")
	if membersCode != "" {
		slog.Warn("no se pudo listar los miembros de la empresa", "codigo", membersCode, "error", membersErr)
		data["Error"] = flashError(membersCode)
	}
	if rolesCode != "" {
		slog.Warn("no se pudo listar el catálogo de roles para el alta", "codigo", rolesCode, "error", rolesErr)
		// El aviso del listado principal manda: es el que explica por qué la tabla está vacía.
		if membersCode == "" {
			data["Error"] = flashError(rolesCode)
		}
	}
	data["Members"] = membersView(miembros, webgin.UserIDFromContext(c))
	data["Roles"] = rolesView(roles)
	renderer.HTML(c, http.StatusOK, "miembros.html", data)
}

// AddMember incorpora a la empresa del token a alguien que YA TIENE CUENTA, por el identificador que
// esa persona aporta desde «Mi identificador» (T1.6), y opcionalmente le asigna un rol en el mismo
// paso (T1.2).
//
// 🔴 Son DOS operaciones de la plataforma y NO son atómicas: no hay endpoint que las una ni
// transacción que las envuelva. Y no es un descuido del cloud —GrantTenantAccess SÍ sabe escribir
// membresía y rol en una sola tx, y la vía del operador la usa así—, sino una decisión suya: por la
// vía de la dueña pasa roleID nil a propósito, porque «dar de alta» y «dar un rol» son dos
// decisiones distintas del administrador. Si el alta va bien y la asignación falla, la persona queda
// incorporada y sin rol, y eso es lo que se le dice (flashAddedWithoutRole) — con el sitio donde
// arreglarlo.
//
// Por qué NO se compensa dando de baja, para que nadie lo "arregle":
//   - la baja NO retira roles ni grants (ver members.go), así que un rollback no devolvería el
//     sistema al estado inicial: dejaría uno TERCERO, sin membresía y con las asignaciones que
//     hubiera de antes;
//   - y borraría una incorporación que la dueña sí quería. El rol es el campo OPCIONAL del
//     formulario; deshacer lo obligatorio porque falló lo opcional es perder trabajo bueno.
func (h *AdminHandler) AddMember(c *gin.Context) {
	userID := formValue(c, "user_id")
	if userID == "" {
		redirectWith(c, "/miembros", flashMissingField, "")
		return
	}
	roleID := formValue(c, "role_id")

	if code := h.call(c, func(accessToken string) error {
		return h.api.Members.Add(c.Request.Context(), accessToken, userID)
	}); code != "" {
		// Sin el identificador: en el log de esta consola no entra la identidad de un tercero.
		slog.Warn("no se pudo incorporar a la persona", "codigo", code)
		redirectWith(c, "/miembros", code, "")
		return
	}

	if roleID == "" {
		redirectWith(c, "/miembros", "", flashMemberAdded)
		return
	}

	if code := h.call(c, func(accessToken string) error {
		return h.api.Roles.Assign(c.Request.Context(), accessToken, userID, roleID)
	}); code != "" {
		// El código concreto del fallo se queda en el log: en pantalla importa el ESTADO en que
		// quedaron las cosas —dentro, sin rol— y no cuál de los desenlaces del asignador ocurrió.
		slog.Warn("la persona quedó incorporada pero sin el rol pedido", "codigo", code)
		redirectWith(c, "/miembros", flashAddedWithoutRole, "")
		return
	}
	redirectWith(c, "/miembros", "", flashMemberAdded)
}

// membersView proyecta la respuesta de la API a filas de la tabla, marcando cuál es el propio
// usuario de la sesión.
func membersView(miembros []apiclient.Member, self string) []memberView {
	out := make([]memberView, 0, len(miembros))
	for _, m := range miembros {
		out = append(out, memberView{
			UserID:   m.UserID,
			Short:    shortID(m.UserID),
			JoinedAt: m.CreatedAt,
			IsSelf:   m.UserID != "" && m.UserID == self,
		})
	}
	return out
}

// RemoveMember da de baja a una persona de la empresa del token (T1.4a).
//
// 🔴 La baja PROPIA se corta AQUÍ, antes de salir a la red, y no es paternalismo: la API la aceptaría
// —es una baja legítima— y el usuario perdería el acceso a esta consola en el mismo clic, sin nadie
// dentro que pueda readmitirlo si era el único administrador. La plantilla tampoco pinta el botón en
// la fila propia, pero la guarda vive en el handler: un botón que no se pinta no impide un POST.
func (h *AdminHandler) RemoveMember(c *gin.Context) {
	userID := c.Param("user_id")
	if userID == "" {
		redirectWith(c, "/miembros", flashMissingField, "")
		return
	}
	if userID == webgin.UserIDFromContext(c) {
		slog.Warn("se rechazó una baja propia desde la consola de cliente")
		redirectWith(c, "/miembros", flashSelfRemoval, "")
		return
	}

	code := h.call(c, func(accessToken string) error {
		return h.api.Members.Remove(c.Request.Context(), accessToken, userID)
	})
	if code != "" {
		// Sin el identificador: en el log de esta consola no entra la identidad de un tercero.
		slog.Warn("no se pudo dar de baja a un miembro", "codigo", code)
	}
	redirectWith(c, "/miembros", code, flashMemberRemoved)
}
