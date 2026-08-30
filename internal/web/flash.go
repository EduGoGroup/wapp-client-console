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

	// --- Desenlaces de la BANDEJA DE SOLICITUDES (T7.2) ---
	//
	// 🔴 Los TRES que hay aquí son los desenlaces de la API. Lo que NO sale de esta tabla son los
	// avisos CON NÚMEROS del descarte —«se descartaron 3 de 5», «marcaste 250 y caben 200»—, y es
	// deliberado: el catálogo traduce códigos a textos FIJOS y no interpola datos, así que un aviso
	// que sin su número no dice nada no cabe aquí. Viajan como vista y los pinta la pantalla; ver
	// avisoSolicitudes.

	// flashSolicitudesFiltrosInvalidos traduce el 400 del listado, que es su único rechazo
	// accionable: una fecha ilegible, un estado que no existe o un orden desconocido. El genérico
	// diría «revisa lo que escribiste» sobre un formulario de cuatro filtros, sin decir cuál.
	flashSolicitudesFiltrosInvalidos = "solicitudes_filtros_invalidos"
	// flashSolicitudesSinPlan traduce el 403 con `feature_not_enabled` que devuelve la plataforma.
	//
	// 🔴 Llega cuando el gate por ruta de esta consola dijo que sí y la plataforma dijo que no, o sea
	// cuando el plan cambió entre las dos. NO puede caer en el genérico «tu usuario no tiene
	// permiso»: no es un permiso lo que falta, es lo contratado, y quien lo lee tiene que saber que
	// la salida está en la contratación y no en pedirle un rol a nadie.
	flashSolicitudesSinPlan = "solicitudes_sin_plan"
	// flashDescarteRechazado traduce el 400 del descarte: el lote no llegó a ejecutarse y no se tocó
	// ninguna solicitud. Ese «no se tocó ninguna» es lo que hace falta decir y lo que el genérico no
	// dice, sobre una operación sin vuelta atrás.
	flashDescarteRechazado = "descarte_rechazado"
	// flashDescarteIncierto es el desenlace más incómodo de esta pantalla, y su texto es incómodo a
	// propósito: cada solicitud del lote es su propia unidad de trabajo en la plataforma, así que un
	// fallo a media faena deja escrito lo ya escrito. Prometer que «no se ha cambiado nada» sería
	// mentir, y lo único honesto es mandar a mirar la bandeja — sabiendo que repetir el mismo lote es
	// seguro, porque lo ya descartado vuelve como `already_discarded`.
	flashDescarteIncierto = "descarte_incierto"

	// --- Desenlaces de las acciones QUE NO LE HABLAN AL CLIENTE (T7.4) ---
	//
	// Son las tres del detalle que solo tocan la ficha: mover el estado, guardar las líneas
	// facturables y corregir la interpretación. Regenerar viaja aquí también porque tampoco le
	// escribe a nadie: reinterpreta el texto que el cliente ya mandó.
	//
	// 🔴 Lo que NO cabe en esta tabla, y hay que decirlo porque el origen sí lo decía: los números.
	// El BFF confirmaba el guardado con «la solicitud queda con 3 líneas y un total de 21000.00» y
	// la regeneración con «aparecerá como la revisión 4 · trabajo abc». El catálogo traduce códigos
	// a textos FIJOS y no interpola datos, así que esos números no viajan por la URL. No se pierden
	// del todo: tras el 303 la ficha se relee y ahí están las líneas, el total y el histórico de
	// revisiones. Lo que se pierde es la frase que los señalaba.

	// flashSolicitudSinEstado es el rechazo LOCAL de un envío sin estado. El desplegable lleva
	// `required`, así que solo llega por un POST hecho a mano; se contesta igual porque `required`
	// es del navegador y no una guarda.
	flashSolicitudSinEstado = "solicitud_sin_estado"
	// flashSolicitudTransicionInvalida traduce el 422 `invalid_transition`.
	//
	// 🔴 El origen repintaba con los destinos que trae ese rechazo (`allowed`) para reponer el
	// desplegable. Aquí NO: D-047.16 manda el desenlace de la API por 303, y tras el 303 el
	// desplegable se arma con `allowed_transitions` del GET, que es la misma autoridad y está más
	// fresca. Por eso el texto manda a mirarlo en vez de enumerar destinos que no puede saber.
	flashSolicitudTransicionInvalida = "solicitud_transicion_invalida"
	// flashSolicitudCambiadaPorOtro traduce ErrIntakeChanged, el 409 que emiten las tres puertas
	// que escriben sobre una solicitud.
	//
	// 🔴 NO puede caer en el genérico: en esta consola ErrConflict significa «ya existe algo con ese
	// nombre» (un rol repetido), y el consejo que hace falta aquí es RECARGAR. Tras el 303 la ficha
	// ya se releyó, así que lo recargado está delante mientras se lee el aviso.
	flashSolicitudCambiadaPorOtro = "solicitud_cambiada_por_otro"
	// flashSolicitudFormularioIncompleto es el rechazo LOCAL de un envío cuyos cinco campos por
	// fila no vienen emparejados: antes que adivinar qué precio va con qué artículo —y guardar una
	// mezcla— se rechaza y se pide recargar.
	flashSolicitudFormularioIncompleto = "solicitud_formulario_incompleto"
	// flashSolicitudLineaSinIdentificar es el rechazo LOCAL de un «Quitar» cuyo índice no señala
	// ninguna fila del envío.
	flashSolicitudLineaSinIdentificar = "solicitud_linea_sin_identificar"
	// flashSolicitudLineasIlegibles es el rechazo LOCAL de una cantidad o un precio que no se
	// pueden leer. Encabeza la lista de defectos que la pantalla pinta fila a fila: el aviso dice
	// que no se guardó NADA, y los defectos dicen dónde.
	flashSolicitudLineasIlegibles = "solicitud_lineas_ilegibles"
	// flashSolicitudLineasRechazadas traduce el 400 `invalid_items` de la plataforma.
	//
	// 🔑 Es el ÚNICO desenlace de la API de esta consola que NO viaja por la URL: se pinta
	// REPINTANDO (D-047.16 extendida el 2026-08-30), porque la edición es todo-o-nada y un
	// `invalid_items` no llegó a escribir nada. Este texto es solo el ENCABEZADO; la lista de
	// defectos por línea que trae el cuerpo la pinta la pantalla anclada a su fila, que es lo que de
	// verdad sirve para corregir.
	flashSolicitudLineasRechazadas = "solicitud_lineas_rechazadas"
	// flashSolicitudNoEditable traduce el 422 `not_editable`: el estado en el que está la solicitud
	// no admite tocar sus líneas. El camino se dice —moverla con el desplegable— sin nombrar los
	// estados, que llegan en el rechazo y no caben en un texto fijo.
	flashSolicitudNoEditable = "solicitud_no_editable"

	// Los de REGENERAR. Son SEIS y no uno porque llevan a sitios distintos: contratar el plan,
	// contratar el add-on, configurar la credencial, esperar, o nada. Un aviso único mandaría a
	// comprar algo que ya se tiene.

	// flashRegeneracionSinPlan es la falta de `llm_intake`: se contrata.
	flashRegeneracionSinPlan = "regeneracion_sin_plan"
	// flashRegeneracionSinAddon es la falta de `api_llm` con la vía externa configurada: o se
	// contrata el add-on, o se cambia la vía. Es OTRO código que el de arriba a propósito.
	flashRegeneracionSinAddon = "regeneracion_sin_addon"
	// flashRegeneracionSinCredencial es el 422 de credencial ausente: NO hay nada que contratar, y
	// decirlo con las palabras del paywall mandaría a comprar algo que la empresa ya tiene.
	flashRegeneracionSinCredencial = "regeneracion_sin_credencial"
	// flashRegeneracionSinOriginal es el 422 `source_unavailable`. El texto no separa `purged` de
	// `never_stored` porque el bloque de comparación de la misma página ya lo dice con esas dos
	// redacciones (ver origenView.RazonText): repetirlo aquí daría dos frases para un hecho.
	flashRegeneracionSinOriginal = "regeneracion_sin_original"
	// flashRegeneracionEnCurso es el 422 `reanalysis_in_progress`: ya hay una encargada.
	flashRegeneracionEnCurso = "regeneracion_en_curso"
	// flashRegeneracionViaInvalida es el 400 `invalid_via`. Esta consola NO manda vía (D-044.51),
	// así que este rechazo no debería poder ocurrir; se traduce con todas las letras en vez de caer
	// en el genérico porque, si ocurre, significa que alguien reintrodujo el campo.
	flashRegeneracionViaInvalida = "regeneracion_via_invalida"
	// flashRegeneracionTextoLargo es el material extra que no cabe. Lo emiten DOS puertas —la guarda
	// local y el 400 `text_too_long` de la plataforma— con el mismo tope, y por eso comparten
	// código: es el mismo hecho. CUÁNTO se pasó no está aquí sino en la vista (regenerarView.Runas),
	// por lo mismo que los avisos con números del descarte.
	flashRegeneracionTextoLargo = "regeneracion_texto_largo"

	// --- Desenlaces de las DOS acciones QUE SÍ LE HABLAN AL CLIENTE (T7.5) ---
	//
	// 🔴 SON CÓDIGOS PROPIOS Y NO REUTILIZAN LOS DE T7.4 AUNQUE VARIOS DESCRIBAN EL MISMO RECHAZO, y
	// la razón es la única cosa que separa a estas dos puertas de las otras cuatro: aquí sale un
	// WhatsApp hacia una persona. Lo que quien lee el aviso necesita saber no es «no se guardó nada»
	// sino «NO SE LE ENVIÓ NADA AL CLIENTE», y son dos frases distintas aunque en la plataforma sean
	// el mismo hecho —el envío va después de la escritura, así que sin escritura no hay mensaje—.
	// Quien las mezcle deja a la dueña deduciendo, sobre un mensaje que no puede desenviar.
	//
	// 🔑 De dónde sale que se pueda AFIRMAR «no se envió nada» en cada uno: del ORDEN de operaciones
	// del cloud, que está escrito y verificado —`cloud/wapp-cloud-platform/internal/intakes/
	// approve.go:373` valida TODO (texto, estado, líneas sin precio, líneas que cotizar) antes de la
	// primera escritura, y el envío es el paso (4), después de la transición y de la revisión—. No es
	// una analogía con `invalid_items`: es el dato del otro lado.

	// flashSolicitudSinRespuesta es el rechazo LOCAL de una aprobación sin texto. No es una
	// formalidad: es la frase que dice por qué esta consola no rellena ese hueco sola.
	flashSolicitudSinRespuesta = "solicitud_sin_respuesta"
	// flashSolicitudSinPregunta es el rechazo LOCAL de una petición de información sin pregunta. Las
	// que prepara el sistema son una propuesta del formulario y jamás salen solas (INV-1).
	flashSolicitudSinPregunta = "solicitud_sin_pregunta"
	// flashSolicitudSinPrecio traduce el 400 `lines_without_price` de la aprobación.
	//
	// 🔑 Es el SEGUNDO desenlace de la API de esta consola que no viaja por la URL —repinta, como el
	// `invalid_items` de las líneas—, y el motivo es el mismo elevado a esta puerta: la comprobación
	// corre ANTES de la primera escritura, así que no mutó nada Y no mandó nada. Este texto es solo el
	// ENCABEZADO; QUÉ líneas son lo pinta la pantalla debajo del campo, con su posición y su etiqueta,
	// porque una lista con números no cabe en una tabla código→texto.
	flashSolicitudSinPrecio = "solicitud_sin_precio"
	// flashSolicitudNoAprobable traduce el 422 `not_approvable`: la solicitud no está en un estado
	// que admita la aprobación. NO es el mismo hecho que `flashSolicitudNoEditable` —aquél es sobre
	// tocar las líneas— y el camino que se ofrece es el mismo: moverla con el desplegable.
	flashSolicitudNoAprobable = "solicitud_no_aprobable"
	// flashSolicitudMovidaSinEnviar traduce los DOS desenlaces de carrera de estas puertas: el 409
	// `intake_changed` y el 422 `invalid_transition`. Van juntos porque significan lo mismo aquí
	// —alguien la movió entre que esta pantalla la leyó y la acción llegó al compare-and-swap—, y el
	// compare-and-swap es justo lo que garantiza que la perdedora no escribió ni mandó nada.
	flashSolicitudMovidaSinEnviar = "solicitud_movida_sin_enviar"
	// flashSolicitudRechazadaSinEnviar es el 400/422 SIN clave conocida. Hoy solo lo produce un caso
	// —la solicitud no tiene ninguna línea que cotizar— y aun así NO se traduce con esas palabras: la
	// clave no viaja, y ponerle nombre a un rechazo que no se sabe cuál es sería adivinar. Lo que sí
	// se afirma es lo que el orden del cloud garantiza para cualquier 400 de estas puertas: que no
	// salió nada hacia el cliente.
	flashSolicitudRechazadaSinEnviar = "solicitud_rechazada_sin_enviar"
	// flashSolicitudEnvioIncierto es el desenlace más incómodo de esta casilla, y su texto es
	// incómodo a propósito — mismo criterio que flashDescarteIncierto y por una razón más fuerte.
	//
	// 🔴 NO PUEDE CAER EN EL GENÉRICO, que dice «Inténtalo de nuevo en un momento». Un 5xx o una
	// conexión rota dejan a esta consola SIN SABER por dónde se cortó: pudo ser antes de escribir, o
	// después de escribir y mandar el mensaje. Invitar a reintentar a ciegas es invitar a que al
	// cliente le llegue la misma cotización DOS VECES, y eso no se deshace.
	flashSolicitudEnvioIncierto = "solicitud_envio_incierto"

	// --- Desenlaces de LA SUGERENCIA DE LA RESPUESTA (T7.6) ---
	//
	// 🔴 SON CÓDIGOS PROPIOS Y NO LOS DE APROBAR, aunque dos de ellos nazcan del MISMO cuerpo del
	// cloud: los de aprobar dicen «NO se le envió nada al cliente», y aquí no había nada que enviar.
	// Esta puerta no le habla a nadie: redacta una propuesta y la deja en el campo. Un aviso que hable
	// de un envío que nunca iba a ocurrir deja a la dueña buscando un mensaje que no existe.

	// flashSugerenciaSinPlan es la capacidad `llm_intake` ausente, y lo emiten DOS caminos con el
	// mismo significado: el corte local de este handler (defensa en profundidad, antes de gastar el
	// viaje) y el 403 `feature_not_enabled` de la plataforma. Comparten código porque son el mismo
	// hecho — la misma razón por la que `regeneracion_sin_plan` viaja por los dos.
	flashSugerenciaSinPlan = "sugerencia_sin_plan"
	// flashSugerenciaSinPrecio traduce el 400 `lines_without_price`, que es el desenlace MÁS PROBABLE
	// en campo: un borrador recién interpretado no tiene precios.
	//
	// 🔑 Es el TERCER desenlace de la API de esta consola que no viaja por la URL —repinta—, y el
	// motivo es el de siempre: el cloud lo decide antes de llamar al modelo, así que no mutó nada, y
	// trae la lista con la que se corrige. QUÉ líneas son lo pinta la pantalla debajo del campo, que
	// es donde caben con su posición y su etiqueta.
	flashSugerenciaSinPrecio = "sugerencia_sin_precio"
	// flashSugerenciaSinLineas es el OTRO 400 de esta puerta: la solicitud no tiene líneas que
	// cotizar.
	//
	// 🔴 SE NOMBRA AUNQUE LA CLAVE NO VIAJE, y hay que dejar escrito el riesgo: esta puerta tiene
	// EXACTAMENTE DOS cuerpos de 400 —el de líneas sin precio, que sí trae clave, y éste— y el segundo
	// llega como un ErrInvalidInput pelado, así que el texto lo AFIRMA a partir del contrato del
	// cloud, no de lo que llegó. El día que la plataforma añada un tercer 400 con motivo, este aviso
	// dirá algo falso; la contramedida es el test que enumera los dos, que empezará a describir mal el
	// caso nuevo en cuanto alguien lo mire.
	flashSugerenciaSinLineas = "sugerencia_sin_lineas"

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

	// Los CUATRO éxitos de las acciones que no le hablan al cliente (T7.4). Son cuatro y no uno
	// porque significan cosas distintas, y la que más lo necesita es la última: la regeneración se
	// ENCARGA, no se hace, y un «listo» dejaría a la dueña recargando, viendo la interpretación
	// vieja y creyendo que falló.
	//
	// Guardar líneas y corregir la interpretación van separados por lo mismo: son dos formularios
	// sobre dos cosas distintas y la plataforma registra la segunda como corrección del dueño.
	flashSolicitudEstadoCambiado     = "solicitud_estado_cambiado"
	flashSolicitudLineasGuardadas    = "solicitud_lineas_guardadas"
	flashSolicitudCorreccionGuardada = "solicitud_correccion_guardada"
	flashRegeneracionEncargada       = "regeneracion_encargada"

	// Los DOS éxitos de las acciones que SÍ le hablan al cliente (T7.5).
	//
	// 🔴 Ninguno de los dos puede decir «enviado» a secas, y ésta es la parte del texto que hay que
	// defender: el 200 de la plataforma significa «se aplicó y quedó registrado», NUNCA «el cliente lo
	// recibió». El envío es el paso (4) del cloud y NO devuelve error a propósito —una aprobación ya
	// escrita no se deshace porque el teléfono esté apagado—, así que con la sesión de WhatsApp caída
	// esta pantalla ve el mismo 200. Prometer la entrega sería prometer algo que esta puerta no sabe.
	flashSolicitudAprobada   = "solicitud_aprobada"
	flashSolicitudInfoPedida = "solicitud_info_pedida"

	// El ÚNICO éxito de la sugerencia (T7.6). Lo pintan DOS caminos —el redirect del PRG y el repinte
	// de reserva cuando la cotización no cupo en la cookie—, y por eso es UN código y no dos: son el
	// mismo hecho contado a la misma persona, y dos textos se separarían en cuanto alguien tocara uno.
	//
	// 🔴 Lo que este texto NO puede callar es que NO SE HA ENVIADO NADA. Es la única acción de la
	// tarjeta «Responderle al cliente» que no le habla al cliente, está pegada a las dos que sí, y su
	// resultado aparece dentro del campo de aprobar: sin esa frase, leer «propuesta lista» junto a un
	// texto ya escrito en el hueco del envío se parece demasiado a haberlo mandado.
	flashSugerenciaLista = "sugerencia_lista"

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
		flashSolicitudesFiltrosInvalidos: "La plataforma rechazó los filtros. Revisa las fechas " +
			"(AAAA-MM-DD) y el estado, y vuelve a filtrar.",
		flashSolicitudesSinPlan: "El plan de tu empresa ya no incluye la bandeja de solicitudes. " +
			"No es cosa de tus permisos: habla con quien lleva la contratación.",
		flashDescarteRechazado: "La plataforma rechazó el descarte, así que NO se tocó ninguna " +
			"solicitud. Revisa lo que marcaste y vuelve a intentarlo.",
		flashDescarteIncierto: "No se pudo saber si el descarte llegó a hacerse. Mira la bandeja " +
			"ANTES de repetirlo; volver a mandar el mismo lote es seguro, porque lo que ya esté " +
			"descartado se queda como está.",

		flashSolicitudSinEstado: "Elige el estado al que quieres mover la solicitud. No se ha " +
			"cambiado nada.",
		flashSolicitudTransicionInvalida: "Desde el estado en el que está esta solicitud no se puede " +
			"pasar al que pediste, así que no se ha cambiado nada. El desplegable de abajo ofrece los " +
			"destinos que la plataforma admite ahora.",
		flashSolicitudCambiadaPorOtro: "Otra persona cambió esta solicitud mientras la mirabas, así " +
			"que no se ha guardado nada. Abajo tienes el estado actual: revísalo y vuelve a intentarlo " +
			"si sigue haciendo falta.",
		flashSolicitudFormularioIncompleto: "El formulario llegó incompleto y no se ha guardado nada. " +
			"Recarga la página e inténtalo de nuevo.",
		flashSolicitudLineaSinIdentificar: "No se pudo identificar la línea que querías quitar, así " +
			"que no se ha guardado nada. Recarga la página e inténtalo de nuevo.",
		flashSolicitudLineasIlegibles: "No se ha guardado nada: hay cantidades o precios que no se " +
			"pueden leer. Están marcados abajo, línea por línea.",
		flashSolicitudLineasRechazadas: "La plataforma rechazó alguna de las líneas y NO se guardó " +
			"nada: la corrección es todo o nada. Revisa cantidades y precios y vuelve a intentarlo.",
		flashSolicitudNoEditable: "Desde el estado en el que está esta solicitud no se corrigen sus " +
			"líneas, así que no se ha guardado nada. Muévela primero con el desplegable de estado.",
		flashRegeneracionSinPlan: "No se pidió nada: el plan de tu empresa no incluye el análisis con " +
			"IA. La bandeja se lee igual; lo que hace falta contratar es volver a interpretar.",
		flashRegeneracionSinAddon: "No se pidió nada: la vía configurada de tu empresa es la API " +
			"externa y el plan no incluye ese add-on. O se contrata, o se cambia la vía a la local " +
			"desde los ajustes de LLM.",
		flashRegeneracionSinCredencial: "No se pidió nada: el plan SÍ incluye la vía externa, pero tu " +
			"empresa no tiene credencial configurada. No hay nada que contratar — se configura en los " +
			"ajustes de LLM.",
		flashRegeneracionSinOriginal: "No se pudo regenerar: no hay texto original del cliente para " +
			"esta solicitud. El bloque de comparación de abajo dice por qué.",
		flashRegeneracionEnCurso: "Ya hay una regeneración en curso para esta solicitud, así que no " +
			"se ha encargado otra. Espera a que termine y recarga la página.",
		flashRegeneracionViaInvalida: "La plataforma rechazó la vía de interpretación. Esta pantalla " +
			"no propone ninguna: la fija la configuración de tu empresa, en los ajustes de LLM.",
		flashRegeneracionTextoLargo: "No se pidió nada: el material extra no cabe. Debajo del campo " +
			"está el tope y cuánto llevas escrito; recórtalo y vuelve a intentarlo.",

		flashSolicitudSinRespuesta: "Escribe la respuesta que quieres enviarle al cliente. NO se ha " +
			"enviado nada: esta consola no manda una cotización que no hayas leído.",
		flashSolicitudSinPregunta: "Escribe la pregunta que quieres hacerle al cliente. NO se ha " +
			"enviado nada: las que propone el sistema no se envían solas.",
		flashSolicitudSinPrecio: "NO se le envió nada al cliente: quedan líneas sin precio y la " +
			"cotización no puede salir. Están listadas debajo del texto; ponles precio en el borrador " +
			"de arriba, guarda la corrección y vuelve a aprobar.",
		flashSolicitudNoAprobable: "Esta solicitud no está en un estado que admita la aprobación, así " +
			"que NO se le envió nada al cliente. Abajo tienes en cuál está; muévela con el desplegable " +
			"de estado si todavía hace falta responderle.",
		flashSolicitudMovidaSinEnviar: "Otra persona movió esta solicitud mientras la mirabas, así que " +
			"NO se le envió nada al cliente. Abajo tienes el estado actual: revísalo y vuelve a " +
			"intentarlo si sigue haciendo falta.",
		flashSolicitudRechazadaSinEnviar: "La plataforma rechazó la petición y NO se le envió nada al " +
			"cliente. Revisa el texto y las líneas de la solicitud antes de volver a intentarlo.",
		flashSolicitudEnvioIncierto: "No se pudo saber si llegó a enviarse. MIRA ESTA SOLICITUD ANTES " +
			"DE REPETIRLO: si aparece respondida, el mensaje ya salió, y volver a mandarlo se lo " +
			"dejaría al cliente DOS VECES.",

		flashSugerenciaSinPlan: "No se pidió nada. El plan de tu empresa no incluye el análisis con " +
			"IA, así que la plataforma no puede redactar la respuesta. La solicitud se responde igual: " +
			"el campo de abajo trae la propuesta que arma esta consola con las líneas, y se edita a mano.",
		flashSugerenciaSinPrecio: "No hay nada que sugerir todavía: quedan líneas sin precio. Están " +
			"listadas debajo del texto; ponles precio en el borrador de arriba, guarda la corrección y " +
			"vuelve a pedir la propuesta. No se ha enviado nada.",
		flashSugerenciaSinLineas: "No hay nada que sugerir: esta solicitud no tiene líneas que cotizar. " +
			"Guarda primero las líneas del borrador de arriba y vuelve a pedir la propuesta. No se ha " +
			"enviado nada.",
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

		flashSolicitudEstadoCambiado: "Estado cambiado. La ficha de abajo ya enseña el estado nuevo.",
		flashSolicitudLineasGuardadas: "Líneas guardadas. La plataforma dejó constancia de la " +
			"corrección; la ficha de arriba enseña las líneas y el total que quedaron.",
		flashSolicitudCorreccionGuardada: "Corrección guardada. La plataforma la registra como " +
			"corrección tuya —no como una interpretación más— y la usa para leer mejor los próximos " +
			"pedidos parecidos.",
		flashRegeneracionEncargada: "Regeneración encargada. TODAVÍA NO ESTÁ LISTA: la plataforma la " +
			"procesa por detrás y lo que ves debajo sigue siendo la interpretación anterior. Vuelve a " +
			"abrir esta solicitud en un momento para verla.",

		flashSolicitudAprobada: "Aprobada: la plataforma registró tu respuesta y la mandó por la " +
			"sesión de esta solicitud. Que quede registrada NO garantiza que el cliente ya la tenga " +
			"delante — eso depende de que la sesión de WhatsApp esté en pie.",
		flashSolicitudInfoPedida: "Pregunta registrada y mandada por la sesión de esta solicitud. Que " +
			"quede registrada NO garantiza que el cliente ya la tenga delante; cuando conteste, su " +
			"respuesta vuelve a esta misma solicitud.",

		flashSugerenciaLista: "Propuesta lista en el campo de abajo. NO SE HA ENVIADO NADA y la " +
			"solicitud sigue donde estaba: léela, cámbiala si hace falta y aprueba tú.",
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

