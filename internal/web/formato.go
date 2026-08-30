package web

import (
	"strconv"
	"strings"
	"time"
)

// formato.go es CÓMO ESCRIBE ESTA CONSOLA lo que llega de la API sin forma de leerse: los instantes
// y los plurales (Plan 047 · T7.3).
//
// 🔴 ES OBRA NUEVA, NO UNA COPIA. Se midió antes de escribirla: ni esta consola ni el BFF tenían un
// solo helper de fecha. Aquí el único campo de fecha se pintaba crudo (`flujos.html`, la columna
// «Creado»), y el BFF pinta el `created_at` tal como viene con un «(UTC)» ESCRITO A MANO al lado
// (`intake-detail.html:33`). Las dos formas fallan igual de callado: `2026-08-20T10:00:00Z` no es
// una fecha que una persona lea, y un rótulo escrito a mano no se entera el día que el valor deje de
// venir en UTC.

// Los dos formatos con los que esta consola escribe un instante.
//
// 🔒 DÍA PRIMERO, y el huso VA DENTRO DEL FORMATO. Las dos decisiones tienen motivo:
//
//   - `02/01/2006` porque toda esta consola está en español y quien la usa lee día/mes; con
//     `01/02` el 8 de septiembre y el 9 de agosto se escriben igual y nada avisa de cuál es.
//   - «UTC» lo pone ESTA FUNCIÓN, que es la misma que hace la conversión. Ese es el arreglo del
//     defecto del BFF: allí el rótulo lo escribe la plantilla al lado de un valor que nadie
//     convierte, así que son dos sitios que pueden dejar de decir lo mismo sin que nada falle.
//
// formatoFechaSola NO lleva huso, y tampoco es un olvido: una fecha sin hora (`2026-08-25`, lo que
// trae `delivery_date`) no afirma ningún instante, y ponerle «UTC» sería añadirle una precisión que
// el dato no tiene.
const (
	formatoFechaHora = "02/01/2006 15:04 UTC"
	formatoFechaSola = "02/01/2006"
)

// fecha escribe un instante de la API para que lo lea una persona. Es helper de plantilla (FuncMap,
// ver server.go) y no un campo de vista porque los instantes llegan dentro de tipos del `apiclient`
// —`Intake.CreatedAt`, `IntakeRevision.CreatedAt`— que pintan DOS pantallas: la bandeja y el detalle.
// Copiarlos a una vista para formatearlos habría dado dos redacciones del mismo dato.
//
// 🔒 EL HUSO ES UTC, Y ES UNA DECISIÓN CON MOTIVO ESCRITO. Las tres alternativas y por qué se caen:
//
//   - El huso de quien mira: NO SE PUEDE. Esta consola no sirve una línea de JavaScript (ADR-0035) y
//     el navegador no manda su zona en ninguna cabecera. Es la única que sería "la buena", y no está
//     al alcance.
//   - Un huso de negocio fijo en el código (`America/Guayaquil`, que es donde está el tenant de UAT:
//     sus teléfonos son +593): sería INVENTARSE UN HECHO QUE LA PLATAFORMA NO PUBLICA. wApp es
//     multi-empresa y ni el detalle ni el listado dicen dónde opera el negocio, así que la primera
//     empresa fuera de ese huso leería mal —y no solo la hora: a las 22:00 UTC, Guayaquil está en el
//     DÍA ANTERIOR, o sea que se equivocaría la FECHA—. Y se equivocaría en silencio.
//   - El huso del servidor (`time.Local`): peor todavía, porque depende de dónde corra el binario:
//     la misma solicitud se leería distinta en el VPS y en el portátil de quien la depura.
//
// Queda UTC, que es lo que la plataforma manda, DICHO EN VOZ ALTA en el propio texto. No es la
// lectura más cómoda; es la única que no miente. El día que la API publique el huso del negocio,
// esta función es el único sitio que hay que tocar.
//
// 🔴 Un valor que no se puede leer se devuelve TAL CUAL, misma doctrina que statusLabel: es
// preferible que la dueña vea `2026-08-20T10:00:00Z` a que la pantalla esconda el dato o se invente
// una fecha. Y el vacío devuelve vacío: quien lo pinta decide si pone un guion.
func fecha(valor string) string {
	v := strings.TrimSpace(valor)
	if v == "" {
		return ""
	}
	// RFC3339 es lo que publica la plataforma (`created_at`, `updated_at`, `literal_pruned_at`), con
	// o sin fracción de segundo: time.Parse acepta las dos con este layout.
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC().Format(formatoFechaHora)
	}
	if t, err := time.Parse(time.DateOnly, v); err == nil {
		return t.Format(formatoFechaSola)
	}
	return valor
}

// cuenta arma «1 revisión» / «5 revisiones» sin que el singular se cuele en plural.
//
// 🔴 NO ES helper de plantilla, y ahí se separa del BFF a propósito. Allí `cuenta` está en el FuncMap
// por UN uso (`intake-detail.html:216`), y una clave del FuncMap es una cadena que no compila nadie:
// renombrarla deja `vet` y el linter en verde y rompe el render. Aquí los textos con número se
// redactan en Go —es lo que ya hacía `avisoSolicitudes` de la bandeja— y la plantilla pinta el
// resultado.
//
// Copias de esta función en el ecosistema al escribirla: UNA (wapp-guardian-bff
// `integrations_handler.go:381`, que se queda allí porque la usan tres consumidores que NO son de la
// bandeja). Ésta es la segunda, no la tercera, así que INV-08 no se activa: subirla a
// `wapp-shared/web` para un solo consumidor externo sería adelantar una extracción sobre seis líneas.
// Si aparece una TERCERA, sube.
func cuenta(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(n) + " " + plural
}
