package web

import (
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
)

// solicitudes_lineas.go es el FORMULARIO DE CORRECCIÓN de las líneas facturables (Plan 041 · T4.10),
// mudado de las VISTAS de `intakes_edit.go` del BFF (Plan 047 · T7.3).
//
// Desde T7.4 están también los DOS handlers que guardan, y el camino de vuelta entero: leer las
// filas, leer los números como los teclea una persona, traducir los defectos y devolverlo todo al
// formulario del que vino.
//
// 📌 T7.3 anotó aquí que ese camino de vuelta era de T7.5. Se midió al traerlo y NO lo es: el plan
// reparte por ACCIÓN —«items» y «correct» están en T7.4— y las dos comparten exactamente este
// aparato. La anotación se corrige en vez de arrastrarla.
//
// 🔒 Y desde el 2026-08-30 el 400 `invalid_items` del cloud REPINTA en vez de redirigir: ver el
// reparto entero en guardarLineas. La frontera de D-047.16 es «no hubo mutación», no «la validación
// es local», y una edición todo-o-nada rechazada no escribió nada.
//
// 🔑 LAS DOS ACCIONES VAN AL MISMO ENDPOINT (`PUT /api/v1/intakes/{id}/items`) y solo las distingue
// UNA BANDERA EN EL CUERPO (`as_correction`, con `omitempty` deliberado). No existe ninguna ruta
// `/correct` en la API y no debe inventarse. La consecuencia para quien escriba un test: comprobar
// «llamó al endpoint» pasa con las dos confundidas — hay que mirar el CUERPO.

// filasEnBlanco son las filas vacías que el formulario ofrece al final para añadir líneas nuevas. Sin
// JS no se pueden crear filas en el navegador, así que vienen servidas.
//
// Son DOS a propósito: una línea tiene cinco campos, y tres filas en blanco convierten el formulario
// en un muro que hay que saltar para llegar al botón de guardar. Con dos se cubre la escena real
// —añadir el extra que el catálogo no tenía— y tras guardar aparecen otras dos.
const filasEnBlanco = 2

// estadoEditable es el estado desde el que la plataforma admite la edición manual
// (`intakes.EditableStatus`, hoy `pending_approval`).
//
// 🔴 ES UN ESPEJO, e incumple la regla que sostiene transicionesDe —esta consola no replica el ciclo
// de vida— por una razón concreta: el detalle publica `allowed_transitions` pero NO publica desde
// dónde se editan las líneas, así que hoy no hay otra forma de saberlo sin provocar un 422. Se asume
// el espejo en vez de emitir el formulario a ciegas porque la doctrina de esta pantalla ya está
// escrita: un estado que no admite la acción no ofrece el botón.
//
// Lo que lo hace soportable es que el espejo NO se usa para deducir el ciclo de vida: para saber si
// una solicitud puede LLEGAR a ser editable se mira `allowed_transitions`, que sí es de la
// plataforma. El riesgo que queda —que allá se añada otro estado editable y aquí no se ofrezca el
// formulario— lo cierra publicar `editable_in` en el detalle, igual que en su día se publicó
// `allowed_transitions`.
const estadoEditable = "pending_approval"

// prefijoSKUSistema es el prefijo de los skus que pone LA PLATAFORMA (hoy la línea de envío,
// `_shipping`). Las líneas que empiezan por él NO se ofrecen en el formulario: son filas del sistema
// y esa puerta las rechaza.
//
// Es otro espejo (`intakes.ReservedSKUPrefix`) y aquí es de PRESENTACIÓN: decide qué filas se pintan
// como editables. La autoridad sigue siendo la plataforma, que rechaza el prefijo en la entrada; si
// este espejo se quedara corto, lo peor que pasa es que el formulario ofrezca una fila que la API
// devuelve con su motivo, no que se cuele una línea del sistema duplicada.
const prefijoSKUSistema = "_"

