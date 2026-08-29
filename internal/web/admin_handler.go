package web

import (
	"errors"
	"net/http"
	"strings"

	webgin "github.com/EduGoGroup/wapp-shared/web/gin"
	"github.com/gin-gonic/gin"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
	"github.com/EduGoGroup/wapp-client-console/internal/config"
)

// AdminHandler sirve las pantallas de administración del tenant: la portada, los miembros y los
// roles. Comparten estructura porque comparten las DOS dependencias —el ciclo de sesión (de donde
// sale el Context Token y el refresco) y el cliente de la API pública—, y partirlas en tres structs
// idénticos solo repetiría el cableado.
//
// El ALTA de una persona (POST /api/v1/members) vive aquí desde T1.2, y la vía es que la persona
// aporte su propio identificador. Lo que sigue sin existir es la búsqueda por correo, y las dos
// formas de tenerla están descartadas a conciencia; el porqué —y la vía buena, la invitación de la
// Ola A / D-047.11— está en MembersClient (internal/apiclient/members.go).
type AdminHandler struct {
	auth *AuthHandler
	api  *apiclient.Client
	cfg  *config.Config
}

// NewAdminHandler construye el handler de las pantallas de administración.
//
// Recibe la config por UNA cosa: la cookie efímera que lleva el código de una invitación del POST al
// GET que lo enseña hereda la MISMA política de despliegue (Secure, SameSite) que la cookie de
// sesión. Dos juegos distintos en la misma consola serían dos verdades que mantener, y la que se
// olvidara sería la de la pantalla que menos se mira. Mismo criterio que ProvisioningHandler en la
// consola de plataforma.
func NewAdminHandler(auth *AuthHandler, api *apiclient.Client, cfg *config.Config) *AdminHandler {
	return &AdminHandler{auth: auth, api: api, cfg: cfg}
}

// pageData arma los datos comunes de toda pantalla de administración: título, avisos del catálogo de
// flash y la EMPRESA de la sesión.
//
// La empresa va como DATO y no como control (ver la plantilla): hoy una sesión tiene exactamente una
// —el canje falla con ErrMultipleTenants si la persona pertenece a más de una, MD-055.2—, así que un
// selector no tendría entre qué elegir. Cuando el canje sepa elegir, el sitio donde aparecerá el
// control es este.
func (h *AdminHandler) pageData(c *gin.Context, title string) gin.H {
	return gin.H{
		"Title":      title,
		"Subtitle":   "Consola del cliente",
		"TenantID":   webgin.TenantIDFromContext(c),
		"UserID":     webgin.UserIDFromContext(c),
		"SinEmpresa": sinEmpresa(c),
		"Error":      flashError(c.Query("error")),
		"Success":    flashSuccess(c.Query("success")),
	}
}

// sinEmpresa dice si la sesión NO tiene empresa todavía.
//
// 🔴 Es un ESTADO NORMAL, no un fallo, y esta consola tenía la suposición contraria metida en tres
// sitios. Quien se registra y entra antes de que nadie lo incorpore a una empresa recibe un Context
// Token VÁLIDO y sin tenant —el canje devuelve tenant vacío con cero membresías, no un 401
// (D-056.12)—, así que llega hasta aquí con sesión y sin nada que administrar.
//
// Lo que hace la plataforma con ese token, y por qué hay que ramificar ANTES de llamarla:
//   - `GET /api/v1/entitlements` responde 401 (no 403): su guarda es `!ok || id.TenantID == ""`. Si
//     la portada lo llamara igual, cada carga gastaría un refresco contra identity para volver a
//     recibir 401, y el aviso diría «no se pudo consultar el plan», que no es lo que pasa.
//   - `GET /api/v1/members` y `GET /api/v1/roles` responden 403, porque un token sin empresa sale sin
//     un solo grant. El texto de 403 es «no tienes permiso», que MIENTE sobre la causa: no le falta
//     un permiso, le falta una empresa.
//
// Por eso las tres pantallas preguntan esto primero y no salen a la red: la respuesta ya se sabe, y
// la única que sirve de algo es la que explica qué hacer (ver ShowMyIdentifier).
func sinEmpresa(c *gin.Context) bool { return webgin.TenantIDFromContext(c) == "" }

// call ejecuta una llamada a la API pública con el token de la sesión y traduce el desenlace.
//
// Devuelve el CÓDIGO de flash y no el mensaje: los handlers de POST redirigen con `?error=<código>`
// (patrón POST-redirect-GET, para que un F5 no repita la operación) y los de GET lo traducen en el
// sitio. Un solo punto de traducción evita que dos pantallas den textos distintos al mismo 404.
//
// Cadena vacía = todo fue bien.
func (h *AdminHandler) call(c *gin.Context, fn func(accessToken string) error) string {
	return flashCodeFor(h.auth.withAuthRetry(c, fn))
}

// sessionIsDead dice si el error es un 401 que sobrevivió al refresco: la sesión ya no vale y lo que
// toca es expulsar, no pintar un aviso en una pantalla que el usuario no puede usar.
func sessionIsDead(err error) bool { return errors.Is(err, apiclient.ErrUnauthorized) }

// redirectWith manda a `path` con el código de flash correspondiente (POST-redirect-GET).
func redirectWith(c *gin.Context, path, errCode, okCode string) {
	if errCode != "" {
		c.Redirect(http.StatusSeeOther, path+"?error="+errCode)
		return
	}
	c.Redirect(http.StatusSeeOther, path+"?success="+okCode)
}

// shortID abrevia un identificador para que quepa en una tabla sin dejar de ser reconocible: los 8
// primeros caracteres y los 4 últimos.
//
// 🔴 Esto es lo que hay, y es el contrato: `GET /api/v1/members` devuelve SOLO UUIDs —no hay `name`
// ni `email`— porque wApp NO guarda PII de personas (`tenant_members` es `user_id, tenant_id,
// created_at`, con «CERO PII» escrito en su migración) y la identidad vive en el proveedor de
// identidad. La pantalla no lo esquiva inventando una fuente de nombres: enseña el identificador
// legible, deja el completo en el `title` para copiarlo, y dice de dónde sale el nombre.
//
// Corta por RUNAS y no por bytes: un identificador no-ASCII partido por la mitad de un carácter se
// pintaría como un rombo. Y por debajo del umbral se devuelve entero, porque abreviar algo que ya es
// corto solo lo hace ilegible.
func shortID(v string) string {
	r := []rune(v)
	const cabeza, cola = 8, 4
	if len(r) <= cabeza+cola+1 {
		return v
	}
	return string(r[:cabeza]) + "…" + string(r[len(r)-cola:])
}

// formValue lee un campo de formulario ya recortado.
func formValue(c *gin.Context, name string) string {
	return strings.TrimSpace(c.PostForm(name))
}
