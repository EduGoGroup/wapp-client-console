package web

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
)

// solicitudes_descarte.go es el descarte por LOTES desde la bandeja (T7.2), mudado de
// `intakes_discard.go` del BFF.
//
// 🔒 EL DESCARTE ES IRREVERSIBLE Y NO HAY PAPELERA (D-041.22), así que son DOS pasos: revisar —que
// enseña qué se va a descartar y no escribe nada— y descartar. La confirmación es SERVER-SIDE (una
// pantalla más, con su token CSRF y su lista) y no un `confirm()` del navegador: esta consola no
// emite una línea de JavaScript, y un diálogo del navegador no puede enseñar los estados ni los
// totales de lo seleccionado, que es precisamente lo que hace informada a la decisión.
//
// El lote se reenvía ENTERO en el paso 2 en vez de guardarse entre pasos: así la consola no tiene
// estado y lo que se descarta es exactamente lo que se confirmó, aunque haya dos pestañas abiertas.

// Nombres de los campos del formulario. Van como constantes porque los lee el handler y los escribe
// la plantilla, y un desajuste entre los dos no lo detecta el compilador.
//
// 🔑 Están EN INGLÉS aunque la ruta esté en español, y es la misma frontera de siempre: la ruta es
// superficie y estos son datos que se leen con el mismo nombre a los dos lados del formulario.
const (
	campoDescarteID     = "intake_id"
	campoDescarteAccion = "action"
	// campoDescarteVisible lleva, en un oculto por fila, los ids de la PÁGINA que se está mirando.
	// Es la materia prima de «seleccionar todo lo visible», y viaja por el formulario en vez de
	// recalcularse en el servidor a propósito: lo que se va a descartar es lo que la dueña TENÍA
	// DELANTE, no lo que la bandeja devolvería si se volviera a leer un segundo después.
	campoDescarteVisible = "visible_intake_id"
	// campoDescarteTodos es la casilla MAESTRA de la cabecera. Marcada significa «toda esta página»,
	// nunca «todo lo que cumpla el filtro»: son 20 solicitudes o son 4.000, y esto no tiene vuelta
	// atrás.
	campoDescarteTodos = "select_visible"
	// descarteConfirmar es el valor del botón que ESCRIBE. Cualquier otro valor —incluido el
	// ausente— es el paso de mirar: el descarte no puede caerse del lado de escribir por un campo
	// que llegó vacío.
	descarteConfirmar = "discard"
)

// razonesDeSalto traduce las razones del contrato (`apiclient.DiscardSkip*`) a la voz de la dueña
// del negocio. Es un diccionario de PRESENTACIÓN, como el de estados: aquí no se decide nada, solo
// se dice en español lo que decidió la plataforma.
//
// `live_event` es la que más se aleja de su clave, y a propósito: en el contrato significa «hay una
// conversación viva detrás de esa solicitud» —hoy, un carrito abierto de ese contacto en esa
// sesión— y a quien lee la pantalla eso le llega como «el cliente está a media compra». Decirle
// «evento vivo» sería contarle la implementación en vez del hecho.
var razonesDeSalto = map[string]string{
	apiclient.DiscardSkipNotFound: "No está en tu bandeja: o no existe o ya no es de este negocio.",
	apiclient.DiscardSkipAlreadyDiscarded: "Ya estaba descartada. No se ha vuelto a tocar y no se ha " +
		"duplicado nada de lo que quedó registrado.",
	apiclient.DiscardSkipNotOpen: "Ya no está abierta, y desde donde está no se descarta. Si estaba " +
		"confirmada, lo que corresponde es cancelarla desde su ficha.",
	apiclient.DiscardSkipLiveEvent: "El cliente sigue en plena conversación con este pedido: descartarlo " +
		"ahora se lo cortaría a medias. Atiéndelo o espera a que termine, y descártalo después.",
}

// razonDeSalto redacta el motivo por el que una solicitud del lote no se descartó. Una razón que no
// esté en el diccionario se pinta TAL CUAL: es preferible que la dueña lea una clave rara a que la
// pantalla se calle que esa solicitud sigue ahí.
func razonDeSalto(razon string) string {
	if texto, ok := razonesDeSalto[razon]; ok {
		return texto
	}
	return razon
}