// Nombres de los campos del formulario. Van como constantes por lo mismo que los del descarte: los
// lee el handler y los escribe la plantilla, y un desajuste entre los dos no lo detecta el
// compilador. En inglés porque son datos que se leen con el mismo nombre a los dos lados.
//
// 🔑 LOS DOS FORMULARIOS USAN LOS MISMOS NOMBRES, y eso es correcto: son dos `<form>` distintos y el
// navegador solo manda el que se envía. Lo que NO se puede compartir es dónde vuelve lo tecleado.
const (
	campoLineaSKU             = "item_sku"
	campoLineaEtiqueta        = "item_label"
	campoLineaPersonalizacion = "item_customization"
	campoLineaCantidad        = "item_qty"
	campoLineaPrecio          = "item_price"
	// campoLineaQuitar lleva el ÍNDICE 0-based de la fila que se quita, no su sku: si se corrigió el
	// sku antes de pulsar «Quitar», la fila que se quiere quitar sigue siendo ésa y no la que dice
	// el texto recién escrito.
	campoLineaQuitar = "remove"
)

// etiquetasDeCampo traduce el campo que nombra un defecto REMOTO —los del contrato, en inglés— al
// rótulo que el formulario tiene delante. Un campo desconocido se devuelve TAL CUAL: es preferible
// que la dueña lea `unit_price` a que la pantalla se calle un defecto que existe.
var etiquetasDeCampo = map[string]string{
	"sku":           "SKU",
	"label":         "artículo",
	"customization": "personalización",
	"qty":           "cantidad",
	"unit_price":    "precio",
}

// defectoLinea es un problema de UNA fila ya redactado: el número de fila que se ve en el formulario
// (1-based), el rótulo del campo y el motivo.
//
// Separa las dos mitades del aviso: el encabezado —«no se guardó nada»— sale del catálogo de flash
// porque es un texto fijo; esto es la lista de abajo, que lleva el NÚMERO DE FILA y por eso no puede
// salir de una tabla código→texto.
//
// 🔴 `Mensaje` lo escribe esta consola cuando el defecto es local, pero es PROSA DEL CLOUD cuando
// viene de un `invalid_items`. Es la única grieta declarada en la doctrina de esta casa —el texto
// que ve la dueña sale del catálogo, no del upstream— y se acepta por una razón concreta: es lo
// único que dice qué le pasa a ESA línea en concreto, y un texto fijo que dijera «alguna línea está
// mal» la dejaría buscándola a ojo.
type defectoLinea struct {
	Fila    int
	Campo   string
	Mensaje string
}

// filaLinea es UNA fila del formulario, con los valores como TEXTO.
//
// Son cadenas y no números a propósito: es lo que la persona tecleó, y hay que poder repintárselo tal
// cual cuando algo se rechaza. Convertir a float64 antes de tiempo obligaría a re-imprimir el número,
// y «8,50» volvería a la pantalla como «8.5» — la corrección de otro, encima de la suya.
type filaLinea struct {
	// Numero es el número de línea que se ve en el formulario, 1-based. Se calcula en Go y no en la
	// plantilla para que sea el MISMO que señalan los defectos (defectoLinea.Fila): si cada uno
	// contara por su cuenta, un rechazo señalaría una fila y se miraría otra.
	//
	// Ojo: el valor con el que viajará «Quitar» NO es éste sino el índice 0-based, que es lo que el
	// handler entiende. Son dos números para dos públicos distintos.
	Numero          int
	SKU             string
	Etiqueta        string
	Personalizacion string
	Cantidad        string
	PrecioUnitario  string
}

// EnBlanco responde si la fila está entera vacía: una fila de alta que nadie rellenó. La pregunta la
// hace la PLANTILLA, que solo ofrece «Quitar» en las filas que tienen algo que quitar.
func (r filaLinea) EnBlanco() bool {
	return strings.TrimSpace(r.SKU) == "" && strings.TrimSpace(r.Etiqueta) == "" &&
		strings.TrimSpace(r.Personalizacion) == "" && strings.TrimSpace(r.Cantidad) == "" &&
		strings.TrimSpace(r.PrecioUnitario) == ""
}

