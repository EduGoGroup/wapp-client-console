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

// ShowMembers pinta la pantalla de miembros del tenant (T1.4a): quién está en la empresa y el botón
// para darlo de baja.
//
// La pantalla sirve 200 aunque el listado falle: el aviso se pinta arriba y el marco sigue
// navegable. La excepción es el 401 que sobrevivió al refresco —ahí la sesión ya no vale y lo que
// toca es expulsar a /login, no enseñar una pantalla vacía con un mensaje que el usuario no puede
// resolver desde ella.
func (h *AdminHandler) ShowMembers(c *gin.Context) {
	// Sin empresa no hay a quién listar, y la API respondería 403 —«no tienes permiso»—, que es un
	// diagnóstico falso: no le falta un permiso, le falta una empresa. Se explica y no se llama.
	if sinEmpresa(c) {
		renderer.HTML(c, http.StatusOK, "miembros.html", h.pageData(c, "Miembros"))
		return
	}

	var miembros []apiclient.Member
	var callErr error
	code := flashCodeFor(h.auth.withAuthRetry(c, func(accessToken string) error {
		var err error
		miembros, err = h.api.Members.List(c.Request.Context(), accessToken)
		callErr = err
		return err
	}))
	if sessionIsDead(callErr) {
		h.auth.expireSession(c)
		return
	}

	data := h.pageData(c, "Miembros")
	if code != "" {
		slog.Warn("no se pudo listar los miembros de la empresa", "codigo", code, "error", callErr)
		data["Error"] = flashError(code)
	}
	data["Members"] = membersView(miembros, webgin.UserIDFromContext(c))
	renderer.HTML(c, http.StatusOK, "miembros.html", data)
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
