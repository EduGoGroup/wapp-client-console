package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ShowHome pinta la portada autenticada: qué empresa y qué usuario lleva la sesión, los accesos a la
// administración y el PLAN de la empresa con sus capacidades.
//
// El plan se resuelve aquí y no en el middleware a propósito: es una consulta por PETICIÓN, sin
// caché (D-040.6), y solo la necesitan las páginas que gatean algo. Ponerla en el middleware la
// cobraría también en el logout y en cada estático.
func (h *AdminHandler) ShowHome(c *gin.Context) {
	data := h.pageData(c, "Inicio")

	// Sin empresa NO se pregunta por el plan. La respuesta ya se sabe —el endpoint responde 401
	// cuando el token no trae tenant— y preguntarla igual tendría dos costes: un refresco contra
	// identity en CADA carga (withAuthRetry reintenta ante el 401) y un aviso rojo que diría «no se
	// pudo consultar el plan» a alguien cuyo problema es otro. Ver sinEmpresa().
	if !sinEmpresa(c) {
		data[entitlementsDataKey] = resolveEntitlements(c, h.auth, h.api)
	}
	renderer.HTML(c, http.StatusOK, "home.html", data)
}
