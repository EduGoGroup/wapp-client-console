package web

import (
	"errors"
	"net/http"

	sharedweb "github.com/EduGoGroup/wapp-shared/web"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
)

// flash.go es el VOCABULARIO de esta consola: los códigos estables que sus handlers ponen en
// `?error=`/`?success=` al redirigir, y el texto en español que ve el usuario.
//
// El MECANISMO —que el texto salga siempre de la tabla y nunca del query string ni del upstream, y
// que un código desconocido caiga al genérico— vive en `wapp-shared/web`.FlashCatalog. Los códigos
// no suben: dependen de las pantallas de cada consola.
//
// Se declaran como CONSTANTES y no como literales sueltos porque el emisor y la tabla tienen que
// decir lo mismo: un código escrito a mano en los dos sitios se desincroniza y el usuario recibe el
// mensaje genérico sin que nada falle. Hay un test que exige que todo código emitido tenga traducción.
const (
	// flashSessionExpired lo emite el AuthMiddleware cuando había cookie de sesión y ya no sirve.
	flashSessionExpired = "session_expired"
	// flashLoggedOut lo emite DoLogout tras cerrar la sesión.
	flashLoggedOut = "logged_out"

	// --- Desenlaces de las pantallas de administración (T1.3 / T1.4a) ---

	// flashNotInYourTenant traduce el 404 de la API.
	//
	// 🔴 El texto NO dice «no encontrado», y es lo más importante de esta tabla: la plataforma
	// responde 404 —y no 403— cuando el identificador pertenece a OTRA empresa, precisamente para no
	// confirmar que existe. Un «no existe» a secas mentiría al usuario y, peor, le haría buscar un
	// error de tecleo donde lo que hay es una frontera de tenant.
	flashNotInYourTenant = "not_in_your_tenant"
	// flashPersonUnknown traduce el 404 del ALTA, que es el único 404 de esta consola que sí
	// significa «no existe»: ahí no hay frontera de tenant que proteger, porque la plataforma está
	// preguntando por el PADRÓN de identity. Decirle «no pertenece a tu empresa» a quien pegó mal un
	// UUID lo mandaría a pedir permisos en vez de a revisar lo que pegó.
	flashPersonUnknown = "person_unknown"
	// flashForbidden traduce el 403: el token vale, pero al usuario le falta el scope.
	flashForbidden = "forbidden"
	// flashInvalidInput traduce el 400/422 de la API.
	flashInvalidInput = "invalid_input"
	// flashConflict traduce el 409 genérico (un rol con ese nombre ya existe).
	flashConflict = "conflict"
	// flashMemberElsewhere traduce el 409 del plano de miembros (MD-055.2).
	flashMemberElsewhere = "member_elsewhere"
	// flashUpstreamUnavailable es el desenlace de todo lo demás: 5xx, red, plazo agotado.
	flashUpstreamUnavailable = "upstream_unavailable"
	// flashSelfRemoval lo emite la baja cuando el usuario intenta darse de baja a sí mismo. No es un
	// error de la API: la consola lo corta antes de llamar (ver members_handler.go).
	flashSelfRemoval = "self_removal"
	// flashMissingField lo emite un formulario incompleto, antes de salir a la red.
	flashMissingField = "missing_field"

	// flashAddedWithoutRole es un DESENLACE A MEDIAS, y por eso vive entre los errores y no entre los
	// éxitos: la persona quedó incorporada, pero el rol que se pidió en el mismo formulario no se le
	// pudo asignar. Son dos llamadas y no hay transacción que las una (ver AddMember); pintar esto
	// como éxito dejaría a alguien dentro de la empresa y sin permisos, creyendo que los tiene.
	flashAddedWithoutRole = "added_without_role"

	// --- Desenlaces de la pantalla de sesiones (T2.1) ---

	// flashSessionNotYours traduce el 404 del plano de SESIONES.
	//
	// Es el mismo 404 con frontera de tenant que flashNotInYourTenant, pero con el sustantivo de esta
	// pantalla: el texto genérico habla de roles y aquí no hay ninguno. Conserva la ambigüedad —«no
	// es tuya o no existe»— porque distinguir las dos cosas confirmaría que ese identificador existe
	// en algún sitio, que es justo lo que el 404 de la plataforma evita.
	flashSessionNotYours = "session_not_yours"
	// flashInvalidProfile lo emite el formulario de perfil ante un valor que no es activa ni pasiva,
	// la cadena vacía incluida (el `value` del <option> «sin dato»). No sale a la red.
	flashInvalidProfile = "invalid_profile"
	// flashSessionOffline traduce el 502 del envío: el teléfono existe y está desconectado. Es el
	// desenlace más frecuente del envío y el más accionable, así que NO puede caer al genérico.
	flashSessionOffline = "session_offline"
	// flashSendTimeout traduce el 504 del envío: el acuse del Edge no llegó a tiempo.
	//
	// 🔴 NO significa «no se envió». La nube ya empujó el comando; lo que expiró es la espera del
	// acuse. Por eso el texto NO dice «inténtalo de nuevo» a secas —repetir puede mandar el mensaje
	// DOS VECES a un cliente real—: dice que se compruebe antes.
	flashSendTimeout = "send_timeout"
	// flashSendNotDelivered es el `ok:false` de un 200: el Edge recibió el comando y no pudo
	// ejecutarlo. Es un DESENLACE A MEDIAS y por eso vive entre los errores: pintarlo como éxito le
	// diría a la dueña que el mensaje salió cuando no salió.
	flashSendNotDelivered = "send_not_delivered"

	flashMemberAdded   = "member_added"
	flashMemberRemoved = "member_removed"
	flashRoleCreated   = "role_created"
	flashRoleAssigned  = "role_assigned"
	flashRoleRemoved   = "role_removed"

	// flashMessageSent es el acuse del Edge: el comando se aceptó. El identificador del comando NO va
	// en el texto —el catálogo traduce códigos, no interpola datos—; viaja aparte y la pantalla lo
	// pinta como chip (ver ackSeguro en sessions_handler.go).
	flashMessageSent = "message_sent"
	// Los DOS éxitos del cambio de perfil, uno por perfil, y no uno solo con el nombre interpolado:
	// el catálogo es código→texto fijo, y meter el valor en el mensaje obligaría a construirlo fuera
	// de la tabla — que es justo por donde entra un texto sin traducir o, peor, uno que venga del
	// query string. Además cada perfil tiene una consecuencia distinta que contar.
	flashProfileActive  = "profile_active"
	flashProfilePassive = "profile_passive"
)

