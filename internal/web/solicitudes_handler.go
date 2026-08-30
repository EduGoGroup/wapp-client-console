package web

import (
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
)

// solicitudes_handler.go sirve la BANDEJA: lo que los clientes han pedido por WhatsApp (Plan 047 ·
// T7.2). Es la casa nueva de `intakes_handler.go` + `intakes.html` de wapp-guardian-bff.
//
// 🔑 LA RUTA VA EN ESPAÑOL —`/solicitudes`—, como decidió la Ola 6 con `/flujos` y `/disparadores`.
// Lo que NO se traduce es lo que viaja por el cable: la query de la API sigue siendo `page`,
// `page_size`, `status`, `session`, y los campos del formulario de descarte siguen siendo
// `intake_id` y `action`, porque eso es lo que la plataforma lee. La traducción es de SUPERFICIE.
//
// Diferencias declaradas contra el origen, ninguna accidental:
//
//  1. 🔒 EL GATE DE `cart_basic` PASA A SER DE RUTA (ver solicitudes_gate.go). En el BFF el GET
//     respondía 200 sin la feature —pintaba la página entera sin el bloque— y solo los POST daban
//     403, cada uno con su `if` copiado. Aquí corta el middleware del grupo, antes de renderizar, y
//     responde 403 en GET y en POST.
//  2. el DESENLACE MALO DEL LISTADO degrada con 200 y el aviso arriba, como el resto de esta casa
//     (ShowFlows, ShowSessions). El BFF respondía 400/403/502 con la página pintada, que es un
//     código de estado que nadie mira y una pantalla que sí. El 403 de esta pantalla lo emite ahora
//     el gate, que es donde significa algo.
//  3. los textos salen del CATÁLOGO DE FLASH y nunca del upstream. El BFF llevaba el cuerpo en prosa
//     de la plataforma hasta la pantalla («La plataforma rechazó los filtros: …»).
//
// Lo que se muda TAL CUAL, porque es contrato y no descuido: el paginador resuelto en Go, que los
// filtros viajen en la query (y no en estado de servidor), el orden `oldest` fijo, la saturación de
// `page_size` a 200 y el aparato entero del descarte (ver solicitudes_descarte.go).

const (
	rutaSolicitudes = "/solicitudes"
	// rutaDescartarSufijo es el verbo estático del descarte DENTRO del grupo, y es lo que se
	// registra (ver server.go). Cuelga de /solicitudes y NO de un `:id` porque la operación es sobre
	// VARIAS solicitudes: ninguna es «el recurso de la URL». La API pública tiene la misma forma
	// (`POST /api/v1/intakes/discard`).
	rutaDescartarSufijo = "/descartar"
	// rutaSolicitudesDescartar es la ruta COMPLETA, y se DERIVA del sufijo a propósito: es la que
	// arma el `action` de los dos formularios, y una segunda copia escrita a mano sería un literal
	// que puede dejar de coincidir con lo que el router sirve sin que nada falle.
	rutaSolicitudesDescartar = rutaSolicitudes + rutaDescartarSufijo

	plantillaSolicitudes = "solicitudes.html"
	tituloSolicitudes    = "Solicitudes"
)

const (
	// solicitudesPorPagina es el tamaño de página del listado. Coincide con el default de la API
	// (50) y se manda EXPLÍCITO para que la URL diga siempre en qué página está quien mira.
	solicitudesPorPagina = 50
	// maxSolicitudesPorPagina es el techo que acepta la API. Se satura aquí para no gastar un viaje
	// en una petición que la plataforma va a rechazar.
	maxSolicitudesPorPagina = 200
	// horasSinResponder es el plazo del que habla la marca `overdue` que publica la plataforma. Solo
	// se usa para redactar el aviso: el cálculo lo hace el cloud y esta pantalla no lo repite.
	horasSinResponder = 24
)

