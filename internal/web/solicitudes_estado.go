package web

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// solicitudes_estado.go es el diccionario de PRESENTACIÓN de los estados de una solicitud, y desde
// T7.4 también la acción que la mueve de uno a otro.
//
// Que quede claro qué NO es, porque la distinción es la que sostiene esta pantalla: aquí no hay
// máquina de estados. No se dice desde dónde se llega a cada estado ni adónde se puede ir —eso lo
// decide `internal/intakes/status.go` de la plataforma y viaja por la API—. Esto es una tabla de
// nombres, y su peor fallo posible es cosmético: si mañana aparece un estado nuevo, statusLabel
// devuelve la clave cruda y el filtro no lo lista, pero nada opera mal ni se ofrece una transición
// falsa.
//
// El handler de abajo no cambia eso: mover una solicitud es pedírselo a la plataforma y contar qué
// dijo. Quien decide si la transición vale es la máquina de estados del cloud, aquí y en el 422.
//
// 📌 El diccionario viaja en T7.2 y no en T7.3, aunque el plan lo ponga allí: `solicitudes.html` usa
// statusLabel para rotular el estado de cada fila, así que sin él la lista no se puede pintar.

// campoEstado es el nombre del `<select>` del formulario. Va como constante por lo mismo que los
// campos de las líneas: lo lee el handler y lo escribe la plantilla, y un desajuste entre los dos no
// lo detecta el compilador. En inglés, como todo lo que viaja por el cable.
const campoEstado = "status"

// estadoDeSolicitud es una clave del ciclo de vida con su nombre de negocio.
type estadoDeSolicitud struct {
	Clave    string
	Etiqueta string
}

// estadosDeSolicitud traduce las claves del ciclo de vida (D-041.10, en inglés por INV-09) al nombre
// con el que la dueña del negocio las llama. Alimenta el desplegable del filtro y la etiqueta de
// cada fila.
//
// El orden es el del CICLO DE VIDA y no el alfabético: el desplegable se lee como el recorrido que
// hace un pedido de verdad.
var estadosDeSolicitud = []estadoDeSolicitud{
	{Clave: "open", Etiqueta: "abierto"},
	{Clave: "pending_approval", Etiqueta: "por aprobar"},
	{Clave: "needs_info", Etiqueta: "falta info"},
	{Clave: "confirmed", Etiqueta: "confirmado"},
	{Clave: "deposit_requested", Etiqueta: "seña solicitada"},
	{Clave: "deposit_paid", Etiqueta: "señado"},
	{Clave: "settled", Etiqueta: "saldado"},
	{Clave: "rejected", Etiqueta: "rechazado"},
	{Clave: "cancelled", Etiqueta: "cancelado"},
	{Clave: "abandoned", Etiqueta: "abandonado"},
	{Clave: "expired", Etiqueta: "vencido (histórico)"},
}

// etiquetasDeEstado indexa el diccionario por clave (se arma una vez, al cargar el paquete).
var etiquetasDeEstado = func() map[string]string {
	etiquetas := make(map[string]string, len(estadosDeSolicitud))
	for _, opt := range estadosDeSolicitud {
		etiquetas[opt.Clave] = opt.Etiqueta
	}
	// `closed` es la clave con la que el módulo cart cierra desde el Plan 016 y que sigue viva en BD.
	// La API la normaliza a `confirmed` al leer, así que en teoría no llega hasta aquí; se traduce
	// igualmente para que una fila legada que se colara no se vea como un estado desconocido.
	etiquetas["closed"] = "confirmado"
	return etiquetas
}()

// statusLabel devuelve el nombre de negocio de un estado. Es el helper de plantilla (FuncMap, ver
// server.go), y conserva el nombre que tenía en el BFF porque es el que usan las dos pantallas que
// se mudan: la lista (T7.2) y el detalle (T7.3).
//
// 🔴 Una clave que no esté en el diccionario se devuelve TAL CUAL: es preferible que la dueña vea
// `deposit_refunded` a que la pantalla se invente una traducción o esconda un estado que existe.
func statusLabel(estado string) string {
	if etiqueta, ok := etiquetasDeEstado[estado]; ok {
		return etiqueta
	}
	return estado
}

// CambiarEstadoSolicitud aplica la transición pedida en el desplegable del detalle (T7.4).
//
// 🔒 EL REPARTO DE DESENLACES, Y POR QUÉ ÉSTE CAE ENTERO DEL LADO DEL 303 (D-047.16):
//
//	sin estado ........ 303 + flash. ES validación LOCAL y aun así NO repinta, y la diferencia con
//	                    el formulario de líneas es que aquí no hay NADA QUE PERDER: el control es un
//	                    `<select>` sobre una lista cerrada que arma el servidor, no texto tecleado.
//	                    La excepción del 400 existe para conservar lo escrito; sin nada escrito, es
//	                    la misma regla con la que el borrado de un disparador va por 303.
//	error de la API ... 303 + flash. Incluidos el 422 de transición inválida y el 409 de carrera.
//	éxito ............. 303 + flash.
//
// 🔴 Lo que se PIERDE respecto del origen, dicho en voz alta: el BFF repintaba el 422 con los
// destinos que trae ese rechazo (`allowed`) y el 409 con el estado recién releído. Tras el 303 la
// página se relee igual, así que el 409 no pierde nada —el estado actual está delante mientras se
// lee el aviso—; el 422 sí pierde su lista, y se acepta porque `allowed_transitions` del GET es la
// misma autoridad y viene más fresca. Ver solicitudDetalleView.DesdeRechazo.
func (h *AdminHandler) CambiarEstadoSolicitud(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))

	// Sin empresa no hay solicitud que mover, y la API respondería 403 sobre una causa que no es esa.
	// Un id vacío no lo produce el router: es una guarda para no gastar el viaje.
	if sinEmpresa(c) || id == "" {
		c.Redirect(http.StatusSeeOther, rutaSolicitudes)
		return
	}
	destino := solicitudURL(id)

	// El desplegable lleva `required`, pero eso lo cumple el NAVEGADOR: un POST hecho a mano llega
	// sin estado. No se adivina ninguno —mover una solicitud al azar es peor que no moverla— y no se
	// gasta el viaje para recibir el 400 que ya se sabe.
	estado := formValue(c, campoEstado)
	if estado == "" {
		redirectWith(c, destino, flashSolicitudSinEstado, "")
		return
	}

	var cambioErr error
	code := flashCodeForEstado(h.auth.withAuthRetry(c, func(accessToken string) error {
		_, err := h.api.Intakes.SetIntakeStatus(c.Request.Context(), accessToken, id, estado)
		cambioErr = err
		return err
	}))
	if sessionIsDead(cambioErr) {
		h.auth.expireSession(c)
		return
	}
	if code != "" {
		// El estado pedido NO entra en el log: no es PII, pero tampoco hace falta, y el log de esta
		// consola no acumula datos de negocio por comodidad de depuración.
		slog.Warn("no se pudo cambiar el estado de la solicitud", "codigo", code, "error", cambioErr)
	}
	redirectWith(c, destino, code, flashSolicitudEstadoCambiado)
}
