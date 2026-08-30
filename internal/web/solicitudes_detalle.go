package web

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
)

// solicitudes_detalle.go es EL DETALLE de UNA solicitud (Plan 047 · T7.3), la casa nueva de
// `ShowIntakeDetail` + `intake-detail.html` de wapp-guardian-bff. Es la pantalla más grande del
// ecosistema: la ficha, la comparación original ↔ interpretado, el borrador, las acciones que le
// hablan al cliente, el cambio de estado y la corrección de líneas.
//
// 📌 ESTA CASILLA ES LA LECTURA. Los formularios se pintan enteros —son parte del HTML de la
// pantalla— pero sus handlers POST llegan después. Desde T7.4 están registrados los CUATRO que no le
// hablan al cliente (estado, líneas, corregir y regenerar, cada uno en el fichero de su tema);
// siguen fuera los tres que sí le hablan o cuestan una inferencia: aprobar y pedir información
// (T7.5) y sugerir la respuesta (T7.6).
//
// 🔑 EL REPINTADO DE D-047.16 ENTRA POR AQUÍ, y por eso detalleRender creció. Un rechazo de
// validación LOCAL no redirige: vuelve a esta misma función con lo tecleado dentro y un 400. Lo que
// eso implica y conviene saber antes de añadir el siguiente: el repintado RELEE el detalle de la API
// —un GET más en el camino malo— porque la pantalla es 90 % datos de la plataforma y solo el 10 %
// tecleado; reconstruirla sin releer obligaría a acarrear el detalle entero por el POST.
//
// Diferencias declaradas contra el origen, ninguna accidental:
//
//  1. 🔒 EL DESENLACE MALO NO PINTA UN DETALLE VACÍO: MANDA A LA BANDEJA con el aviso (303 + flash).
//     Es el precedente de esta casa, escrito en ShowFlowDetail: «un flujo que no se puede abrir manda
//     a la LISTA, no a un detalle vacío: ahí están los que sí existen». El BFF respondía 404/403/502
//     con la página pintada y un párrafo que decía «No se pudo mostrar esta solicitud», que es una
//     pantalla sin nada que hacer en ella. El 403 del plan lo emite ahora el gate de la ruta, que es
//     donde significa algo.
//  2. las fechas se LEEN (ver formato.go). El BFF las pinta crudas —`2026-08-20T10:00:00Z`— con un
//     «(UTC)» escrito a mano al lado.
//  3. la vista del plan la SIEMBRA el gate y aquí se REUTILIZA: el BFF la resolvía otra vez dentro
//     del render.

const (
	// rutaSolicitudDetalle es el sufijo con el que el detalle se registra DENTRO del grupo. El verbo
	// estático `descartar` va antes que este parámetro, que es la regla de esta casa (ver server.go).
	rutaSolicitudDetalle = "/:id"

	plantillaSolicitud = "solicitud.html"
	tituloSolicitud    = "Solicitud"
)

// Los SUFIJOS de las siete acciones de esta pantalla, colgando de /solicitudes/{id}.
//
// 🔑 Se declaran AQUÍ y no en la plantilla aunque hoy no los registre nadie, y ése es justo el punto:
// el `action` de cada formulario se compone en Go (ver urlsDeSolicitud) y las casillas que traigan los
// handlers registran la ruta CON ESTA MISMA CONSTANTE. Escritos como literal en el HTML serían siete
// cadenas que no compila nadie, y un formulario apuntando a una ruta que el router escribe distinto
// da un 404 que ningún gate ve venir.
//
// Están en español como el resto de la superficie de esta consola (la ruta es superficie; lo que
// viaja por el cable —los nombres de campo— no se traduce, ver solicitudes_handler.go).
const (
	sufijoEstado    = "/estado"
	sufijoLineas    = "/lineas"
	sufijoCorregir  = "/corregir"
	sufijoAprobar   = "/aprobar"
	sufijoPedirInfo = "/pedir-info"
	sufijoRegenerar = "/regenerar"
	sufijoSugerir   = "/sugerir-respuesta"
)