// avisoSolicitudes es el aviso CON NÚMEROS de esta pantalla, y es la única cosa de esta consola cuyo
// texto no sale del catálogo de flash.
//
// 🔴 No es un descuido y conviene decir por qué: el catálogo traduce CÓDIGOS a textos fijos y no
// interpola datos (flash.go lo dice en tres sitios). Los desenlaces del descarte son «se descartaron
// 3 de 5» y «marcaste 250 y caben 200»: sin los números no dicen nada, y con ellos no caben en una
// tabla código→texto. Por eso viajan como vista y se pintan en la página, no como `?error=` en la
// URL. Todo lo demás de esta pantalla —los desenlaces de la API— sí sale del catálogo.
type avisoSolicitudes struct {
	Exito   bool
	Mensaje string
}

// solicitudesFiltroView son los filtros tal como vuelven al formulario: se repintan siempre para que
// quien mira vea con qué criterios está mirando la bandeja.
type solicitudesFiltroView struct {
	Desde  string
	Hasta  string
	Estado string
	Sesion string
}

// solicitudesPaginaView es el paginador YA RESUELTO.
//
// 🔴 Las cuentas se hacen en Go y no en la plantilla, y las URLs de anterior/siguiente ARRASTRAN los
// filtros vigentes: cambiar de página no puede cambiar en silencio lo que se está mirando. Es la
// pieza que el BFF ya tenía resuelta y que esta consola no tenía en absoluto —hasta esta casilla no
// había una sola línea de paginación en todo el repo—.
type solicitudesPaginaView struct {
	Pagina    int
	PorPagina int
	Total     int
	Paginas   int
	// DesdeFila y HastaFila son el rango humano («1–5 de 7»). Quedan en cero cuando la página no
	// trajo ninguna fila: un «1–0 de 7» sería peor que no decir nada.
	DesdeFila int
	HastaFila int

	HayAnterior  bool
	HaySiguiente bool
	AnteriorURL  string
	SiguienteURL string
}

// solicitudesRender son las variables con las que se pinta la bandeja. Va como struct y no como
// argumentos sueltos por lo mismo que triggerFormView: la mitad son opcionales, y una llamada con
// dos `nil` seguidos no dice cuál es cuál.
type solicitudesRender struct {
	// status es el código con el que se responde. La bandeja se pinta igual: el 400 del descarte es
	// el repintado de D-047.16, no una pantalla distinta.
	status int
	// filtro manda tanto en la consulta como en lo que se repinta.
	filtro apiclient.IntakeFilter
	// aviso es el desenlace CON NÚMEROS de la operación que trajo hasta aquí (ver avisoSolicitudes).
	aviso *avisoSolicitudes
	// descarte es el lote que espera confirmación o el que ya se ejecutó. Nunca los dos.
	descarte *descarteView
}

// ShowSolicitudes pinta la bandeja de la empresa con los filtros de la query (T7.2).
func (h *AdminHandler) ShowSolicitudes(c *gin.Context) {
	h.renderSolicitudes(c, solicitudesRender{status: http.StatusOK, filtro: filtroDeLaQuery(c)})
}