// flashCodeForSolicitudes es el traductor del LISTADO de la bandeja, y existe por lo mismo que
// flashCodeForSessions o flashCodeForEditor: hay desenlaces cuyo significado es propio de este plano
// y que el traductor general se comería.
//
// 🔴 EL ORDEN DE LAS DOS RAMAS ES CONTRATO, y por la razón de siempre —el específico envuelve al
// genérico—, con un matiz que conviene dejar escrito porque no se ve en el código de aquí:
// `*apiclient.FeatureNotEnabledError` DESENVUELVE a ErrForbidden (apiclient/intakes.go), así que
// preguntar antes por ErrForbidden se comería el único desenlace que manda a la contratación en vez
// de a pedir permisos, y todo seguiría verde.
func flashCodeForSolicitudes(err error) string {
	if _, ok := apiclient.FeatureNotEnabledOf(err); ok {
		return flashSolicitudesSinPlan
	}
	if errors.Is(err, apiclient.ErrInvalidInput) {
		return flashSolicitudesFiltrosInvalidos
	}
	return flashCodeFor(err)
}

// flashCodeForDescarte es el traductor del DESCARTE, y es OTRO que el del listado por UN desenlace:
// el genérico.
//
// Leer una bandeja que no contesta es «inténtalo de nuevo en un momento». Un descarte que no
// contesta NO lo es: la petición salió, cada solicitud del lote es su propia unidad de trabajo en la
// plataforma y lo que se haya escrito queda escrito. Ahí el consejo útil es mirar antes de repetir,
// y ese es un consejo que el texto genérico no da.
//
// El 400 tiene el mismo trato por el mismo motivo: sobre una operación irreversible, «revisa lo que
// escribiste» se calla lo único que importa —que no se descartó ninguna—.
func flashCodeForDescarte(err error) string {
	switch {
	case err == nil:
		return ""
	case featureAusente(err):
		return flashSolicitudesSinPlan
	case errors.Is(err, apiclient.ErrInvalidInput):
		return flashDescarteRechazado
	}
	if code := flashCodeFor(err); code != flashUpstreamUnavailable {
		return code
	}
	return flashDescarteIncierto
}