// urlsDeSolicitud son los destinos de los formularios de esta pantalla, ya compuestos y con el
// identificador escapado. Se arman en Go por lo mismo que los sufijos: la plantilla no puede escapar
// un segmento de ruta y no debería saber cómo se compone una.
type urlsDeSolicitud struct {
	Estado    string
	Lineas    string
	Corregir  string
	Aprobar   string
	PedirInfo string
	Regenerar string
	Sugerir   string
}

func urlsDe(solicitudID string) urlsDeSolicitud {
	base := solicitudURL(solicitudID)
	return urlsDeSolicitud{
		Estado:    base + sufijoEstado,
		Lineas:    base + sufijoLineas,
		Corregir:  base + sufijoCorregir,
		Aprobar:   base + sufijoAprobar,
		PedirInfo: base + sufijoPedirInfo,
		Regenerar: base + sufijoRegenerar,
		Sugerir:   base + sufijoSugerir,
	}
}

// solicitudDetalleView es el detalle entero tal como lo pinta la plantilla.
//
// Los destinos del desplegable NO se calculan aquí: el ciclo de vida (D-041.10) vive en la plataforma
// y replicarlo garantizaría que las dos copias divergieran. Por eso la pantalla solo ofrece lo que la
// plataforma dice, y cuando la plataforma no lo dice, lo declara en vez de adivinar (ver
// transicionesDe).
type solicitudDetalleView struct {
	Detalle *apiclient.IntakeDetail
	// Transiciones son los destinos ofrecidos en el desplegable (vacío ⇒ no se ofrece ninguno).
	Transiciones []string
	// Conocidas distingue «este estado es terminal» de «la plataforma no informa de los destinos».
	Conocidas bool
	// DesdeRechazo avisa de que los destinos salieron del rechazo 422 de un intento previo, no del
	// detalle: es información válida —misma fuente de verdad, el dominio— pero llega tarde y conviene
	// que se note.
	//
	// 🔴 HOY NO LO PRODUCE NADIE, Y YA NO ES «FALTA LA CASILLA SIGUIENTE»: es la consecuencia medida
	// de D-047.16. T7.3 lo dejó anotado esperando al POST de estado; ese POST llegó en T7.4 y NO
	// puede rellenarlo, porque el 422 que trae los destinos es un desenlace de la API y sale por 303
	// —y un 303 no lleva una lista consigo—. Tras el redirect los destinos se vuelven a pedir con el
	// GET (`allowed_transitions`), que es la MISMA autoridad y está más fresca; el único caso en que
	// se pierde algo es una plataforma que no publique el campo en el detalle pero sí en el rechazo,
	// y ahí la pantalla ya dice en voz alta que no los conoce.
	//
	// Se conserva la rama en vez de borrarla porque el día que se quiera rescatar el 422 —con una
	// cookie de un solo uso, que es el mecanismo que esta casa ya tiene— este es el sitio donde
	// entra. Fleco con dueño; no es de esta casilla.
	DesdeRechazo bool
	// Lineas es el formulario de corrección de las líneas facturables (Plan 041 · T4.10).
	Lineas *formularioLineas
	// Borrador es el §7.5, que sale de la última revisión `interpreted` y NO de `Items`. Nil cuando la
	// solicitud no tiene ninguna: entonces la pantalla enseña las líneas de la ficha y dice que no hay
	// interpretación, en vez de fingir un borrador vacío.
	Borrador *borradorView
	// Acciones son las que le hablan al cliente. Nil cuando el estado no las admite.
	Acciones *accionesView
	// Comparacion es el §7.6: el texto del cliente al lado de lo que se entendió, la navegación por
	// las interpretaciones y el botón de regenerar. Nil cuando no hay ninguna revisión `interpreted`.
	//
	// Se pinta sobre la revisión SELECCIONADA por la query, que no tiene por qué ser la última;
	// `Borrador` sigue saliendo siempre de la última, porque es lo que se corrige.
	Comparacion *comparacionView
	// HorasSinResponder es el plazo tras el que la plataforma marca `overdue`, para poder redactarlo.
	HorasSinResponder int
	// URLs son los destinos de los siete formularios, ya compuestos (ver urlsDeSolicitud).
	URLs urlsDeSolicitud
}