// renderSolicitudes pinta la bandeja: el listado con sus filtros y su paginador y, si la operación
// que trajo hasta aquí lo pide, el descarte por lotes.
//
// DEGRADA en vez de tumbar, igual que las otras pantallas de esta casa: si el listado no se puede
// leer, la pantalla sigue en pie con el aviso arriba y el formulario de filtros intacto. La
// excepción es el 401 que sobrevivió al refresco: ahí la sesión ya no vale y lo que toca es expulsar.
func (h *AdminHandler) renderSolicitudes(c *gin.Context, r solicitudesRender) {
	// Sin empresa no hay bandeja que listar, y la API respondería 403 —«no tienes permiso»—, que es
	// un diagnóstico falso: no le falta un permiso, le falta una empresa. Ver sinEmpresa().
	if sinEmpresa(c) {
		renderer.HTML(c, http.StatusOK, plantillaSolicitudes, h.pageData(c, tituloSolicitudes))
		return
	}

	data := h.pageData(c, tituloSolicitudes)
	// 🔑 La vista del plan la SEMBRÓ el gate (solicitudes_gate.go) y aquí se REUTILIZA: sin esto se
	// pagarían DOS llamadas a /entitlements por petición, una por el gate y otra por la pantalla.
	data[entitlementsDataKey] = entitlementsFromContext(c)
	data["Filtro"] = filtroView(r.filtro)
	data["EstadoOptions"] = estadosDeSolicitud
	data["Aviso"] = r.aviso
	data["Descarte"] = r.descarte
	data["DescartarURL"] = descartarURL(r.filtro)
	data["HorasSinResponder"] = horasSinResponder

	var pagina *apiclient.IntakePage
	var listErr error
	code := flashCodeForSolicitudes(h.auth.withAuthRetry(c, func(accessToken string) error {
		var err error
		pagina, err = h.api.Intakes.ListIntakes(c.Request.Context(), accessToken, r.filtro)
		listErr = err
		return err
	}))
	if sessionIsDead(listErr) {
		h.auth.expireSession(c)
		return
	}
	if code != "" {
		slog.Warn("no se pudo leer la bandeja de solicitudes (modo degradado)", "codigo", code, "error", listErr)
		// El aviso del listado NO pisa al que traía la petición (mismo criterio que renderTriggers):
		// quien acaba de recibir el desenlace de su descarte necesita leer ESE, y que la tabla no
		// cargara ya se ve en su propio hueco.
		if data["Error"] == "" {
			data["Error"] = flashError(code)
		}
		data["SolicitudesError"] = true
		// 🔴 Sin la bandeja delante NO se ofrece confirmar. El desglose de un lote YA ejecutado sí se
		// conserva —es lo que pasó, y ocultarlo dejaría a la dueña sin saber qué se descartó—, pero
		// confirmar a ciegas es exactamente lo que esta pantalla no puede hacer: enseñar qué se va a
		// matar es la condición de una acción sin vuelta atrás (D-041.22).
		if r.descarte != nil && r.descarte.Confirmando {
			data["Descarte"] = nil
		}
		renderer.HTML(c, r.status, plantillaSolicitudes, data)
		return
	}

	if r.descarte != nil {
		r.descarte.describirCon(pagina.Intakes)
	}
	data["Solicitudes"] = pagina.Intakes
	data["Pager"] = paginadorView(r.filtro, pagina)
	renderer.HTML(c, r.status, plantillaSolicitudes, data)
}

// filtroDeLaQuery lee filtros y paginación de la query string.
//
// NO valida los valores: quien manda sobre qué es una fecha o un estado válido es la API, y duplicar
// aquí esa validación significaría mantener dos criterios que acabarían discrepando. El único saneo
// es el de la paginación, que evita pedir una página imposible, y la saturación del tamaño al techo
// que la API acepta.
func filtroDeLaQuery(c *gin.Context) apiclient.IntakeFilter {
	pagina, err := strconv.Atoi(strings.TrimSpace(c.Query("page")))
	if err != nil || pagina < 1 {
		pagina = 1
	}
	tam, err := strconv.Atoi(strings.TrimSpace(c.Query("page_size")))
	if err != nil || tam < 1 {
		tam = solicitudesPorPagina
	}
	if tam > maxSolicitudesPorPagina {
		tam = maxSolicitudesPorPagina
	}
	return apiclient.IntakeFilter{
		From:    strings.TrimSpace(c.Query("from")),
		To:      strings.TrimSpace(c.Query("to")),
		Status:  strings.TrimSpace(c.Query("status")),
		Session: strings.TrimSpace(c.Query("session")),
		// 🔴 El orden NO es un filtro que se elija: esta bandeja pide SIEMPRE lo más antiguo primero
		// (D-044.47 §2). Lo que lleva más tiempo esperando es lo que hay que atender, y con el
		// default de la API —lo más reciente arriba— eso queda al final de la última página, que es
		// donde nadie mira. Se manda explícito en vez de confiar en el default por lo mismo que el
		// `page_size`: la petición dice lo que quiere, y la traza lo enseña.
		Sort:     apiclient.IntakeSortOldest,
		Page:     pagina,
		PageSize: tam,
	}
}

