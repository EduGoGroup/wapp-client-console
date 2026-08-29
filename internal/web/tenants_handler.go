package web

import (
	"errors"
	"log/slog"

	webgin "github.com/EduGoGroup/wapp-shared/web/gin"
	"github.com/gin-gonic/gin"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
)

// tenants_handler.go es EL SELECTOR DE EMPRESAS (Plan 047 · Ola 5 · T5.3).
//
// 🔴 SON TRES ESTADOS Y NO DOS, y esa es toda la tarea. Hasta hoy esta consola creía que una sesión
// sin empresa era una sola cosa —«todavía no te han dado acceso»— y pintaba la pantalla de espera. No
// lo es: el Context Token de quien tiene CERO empresas y el de quien tiene DOS y no ha elegido son
// IDÉNTICOS (los dos sin tenant y sin un solo grant), así que del token no sale la diferencia.
//   - cero empresas  ⇒ pantalla de espera (el parcial sin_empresa, con su canje de invitación).
//   - una empresa    ⇒ exactamente lo de siempre, y NINGÚN selector: no habría entre qué elegir.
//   - varias         ⇒ selector.
//
// La única forma de distinguirlos es PREGUNTAR (GET /api/v1/auth/tenants), y por eso este listado se
// resuelve en pageData: no es un adorno de una pantalla, es lo que decide qué pantalla es.

// rutaEmpresa es la ruta del POST que fija la empresa activa.
//
// Es una CONSTANTE y no un literal repartido porque la usan tres sitios que tienen que decir lo
// mismo: el router, el `action` de los dos formularios y el test que afirma que con UNA sola empresa
// no hay ningún control capaz de cambiarla. Un selector «llamado de otra forma» seguiría teniendo que
// apuntar aquí para hacer algo, así que esta cadena es el ancla que lo caza.
const rutaEmpresa = "/empresa"

// tenantOptionView es UNA empresa tal como la pinta el selector.
//
// `Name` no es `DisplayName` a secas: una empresa que llegara sin nombre pintaría un `<option>` vacío
// —imposible de elegir a ciegas—, así que cae al identificador abreviado, igual que hace la tabla de
// miembros con las personas. `Active` viene tal cual del servidor y NO se recalcula aquí (ver
// apiclient.TenantOption).
type tenantOptionView struct {
	ID     string
	Name   string
	Active bool
}

// resolveTenants pide las empresas del sujeto y devuelve SIEMPRE una lista usable: el fallo se
// traduce en lista VACÍA, nunca en un error que tumbe la página. Es el mismo criterio que
// resolveEntitlements y falla hacia el mismo lado —el conservador—: sin listado no se pinta selector,
// y la sesión sin empresa vuelve a ver la pantalla de espera de siempre, que es lo que veía ayer.
//
// 🔑 A DIFERENCIA DE LOS ENTITLEMENTS, ESTA SÍ SE LLAMA SIN EMPRESA. `GET /api/v1/entitlements`
// responde 401 cuando el token no trae tenant —de ahí que sinEmpresa() corte antes de llamarlo—,
// pero este endpoint está montado detrás de `Authenticate` y SIN `RequirePermission` precisamente
// porque quien lo necesita es quien todavía no tiene empresa en su token. Cortar aquí por sinEmpresa
// dejaría sin respuesta la única pregunta que importa.
func resolveTenants(c *gin.Context, auth *AuthHandler, api *apiclient.Client) []tenantOptionView {
	var empresas []apiclient.TenantOption
	err := auth.withAuthRetry(c, func(accessToken string) error {
		var lerr error
		empresas, lerr = api.Tenants.List(c.Request.Context(), accessToken)
		return lerr
	})
	if err != nil {
		slog.Warn("no se pudieron leer las empresas del usuario (sin selector; se pinta lo de siempre)", "error", err)
		return nil
	}

	out := make([]tenantOptionView, 0, len(empresas))
	for _, e := range empresas {
		nombre := e.DisplayName
		if nombre == "" {
			nombre = shortID(e.ID)
		}
		out = append(out, tenantOptionView{ID: e.ID, Name: nombre, Active: e.Active})
	}
	return out
}