// detalleRender son las variables con las que se pinta el detalle.
//
// Va como struct y no como argumentos sueltos por lo mismo que solicitudesRender: las casillas
// siguientes le añaden lo que cada POST necesita devolver a la pantalla —lo tecleado, los defectos,
// los destinos de un 422— y con una lista de parámetros eso obligaría a tocar todos los sitios.
type detalleRender struct {
	// id es la solicitud a pintar.
	id string
	// revision es la interpretación que se está mirando en la comparación (0 ⇒ la última). Viaja por
	// la QUERY y no por estado de servidor: sin JavaScript (ADR-0035), saltar de una revisión a otra
	// es un enlace a esta misma página y el «después» lo pinta el servidor.
	revision int

	// --- Lo que solo llena un REPINTADO de D-047.16 (T7.4) ---

	// status es el código de respuesta. Cero ⇒ 200. Lo pone en 400 el rechazo de validación local, y
	// el 400 es la mitad de la decisión: con 200 el navegador no distinguiría el rechazo.
	status int
	// code es el flash del rechazo, que se pinta arriba de la pantalla. Pisa al `?error=` de la URL
	// porque en un repintado no hay URL de la que venir.
	code string
	// filas y defectos son lo tecleado en el formulario de LÍNEAS FACTURABLES y lo que se le objeta.
	filas    []filaLinea
	defectos []defectoLinea
	// filasBorrador y defectosBorrador son lo mismo para el formulario del BORRADOR.
	//
	// 🔴 SON DOS PARES Y NO UNO, y confundirlos es el error caro de esta pantalla: son dos
	// formularios distintos sobre dos representaciones distintas de las líneas —uno edita `items`,
	// las facturables; el otro edita la interpretación, que es la única que tiene la línea sin
	// match—. Volcar lo tecleado en uno dentro del otro pondría los precios en filas ajenas.
	filasBorrador    []filaLinea
	defectosBorrador []defectoLinea
	// textoRegenerar es el material extra tecleado, para devolverlo al textarea.
	textoRegenerar string
	// runasRegenerar es cuántas runas trae ese material cuando se pasó del tope (0 ⇒ no se pasó). Es
	// el número que el catálogo de flash no puede interpolar, así que viaja como vista — mismo
	// criterio que los avisos con números del descarte.
	runasRegenerar int
}

// ShowSolicitudDetalle pinta una solicitud con sus líneas, su interpretación y sus acciones (T7.3).
func (h *AdminHandler) ShowSolicitudDetalle(c *gin.Context) {
	h.renderSolicitudDetalle(c, detalleRender{
		id:       strings.TrimSpace(c.Param("id")),
		revision: revisionDeLaQuery(c),
	})
}