var (
	// El fallback vacío cae a web.DefaultFlashFallback ("Ocurrió un error inesperado.").
	flashErrors = sharedweb.NewFlashCatalog("", map[string]string{
		flashSessionExpired: "Tu sesión caducó. Vuelve a entrar.",
		flashNotInYourTenant: "Ese identificador no pertenece a tu empresa, o el rol no está disponible para ella. " +
			"La plataforma no dice cuál de las dos cosas es, a propósito.",
		flashForbidden:       "Tu usuario no tiene permiso para esta operación.",
		flashInvalidInput:    "La plataforma rechazó los datos. Revisa lo que escribiste.",
		flashConflict:        "Ya existe algo con ese nombre en tu empresa.",
		flashMemberElsewhere: "Esa persona ya pertenece a otra empresa. Mientras el canje no sepa elegir empresa, una segunda membresía le quitaría el acceso en vez de dárselo.",
		flashPersonUnknown: "Ese identificador no existe en wApp. Comprueba que lo has pegado entero: " +
			"la persona tiene que registrarse primero y pasarte el suyo desde «Mi identificador».",
		flashAddedWithoutRole: "La persona quedó incorporada a tu empresa, pero el rol NO se le pudo asignar: " +
			"entra en Roles y asígnaselo desde ahí.",
		flashUpstreamUnavailable: "No se pudo completar la operación ahora mismo. Inténtalo de nuevo en un momento.",
		flashSelfRemoval:         "No puedes darte de baja a ti mismo: te quedarías sin acceso a esta consola.",
		flashMissingField:        "Faltan datos: completa el formulario antes de enviarlo.",
		flashSessionNotYours: "Esa sesión no es de tu empresa, o ya no existe. La plataforma no dice cuál de las dos " +
			"cosas es, a propósito: elige una del listado.",
		flashInvalidProfile: "Elige un perfil válido para la sesión: activa o pasiva.",
		flashSessionOffline: "El teléfono de esa sesión está desconectado ahora mismo. Inténtalo cuando vuelva a " +
			"estar en línea.",
		flashSendTimeout: "El acuse del equipo no llegó a tiempo, así que no se sabe si el mensaje salió. " +
			"Compruébalo en el teléfono ANTES de repetirlo: volver a enviarlo puede duplicarlo.",
		flashSendNotDelivered: "El equipo recibió el mensaje pero no pudo entregarlo. Revisa el número de destino " +
			"e inténtalo de nuevo.",
	})

	flashSuccesses = sharedweb.NewFlashCatalog("Acción completada.", map[string]string{
		flashLoggedOut: "Sesión cerrada.",
		flashMemberAdded: "Persona incorporada a tu empresa. Si ya era miembro, no ha cambiado nada: " +
			"la plataforma no distingue los dos casos, a propósito.",
		flashMemberRemoved: "Persona dada de baja de tu empresa. Sus roles y permisos NO se han borrado: si vuelve a entrar, los recupera.",
		flashRoleCreated:   "Rol creado.",
		flashRoleAssigned:  "Rol asignado.",
		flashRoleRemoved:   "Rol retirado.",
		flashMessageSent:   "El equipo aceptó el mensaje y lo está enviando.",
		flashProfileActive: "Perfil cambiado a ACTIVA: esa sesión vuelve a conversar sola y contesta por su cuenta.",
		flashProfilePassive: "Perfil cambiado a PASIVA: esa sesión solo envía. Lo que le escriban se descarta en tu " +
			"equipo y no sube a la nube.",
	})
)

