package web

import (
	"net/http"

	webgin "github.com/EduGoGroup/wapp-shared/web/gin"
	"github.com/gin-gonic/gin"
)

// showHome pinta la única pantalla autenticada de esta tanda: qué empresa y qué usuario lleva la
// sesión que el login acaba de abrir.
//
// Es deliberadamente una función y no un tipo con dependencias: no llama a la API pública ni a
// ningún otro upstream. Las pantallas de negocio —miembros, roles, bandeja— llegan en la tanda
// siguiente y traerán su propio cliente HTTP; adelantar aquí un `struct` vacío sería fingir que ya
// existe algo que consultar.
//
// Los dos datos salen del gin.Context, donde los sembró el AuthMiddleware leyendo los claims del
// Context Token. No se vuelven a decodificar aquí: el token se lee UNA vez, en el middleware.
func showHome(c *gin.Context) {
	renderer.HTML(c, http.StatusOK, "home.html", gin.H{
		"Title":    "Inicio",
		"Subtitle": "Consola del cliente",
		"UserID":   webgin.UserIDFromContext(c),
		"TenantID": webgin.TenantIDFromContext(c),
	})
}