// formularioLineas es el formulario de corrección tal como lo pinta la plantilla.
type formularioLineas struct {
	// Filas son las líneas editables (las del cliente) más las de alta en blanco.
	Filas []filaLinea
	// Defectos son los problemas de la última tentativa (vacío = no hubo). Los pinta la pantalla
	// junto al formulario, no en el aviso de arriba: son una lista con la que se corrige, no un
	// motivo que se lee.
	Defectos []defectoLinea
	// LineasDelSistema es cuántas líneas puso la plataforma (hoy el envío). No se editan aquí y no
	// viajan en el cuerpo, así que la pantalla lo dice en vez de dejar creer que se perdieron.
	LineasDelSistema int
	// Editable es si el estado actual admite la corrección: SOLO entonces se emite el formulario. Un
	// estado que no la admite no puede ofrecer un botón que la plataforma va a rechazar — es la misma
	// regla con la que un terminal no ofrece desplegable de transición.
	Editable bool
	// Alcanzable es si la solicitud puede LLEGAR a un estado editable, y sale de los destinos que
	// publica la PLATAFORMA, no del espejo. Es lo que separa las dos negativas: a un `confirmed` se
	// le dice cómo llegar a corregir sus líneas, y a un terminal no se le promete un camino que no
	// existe.
	Alcanzable bool
	// EditableEn son los nombres de negocio de los estados desde los que sí se edita.
	EditableEn []string
}

// EditableEnText redacta los estados desde los que sí se corrige, para el aviso de la pantalla.
// Devuelve el texto ya montado —«por aprobar», o «X» o «Y» si algún día son varios— en vez de dejar
// que la plantilla indexe la lista: una lista vacía indexada revienta el render entero, y este aviso
// no vale una página en blanco.
func (f *formularioLineas) EditableEnText() string {
	if len(f.EditableEn) == 0 {
		return "el estado que la plataforma admita"
	}
	return "«" + strings.Join(f.EditableEn, "» o «") + "»"
}

// formularioLineasDe arma el formulario con lo que dice la plataforma: una fila por línea de CLIENTE,
// más las de alta. Las líneas del sistema se cuentan pero no se ofrecen.
//
// `transiciones` son los destinos ya resueltos por transicionesDe: con ellos se decide si a una
// solicitud que hoy no se puede corregir se le enseña el camino o no se le enseña nada.
func formularioLineasDe(detalle *apiclient.IntakeDetail, transiciones []string) *formularioLineas {
	form := &formularioLineas{Editable: detalle.Status == estadoEditable}
	if !form.Editable {
		form.Alcanzable = slices.Contains(transiciones, estadoEditable)
	}
	for _, item := range detalle.Items {
		if strings.HasPrefix(item.SKU, prefijoSKUSistema) {
			form.LineasDelSistema++
			continue
		}
		form.Filas = append(form.Filas, filaLinea{
			SKU:             item.SKU,
			Etiqueta:        item.Label,
			Personalizacion: item.Customization,
			Cantidad:        strconv.Itoa(item.Qty),
			// El precio se re-imprime con dos decimales, que es como lo enseña la ficha de arriba: si
			// el formulario dijera «8.5» donde la tabla dice «8.50», habría dos cifras distintas para
			// el mismo precio delante de los ojos.
			PrecioUnitario: strconv.FormatFloat(item.UnitPrice, 'f', 2, 64),
		})
	}
	form.Filas = filasNumeradas(append(form.Filas, make([]filaLinea, filasEnBlanco)...))
	form.EditableEn = etiquetasDe([]string{estadoEditable})
	return form
}

// filasNumeradas numera las filas para la pantalla (1-based). Es el ÚNICO sitio donde se decide ese
// número, y por eso coincide con el que llevan los defectos.
func filasNumeradas(filas []filaLinea) []filaLinea {
	for i := range filas {
		filas[i].Numero = i + 1
	}
	return filas
}

// etiquetasDe traduce una lista de claves de estado (p. ej. los destinos permitidos de un 422) a sus
// nombres de negocio.
func etiquetasDe(estados []string) []string {
	out := make([]string, 0, len(estados))
	for _, s := range estados {
		out = append(out, statusLabel(s))
	}
	return out
}

