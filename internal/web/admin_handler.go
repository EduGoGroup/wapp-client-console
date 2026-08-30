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
// flash y la EMPRESA de la sesión —cuál es, cómo se llama y entre cuáles se puede elegir—.
//
// 🔴 HACE UNA LLAMADA DE RED, y hasta T5.3 no hacía ninguna. El coste elegido, dicho sin rebajarlo:
// UN `GET /api/v1/auth/tenants` más por cada render autenticado, es decir en las SEIS pantallas con
// sesión. No lo pagan ni /login, ni /logout, ni la sonda, ni los estáticos —que es justo lo que
// evita el middleware y por lo que los entitlements se resuelven en su handler (home_handler.go)—,
// ni ningún POST: pageData solo lo llaman los seis GET que pintan.
//
// Por qué se paga ahí y no en un handler suelto, con el código delante:
//   - No es un adorno de una pantalla: DECIDE QUÉ PANTALLA ES. Cinco de las seis pintan el parcial
//     `sin_empresa`, y sin el listado no pueden distinguir «cero empresas» (espera) de «varias sin
//     elegir» (selector) — los dos llegan con el MISMO token vacío. Esas cinco lo pagarían igual
//     aunque el selector viviera en una sola pantalla.
//   - El control tiene que estar donde está «Cerrar sesión»: en la barra, no dentro de una tabla.
//     Un control de navegación que aparece en cinco pantallas y desaparece en la sexta se lee como
//     un fallo.
//   - Repetirlo en seis handlers costaría lo mismo y añadiría seis sitios donde olvidarlo.
//
// La sexta —«Mi identificador»— es la que más se pensó, porque su handler declara por escrito que NO
// sale a la red y que añadirle un viaje sería darle un modo de fallo a lo único que nunca debe
// fallar. Ese argumento se sostenía sobre una premisa que aquí NO se cumple: decía que el viaje «no
// compraría un dato distinto, solo una forma de no tenerlo», y era cierto para `whoami` porque el
// identificador ya viene en el token. El listado de empresas NO viene en el token y no puede venir
// —el de cero empresas y el de varias sin elegir son idénticos—, así que sí compra un dato que no se
// tiene de otra forma: sin él, esa pantalla le dice «todavía no perteneces a ninguna empresa» a
// alguien que pertenece a dos. Se paga el viaje y se conserva la garantía por el otro lado:
// resolveTenants NUNCA falla la página, así que el identificador se pinta igual con la plataforma
// caída. Ver ShowMyIdentifier, cuyo comentario se corrigió con este cambio y no después.
func (h *AdminHandler) pageData(c *gin.Context, title string) gin.H {
	tenantID := webgin.TenantIDFromContext(c)
	empresas := resolveTenants(c, h.auth, h.api)
	return gin.H{
		"Title":    title,
		"Subtitle": "Consola del cliente",
		"TenantID": tenantID,
		// TenantName es el nombre legible de la empresa del token, y cae al propio identificador si
		// el listado no se resolvió. Las pantallas lo pintan como TEXTO y dejan el identificador
		// completo en el `title`, igual que la tabla de miembros hace con las personas: se gana
		// legibilidad sin perder el dato que hay que copiar.
		"TenantName": nombreDeLaEmpresa(empresas, tenantID),
		// Tenants son las empresas entre las que ESTE usuario puede elegir. Vacía cuando no se pudo
		// preguntar, y entonces no se pinta ningún selector: se falla hacia lo que ya había.
		"Tenants":    empresas,
		"UserID":     webgin.UserIDFromContext(c),
		"SinEmpresa": sinEmpresa(c),
		"Error":      flashError(c.Query("error")),
		"Success":    flashSuccess(c.Query("success")),
	}
}

// sinEmpresa dice si la sesión NO tiene empresa ACTIVA.
//
// 🆕 🔴 NO ES UN SOLO ESTADO, y creerlo era la suposición que T5.3 vino a romper: aquí caen tanto
// quien no pertenece a ninguna empresa como quien pertenece a VARIAS y no ha elegido con cuál entra.
// Los dos tokens son idénticos —sin tenant y sin grants—, así que esta función no puede separarlos y
// no lo intenta: quien decide qué se pinta es el LISTADO (`.Tenants` en pageData, resolveTenants),
// y el reparto vive en el parcial `sin_empresa`. Lo que esta función sigue respondiendo es lo único
// que el token sabe: si hay empresa acotada o no.
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
//
// 🔒 EL PRG DE ESTA CONSOLA TIENE UNA REGLA, Y ESTÁ ESCRITA AQUÍ A PROPÓSITO (D-047.16, decidida el
// 2026-08-29): era universal —303 siempre— y desde la Ola 6 no lo es. La frontera:
//
//	validación que falla ANTES de llamar a la API .... 400 REPINTANDO, con el formulario intacto
//	la API responde error (409, 502, 401…) ........... 303 + flash (esta función)
//	éxito ............................................ 303 + flash (esta función)
//
// 🔑 El argumento no es de gusto: el POST-Redirect-GET existe para que RECARGAR NO REENVÍE UNA
// MUTACIÓN. Un rechazo de validación local no mutó nada —la petición ni siquiera salió—, así que
// repintarlo no crea el problema que el PRG resuelve, y sí evita que el usuario pierda lo escrito.
// Por eso no es «romper el PRG»: es aplicarlo donde protege.
//
// 🔒 EXTENSIÓN DEL 2026-08-30 (decidida por Jhoan, aplicada en T7.4): el criterio de la línea de
// arriba NO es «la validación es local», es «NO HUBO MUTACIÓN» — y el mismo argumento vale cuando la
// validación vive al otro lado del cable. El primer caso que entra por ahí es el 400 `invalid_items`
// del cloud al guardar líneas (solicitudes_lineas.go): la edición es todo-o-nada, así que ese
// rechazo no escribió nada y repintarlo devuelve la tabla tecleada Y la lista de defectos por línea,
// que es con lo que se corrige. Lo que NO se extiende es el resto: 409, 422, 502 y los 400 sin
// cuerpo nombrado sí pudieron mutar, y siguen saliendo por aquí.
//
// Quien traiga un desenlace nuevo tiene que responder a UNA pregunta, y solo a ésa: ¿pudo escribir
// algo al otro lado? Si no, repinta; si sí —o si no se puede saber—, 303.
//
// Hoy la excepción vive en DOS sitios y en los dos hay algo que perder: la publicación de un flujo
// (el `definition` entero, que puede ser un JSON de decenas de líneas) y el alta de un disparador
// (sus ocho campos). Ver editor_handler.go. Lo que NO entra en la excepción es un formulario sin nada
// que perder —el borrado de un disparador es un botón suelto—: ahí el desenlace malo va por 303 igual
// que el bueno.
//
// ❌ Lo descartado, para que no se reabra: «303 siempre + borrador en cookie de un solo uso». Para los
// ocho campos cortos funcionaría; para el `definition` NO CABE —pasados ~4 KB el navegador descarta la
// cookie EN SILENCIO (defecto documentado en T3.5)— y el usuario perdería lo tecleado igual, pero
// ahora sin saber por qué.
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