// featureAusente dice si el rechazo es el 403 de capacidad que falta.
func featureAusente(err error) bool {
	_, ok := apiclient.FeatureNotEnabledOf(err)
	return ok
}

// flashCodeForEstado es el traductor del CAMBIO DE ESTADO (T7.4).
//
// 🔴 NO delega en flashCodeForSolicitudes aunque sea la misma pantalla, y esa es la trampa que hay
// que dejar escrita: aquel traduce cualquier ErrInvalidInput como «revisa las fechas (AAAA-MM-DD) y
// el estado», porque su único 400 son los filtros del listado. Un 400 del cambio de estado no tiene
// nada que ver con unos filtros, y el usuario leería un consejo sobre un formulario que no tocó.
// Los planos comparten pantalla, no traductor.
//
// 🔴 EL ORDEN DE LAS RAMAS ES CONTRATO, y aquí son DOS los pares que dependen de él:
//   - `*FeatureNotEnabledError` desenvuelve a ErrForbidden: preguntar antes por el genérico mandaría
//     a pedir permisos en vez de a la contratación.
//   - `ErrIntakeChanged` desenvuelve a ErrConflict, que en esta consola significa «ya existe algo con
//     ese nombre». El consejo que hace falta es RECARGAR, y ése es un consejo que «ya existe» no da.
func flashCodeForEstado(err error) string {
	if err == nil {
		return ""
	}
	if featureAusente(err) {
		return flashSolicitudesSinPlan
	}
	if _, ok := apiclient.InvalidTransitionOf(err); ok {
		return flashSolicitudTransicionInvalida
	}
	if errors.Is(err, apiclient.ErrIntakeChanged) {
		return flashSolicitudCambiadaPorOtro
	}
	return flashCodeFor(err)
}