// GuardarLineasSolicitud guarda las LÍNEAS FACTURABLES desde el formulario del detalle (T7.4).
//
// Es la puerta con la que la dueña le pone precio a lo que el cliente pidió por escrito y el
// catálogo no tenía (D-041.26): el «queso extra» anotado en la personalización y cobrado a 0.
func (h *AdminHandler) GuardarLineasSolicitud(c *gin.Context) {
	h.guardarLineas(c, false)
}

// CorregirInterpretacionSolicitud guarda el formulario del BORRADOR (Plan 044 · T4.4): el mismo
// guardado, mandado con `as_correction` para que la plataforma lo registre como corrección de la
// dueña y deje la señal few-shot (D-044.48 §1).
//
// 🔑 Va por una ruta propia de esta consola —y no por un botón más del formulario de arriba— porque
// son dos formularios distintos sobre dos representaciones distintas de las líneas: aquél edita
// `items`, las líneas ya resueltas; éste edita la INTERPRETACIÓN, que es la única que tiene la línea
// `unmatched` — la que hay que poner a precio y que en `items` ni siquiera aparece.
func (h *AdminHandler) CorregirInterpretacionSolicitud(c *gin.Context) {
	h.guardarLineas(c, true)
}

// guardarLineas es el guardado que comparten los dos formularios de líneas.
//
// Lo único que cambia entre ellos es qué método del cliente se llama y en qué formulario vuelve lo
// tecleado cuando algo se rechaza; todo lo demás —leer las filas, quitar una, traducir los
// defectos— es idéntico, y duplicarlo garantizaría que el siguiente arreglo entrara solo en una de
// las dos copias.
//
// 🔒 EL REPARTO DE DESENLACES (D-047.16, EXTENDIDA POR JHOAN EL 2026-08-30):
//
//	formulario desemparejado / «Quitar» ilocalizable / cantidad o precio ilegibles
//	   ...... 400 REPINTANDO, con lo tecleado dentro y los defectos marcados fila a fila.
//	400 `invalid_items` DEL CLOUD
//	   ...... 400 REPINTANDO IGUAL, con lo tecleado Y los defectos que manda la plataforma, ya
//	          traducidos a números de fila del formulario.
//	resto de la API (422 no editable, 409, 502, 400 sin cuerpo nombrado…)
//	   ...... 303 + flash. Ésos SÍ pudieron mutar, que es el caso del PRG.
//	éxito .... 303 + flash.
//
// 🔑 POR QUÉ EL `invalid_items` REPINTA AUNQUE VENGA DE LA API, que es la extensión de la regla: la
// frontera de D-047.16 NO es «la validación es local», es «NO HUBO MUTACIÓN». La edición de líneas
// es todo-o-nada, así que un `invalid_items` rechaza el cuerpo entero ANTES de escribir; repintarlo
// no crea el problema que el PRG resuelve, y sí evita perder la tabla recién rellenada. Que la
// validación viva al otro lado del cable no cambia el hecho.
//
// Y lo que se gana es lo que se estaba tirando: ese cuerpo trae la lista COMPLETA de defectos por
// línea, que es con lo que la dueña corrige. Un 303 la habría cambiado por un texto fijo que dice
// «alguna línea está mal».
//
// 🔴 LO QUE NO SE EXTIENDE, y conviene que se lea aquí: el 409 y el 422 siguen yendo por 303. El 409
// significa que otra persona la movió —el estado que se tiene delante ya es viejo, y repintarlo sería
// mentir— y el 422 que el estado no admite editar, con lo que el formulario ni siquiera se emite.
func (h *AdminHandler) guardarLineas(c *gin.Context, comoCorreccion bool) {
	id := strings.TrimSpace(c.Param("id"))

	// enFormulario deja lo tecleado y lo rechazado en el formulario DEL QUE VINIERON, que es el único
	// sitio donde se puede corregir.
	//
	// 🔴 ES EL PUNTO DELICADO DE ESTA FUNCIÓN: son dos formularios en la misma página sobre dos
	// listas distintas de líneas. Volcar lo de uno en el otro pondría los precios en filas ajenas —y
	// nada fallaría: la pantalla se pintaría entera, con los números cambiados de sitio—.
	enFormulario := func(r detalleRender, filas []filaLinea, defectos []defectoLinea) detalleRender {
		if comoCorreccion {
			r.filasBorrador, r.defectosBorrador = filas, defectos
			return r
		}
		r.filas, r.defectos = filas, defectos
		return r
	}

	// Sin empresa no hay solicitud que corregir y la API respondería 403 sobre una causa que no es
	// esa. Va por 303 y no repintando porque el repintado releería un detalle que tampoco existe.
	if sinEmpresa(c) {
		c.Redirect(http.StatusSeeOther, rutaSolicitudes)
		return
	}
	// Un id vacío no lo produce el router; es una guarda, y lo que evita es gastar el viaje para
	// recibir el 404 que ya se sabe. Sin id no hay detalle al que repintar, así que va a la bandeja.
	if id == "" {
		c.Redirect(http.StatusSeeOther, rutaSolicitudes)
		return
	}

	destino := solicitudURL(id)

	filas, emparejadas := filasDelFormulario(c)
	if !emparejadas {
		// Sin las filas emparejadas no hay «lo tecleado» que devolver —no se sabe qué precio va con
		// qué artículo, que es justo el motivo del rechazo—, así que se repinta con lo que dice la
		// plataforma y el aviso manda a recargar.
		h.renderSolicitudDetalle(c, detalleRender{
			id: id, status: http.StatusBadRequest, code: flashSolicitudFormularioIncompleto,
		})
		return
	}

	quitar := -1
	if crudo := formValue(c, campoLineaQuitar); crudo != "" {
		idx, err := strconv.Atoi(crudo)
		if err != nil || idx < 0 || idx >= len(filas) {
			h.renderSolicitudDetalle(c, enFormulario(detalleRender{
				id: id, status: http.StatusBadRequest, code: flashSolicitudLineaSinIdentificar,
			}, filas, nil))
			return
		}
		quitar = idx
	}

	items, origen, defectos := itemsDeLasFilas(filas, quitar)
	if len(defectos) > 0 {
		h.renderSolicitudDetalle(c, enFormulario(detalleRender{
			id: id, status: http.StatusBadRequest, code: flashSolicitudLineasIlegibles,
		}, filas, defectos))
		return
	}

	var guardarErr error
	code := flashCodeForLineas(h.auth.withAuthRetry(c, func(accessToken string) error {
		var err error
		// 🔑 AQUÍ SE DECIDE QUÉ ES CADA UNA, Y ES LA ÚNICA DIFERENCIA ENTRE LAS DOS ACCIONES. Los dos
		// métodos emiten el MISMO `PUT /api/v1/intakes/{id}/items` y solo se distinguen por
		// `as_correction` en el cuerpo, que además lleva `omitempty`: con `false` la clave ni se
		// emite. Cambiar esta rama por la de al lado deja la corrección viajando como una edición
		// normal —la plataforma no registraría la corrección de la dueña ni dejaría la señal
		// few-shot— y NINGÚN test que mire la ruta lo vería. El que lo ve mira el cuerpo.
		if comoCorreccion {
			_, err = h.api.Intakes.CorrectIntakeItems(c.Request.Context(), accessToken, id, items)
		} else {
			_, err = h.api.Intakes.ReplaceIntakeItems(c.Request.Context(), accessToken, id, items)
		}
		guardarErr = err
		return err
	}))
	if sessionIsDead(guardarErr) {
		h.auth.expireSession(c)
		return
	}
	if code != "" {
		slog.Warn("no se pudieron guardar las líneas de la solicitud",
			"codigo", code, "correccion", comoCorreccion, "error", guardarErr)
	}
	// El `invalid_items` es el ÚNICO desenlace de la API que no sale por la puerta de al lado: no
	// mutó nada y trae con qué corregir, así que vuelve a la pantalla en vez de a la URL. Los
	// defectos se anclan a la fila del formulario con `origen`, y van al formulario del que vinieron.
	if invalido, ok := apiclient.InvalidItemsOf(guardarErr); ok {
		h.renderSolicitudDetalle(c, enFormulario(detalleRender{
			id: id, status: http.StatusBadRequest, code: code,
		}, filas, defectosRemotos(invalido.Defects, origen)))
		return
	}
	redirectWith(c, destino, code, exitoDeLineas(comoCorreccion))
}

