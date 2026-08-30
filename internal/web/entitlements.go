package web

import (
	"errors"
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
)

// entitlementsDataKey es la clave con la que la vista del plan entra en los datos de plantilla.
// Cualquier página que quiera gatear un bloque la necesita puesta: sin ella la plantilla ni siquiera
// puede preguntar, y `{{ if $.Entitlements.Has "x" }}` sobre un dato ausente no emite el bloque —el
// olvido falla CERRADO, que es el lado correcto por el que fallar.
const entitlementsDataKey = "Entitlements"

// featureCatalogImport es la feature que gatea la sección de importación de catálogo en la portada.
//
// 🔴 Por qué ESTA y no otra (D-047.10): el plano de roles y miembros es CAPACIDAD BASE y NO va
// detrás de ninguna feature —una empresa sin plan sigue teniendo que poder administrar a su gente—,
// así que el gate tenía que colgar de otra sección. `catalog_import` es la única candidata que
// cumple las tres condiciones a la vez: (1) es administración del tenant y por tanto del perímetro de
// ESTA consola, no del pipeline conversacional; (2) sus DOS ramas son alcanzables en producción
// —`basic` NO la tiene y `commerce` en adelante SÍ (seed 0039)—, así que ni el bloque ON ni el OFF
// son estados de laboratorio; y (3) no es un add-on suelto como `multi_empresa` o `passive_profiles`,
// que no entran en ningún paquete comercial y dejarían la rama ON sin ocurrir nunca fuera de `pro`.
const featureCatalogImport = "catalog_import"

// featureCartBasic es la capacidad que abre la BANDEJA DE SOLICITUDES (Plan 047 · T7.2).
//
// Es la MISMA clave con la que la plataforma gatea las diez rutas de la bandeja
// (`RequireFeature("cart_basic")`), y esa sigue siendo la autoridad: aquí decide si esta consola
// deja ENTRAR en la pantalla, allí decide si se PUEDE operar. Un gate de esta capa nunca sustituye
// al del servidor; le ahorra el viaje y da la explicación en la voz de esta consola.
//
// 🔴 A diferencia de featureCatalogImport, esta NO gatea un bloque de plantilla: gatea la RUTA
// entera, con middleware sobre el grupo. Ver solicitudes_gate.go.
const featureCartBasic = "cart_basic"

// entitlementsView es el plan del tenant tal como lo consume la plantilla.
//
// El gate de esta consola es SERVER-SIDE: el bloque de una sección sin feature NO SE EMITE en el
// HTML. No se esconde con CSS ni con JS —que además la CSP endurecida no permitiría sin nonce—, y
// sobre todo: lo que no está contratado no está ahí para que nadie lo destape con el inspector.
type entitlementsView struct {
	// Plan es el identificador del plan del tenant ("basic", "advisor_ai", …). Vacío si no se resolvió.
	Plan string
	// Features son las EFECTIVAS, en el orden estable que fija el servidor.
	Features []string
	// Resolved distingue «el tenant no tiene features» de «no se pudo preguntar». Sin él, la pantalla
	// pintaría el mismo vacío en los dos casos y el usuario no sabría cuál está viendo.
	Resolved bool
	// Notice es el aviso legible del modo degradado (vacío cuando se resolvió).
	Notice string

	enabled map[string]bool
}

// Has responde si la feature está habilitada para el tenant.
//
// Fail-closed POR CONSTRUCCIÓN, no por una condición que alguien pueda olvidar: la vista cero —la que
// queda cuando el endpoint falla, responde 403 o el usuario no tiene el scope— tiene el mapa nil, y
// una lectura sobre un mapa nil devuelve false. Preferimos una consola que enseña de menos a una que
// promete lo que el tenant no ha contratado.
func (v entitlementsView) Has(feature string) bool { return v.enabled[feature] }

// resolveEntitlements pide las features efectivas del tenant con el token de la sesión y devuelve
// SIEMPRE una vista usable: el fallo se traduce en la vista cero más un aviso, nunca en un error que
// tumbe la página. La portada debe seguir sirviendo aunque el catálogo de planes no conteste.
//
// SIN CACHÉ, una vez por petición (D-040.6): ver el comentario de apiclient.EntitlementsClient.Get.
//
// El 401 no se trata aparte: withAuthRetry ya refrescó y reintentó, y si aun así persiste, quien
// decide expulsar al usuario es la llamada de negocio de la página, no esta consulta accesoria.
func resolveEntitlements(c *gin.Context, auth *AuthHandler, api *apiclient.Client) entitlementsView {
	var ent *apiclient.Entitlements
	err := auth.withAuthRetry(c, func(accessToken string) error {
		var gerr error
		ent, gerr = api.Entitlements.Get(c.Request.Context(), accessToken)
		return gerr
	})
	if err != nil || ent == nil {
		slog.Warn("no se pudieron leer las features del tenant (modo degradado; el gate CIERRA)", "error", err)
		return entitlementsView{Notice: entitlementsNotice(err)}
	}

	enabled := make(map[string]bool, len(ent.Features))
	for _, f := range ent.Features {
		enabled[f] = true
	}
	return entitlementsView{
		Plan:     ent.Plan,
		Features: ent.Features,
		Resolved: true,
		enabled:  enabled,
	}
}

// entitlementsNotice traduce el fallo a un aviso legible SIN filtrar el detalle del upstream: el
// cuerpo de la API no acaba en pantalla, igual que en el resto de la consola.
func entitlementsNotice(err error) string {
	if errors.Is(err, apiclient.ErrForbidden) {
		return "Tu usuario no tiene permiso para consultar el plan de la empresa. " +
			"Las secciones que dependen de una capacidad quedan ocultas."
	}
	return "No se pudo consultar el plan de la empresa ahora mismo. " +
		"Las secciones que dependen de una capacidad quedan ocultas hasta que se pueda comprobar."
}

// entitlementsContextKey es la clave con la que el GATE POR RUTA deja la vista del plan en el
// contexto de gin para que el handler la reutilice (ver solicitudes_gate.go).
//
// 🔴 Es OTRA clave que entitlementsDataKey y no la misma por accidente: aquella nombra el dato en la
// PLANTILLA y esta en el CONTEXTO de la petición. Son dos mapas distintos y confundirlos no daría un
// error, daría un dato que aparece donde no se espera.
const entitlementsContextKey = "wapp.entitlements"

// entitlementsFromContext devuelve la vista del plan que sembró el gate.
//
// 🔑 Existe para que el coste se quede en UNA llamada por petición: el gate ya preguntó, y un
// handler que volviera a llamar a resolveEntitlements pagaría el viaje dos veces (esta consola
// resuelve el plan sin caché, una vez por petición).
//
// FAIL-CLOSED POR CONSTRUCCIÓN: si no hay nada sembrado —o hay algo de otro tipo— se devuelve la
// vista CERO, cuyo mapa `enabled` es nil y por tanto `Has` da false para todo. No hay ninguna rama
// que lo abra.
func entitlementsFromContext(c *gin.Context) entitlementsView {
	valor, ok := c.Get(entitlementsContextKey)
	if !ok {
		return entitlementsView{}
	}
	vista, ok := valor.(entitlementsView)
	if !ok {
		return entitlementsView{}
	}
	return vista
}
