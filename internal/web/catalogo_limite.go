package web

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	sharedweb "github.com/EduGoGroup/wapp-shared/web"
)

// catalogo_limite.go es EL TECHO DEL CUERPO de la única ruta de esta consola que recibe un fichero
// (Plan 047 · T8.4), mudado del `BodyLimit` que el BFF tenía sobre `/catalog-import`.
//
// ════════════════════════════════════════════════════════════════════════════
// SON DOS LÍMITES Y MIDEN COSAS DISTINTAS. NO LOS FUNDAS EN UNO.
// ════════════════════════════════════════════════════════════════════════════
//
//	                 qué mide                              quién lo aplica          valor
//	 maxCuerpoCatalogo  el SOBRE de la petición entera      este middleware          4 MiB
//	 maxArchivoCatalogo el FICHERO ya extraído              archivoDelFormulario     1 MiB
//
// 🔴 Y LA TENTACIÓN ES BAJAR EL PRIMERO AL SEGUNDO, porque «el cloud solo honra 1 MiB». Sería un
// defecto: el PASO 2 no manda un fichero, manda el documento NORMALIZADO dentro de un campo de
// formulario `x-www-form-urlencoded`, donde cada `"`, `{`, `}`, `:` y espacio se convierte en `%XX`.
// El cuerpo que sale del navegador es varias veces mayor que el JSON que el cloud acaba midiendo, así
// que un techo de 1 MiB aquí rechazaría documentos que la plataforma SÍ acepta — y lo haría con el
// mensaje equivocado, hablando de un fichero que en ese paso ya no existe.
//
// Éste, por tanto, NO protege el contrato con la plataforma: protege EL PROCESO, para que una sesión
// legítima no pueda empujar 500 MB que acabarían en memoria y en disco antes de que nadie los mire.
// Quien mide contra el contrato es la comprobación de negocio, que además dice la cifra.
//
// 🔴 EL ORDEN NO ES NEGOCIABLE: esto se registra ANTES del CSRF. El CSRF lee el formulario para
// comparar el token, y con eso consume el cuerpo entero —a memoria hasta MaxMultipartMemory y a disco
// lo que sobre—, así que un tope montado después llegaría cuando el daño ya está hecho. Esa es
// también la razón de que la página del rechazo no lleve barra de navegación: aquí todavía no ha
// corrido el AuthMiddleware, que va DENTRO del grupo protegido, o sea después del CSRF.

// maxCuerpoCatalogo es el techo del SOBRE de la importación (4 MiB). Ver arriba por qué no es 1 MiB.
const maxCuerpoCatalogo = 4 << 20

// plantillaCuerpoGrande es la página del rechazo por tamaño.
const plantillaCuerpoGrande = "cuerpo-demasiado-grande.html"

// limiteDeCuerpo acota el cuerpo de las rutas indicadas (rutas EXACTAS, y solo en los métodos que
// mutan).
//
// 🔑 REUSA `sharedweb.NewBodyLimit` PARA DECIDIR y escribe la respuesta a mano, en vez de usar
// `webgin.BodyLimit` entero. La decisión —qué rutas, qué métodos— es la misma y no se duplica; lo que
// no se hereda es la RESPUESTA, porque la del módulo es un `AbortWithStatusJSON` y ésta es una casa
// que contesta HTML. Un JSON crudo en la cara de la dueña de una panadería no es «un mensaje»: es el
// mismo desenlace que T8.4 vino a evitar, con otro disfraz.
//
// Lo que este middleware NO cubre, y conviene que esté escrito: una petición SIN `Content-Length`
// —troceada— no se puede rechazar por adelantado. Para ésa queda el `MaxBytesReader`, y el corte se
// nota más abajo, al parsear el formulario; el desenlace entonces es el 403 del CSRF (que no
// encuentra el token en un cuerpo truncado), no una página que explique el tamaño. Es peor mensaje y
// sigue sin ser ni un corte en seco ni un 500. Ningún navegador manda una subida así.
func limiteDeCuerpo(limite int64, rutas ...string) gin.HandlerFunc {
	guarda := sharedweb.NewBodyLimit(limite, rutas...)

	return func(c *gin.Context) {
		if !guarda.Guards(c.Request.Method, c.Request.URL.Path) {
			c.Next()
			return
		}
		if c.Request.ContentLength > guarda.Limit() {
			slog.Warn("petición rechazada por tamaño del cuerpo",
				"ruta", c.Request.URL.Path, "bytes", c.Request.ContentLength, "limite", guarda.Limit())
			rechazaPorTamano(c, guarda.Limit())
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, guarda.Limit())
		c.Next()
	}
}

// rechazaPorTamano pinta la página del 413.
//
// Es una página y no un aviso dentro de la pantalla de importación por una razón mecánica: aquí el
// cuerpo NO se ha leído —ése es el punto entero del tope—, así que no hay ref, ni documento, ni token
// CSRF, ni sesión resuelta que devolver al formulario. Lo único honesto que se puede pintar es qué
// pasó, cuál es el techo y por dónde se vuelve.
//
// El límite se pasa como argumento y no se lee de la constante para que la página no pueda decir un
// número distinto del que acaba de cortar.
func rechazaPorTamano(c *gin.Context, limite int64) {
	renderer.HTML(c, http.StatusRequestEntityTooLarge, plantillaCuerpoGrande, gin.H{
		"Title":    tituloCatalogo,
		"Subtitle": "Consola del cliente",
		// En MiB porque es la unidad en la que está escrito el techo, y redondear a «4 MB» diría un
		// número que no es el que se comparó.
		"LimiteMiB": limite >> 20,
		"Volver":    rutaCatalogo,
	})
	// Abort y no `return` a secas: sin él Gin seguiría llamando a la cadena, que escribiría una
	// SEGUNDA respuesta sobre la misma petición.
	c.Abort()
}
