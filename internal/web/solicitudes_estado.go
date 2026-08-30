package web

// solicitudes_estado.go es el diccionario de PRESENTACIÓN de los estados de una solicitud.
//
// Que quede claro qué NO es, porque la distinción es la que sostiene esta pantalla: aquí no hay
// máquina de estados. No se dice desde dónde se llega a cada estado ni adónde se puede ir —eso lo
// decide `internal/intakes/status.go` de la plataforma y viaja por la API—. Esto es una tabla de
// nombres, y su peor fallo posible es cosmético: si mañana aparece un estado nuevo, statusLabel
// devuelve la clave cruda y el filtro no lo lista, pero nada opera mal ni se ofrece una transición
// falsa.
//
// 📌 Viaja en T7.2 y no en T7.3, aunque el plan la ponga allí: `intakes.html` —la pantalla de esta
// casilla— la usa para rotular el estado de cada fila, así que sin ella la lista no se puede pintar.
// T7.3 la da por hecha.

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