// descarteFila es UNA solicitud del lote tal como se enseña ANTES de descartarla. Los valores llegan
// ya redactados —el estado con su nombre de negocio, el total con sus dos decimales— porque la
// plantilla de confirmación no calcula nada.
type descarteFila struct {
	ID        string
	ContactID string
	SessionID string
	Estado    string
	Total     string
	// Listada dice si la solicitud se pudo describir con la bandeja que se está mirando. Con `false`
	// solo se conoce el id —la fila cambió de página o de estado entre la selección y el envío— y la
	// pantalla lo dice, en vez de pintar celdas vacías que parecerían datos.
	Listada bool
}

// descarteSaltada es una solicitud que NO se descartó, con el motivo ya en español.
type descarteSaltada struct {
	ID    string
	Razon string
}

// descarteView es el descarte por lotes tal como lo pinta la bandeja: o el lote que espera
// confirmación, o el desglose del que ya se ejecutó. Nunca los dos a la vez.
type descarteView struct {
	// Confirmando dice que hay un lote esperando el «Descartar definitivamente». Mientras sea true
	// NO se ha llamado a la API: mirar no escribe.
	Confirmando bool
	// Seleccionadas son las solicitudes del lote, en el orden en que se marcaron.
	Seleccionadas []descarteFila
	// Accion es la URL del formulario que confirma (arrastra los filtros vigentes) y CancelarURL la
	// vuelta a la bandeja tal como se estaba mirando.
	Accion      string
	CancelarURL string

	// Hecho dice que el lote se ejecutó. NO significa que se descartara nada: para eso están las dos
	// listas de abajo, que es justo lo que un «listo» global escondería.
	Hecho       bool
	Descartadas []string
	Saltadas    []descarteSaltada

	// ids es el lote en crudo mientras espera confirmación. No se exporta porque la plantilla pinta
	// Seleccionadas: las rellena describirCon cuando la bandeja ya está leída.
	ids []string
}

// Total es cuántas solicitudes ejecutó el lote. Lo pregunta la plantilla para encabezar el desglose
// sin sumar en el HTML.
func (v *descarteView) Total() int { return len(v.Descartadas) + len(v.Saltadas) }

// describirCon completa las filas del lote pendiente con lo que dice la bandeja recién leída.
//
// Lo que NO hace es pedir cada solicitud a la API para describirla: son hasta 200 y quien descarta
// está mirando esa misma página. Un id que no esté en ella se conserva en el lote —es su selección,
// y quien decide si existe es la plataforma— pero se marca como no descrito.
func (v *descarteView) describirCon(listadas []apiclient.Intake) {
	if v == nil || len(v.ids) == 0 {
		return
	}
	porID := make(map[string]apiclient.Intake, len(listadas))
	for _, in := range listadas {
		porID[in.ID] = in
	}

	filas := make([]descarteFila, 0, len(v.ids))
	for _, id := range v.ids {
		fila := descarteFila{ID: id}
		if in, ok := porID[id]; ok {
			fila.Listada = true
			fila.ContactID = in.ContactID
			fila.SessionID = in.SessionID
			fila.Estado = statusLabel(in.Status)
			fila.Total = strconv.FormatFloat(in.Total, 'f', 2, 64)
		}
		filas = append(filas, fila)
	}
	v.Seleccionadas = filas
}