// flashCodeForLineas es el traductor de los DOS formularios de líneas: el de las facturables y el de
// la interpretación. Es UNO para los dos porque van al mismo endpoint y sus rechazos son los mismos;
// lo único que cambia entre ellos —dónde vuelve lo tecleado— no es cosa de esta tabla.
//
// El orden manda por lo mismo que arriba, con un tercer par propio: `*InvalidItemsError` y
// `*NotEditableError` desenvuelven a ErrInvalidInput, así que preguntar antes por el genérico se
// comería los dos únicos rechazos que dicen qué hacer.
func flashCodeForLineas(err error) string {
	if err == nil {
		return ""
	}
	if featureAusente(err) {
		return flashSolicitudesSinPlan
	}
	if _, ok := apiclient.InvalidItemsOf(err); ok {
		return flashSolicitudLineasRechazadas
	}
	if _, ok := apiclient.NotEditableOf(err); ok {
		return flashSolicitudNoEditable
	}
	if errors.Is(err, apiclient.ErrIntakeChanged) {
		return flashSolicitudCambiadaPorOtro
	}
	return flashCodeFor(err)
}

// flashCodeForRegeneracion es el traductor de REGENERAR, y es el que más ramas tiene de la consola
// porque es la puerta con más desenlaces nombrados: seis, y cada uno lleva a un sitio distinto.
//
// 🔴 El 403 se abre en DOS: `llm_intake` se contrata y `api_llm` es un add-on que además tiene
// alternativa (cambiar la vía a la local). Mezclarlos mandaría a comprar lo que ya se tiene. Es la
// única puerta de la bandeja donde `FeatureNotEnabledError.Feature` puede traer las dos, porque es
// la única SIN el middleware de entitlements del cloud: el 403 lo emite su propio handler.
//
// 🔴 El 403 de capacidad y el 422 de credencial tampoco se dicen igual: uno lleva a contratar y el
// otro a los ajustes.
func flashCodeForRegeneracion(err error) string {
	if err == nil {
		return ""
	}
	if missing, ok := apiclient.FeatureNotEnabledOf(err); ok {
		if missing.Feature == featureAPILLM {
			return flashRegeneracionSinAddon
		}
		return flashRegeneracionSinPlan
	}
	if _, ok := apiclient.LLMCredentialsMissingOf(err); ok {
		return flashRegeneracionSinCredencial
	}
	if _, ok := apiclient.SourceUnavailableOf(err); ok {
		return flashRegeneracionSinOriginal
	}
	if _, ok := apiclient.ReanalysisInProgressOf(err); ok {
		return flashRegeneracionEnCurso
	}
	if _, ok := apiclient.InvalidViaOf(err); ok {
		return flashRegeneracionViaInvalida
	}
	if _, ok := apiclient.TextTooLongOf(err); ok {
		return flashRegeneracionTextoLargo
	}
	return flashCodeFor(err)
}