// flashError traduce un código de error al mensaje que ve el usuario.
func flashError(code string) string { return flashErrors.Message(code) }

// flashSuccess traduce un código de éxito al mensaje que ve el usuario.
func flashSuccess(code string) string { return flashSuccesses.Message(code) }

// flashCodeFor traduce un error del apiclient al código de flash que le corresponde.
//
// El ORDEN de las ramas es contrato, no estilo, y ya son DOS los pares que dependen de él. La regla
// es la misma: el sentinela ESPECÍFICO envuelve al genérico, así que va antes o el genérico se lo
// come sin que nada falle — el usuario lee un texto equivocado y todo sigue verde.
//   - ErrMemberOfAnotherTenant envuelve a ErrConflict: al revés, la guarda de membresía única
//     (MD-055.2) se explicaría como «ya existe algo con ese nombre».
//   - ErrPersonUnknown envuelve a ErrNotFound: al revés, un UUID que no existe en NINGUNA empresa se
//     explicaría como «no pertenece a tu empresa», que manda a pedir permisos en vez de a revisar lo
//     que se pegó.
//
// Hay un test por cada par, y cada uno cae si se mueve su rama detrás de la genérica.
func flashCodeFor(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, apiclient.ErrMemberOfAnotherTenant):
		return flashMemberElsewhere
	case errors.Is(err, apiclient.ErrPersonUnknown):
		return flashPersonUnknown
	case errors.Is(err, apiclient.ErrNotFound):
		return flashNotInYourTenant
	case errors.Is(err, apiclient.ErrForbidden):
		return flashForbidden
	case errors.Is(err, apiclient.ErrInvalidInput):
		return flashInvalidInput
	case errors.Is(err, apiclient.ErrConflict):
		return flashConflict
	case errors.Is(err, apiclient.ErrUnauthorized):
		// El 401 llega aquí solo si withAuthRetry ya refrescó y volvió a fallar: la sesión no sirve.
		return flashSessionExpired
	default:
		return flashUpstreamUnavailable
	}
}

// flashCodeForSessions es el traductor del plano de SESIONES, y existe por lo mismo que
// memberStatusError en el apiclient: hay desenlaces cuyo SIGNIFICADO es propio de este plano y que el
// traductor general se comería.
//
// Son tres, y los tres son accionables por quien mira la pantalla:
//   - 502 · el teléfono está desconectado. En el genérico sería «no se pudo completar la operación»,
//     que manda a reintentar contra una sesión que va a fallar igual hasta que vuelva a estar en línea.
//   - 504 · el acuse no llegó a tiempo, que NO es «no se envió» (ver flashSendTimeout).
//   - 404 · frontera de empresa, con el sustantivo de esta pantalla en vez del genérico, que habla de
//     roles.
//
// 🔴 El ORDEN importa, y por el mismo motivo que en flashCodeFor: los dos primeros se reconocen por
// STATUS (llegan como *APIError, sin sentinela) y el tercero por sentinela. StatusCodeOf devuelve 0
// para los sentinelas, así que un 404 nunca entra por el switch de arriba; al revés no se cumple —si
// la rama de ErrNotFound fuera antes, seguiría funcionando—, pero se leen mejor en el orden en que la
// petición los produce.
func flashCodeForSessions(err error) string {
	switch apiclient.StatusCodeOf(err) {
	case http.StatusBadGateway:
		return flashSessionOffline
	case http.StatusGatewayTimeout:
		return flashSendTimeout
	}
	if errors.Is(err, apiclient.ErrNotFound) {
		return flashSessionNotYours
	}
	return flashCodeFor(err)
}