// renderSolicitudDetalle pinta el detalle que relee de la API.
func (h *AdminHandler) renderSolicitudDetalle(c *gin.Context, r detalleRender) {
	// 🔴 EL LITERAL DEL CLIENTE NO SE CACHEA EN CLARO. Ésta es la única página de esta consola que
	// pinta el texto que una persona escribió por WhatsApp (§7.6), y llega YA DESCIFRADO del cloud
	// —esta consola no tiene KEK—: sin esta cabecera, un proxy intermedio o el disco del navegador se
	// quedarían con una copia en claro que nadie volvería a mirar.
	//
	// Va INCONDICIONAL y AL PRINCIPIO, no colgando de la rama que pinta el literal: una cabecera que
	// depende de por dónde salga la función es una cabecera que un día no sale. Y por eso cubre
	// también las salidas tempranas —sin empresa, y el 303 a la bandeja—, que son justo las que se
	// olvidarían.
	c.Header("Cache-Control", "no-store")

	// Sin empresa no hay solicitud que leer, y la API respondería 403 —«no tienes permiso»—, que es un
	// diagnóstico falso: no le falta un permiso, le falta una empresa. Ver sinEmpresa().
	if sinEmpresa(c) {
		renderer.HTML(c, http.StatusOK, plantillaSolicitud, h.pageData(c, tituloSolicitud))
		return
	}

	// Un id vacío no lo produce el router —Gin manda `/solicitudes/` a `/solicitudes`—, así que esto
	// es una guarda y no un camino: lo que evita es gastar un viaje a la API para recibir el 404 que
	// ya se sabe.
	if r.id == "" {
		c.Redirect(http.StatusSeeOther, rutaSolicitudes)
		return
	}

	var detalle *apiclient.IntakeDetail
	var getErr error
	code := flashCodeForSolicitudes(h.auth.withAuthRetry(c, func(accessToken string) error {
		var err error
		detalle, err = h.api.Intakes.GetIntake(c.Request.Context(), accessToken, r.id)
		getErr = err
		return err
	}))
	if sessionIsDead(getErr) {
		h.auth.expireSession(c)
		return
	}
	if code != "" {
		// 🔑 UNA SOLICITUD QUE NO SE PUEDE ABRIR MANDA A LA BANDEJA, no a un detalle vacío: allí están
		// las que sí existen, y el aviso explica cuál de los tres casos fue —frontera de empresa, plan
		// o upstream—. Es el mismo criterio que ShowFlowDetail, y la diferencia con el BFF, que
		// pintaba una página con un párrafo y ningún camino de salida.
		//
		// 📌 En un REPINTADO esto se lleva por delante lo tecleado, y se acepta: si la solicitud ya no
		// se puede leer, la pantalla en la que se estaba escribiendo tampoco se puede pintar. El aviso
		// que se lee entonces es el de la lectura, que es el que dice qué pasó de verdad.
		slog.Warn("no se pudo leer la solicitud", "codigo", code, "error", getErr)
		redirectWith(c, rutaSolicitudes, code, "")
		return
	}

	// 🔑 La vista del plan la SEMBRÓ el gate (solicitudes_gate.go) y aquí se REUTILIZA: sin esto se
	// pagarían DOS llamadas a /entitlements por petición, y esta consola resuelve el plan SIN CACHÉ.
	ent := entitlementsFromContext(c)

	vista := transicionesDe(detalle, nil)
	vista.Lineas = formularioLineasDe(detalle, vista.Transiciones)
	// Lo TECLEADO gana sobre lo que dice la plataforma, y solo en el formulario del que vino: es la
	// mitad de D-047.16 que el 400 por sí solo no cumple. Un repintado con las filas de la API
	// respondería 400 «de palabra» y le habría borrado a la dueña lo que acababa de escribir.
	vista.Lineas.conLoTecleado(r.filas, r.defectos)
	vista.Borrador = borradorDe(detalle)
	vista.Borrador.conLoTecleado(r.filasBorrador, r.defectosBorrador)
	// La espera que se anuncia es la que esta consola cumple HOY: el plazo del grupo
	// (RequestDeadline(cfg.UpstreamTimeout), server.go), porque la ruta de la sugerencia todavía no
	// tiene uno propio. Cuando T7.6 traiga el plazo por ruta —el BFF le da 58 s a esa sola—, éste es
	// el ÚNICO sitio que cambia, y el texto de la pantalla cambia con él sin tocar la plantilla.
	vista.Acciones = accionesDe(detalle, vista.Borrador, ent, h.cfg.UpstreamTimeout)
	vista.Comparacion = comparacionDe(detalle, ent, r.revision)
	vista.conLoTecleadoEnRegenerar(r.textoRegenerar, r.runasRegenerar)
	vista.HorasSinResponder = horasSinResponder
	vista.URLs = urlsDe(detalle.ID)

	data := h.pageData(c, tituloSolicitud)
	data["Solicitud"] = vista
	if r.code != "" {
		// Pisa al `?error=` que pageData saca de la URL: en un repintado no se viene de ninguna URL,
		// y el aviso del rechazo es el único que hay.
		data["Error"] = flashError(r.code)
	}
	renderer.HTML(c, statusODoscientos(r.status), plantillaSolicitud, data)
}