// exitoDeLineas dice cuál de los dos éxitos toca. Son dos y no uno porque significan cosas
// distintas: guardar las facturables deja las líneas y el total, y corregir la interpretación deja
// además la señal con la que la plataforma lee mejor los pedidos parecidos.
func exitoDeLineas(comoCorreccion bool) string {
	if comoCorreccion {
		return flashSolicitudCorreccionGuardada
	}
	return flashSolicitudLineasGuardadas
}

// conLoTecleado devuelve al formulario lo que la persona escribió, con sus defectos marcados. Nil ⇒
// no hubo repintado y el formulario se queda con lo que dice la plataforma.
//
// 🔴 REEMPLAZA las filas en vez de mezclarlas, y ahí está el punto: lo tecleado ya trae las
// altas nuevas, las bajas y las correcciones. Cruzarlo con lo de la plataforma daría una tercera
// lista que no es ni lo guardado ni lo escrito.
func (f *formularioLineas) conLoTecleado(filas []filaLinea, defectos []defectoLinea) {
	if f == nil || len(filas) == 0 {
		return
	}
	f.Filas = filasNumeradas(append(append([]filaLinea{}, filas...), make([]filaLinea, filasEnBlanco)...))
	f.Defectos = defectos
}

// filasDelFormulario lee las filas de un envío.
//
// Devuelve false si los cinco arrays NO vienen emparejados, que es señal de un envío manipulado o
// truncado: antes que adivinar qué precio va con qué artículo —y arriesgarse a guardar una mezcla—
// se rechaza y se pide recargar.
func filasDelFormulario(c *gin.Context) ([]filaLinea, bool) {
	skus := c.PostFormArray(campoLineaSKU)
	etiquetas := c.PostFormArray(campoLineaEtiqueta)
	personalizaciones := c.PostFormArray(campoLineaPersonalizacion)
	cantidades := c.PostFormArray(campoLineaCantidad)
	precios := c.PostFormArray(campoLineaPrecio)

	n := len(skus)
	if len(etiquetas) != n || len(personalizaciones) != n || len(cantidades) != n || len(precios) != n {
		return nil, false
	}
	filas := make([]filaLinea, 0, n)
	for i := range skus {
		filas = append(filas, filaLinea{
			SKU: skus[i], Etiqueta: etiquetas[i], Personalizacion: personalizaciones[i],
			Cantidad: cantidades[i], PrecioUnitario: precios[i],
		})
	}
	return filasNumeradas(filas), true
}

