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
// alguien se registra, entra, y todavía no pertenece a ningún sitio. Por eso este handler NO llama a
// la API pública ni una vez —lo que pinta sale del token que la sesión ya decodificó— y por eso no
// puede degradar: es la pantalla a la que se manda a alguien cuando lo demás no funciona, y añadirle
// un viaje de red sería darle un modo de fallo a lo único que nunca debe fallar.
//
// De dónde sale el dato: `webgin.UserIDFromContext`, que el AuthMiddleware sembró del claim `user_id`
// del Context Token. Es EXACTAMENTE el mismo valor que devuelve `GET /api/v1/auth/whoami` en su campo
// `subject` —ese handler lo construye con `Subject: claims.UserID`
// (internal/platform/httpapi/authmw.go:112)—, así que el viaje de red no compraría un dato distinto,
// solo una forma de no tenerlo.
func (h *AdminHandler) ShowMyIdentifier(c *gin.Context) {
	renderer.HTML(c, http.StatusOK, "mi-identificador.html", h.pageData(c, "Mi identificador"))
}