// statusODoscientos es el 200 por defecto del detalle. Existe para que los cuatro POST no tengan que
// escribir `status: http.StatusOK` en su camino bueno —que no pasa por aquí— ni el GET tenga que
// rellenar un campo que solo usa el repintado.
func statusODoscientos(status int) int {
	if status == 0 {
		return http.StatusOK
	}
	return status
}

// conLoTecleadoEnRegenerar devuelve al textarea el material extra y, si se pasó del tope, cuánto.
//
// Va sobre la vista y no dentro de regenerarDe porque regenerarDe decide si el botón se puede
// PULSAR, que es una pregunta sobre el plan y el original; esto es el camino de vuelta de un
// rechazo. La comparación puede ser nil (una solicitud sin ninguna interpretación), y entonces no
// hay textarea al que devolver nada.
func (v *solicitudDetalleView) conLoTecleadoEnRegenerar(texto string, runas int) {
	if v.Comparacion == nil || (texto == "" && runas == 0) {
		return
	}
	v.Comparacion.Regenerar.Texto = texto
	v.Comparacion.Regenerar.Runas = runas
}

// revisionDeLaQuery lee qué interpretación se está mirando.
//
// 🔴 Un valor ilegible o menor que 1 vale lo mismo que no mandar nada —se mira la última— y NO
// rechaza la página: un enlace tecleado a mano no puede dejar a la dueña sin su solicitud. Que el
// número no exista lo dice la comparación, con su propio aviso.
func revisionDeLaQuery(c *gin.Context) int {
	revision, err := strconv.Atoi(strings.TrimSpace(c.Query("revision")))
	if err != nil || revision < 1 {
		return 0
	}
	return revision
}

// transicionesDe decide qué destinos ofrece el desplegable, por orden de fiabilidad:
//
//  1. los que publica el detalle (`allowed_transitions`) — la fuente buena: sale de la máquina de
//     estados de la plataforma en el momento de leer la solicitud;
//  2. si el detalle NO trae el campo, los que devolvió el 422 de un intento previo — misma autoridad,
//     solo que llega tarde, y que llegó tarde se DICE (DesdeRechazo);
//  3. si no hay ninguno de los dos, NINGUNO, y la pantalla lo declara.
//
// Lo que nunca ocurre es el paso 4 que sería cómodo: deducirlos de una tabla propia. Esta consola no
// conoce el ciclo de vida y no debe fingir que sí.
//
// 🔴 Los tres casos salen del PUNTERO de `AllowedTransitions`, y por eso el campo es puntero: `nil`
// —la plataforma no manda el campo— es «no se sabe», y la lista VACÍA es «terminal». Colapsarlos en
// un slice haría que un servidor viejo se leyera como un estado final.
func transicionesDe(detalle *apiclient.IntakeDetail, desdeRechazo []string) solicitudDetalleView {
	view := solicitudDetalleView{Detalle: detalle}
	switch {
	case detalle.AllowedTransitions != nil:
		view.Transiciones = *detalle.AllowedTransitions
		view.Conocidas = true
	case len(desdeRechazo) > 0:
		view.Transiciones = desdeRechazo
		view.Conocidas = true
		view.DesdeRechazo = true
	}
	return view
}