// itemsDeLasFilas convierte lo tecleado en el cuerpo que va a la API.
//
// 🔒 LA FRONTERA: aquí se valida la LECTURA del número —que «8,50» sean ocho con cincuenta y que
// «ocho» no sea nada—, nunca lo que el número SIGNIFICA. Que el precio no sea negativo o que la
// cantidad sea 1 o más lo dice el dominio, y duplicarlo aquí daría dos criterios para la misma regla
// que un día divergirían.
//
// 🔑 DEVUELVE ADEMÁS `origen`: para cada posición del cuerpo, la FILA DEL FORMULARIO de la que salió.
// Hace falta porque las filas en blanco —y la que se quita— NO se mandan, así que el índice 0-based
// con el que la plataforma señala un defecto NO es el número de fila que la dueña tiene delante. Sin
// esa traducción, un rechazo de la línea 4 apuntaría a la 2 y mandaría a corregir la línea
// equivocada: el defecto seguiría viéndose, con el texto correcto, colgado de otra fila. Es un fallo
// que no rompe nada y que solo se nota corrigiendo la línea que no era.
func itemsDeLasFilas(filas []filaLinea, quitar int) (items []apiclient.IntakeItem, origen []int, defectos []defectoLinea) {
	items = make([]apiclient.IntakeItem, 0, len(filas))
	origen = make([]int, 0, len(filas))

	for i, fila := range filas {
		if i == quitar || fila.EnBlanco() {
			continue
		}
		numero := i + 1

		cantidad, errCantidad := leerCantidad(fila.Cantidad)
		if errCantidad != "" {
			defectos = append(defectos, defectoLinea{Fila: numero, Campo: "cantidad", Mensaje: errCantidad})
		}
		precio, errPrecio := leerPrecio(fila.PrecioUnitario)
		if errPrecio != "" {
			defectos = append(defectos, defectoLinea{Fila: numero, Campo: "precio", Mensaje: errPrecio})
		}
		if errCantidad != "" || errPrecio != "" {
			continue
		}

		items = append(items, apiclient.IntakeItem{
			SKU:           strings.TrimSpace(fila.SKU),
			Label:         strings.TrimSpace(fila.Etiqueta),
			Customization: strings.TrimSpace(fila.Personalizacion),
			Qty:           cantidad,
			UnitPrice:     precio,
		})
		origen = append(origen, numero)
	}
	return items, origen, defectos
}

