package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// solicitudes_gate.go es EL GATE POR RUTA de esta consola, y nace aquí (Plan 047 · T7.2).
//
// Hasta esta casilla el gate por plan era SOLO DE PLANTILLA: `home.html` no emite el bloque de la
// importación de catálogo sin `catalog_import`, y eso es todo. No existía —ni aquí ni en
// `wapp-shared/web`— ningún `RequireFeature` ni equivalente: la única implementación del ecosistema
// vive en el cloud (`internal/entitlements/middleware.go`) y NO es portable —es net/http, lee un
// token de API en vez de una sesión de cookie y responde JSON—.
//
// 🔴 POR QUÉ NO BASTA CON ESCONDER EL ENLACE, que es lo que esta consola ya sabía hacer: esconder un
// enlace decide lo que se PINTA, no lo que se PUEDE. Quien teclea la URL entra igual. La autoridad
// última sigue siendo la plataforma —`RequireFeature("cart_basic")` corta con 403 y
// `{"error":"feature_not_enabled"}` antes de su handler—, y este gate no la sustituye: le ahorra el
// viaje y, sobre todo, da una respuesta en la voz de esta consola en vez de un JSON.
//
// 🔑 UN SOLO PUNTO, NO CINCO. El BFF replicaba el mismo `if` en cinco handlers, y por eso su GET y
// sus POST acabaron respondiendo códigos distintos ante la misma ausencia de feature sin que nadie
// lo decidiera. Aquí el corte es un middleware SOBRE EL GRUPO: quien añada una ruta de solicitudes
// la registra dentro y hereda el gate sin acordarse de él.
//
// 🔒 QUÉ RESPONDE: 403 + la plantilla de la pantalla VACÍA, explicando la razón, en GET y en POST.
// Es el PRIMER StatusForbidden de este repo (antes de esta casilla, `grep StatusForbidden` sobre
// internal/web daba cero), así que se decide en vez de heredarse. Las alternativas y por qué se
// caen:
//   - 200 al estilo `sinEmpresa`: ese patrón es para un ESTADO NORMAL, no para una denegación. Y con
//     200, la mutación que esta casilla declara —quitar el gate de la ruta— no tendría nada que la
//     matara: la pantalla ya se pinta igual.
//   - 303 + flash: `redirectWith` es el PRG, y D-047.16 lo acota a donde hubo mutación. Un GET no
//     mutó nada, y el POST corta ANTES de llamar a la API, así que tampoco. Además, redirigir a una
//     pantalla que ese tenant no puede ver es un bucle.
//   - 400 repintando: esa excepción existe para no perder lo tecleado; sin la feature no hay
//     formulario que preservar.
//
// 🔴 FAIL-CLOSED POR CONSTRUCCIÓN, no por una condición que alguien pueda olvidar: si el plan no se
// puede leer, resolveEntitlements devuelve la vista CERO, cuyo mapa es nil, y `Has` sobre un mapa nil
// es false. Aquí NO se escribe ningún `if ent.Resolved`: escribirlo sería añadir la única forma de
// que este gate se abriera por un fallo del upstream.

// requiereFeature corta las rutas del grupo cuando el plan del tenant no incluye `feature`.
//
// SIEMBRA LA VISTA DEL PLAN EN EL CONTEXTO para que el handler la reutilice: sin eso se pagarían DOS
// llamadas a /entitlements por petición —una del gate y otra de la pantalla—, y esta consola resuelve
// el plan SIN CACHÉ, una vez por petición. Ver entitlementsFromContext.
//
// `plantilla` y `titulo` son los de la pantalla que se está gateando: la denegación se pinta con su
// propia página vacía —el marco, la barra y el bloque que explica que el plan no la incluye— y no con
// un error genérico, porque quien llega aquí necesita saber qué le falta, no que algo falló.
func (h *AdminHandler) requiereFeature(feature, plantilla, titulo string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 🔴 SIN EMPRESA NO SE PREGUNTA POR EL PLAN, y es el mismo razonamiento escrito en ShowHome:
		// `GET /api/v1/entitlements` responde 401 cuando el token no trae tenant, así que preguntar
		// costaría un refresco contra identity en cada carga para acabar en la vista cero. Y el 403
		// que saldría de ahí sería un DIAGNÓSTICO FALSO: a esa sesión no le falta un plan, le falta
		// una empresa, y quien se lo tiene que explicar es el parcial `sin_empresa` de la pantalla.
		//
		// Esto no abre ninguna puerta: sin empresa el handler no llama a la API y no pinta bandeja
		// alguna (ver renderSolicitudes), y el POST del descarte se va por 303 antes de tocar nada.
		if sinEmpresa(c) {
			c.Next()
			return
		}

		ent := resolveEntitlements(c, h.auth, h.api)
		c.Set(entitlementsContextKey, ent)
		if ent.Has(feature) {
			c.Next()
			return
		}

		data := h.pageData(c, titulo)
		data[entitlementsDataKey] = ent
		renderer.HTML(c, http.StatusForbidden, plantilla, data)
		// Abort y no `return` a secas: sin él Gin seguiría llamando al handler, que escribiría una
		// SEGUNDA respuesta sobre la misma petición.
		c.Abort()
	}
}