// nombreDeLaEmpresa devuelve el nombre legible de la empresa del token.
//
// 🔴 BUSCA POR IDENTIFICADOR, no coge «la primera» ni «la marcada como activa». Las tres coinciden
// mientras todo va bien, y justo por eso la diferencia solo se vería cuando algo va mal: una sesión
// cuyo token quedó con la empresa vieja tras un cambio pintaría el nombre de la NUEVA, que es la
// forma más rápida de convencer a alguien de que está mirando datos de otro negocio.
//
// Cae al propio identificador cuando el listado no se resolvió: se pierde legibilidad, no el dato.
func nombreDeLaEmpresa(empresas []tenantOptionView, tenantID string) string {
	for _, e := range empresas {
		if e.ID == tenantID {
			return e.Name
		}
	}
	return tenantID
}

// SelectTenant fija la empresa activa del sujeto y REEMITE la sesión (T5.3).
//
// 🔴 TRAS EL 204 HAY QUE REFRESCAR, y no es cosmética: la elección queda escrita en el servidor, pero
// el Context Token que la persona tiene en la mano se emitió ANTES y sigue diciendo la empresa de
// antes (o ninguna). Sin el refresco, el redirect la devolvería exactamente a la pantalla que acaba
// de usar, sin que nada haya cambiado a la vista. El refresco vuelve a canjear el Identity Token y
// ESE canje ya lee la empresa elegida. Es el mismo razonamiento —y el mismo desenlace a medias— que
// el canje de una invitación (ver RedeemInvitation).
//
// Redirige a la PORTADA y no a la pantalla de origen: al cambiar de empresa, lo que se estaba mirando
// era de la otra. Devolver a la misma tabla con otro contenido invita a leerla como si fuera la
// misma lista.
func (h *AdminHandler) SelectTenant(c *gin.Context) {
	elegida := formValue(c, "tenant_id")
	if elegida == "" {
		redirectWith(c, "/", flashMissingField, "")
		return
	}

	if code := flashCodeForEmpresa(h.auth.withAuthRetry(c, func(accessToken string) error {
		return h.api.Tenants.SetActive(c.Request.Context(), accessToken, elegida)
	})); code != "" {
		slog.Warn("no se pudo fijar la empresa activa", "codigo", code)
		redirectWith(c, "/", code, "")
		return
	}

	refreshToken := webgin.RefreshTokenFromContext(c)
	if refreshToken == "" {
		slog.Warn("empresa elegida, pero la sesión no tiene refresh token con el que releerla")
		redirectWith(c, "/", flashTenantRelogin, "")
		return
	}
	if _, err := h.auth.refreshSession(c, refreshToken); err != nil {
		slog.Warn("empresa elegida, pero el refresco de la sesión falló: no se verá hasta volver a entrar",
			"error", err)
		redirectWith(c, "/", flashTenantRelogin, "")
		return
	}
	redirectWith(c, "/", "", flashTenantSwitched)
}

// flashCodeForEmpresa es el traductor de la elección de empresa, y existe por UN desenlace: el 404.
//
// La plataforma responde lo mismo a «no eres miembro de esa empresa» y a «esa empresa no existe», a
// propósito: distinguirlas dejaría sondear UUIDs y levantar el censo de empresas de la plataforma. El
// traductor general daría «Ese identificador no pertenece a tu empresa, o el rol no está disponible
// para ella», que además de hablar de roles —aquí no hay ninguno— confirmaría la lectura de que ese
// identificador existe en algún sitio. El texto propio no delata nada y dice qué hacer: elegir una de
// la lista.
//
// Todo lo demás delega: un traductor que copiara el resto de la tabla sería una tabla paralela
// esperando a desincronizarse.
func flashCodeForEmpresa(err error) string {
	if errors.Is(err, apiclient.ErrNotFound) {
		return flashTenantNotYours
	}
	return flashCodeFor(err)
}