// DescartarSolicitudes atiende los DOS pasos del descarte desde la bandeja: revisar (el normal) y
// descartar (el que escribe). Cuál se pide lo dice el botón pulsado, y el que escribe SOLO existe en
// el formulario de confirmación, o sea después de haber enseñado qué se va a descartar.
//
// 🔒 EL REPARTO DE DESENLACES, Y SU RELACIÓN CON D-047.16:
//
//	0 marcadas / lote > 200 ...... 400 REPINTANDO. Es validación LOCAL: la petición no llegó a salir,
//	                               no hubo mutación, y la selección sigue viva en la página.
//	revisar ...................... 200 con la tarjeta de confirmación. Tampoco se llamó a la API.
//	error de la API .............. 303 + flash a la MISMA página de la bandeja (con sus filtros).
//	                               La petición salió y pudo mutar: es justo el caso del PRG.
//	éxito ........................ 200 REPINTANDO con el desglose ítem a ítem.
//
// 🔴 Ese último es la SEGUNDA excepción declarada al PRG de esta consola, y no es de gusto: el
// resultado de un lote es un DESGLOSE —qué cayó y qué no, con su motivo— y el catálogo de flash
// traduce códigos a textos fijos, no interpola datos. Un 303 + «descarte hecho» le diría a la dueña
// que se descartaron las cinco cuando puede que solo cayeran dos. El precio del repintado es que un
// F5 reenvía el lote, y ese precio se puede pagar aquí y solo aquí: repetir el MISMO lote es seguro
// por construcción —lo ya descartado vuelve como `already_discarded`— y el desglose se pinta otra
// vez diciendo exactamente eso.
func (h *AdminHandler) DescartarSolicitudes(c *gin.Context) {
	filtro := filtroDeLaQuery(c)

	// Sin empresa no hay nada que descartar y la API respondería 403 sobre una causa que no es esa.
	// Va por 303 y no repintando porque no hubo mutación ni hay nada tecleado que perder: quien
	// llegue aquí acaba en la bandeja, que le explica lo que le pasa (parcial `sin_empresa`).
	if sinEmpresa(c) {
		c.Redirect(http.StatusSeeOther, rutaSolicitudes)
		return
	}

	ids := idsSeleccionados(c)
	switch {
	case len(ids) == 0:
		h.renderSolicitudes(c, solicitudesRender{
			status: http.StatusBadRequest, filtro: filtro,
			aviso: &avisoSolicitudes{Mensaje: "Marca al menos una solicitud antes de descartar. " +
				"No se ha tocado nada."},
		})
		return
	case len(ids) > apiclient.MaxIntakeDiscardBatch:
		// La plataforma responde 400 a un lote así y no descarta ninguna. Aquí se corta antes y se
		// explica en español, que es lo que ese 400 no trae: la selección sigue viva y la salida es
		// partirla en dos tandas.
		h.renderSolicitudes(c, solicitudesRender{
			status: http.StatusBadRequest, filtro: filtro,
			aviso: &avisoSolicitudes{Mensaje: loteDemasiadoGrande(len(ids))},
		})
		return
	}

	if formValue(c, campoDescarteAccion) != descarteConfirmar {
		h.renderSolicitudes(c, solicitudesRender{
			status: http.StatusOK, filtro: filtro,
			descarte: &descarteView{
				Confirmando: true, ids: ids,
				Accion: descartarURL(filtro), CancelarURL: solicitudesURL(filtro, filtro.Page),
			},
		})
		return
	}

	var resultado *apiclient.IntakeDiscardResult
	var descarteErr error
	code := flashCodeForDescarte(h.auth.withAuthRetry(c, func(accessToken string) error {
		var err error
		resultado, err = h.api.Intakes.DiscardIntakes(c.Request.Context(), accessToken, ids)
		descarteErr = err
		return err
	}))
	if sessionIsDead(descarteErr) {
		h.auth.expireSession(c)
		return
	}
	if code != "" {
		slog.Warn("no se pudo descartar el lote de solicitudes", "codigo", code, "error", descarteErr)
		redirigirALaBandeja(c, filtro, code)
		return
	}

	h.renderSolicitudes(c, solicitudesRender{
		status: http.StatusOK, filtro: filtro,
		descarte: resultadoDelDescarte(resultado), aviso: avisoDelResultado(resultado),
	})
}

