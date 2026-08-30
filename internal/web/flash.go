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

	// --- Desenlaces de la pantalla de invitaciones (T-A7 · T-A8) ---

	// flashInvalidTTL lo emite el formulario de emisión ante una caducidad que no es ninguna de las
	// que la lista ofrece. No sale a la red, igual que flashInvalidProfile: el recorte a
	// [60 s, 30 días] lo hace el servidor, y esta consola no lo repite — solo se niega a mandar un
	// valor que ella misma no ha ofrecido.
	flashInvalidTTL = "invalid_ttl"
	// flashInvitationLost es el desenlace más raro y el más difícil de explicar: la invitación SÍ se
	// creó, pero su código no llegó a la pantalla (no se pudo empaquetar en la cookie de un solo uso).
	//
	// Es un DESENLACE A MEDIAS y por eso vive entre los errores: el código ya no se puede recuperar
	// —existió una vez— pero la invitación está viva y contando su plazo. El texto tiene que decir las
	// dos mitades y qué hacer: anularla desde el listado y emitir otra.
	flashInvitationLost = "invitation_lost"
	// flashInvitationRedeemed traduce el 409 de REVOCAR: esa invitación ya se canjeó.
	//
	// 🔴 El texto NO puede decir «hecho»: revocar algo ya consumido no deshace la membresía que el
	// canje escribió, y dar por buena la operación le diría a la dueña que acaba de retirarle el
	// acceso a alguien que sigue dentro. Dice dónde se retira de verdad: la baja, en Miembros.
	flashInvitationRedeemed = "invitation_redeemed"

	// --- Desenlaces del CANJE (los cuatro del criterio, y son para el INVITADO) ---
	//
	// Quien lee estos cuatro textos no administra nada: acaba de registrarse y ha pegado un código que
	// le pasaron por WhatsApp. Cada uno lleva a una acción distinta, y esa es la razón de que sean
	// cuatro y no uno: volver a copiar el código, pedir otro, o hablar con quien se lo mandó.

	// flashInvitationUnknown traduce el 404: no hay ninguna invitación con ese código. Aquí sí
	// significa «no existe» —quien canjea no tiene todavía ninguna empresa cuya frontera proteger—,
	// así que el consejo útil es revisar lo que se pegó.
	flashInvitationUnknown = "invitation_unknown"
	// flashInvitationExpired traduce el 410: existía y venció.
	//
	// 🔴 Es el que NO puede caer al genérico: «inténtalo de nuevo en un momento» manda a repetir algo
	// que va a fallar igual para siempre. Lo que hay que hacer es pedir otra invitación.
	flashInvitationExpired = "invitation_expired"
	// flashInvitationUnusable traduce el 409, que funde dos causas que el servidor tampoco separa: la
	// invitación ya se usó o se anuló, o esta cuenta ya pertenece a otra empresa. El texto las dice
	// las dos porque quien lee no puede saber cuál es la suya, y las dos se resuelven igual.
	flashInvitationUnusable = "invitation_unusable"
	// flashJoinedRelogin es el 204 con la sesión a medio actualizar: la persona YA es miembro, pero
	// esta sesión sigue con el token que se emitió cuando no lo era, y el refresco que lo cambiaría
	// falló.
	//
	// Vive entre los ERRORES por lo mismo que flashAddedWithoutRole: pintarlo como éxito la dejaría
	// mirando la pantalla de «no perteneces a ninguna empresa» justo después de leer «¡listo!», que es
	// la forma más rápida de convencer a alguien de que el canje no funcionó.
	flashJoinedRelogin = "joined_relogin"

	// --- Desenlaces del EDITOR: flujos y disparadores (T6.3 · T6.4) ---
	//
	// 🔴 Este bloque tiene DOS familias y no una, y la diferencia no es de tema sino de RESPUESTA
	// (D-047.16): los códigos de VALIDACIÓN LOCAL —los que empiezan por trigger_ y el del JSON— se
	// pintan REPINTANDO el formulario con un 400, y no viajan nunca por el query string; los que
	// traducen un desenlace de la API viajan en el `?error=` de un 303. Los dos salen de esta misma
	// tabla a propósito: un texto escrito a mano en el handler es el que acaba diciendo otra cosa
	// que el catálogo ante el mismo desenlace.

	// flashFlowInvalidJSON lo emite la publicación ante una definición que no es JSON. Es validación
	// LOCAL: no sale a la red, y por eso su pantalla REPINTA (400) en vez de redirigir. Ver
	// PublishFlow y D-047.16.
	flashFlowInvalidJSON = "flow_invalid_json"
	// flashFlowVersionConflict traduce el 409 de publicar: alguien publicó entre medias.
	//
	// ⚠️ HOY LA PLATAFORMA NO LO EMITE (ver ErrFlowVersionConflict en apiclient/editor.go). Se
	// traduce igual porque el día que lo emita —o lo emita un proxy por delante— caería en el
	// genérico «ya existe algo con ese nombre en tu empresa», que ante una publicación no describe
	// nada.
	flashFlowVersionConflict = "flow_version_conflict"

	// flashTriggerDuplicate traduce el 409 de crear un disparador, con el mismo matiz: la plataforma
	// no tiene unicidad y hoy no lo emite.
	flashTriggerDuplicate = "trigger_duplicate"
	// flashTriggerWithoutEventStart traduce el 422 de crear, y es el ÚNICO de los tres que la
	// plataforma SÍ devuelve hoy (D-054.8 / MD-054.2).
	//
	// 🔴 Es el que no puede caer al genérico: `statusError` mete 400 y 422 en el mismo
	// ErrInvalidInput, así que sin este código la pantalla diría «revisa lo que escribiste» ante un
	// formulario cuyos datos están TODOS bien. Lo que falta no está en el formulario: falta un
	// `event_start` en la empresa que le dé salida a la conversación.
	flashTriggerWithoutEventStart = "trigger_without_event_start"

	// Los OCHO desenlaces de validateTriggerForm y el de la prioridad. Son códigos y no textos
	// escritos en el handler porque el vocabulario de esta consola es cerrado, y son NUEVE y no uno
	// porque cada uno dice qué campo falta: un «revisa el formulario» genérico ante ocho campos deja
	// a quien administra probando a ciegas.
	flashTriggerPriorityNotInteger   = "trigger_priority_not_integer"
	flashTriggerKeywordIncomplete    = "trigger_keyword_incomplete"
	flashTriggerFallbackWithoutFlow  = "trigger_fallback_without_flow"
	flashTriggerEscapeWithoutKeyword = "trigger_escape_without_keyword"
	flashTriggerEventStartNoKeyword  = "trigger_event_start_without_keyword"
	flashTriggerEventStartNoKind     = "trigger_event_start_without_kind"
	flashTriggerEventKindUnknown     = "trigger_event_kind_unknown"
	flashTriggerEventStopWithoutKey  = "trigger_event_stop_without_keyword"
	flashTriggerKindUnknown          = "trigger_kind_unknown"

	// --- Desenlaces del SELECTOR DE EMPRESAS (T5.3) ---

	// flashTenantNotYours traduce el 404 de POST /api/v1/auth/active-tenant.
	//
	// 🔴 El texto NO dice si esa empresa existe, y es lo único que importa de este código: el
	// servidor responde igual a «no eres miembro» y a «no existe» para que nadie pueda sondear UUIDs
	// y levantar el censo de empresas de la plataforma. Un mensaje que separase los dos casos
	// desharía ese anti-oráculo desde la UI, que es donde más se lee.
	flashTenantNotYours = "tenant_not_yours"
	// flashTenantRelogin es el 204 con la sesión a medio actualizar: la empresa YA quedó elegida en
	// el servidor, pero esta sesión sigue con el token que se emitió antes y el refresco que lo
	// cambiaría falló.
	//
	// Vive entre los ERRORES por lo mismo que flashJoinedRelogin: pintarlo como éxito dejaría a la
	// persona mirando los datos de la empresa ANTERIOR justo después de leer que ya cambió.
	flashTenantRelogin = "tenant_relogin"

	// Los TRES éxitos del editor. «Publicado» no dice la versión y no puede decirla —el catálogo
	// traduce códigos, no interpola datos—; la versión nueva está en la lista a la que se redirige,
	// que es donde se comprueba de verdad.
	flashFlowPublished  = "flow_published"
	flashTriggerCreated = "trigger_created"
	flashTriggerDeleted = "trigger_deleted"

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

	// flashInvitationRevoked es el acuse de la revocación. NO dice que la invitación desaparezca:
	// sigue en el listado, marcada como anulada, porque quien administra necesita ver que aquel código
	// que repartió ya no vale.
	flashInvitationRevoked = "invitation_revoked"
	// flashInvitationAccepted es el 204 del canje CON la sesión ya actualizada. Es el único éxito de
	// esta consola que lee alguien que no administra nada.
	flashInvitationAccepted = "invitation_accepted"

	// flashTenantSwitched es el acuse del cambio de empresa CON la sesión ya reemitida. No nombra la
	// empresa elegida —el catálogo traduce códigos, no interpola datos—, y no le hace falta: la
	// pantalla a la que redirige la tiene pintada arriba.
	flashTenantSwitched = "tenant_switched"
)