// leerCantidad lee la cantidad. Devuelve el motivo (vacío = bien leída).
//
// Solo un entero: «2,5 hamburguesas» no es una cantidad de un pedido, y aceptarla redondeando
// decidiría por quien la escribió cuál de las dos quiso decir.
func leerCantidad(crudo string) (int, string) {
	s := strings.TrimSpace(crudo)
	if s == "" {
		return 0, "falta la cantidad: escribe cuántas unidades son (por ejemplo 2)"
	}
	cantidad, err := strconv.Atoi(s)
	if err != nil {
		return 0, "«" + s + "» no es una cantidad: escribe un número entero (por ejemplo 2)"
	}
	return cantidad, ""
}

// leerPrecio lee el precio tal como lo teclea una persona con prisa, y RECHAZA todo lo que no pueda
// leer con certeza. Devuelve el motivo (vacío = bien leído).
//
// 🔒 LA REGLA DE FONDO: un precio mal leído no avisa. Si «8,50» se leyera como 0 se regala el
// artículo, y si «1.234» se leyera como 1,234 se cobra mil veces de menos; las dos cosas se
// descubren cuando ya se entregó. Por eso aquí no hay ninguna rama que, ante la duda, se quede con
// un valor: o el número se lee sin ambigüedad, o se rechaza con un motivo que dice cómo escribirlo.
//
// Qué se acepta:
//   - coma o punto decimal, indistintamente («8,50» y «8.50» son lo mismo);
//   - separador de miles cuando la escritura lo deja claro, que es cuando aparecen los dos signos
//     («1.234,56» y «1,234.56» son los dos 1234.56) o cuando el mismo se repite («1.234.567»);
//   - como mucho DOS decimales, que es con los que la pantalla imprime.
//
// Y el caso que se rechaza aunque parezca inofensivo: un único separador con exactamente tres
// dígitos detrás («1.234»). Ahí no hay forma de saber si son mil doscientos treinta y cuatro o uno
// con doscientos treinta y cuatro milésimas, y las dos lecturas se diferencian en tres ceros.
func leerPrecio(crudo string) (float64, string) {
	s := strings.TrimSpace(crudo)
	if s == "" {
		return 0, "falta el precio: escribe cuánto cuesta la unidad; si va de regalo, escribe 0"
	}

	// El signo se separa antes del filtro de caracteres para poder decir «no puede ser negativo» en
	// vez de «no es un número», que es un motivo que no ayuda a corregir. Que sea negativo lo juzga
	// la plataforma; aquí solo se lee el número que hay detrás del signo.
	signo := 1.0
	cuerpo := s
	if resto, ok := strings.CutPrefix(cuerpo, "-"); ok {
		signo, cuerpo = -1, strings.TrimSpace(resto)
	} else if resto, ok := strings.CutPrefix(cuerpo, "+"); ok {
		cuerpo = strings.TrimSpace(resto)
	}

	digitos, puntos, comas := 0, 0, 0
	for _, r := range cuerpo {
		switch {
		case r >= '0' && r <= '9':
			digitos++
		case r == '.':
			puntos++
		case r == ',':
			comas++
		default:
			return 0, precioIlegible(s)
		}
	}
	if digitos == 0 {
		return 0, precioIlegible(s)
	}

	entera, decimal := cuerpo, ""
	if ultimo := strings.LastIndexAny(cuerpo, ".,"); ultimo >= 0 {
		entera, decimal = cuerpo[:ultimo], cuerpo[ultimo+1:]

		switch {
		case decimal == "":
			return 0, "«" + s + "» acaba en un separador y le falta la parte decimal: escribe 1,50 o 1"
		case puntos > 0 && comas > 0:
			// Los dos signos presentes: el último es el decimal y el otro el de miles. Las dos
			// escrituras habituales —«1.234,56» y «1,234.56»— caen aquí y dan lo mismo.
		case puntos+comas > 1:
			// El mismo signo repetido solo puede ser separador de miles («1.234.567»): un número no
			// tiene dos comas decimales. La parte decimal, entonces, no existe.
			entera, decimal = cuerpo, ""
		case len(decimal) == 3:
			sinSeparador := strings.NewReplacer(".", "", ",", "").Replace(cuerpo)
			return 0, "no se sabe si «" + s + "» son " + sinSeparador + " o " + entera + "," + decimal +
				": escríbelo sin separador de miles (" + sinSeparador + ") o con decimales (" +
				entera + "," + decimal + "0)"
		}
		if len(decimal) > 2 {
			return 0, "el precio admite como mucho dos decimales, y «" + s + "» trae " +
				strconv.Itoa(len(decimal))
		}
	}

	// Lo que queda a la izquierda del decimal son grupos de miles, y tienen que estar bien formados:
	// «1.23.456» no es un número escrito de otra manera, es un número mal escrito, y limpiarle los
	// puntos para quedarse con 123456 sería inventarse lo que quiso decir.
	grupos := strings.FieldsFunc(entera, func(r rune) bool { return r == '.' || r == ',' })
	if strings.ContainsAny(entera, ".,") {
		for i, g := range grupos {
			if (i == 0 && (len(g) < 1 || len(g) > 3)) || (i > 0 && len(g) != 3) {
				return 0, "«" + s + "» tiene los miles mal agrupados: escríbelo sin separador (" +
					strings.Join(grupos, "") + ")"
			}
		}
	}

	limpio := strings.Join(grupos, "")
	if decimal != "" {
		limpio += "." + decimal
	}
	valor, err := strconv.ParseFloat(limpio, 64)
	if err != nil {
		return 0, precioIlegible(s)
	}
	return signo * valor, ""
}

