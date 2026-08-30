package web

// tablaVista es lo que consume el partial `data_table`: el armazón de una tabla de datos.
//
// `Page` es el dot de la PÁGINA, no la lista de filas. El partial se lo entrega tal cual a la
// plantilla de filas, y por eso dentro de ella `$` sigue siendo lo mismo que era cuando la tabla
// estaba escrita inline —`$.CSRFToken` incluido—. Es lo que permite mover una tabla al partial sin
// tocar el HTML de sus celdas, que es lo que el criterio de T6.2 exige demostrar.
type tablaVista struct {
	ID           string
	Caption      string
	Columns      []string
	RowsTemplate string
	Page         any
}

// tabla arma el descriptor desde la plantilla, que es el único sitio donde se sabe qué columnas
// tiene cada pantalla.
//
// Existe porque html/template no tiene forma de construir un valor compuesto: sin este helper, el
// descriptor habría que armarlo en el handler, y una tabla se describiría a un fichero de distancia
// de la pantalla que la pinta. Las columnas son variádicas a propósito —son una LISTA, no un
// catálogo de banderas—: si algún día hiciera falta un parámetro POR COLUMNA, eso sería la señal de
// que ese caso necesita su propio partial y no un argumento más aquí.
func tabla(page any, id, caption, rowsTemplate string, columnas ...string) tablaVista {
	return tablaVista{
		ID:           id,
		Caption:      caption,
		Columns:      columnas,
		RowsTemplate: rowsTemplate,
		Page:         page,
	}
}