// flashCodeForSugerencia es el traductor de LA SUGERENCIA DE LA RESPUESTA (T7.6).
//
// 🔴 ES OTRO TRADUCTOR Y NO UNA RAMA DE flashCodeForAprobar aunque comparta con él DOS desenlaces
// —el 403 de capacidad y el 400 `lines_without_price`, que son el mismo muro sobre el mismo objeto—.
// Lo que cambia no son los códigos: es que aquella puerta manda un WhatsApp y ésta no manda nada, así
// que sus textos contestan «¿se le envió algo al cliente?» y aquí esa pregunta no existe. Delegar
// dejaría avisos hablando de un envío que nunca iba a ocurrir.
//
// 🔴 EL ORDEN DE LAS RAMAS ES CONTRATO, y aquí son DOS los pares que dependen de él:
//   - `*FeatureNotEnabledError` desenvuelve a ErrForbidden: preguntar antes por el genérico mandaría
//     a pedir permisos en vez de a la contratación.
//   - `*LinesWithoutPriceError` desenvuelve a ErrInvalidInput (el 400 cae ahí, statusError):
//     preguntar antes por el genérico se comería el único rechazo que dice qué hacer, y además
//     dejaría el repintado sin disparar, porque el handler lo reconoce por el mismo tipo.
//
// 🔑 EL 403 SE ABRE EN DOS Y NO ES UN LUJO: esta ruta lleva DOS gates encadenados —`cart_basic` en el
// grupo y `llm_intake` en el handler—, y la plataforma puede rechazar por cualquiera de los dos. Sin
// la capacidad de la bandeja lo que se pierde es la pantalla entera, y decirlo con las palabras de la
// IA mandaría a contratar lo que no falta.
//
// 🔑 Y EL ErrInvalidInput SE NOMBRA en vez de caer en «revisa lo que escribiste»: aquí no hay nada
// escrito que revisar —esta puerta es un botón— y el único 400 sin clave que el cloud emite es «no
// hay líneas que cotizar». Ver flashSugerenciaSinLineas, que deja escrito el riesgo de afirmarlo.
func flashCodeForSugerencia(err error) string {
	if err == nil {
		return ""
	}
	if missing, ok := apiclient.FeatureNotEnabledOf(err); ok {
		if missing.Feature == featureCartBasic {
			return flashSolicitudesSinPlan
		}
		return flashSugerenciaSinPlan
	}
	if _, ok := apiclient.LinesWithoutPriceOf(err); ok {
		return flashSugerenciaSinPrecio
	}
	if errors.Is(err, apiclient.ErrInvalidInput) {
		return flashSugerenciaSinLineas
	}
	return flashCodeFor(err)
}