// precioIlegible es el motivo único de «esto no es un precio». Va en una función para que las tres
// puertas que pueden llegar a él digan exactamente lo mismo.
func precioIlegible(crudo string) string {
	return "«" + crudo + "» no es un precio: escribe solo números, con coma o punto para los " +
		"decimales (por ejemplo 1,50)"
}

// defectosRemotos traduce los defectos que manda la plataforma a filas del formulario.
//
// 🔑 `origen` mapea cada posición del cuerpo ENVIADO a su fila del formulario, y es la razón de ser
// de esta función: la plataforma señala con un índice 0-based sobre lo que recibió, y lo que recibió
// no lleva las filas en blanco. Traducir mal aquí no rompe nada — se pinta un defecto real, con su
// texto correcto, colgado de la fila que no es —, y eso solo se descubre corrigiendo la línea
// equivocada.
//
// Un índice FUERA DE RANGO —un servidor que señale una línea que no se mandó— se pinta como fila 0 y
// la plantilla omite el «Línea N», en vez de descartarlo: un defecto que no se entiende sigue siendo
// un defecto, y callarlo dejaría a la dueña dándole a «Guardar» sin saber por qué no entra.
func defectosRemotos(defectos []apiclient.ItemDefect, origen []int) []defectoLinea {
	out := make([]defectoLinea, 0, len(defectos))
	for _, d := range defectos {
		fila := 0
		if d.Index >= 0 && d.Index < len(origen) {
			fila = origen[d.Index]
		}
		campo := d.Field
		if etiqueta, ok := etiquetasDeCampo[d.Field]; ok {
			campo = etiqueta
		}
		out = append(out, defectoLinea{Fila: fila, Campo: campo, Mensaje: d.Message})
	}
	return out
}
