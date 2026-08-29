package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ShowMyIdentifier pinta «Mi identificador»: el identificador de quien tiene la sesión abierta, para
// que pueda copiarlo y dárselo a quien administra su empresa.
//
// Es la pantalla que hace utilizable el resto. wApp NO guarda correos de personas, así que quien
// administra no puede buscar a nadie por su correo: la única forma de incorporar a alguien es que esa
// persona APORTE su identificador. Sin esta pantalla, ese identificador no está a la vista de su
// dueño en ninguna parte.
//
// 🔴 La sirve CUALQUIER sesión, tenga empresa o no, y el caso principal es justo el que no la tiene:
// alguien se registra, entra, y todavía no pertenece a ningún sitio.
//
// De dónde sale el identificador: `webgin.UserIDFromContext`, que el AuthMiddleware sembró del claim
// `user_id` del Context Token. Es EXACTAMENTE el mismo valor que devuelve `GET /api/v1/auth/whoami`
// en su campo `subject` —ese handler lo construye con `Subject: claims.UserID`
// (internal/platform/httpapi/authmw.go:112)—, así que preguntarlo por red no compraría un dato
// distinto, solo una forma de no tenerlo. Eso sigue siendo verdad y por eso NO se pregunta.
//
// 🆕 🔴 LO QUE CAMBIÓ CON T5.3, Y SE DICE AQUÍ EN VEZ DE ROMPERLO EN SILENCIO. Este comentario decía
// que el handler no llama a la API pública NI UNA VEZ, y que añadirle un viaje de red sería darle un
// modo de fallo a lo único que nunca debe fallar. Desde T5.3 hace uno: pageData resuelve el listado
// de empresas del sujeto. El argumento de arriba no lo cubría, porque su premisa era «el viaje no
// compra un dato distinto» — y aquí sí lo compra, uno que el token NO PUEDE traer: el de quien tiene
// cero empresas y el de quien tiene varias sin elegir son idénticos. Sin ese viaje, esta pantalla le
// dice «todavía no perteneces a ninguna empresa» a alguien que pertenece a dos.
//
// La garantía se conserva por el otro lado y es lo que hace aceptable el cambio: `resolveTenants`
// nunca falla la página —un listado que no se puede leer es una lista vacía—, así que con la
// plataforma caída esta pantalla sigue pintando el identificador exactamente igual que ayer. Lo que
// se paga es UNA llamada, no un modo de fallo.
func (h *AdminHandler) ShowMyIdentifier(c *gin.Context) {
	renderer.HTML(c, http.StatusOK, "mi-identificador.html", h.pageData(c, "Mi identificador"))
}