// flashCodeForAprobar es el traductor de la APROBACIÓN (T7.5), la puerta que le responde al cliente
// con la cotización.
//
// 🔴 ES OTRO TRADUCTOR Y NO UNA RAMA MÁS DE flashCodeForEstado O flashCodeForLineas, aunque los tres
// vivan en la misma pantalla y compartan tres de sus rechazos. Lo que cambia no son los códigos HTTP:
// es lo que hay que decir. Aquí sale un mensaje hacia una persona, así que cada desenlace tiene que
// contestar «¿se le envió algo al cliente?», y esa pregunta no existe en las otras cuatro acciones.
// Los planos comparten pantalla, no traductor — la frase ya estaba escrita en flashCodeForEstado.
//
// 🔴 EL ORDEN DE LAS RAMAS ES CONTRATO, y aquí son CUATRO los pares que dependen de él, más que en
// ninguna otra puerta de esta consola:
//   - `*FeatureNotEnabledError` desenvuelve a ErrForbidden: preguntar antes por el genérico mandaría
//     a pedir permisos en vez de a la contratación.
//   - `*LinesWithoutPriceError`, `*NotApprovableError` e `*InvalidTransitionError` desenvuelven LOS
//     TRES a ErrInvalidInput (el 400 y el 422 caen ahí, statusError): preguntar antes por el genérico
//     se comería los tres únicos rechazos que dicen qué hacer, y además dejaría el repintado del
//     `lines_without_price` sin disparar, porque el handler lo reconoce por el mismo tipo.
//   - `ErrIntakeChanged` desenvuelve a ErrConflict, que en esta consola significa «ya existe algo con
//     ese nombre».
//
// 🔑 EL GENÉRICO NO ES flashCodeFor. Lo que quede fuera de las ramas nombradas —un 5xx, una conexión
// cortada, un cuerpo que esta consola no conoce— sale por flashSolicitudEnvioIncierto y NO por
// «inténtalo de nuevo en un momento»: es el caso en el que no se sabe si el WhatsApp salió, y el
// consejo del genérico produciría un segundo mensaje al cliente. Mismo criterio que
// flashCodeForDescarte, sobre algo que se deshace todavía menos.
func flashCodeForAprobar(err error) string {
	if err == nil {
		return ""
	}
	if featureAusente(err) {
		return flashSolicitudesSinPlan
	}
	if _, ok := apiclient.LinesWithoutPriceOf(err); ok {
		return flashSolicitudSinPrecio
	}
	if _, ok := apiclient.NotApprovableOf(err); ok {
		return flashSolicitudNoAprobable
	}
	return flashCodeParaLoQueLeHablaAlCliente(err)
}