// LA EMISIÓN NO TIENE CÓDIGO DE ÉXITO, y es deliberado: su acuse es el CÓDIGO en pantalla, que se
// enseña una sola vez. Un «invitación creada» en el query string sobreviviría al F5 —la URL no
// cambia— y volvería a saludar sin el código debajo, que es justo la lectura que hay que evitar:
// «¿se ha creado otra?». Sin banner, tras recargar solo queda el listado, donde la invitación nueva
// está la primera.

var (
	// El fallback vacío cae a web.DefaultFlashFallback ("Ocurrió un error inesperado.").
	flashErrors = sharedweb.NewFlashCatalog("", map[string]string{
		flashSessionExpired: "Tu sesión caducó. Vuelve a entrar.",
		flashNotInYourTenant: "Ese identificador no pertenece a tu empresa, o el rol no está disponible para ella. " +
			"La plataforma no dice cuál de las dos cosas es, a propósito.",
		flashForbidden:    "Tu usuario no tiene permiso para esta operación.",
		flashInvalidInput: "La plataforma rechazó los datos. Revisa lo que escribiste.",
		flashConflict:     "Ya existe algo con ese nombre en tu empresa.",
		flashMemberElsewhere: "Esa persona ya pertenece a otra empresa, y tu plan no incluye que alguien esté en varias a la vez. " +
			"Habla con quien lleva la contratación si necesitas que también esté en la tuya.",
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
		flashInvalidTTL: "Elige una de las caducidades de la lista para la invitación.",
		flashInvitationLost: "La invitación se creó, pero su código no se pudo mostrar y ya no se puede recuperar. " +
			"Anúlala en el listado y emite otra.",
		flashInvitationRedeemed: "Esa invitación ya se canjeó, así que anularla no cambia nada: la persona ya está " +
			"dentro de tu empresa. Para retirarle el acceso, dale de baja en Miembros.",
		flashInvitationUnknown: "No encontramos esa invitación. Cópiala otra vez ENTERA —sin espacios ni saltos de " +
			"línea— y vuelve a pegarla.",
		flashInvitationExpired: "Esa invitación ya caducó. Pídele una nueva a quien te la mandó: las invitaciones " +
			"duran poco a propósito.",
		flashInvitationUnusable: "Esa invitación ya se usó o se anuló, o tu cuenta ya pertenece a una empresa. " +
			"Si crees que es un error, pídele una nueva a quien te la mandó.",
		flashJoinedRelogin: "Ya formas parte de la empresa, pero esta sesión todavía no lo ve. Cierra sesión y " +
			"vuelve a entrar y la tendrás.",
		flashTenantNotYours: "No pudimos entrar en esa empresa. Elige una de las de tu lista: la consola no dice " +
			"nada sobre las empresas que no son tuyas, a propósito.",
		flashTenantRelogin: "La empresa quedó elegida, pero esta sesión todavía no lo ve. Cierra sesión y vuelve a " +
			"entrar y estarás dentro de ella.",
		flashFlowInvalidJSON: "Eso no es un JSON válido, así que no se ha publicado nada. Revisa la definición " +
			"—lo que escribiste sigue aquí— y vuelve a intentarlo.",
		flashFlowVersionConflict: "Ese flujo cambió mientras lo editabas: alguien publicó otra versión. " +
			"Vuelve a abrirlo, comprueba la definición que hay ahora y publica sobre ella.",
		flashTriggerDuplicate: "Ya existe un disparador igual en tu empresa. Revisa el listado antes de crear otro.",
		flashTriggerWithoutEventStart: "El disparador está bien escrito, pero guardarlo dejaría la conversación sin " +
			"salida: tu empresa no tiene ningún disparador de tipo event_start que lleve a un evento. Crea ese " +
			"primero y luego vuelve a este.",
		flashTriggerPriorityNotInteger:   "La prioridad tiene que ser un número entero.",
		flashTriggerKeywordIncomplete:    "Un disparador de tipo keyword necesita la palabra clave Y el flujo al que lleva.",
		flashTriggerFallbackWithoutFlow:  "Un disparador de tipo fallback necesita el flujo al que lleva.",
		flashTriggerEscapeWithoutKeyword: "Un disparador de tipo escape necesita la palabra clave que corta la conversación.",
		flashTriggerEventStartNoKeyword:  "Un disparador de tipo event_start necesita la palabra clave que arranca el evento.",
		flashTriggerEventStartNoKind: "Un disparador de tipo event_start necesita además el tipo de evento: " +
			"menu, cart, survey o media.",
		flashTriggerEventKindUnknown:    "Ese tipo de evento no existe. Elige menu, cart, survey o media.",
		flashTriggerEventStopWithoutKey: "Un disparador de tipo event_stop necesita la palabra clave que desactiva el evento.",
		flashTriggerKindUnknown: "Elige un tipo de disparador de la lista: keyword, fallback, escape, event_start " +
			"o event_stop.",
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
		flashInvitationRevoked: "Invitación anulada. Quien tuviera ese código ya no puede usarlo; sigue en el " +
			"listado para que veas cuál era.",
		flashInvitationAccepted: "¡Listo! Ya formas parte de la empresa y puedes empezar a trabajar.",
		flashTenantSwitched: "Estás en la empresa que elegiste. Todo lo que veas a partir de ahora —sesiones, " +
			"miembros, roles e invitaciones— es de ella y solo de ella.",
		flashFlowPublished: "Flujo publicado como una versión NUEVA. Las anteriores siguen ahí: los flujos son " +
			"inmutables y la plataforma no edita en sitio.",
		flashTriggerCreated: "Disparador creado.",
		flashTriggerDeleted: "Disparador borrado. Los flujos a los que llevaba siguen publicados: lo que se " +
			"retiró es la regla que los arrancaba.",
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

// flashCodeForInvitaciones es el traductor del plano de INVITACIONES visto por quien ADMINISTRA:
// emitir, listar y revocar.
//
// Existe por UN desenlace, igual que memberStatusError existía por uno: el 409 de revocar, que aquí
// significa «esa invitación ya se canjeó» y no «ya existe algo con ese nombre en tu empresa». El
// genérico daría un texto que no describe nada de lo que pasó y, peor, dejaría a quien administra sin
// saber que la persona ya está dentro y que retirarla es otra operación.
//
// Todo lo demás delega: un traductor propio que copiara el resto de la tabla sería una tabla paralela
// esperando a desincronizarse.
func flashCodeForInvitaciones(err error) string {
	if errors.Is(err, apiclient.ErrInvitationRedeemed) {
		return flashInvitationRedeemed
	}
	return flashCodeFor(err)
}

// flashCodeForCanje es el traductor del CANJE, y es OTRO porque su lector es otro: no administra
// nada, acaba de registrarse y ha pegado un código que le llegó por WhatsApp.
//
// 🔴 SON CUATRO DESENLACES Y TIENEN QUE LLEGAR DISTINTOS HASTA LA PANTALLA (criterio de la ola). El
// servidor iguala a propósito el CUERPO y el TIEMPO del 404 y el 410 —para que nadie pueda sondear
// qué códigos existieron—, pero deja distinto el CÓDIGO DE ESTADO justo para que la UI pueda dar un
// consejo útil. Por eso aquí se distinguen por sentinela y jamás por el texto del upstream.
//
// El ORDEN de las ramas no es contrato entre ellas —las tres son excluyentes—, pero SÍ frente a la
// delegación final: ErrInvitationUnknown envuelve a ErrNotFound y ErrInvitationUnusable envuelve a
// ErrConflict, así que delegar antes de preguntar por ellos convertiría «revisa lo que pegaste» en
// «no pertenece a tu empresa», que es un sinsentido para quien todavía no tiene ninguna.
func flashCodeForCanje(err error) string {
	switch {
	case errors.Is(err, apiclient.ErrInvitationUnknown):
		return flashInvitationUnknown
	case errors.Is(err, apiclient.ErrInvitationExpired):
		return flashInvitationExpired
	case errors.Is(err, apiclient.ErrInvitationUnusable):
		return flashInvitationUnusable
	default:
		return flashCodeFor(err)
	}
}

// flashCodeForEditor es el traductor del plano del EDITOR —flujos y disparadores— y existe por lo
// mismo que flashCodeForSessions y flashCodeForInvitaciones: hay desenlaces cuyo significado es
// propio de este plano y que el traductor general se comería.
//
// Son TRES, y el ORDEN de las ramas es contrato: los tres sentinelas ENVUELVEN a su genérico
// (apiclient/editor.go lo declara así a propósito), de modo que preguntar antes por el genérico se
// come el significado sin que nada falle — el usuario lee un texto equivocado y todo sigue verde.
//
//   - 422 · ErrTriggerWithoutEventStart. Es el ÚNICO de los tres que la plataforma devuelve hoy, y
//     el que hace falta de verdad: `statusError` mete 400 y 422 en el MISMO ErrInvalidInput, así que
//     por el genérico la pantalla diría «revisa lo que escribiste» ante un formulario correcto. Es
//     el defecto de campo de la Ola 5 con otro número.
//   - 409 de crear · ErrTriggerDuplicate, y 409 de publicar · ErrFlowVersionConflict. HOY LA
//     PLATAFORMA NO EMITE NINGUNO (no hay unicidad en triggers, y publicar versiona N+1 sin
//     comprobar contra qué se editaba). Se traducen igual porque el día que existan —o los ponga un
//     proxy— caerían en «ya existe algo con ese nombre en tu empresa», que ante una publicación no
//     describe nada.
//
// Es UNO para los dos planos y no dos: publicar no puede producir los de disparador ni al revés, y
// dos tablas que se copian el resto son dos tablas esperando a desincronizarse.
func flashCodeForEditor(err error) string {
	switch {
	case errors.Is(err, apiclient.ErrTriggerWithoutEventStart):
		return flashTriggerWithoutEventStart
	case errors.Is(err, apiclient.ErrTriggerDuplicate):
		return flashTriggerDuplicate
	case errors.Is(err, apiclient.ErrFlowVersionConflict):
		return flashFlowVersionConflict
	default:
		return flashCodeFor(err)
	}
}