// idsSeleccionados lee las solicitudes marcadas. Colapsa los repetidos conservando el ORDEN de
// llegada, que es el de la bandeja que se tiene delante.
//
// 🔴 El colapso ocurre ANTES de medir el lote, y esa es la diferencia con la plataforma —que mide el
// cuerpo tal como llega—: lo que se mide aquí es lo que se va a mandar, así que las dos cuentas dan
// lo mismo y no hay forma de que esta consola acepte un lote que el otro lado rechace por tamaño.
//
// CON LA MAESTRA MARCADA la selección son los ids de la PÁGINA, y de ahí sale el límite de esta
// pantalla: la lista llega del formulario, o sea de las filas que se PINTARON, y el servidor no
// tiene forma de ampliarla ni aunque quisiera. «Todo lo visible» no puede convertirse en «todo lo
// que cumple el filtro» por un descuido, porque el conjunto ancho no está aquí.
//
// La maestra GANA sobre las casillas sueltas en vez de sumarse a ellas, y es lo mismo: los ocultos
// de la página son un superconjunto de lo que se pueda haber marcado a mano en ella.
func idsSeleccionados(c *gin.Context) []string {
	campo := campoDescarteID
	if strings.TrimSpace(c.PostForm(campoDescarteTodos)) != "" {
		campo = campoDescarteVisible
	}
	crudos := c.PostFormArray(campo)
	vistos := make(map[string]struct{}, len(crudos))
	ids := make([]string, 0, len(crudos))
	for _, valor := range crudos {
		id := strings.TrimSpace(valor)
		if id == "" {
			continue
		}
		if _, dup := vistos[id]; dup {
			continue
		}
		vistos[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

// resultadoDelDescarte proyecta la respuesta de la plataforma a lo que pinta la pantalla, traduciendo
// cada razón. Las dos listas se conservan enteras: el desglose por ítem ES el resultado de esta
// operación, y resumirlo a «hecho» escondería que hay solicitudes que siguen ahí.
func resultadoDelDescarte(res *apiclient.IntakeDiscardResult) *descarteView {
	saltadas := make([]descarteSaltada, 0, len(res.Skipped))
	for _, s := range res.Skipped {
		saltadas = append(saltadas, descarteSaltada{ID: s.IntakeID, Razon: razonDeSalto(s.Reason)})
	}
	return &descarteView{Hecho: true, Descartadas: res.Discarded, Saltadas: saltadas}
}

// avisoDelResultado encabeza el desglose diciendo cuántas cayeron de cuántas.
//
// 🔴 Solo es un aviso de ÉXITO cuando no se saltó ninguna. Un lote mixto se anuncia como lo que es
// —un trabajo a medias— porque el verde de arriba es lo único que mucha gente lee: dárselo a un lote
// en el que tres solicitudes siguen en la bandeja sería enseñarle a no leer la tabla.
func avisoDelResultado(res *apiclient.IntakeDiscardResult) *avisoSolicitudes {
	hechas, saltadas := len(res.Discarded), len(res.Skipped)
	switch {
	case hechas == 0 && saltadas == 1:
		return &avisoSolicitudes{Mensaje: "No se descartó la solicitud que mandaste. Abajo está por qué."}
	case hechas == 0:
		return &avisoSolicitudes{Mensaje: "No se descartó ninguna de las " + cuentaSolicitudes(saltadas) +
			" que mandaste. Abajo está qué pasó con cada una."}
	case saltadas == 0:
		return &avisoSolicitudes{Exito: true, Mensaje: "Descartadas " + cuentaSolicitudes(hechas) +
			". Salen de tu bandeja de pendientes y esto no se puede deshacer; lo que pidió el cliente " +
			"sigue guardado."}
	default:
		cayeron := "Se descartaron " + strconv.Itoa(hechas)
		if hechas == 1 {
			cayeron = "Se descartó 1"
		}
		resto := "Las otras " + strconv.Itoa(saltadas) + " siguen"
		if saltadas == 1 {
			resto = "La otra sigue"
		}
		return &avisoSolicitudes{Mensaje: cayeron + " de " + cuentaSolicitudes(hechas+saltadas) + ". " +
			resto + " en tu bandeja: abajo está por qué."}
	}
}

// loteDemasiadoGrande redacta el rechazo del lote que no cabe.
func loteDemasiadoGrande(n int) string {
	return "Marcaste " + cuentaSolicitudes(n) + " y de una vez se pueden descartar como mucho " +
		strconv.Itoa(apiclient.MaxIntakeDiscardBatch) + ". No se ha descartado ninguna: quita las que " +
		"sobren o hazlo en varias tandas."
}

// cuentaSolicitudes escribe «1 solicitud» o «N solicitudes», para que ningún mensaje de esta pantalla
// acabe diciendo «1 solicitudes».
//
// Delega en `cuenta` (formato.go) desde T7.3: el detalle necesitaba el mismo plural con otro
// sustantivo, y dos funciones de plural en el mismo paquete son dos sitios donde arreglar el día que
// alguna diga «1 solicitudes».
func cuentaSolicitudes(n int) string { return cuenta(n, "solicitud", "solicitudes") }