// filtroView devuelve los filtros al formulario.
func filtroView(f apiclient.IntakeFilter) solicitudesFiltroView {
	return solicitudesFiltroView{Desde: f.From, Hasta: f.To, Estado: f.Status, Sesion: f.Session}
}

// paginadorView resuelve el paginador con lo que respondió la API.
//
// El total y el tamaño los fija el SERVIDOR —puede acotar el `page_size` pedido—, así que las
// cuentas salen de la respuesta y no de lo que se pidió. Es la diferencia entre un paginador que
// dice la verdad y uno que repite lo que le gustaría.
func paginadorView(f apiclient.IntakeFilter, p *apiclient.IntakePage) solicitudesPaginaView {
	tam := p.PageSize
	if tam <= 0 {
		tam = solicitudesPorPagina
	}
	actual := p.Page
	if actual <= 0 {
		actual = 1
	}

	paginas := p.Total / tam
	if p.Total%tam != 0 {
		paginas++
	}

	view := solicitudesPaginaView{
		Pagina: actual, PorPagina: tam, Total: p.Total, Paginas: paginas,
		HayAnterior:  actual > 1,
		HaySiguiente: actual*tam < p.Total,
	}
	if len(p.Intakes) > 0 {
		view.DesdeFila = (actual-1)*tam + 1
		view.HastaFila = view.DesdeFila + len(p.Intakes) - 1
	}
	if view.HayAnterior {
		view.AnteriorURL = solicitudesURL(f, actual-1)
	}
	if view.HaySiguiente {
		view.SiguienteURL = solicitudesURL(f, actual+1)
	}
	return view
}

// solicitudesURL arma el enlace a una página conservando los filtros vigentes.
func solicitudesURL(f apiclient.IntakeFilter, pagina int) string {
	return solicitudFiltradaURL(rutaSolicitudes, f, pagina)
}

// descartarURL es la ruta a la que apuntan los dos formularios del descarte, con los filtros
// vigentes EN LA QUERY.
//
// Van en la URL y no en campos ocultos a propósito: así el POST se lee con el MISMO filtroDeLaQuery
// que el GET —una sola forma de saber qué bandeja se está mirando— y el repintado tras descartar cae
// exactamente en la página desde la que se descartó.
func descartarURL(f apiclient.IntakeFilter) string {
	pagina := f.Page
	if pagina < 1 {
		pagina = 1
	}
	return solicitudFiltradaURL(rutaSolicitudesDescartar, f, pagina)
}

// solicitudFiltradaURL arma una URL de la bandeja conservando los filtros vigentes.
//
// Es el ÚNICO sitio donde se decide qué filtros sobreviven a una navegación: si el paginador y el
// descarte contaran cada uno por su cuenta, descartar podría devolver a una bandeja distinta de la
// que se estaba mirando.
func solicitudFiltradaURL(path string, f apiclient.IntakeFilter, pagina int) string {
	q := url.Values{}
	for clave, valor := range map[string]string{
		"from": f.From, "to": f.To, "status": f.Status, "session": f.Session,
	} {
		if valor != "" {
			q.Set(clave, valor)
		}
	}
	q.Set("page", strconv.Itoa(pagina))
	if f.PageSize > 0 && f.PageSize != solicitudesPorPagina {
		q.Set("page_size", strconv.Itoa(f.PageSize))
	}
	return path + "?" + q.Encode()
}

// redirigirALaBandeja es el 303 + flash de esta pantalla, y NO usa redirectWith a propósito.
//
// 🔴 El motivo es concreto: redirectWith concatena `path + "?error=" + code`, y aquí el destino YA
// LLEVA QUERY —los filtros vigentes—, así que esa concatenación produciría
// `/solicitudes?page=2?error=x`, que el navegador manda tal cual y que la pantalla de destino lee
// como una página ilegible. El código de flash entra por el mismo url.Values que los filtros.
func redirigirALaBandeja(c *gin.Context, f apiclient.IntakeFilter, errCode string) {
	destino := solicitudesURL(f, f.Page)
	if errCode != "" {
		destino += "&error=" + url.QueryEscape(errCode)
	}
	c.Redirect(http.StatusSeeOther, destino)
}