// flashCodeForPedirInfo es el traductor de PEDIR MÁS INFORMACIÓN (T7.5).
//
// Es OTRO que el de la aprobación y no una llamada a él, y la diferencia es real y está en el
// contrato del cloud: esta puerta NO emite `lines_without_price` —no cotiza nada, así que los precios
// no son precondición suya— ni `not_approvable` —no estrecha la máquina de estados, y su único 422 es
// el del ciclo de vida (`writeRequestInfoError`, publicapi/intakes.go)—. Delegar en el de aprobar
// dejaría dos ramas vivas que esta puerta no puede producir, y con ellas la idea de que sí.
func flashCodeForPedirInfo(err error) string {
	if err == nil {
		return ""
	}
	if featureAusente(err) {
		return flashSolicitudesSinPlan
	}
	return flashCodeParaLoQueLeHablaAlCliente(err)
}

// flashCodeParaLoQueLeHablaAlCliente traduce lo que las DOS puertas que mandan un WhatsApp comparten:
// la carrera, el rechazo sin clave y —sobre todo— el desenlace que no se sabe.
//
// Va aparte porque es justo la mitad en la que las dos coinciden y en la que equivocarse cuesta un
// mensaje repetido a una persona; tenerla dos veces garantizaría que el siguiente arreglo entrara
// solo en una de las copias.
//
// 🔴 LO QUE ESTA FUNCIÓN AFIRMA, y de dónde sale: que un 400/422 de estas dos puertas NO envió nada.
// No es una analogía —es el orden del cloud, donde las validaciones van todas antes de la primera
// escritura y el envío es el último paso (intakes/approve.go y intakes/requestinfo.go)—. El 401 se
// deja pasar tal cual porque su desenlace no es un aviso sino una expulsión: lo mira sessionIsDead
// antes de llegar aquí.
func flashCodeParaLoQueLeHablaAlCliente(err error) string {
	if _, ok := apiclient.InvalidTransitionOf(err); ok {
		return flashSolicitudMovidaSinEnviar
	}
	if errors.Is(err, apiclient.ErrIntakeChanged) {
		return flashSolicitudMovidaSinEnviar
	}
	if errors.Is(err, apiclient.ErrInvalidInput) {
		return flashSolicitudRechazadaSinEnviar
	}
	// El resto sale por el traductor de la casa SALVO su genérico: un desenlace que esta consola no
	// sabe leer es exactamente el caso en el que no puede afirmar que no se envió nada.
	if code := flashCodeFor(err); code != flashUpstreamUnavailable {
		return code
	}
	return flashSolicitudEnvioIncierto
}
