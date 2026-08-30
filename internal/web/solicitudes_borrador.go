package web

import (
	"strconv"
	"strings"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
)

// solicitudes_borrador.go es EL BORRADOR que lee la dueña (design §7.5), mudado de
// `intakes_draft.go` del BFF (Plan 047 · T7.3).
//
// 🔴 SALE DE LA ÚLTIMA REVISIÓN `interpreted`, NO DE `Items`, y la diferencia no es de gusto: `Items`
// son las líneas RESUELTAS —las que la plataforma factura— con un `unit_price` que no sabe decir
// «todavía no hay precio», y la línea `unmatched` —justo la que hay que atender— NI SIQUIERA ESTÁ
// ahí. Sin este bloque esa línea no se ve en ninguna pantalla del ecosistema.

// etiquetaAudio es el rótulo con el que el borrador nombra un audio del cliente. Es el mismo literal
// que pone la plataforma (`anclaje.EtiquetaAudio`) y solo se usa como RESPALDO: si la referencia trae
// su etiqueta, se pinta la suya.
const etiquetaAudio = "🎙️ audio del cliente — escúchalo"

// borradorAdjunto es un adjunto del cliente ya redactado para la pantalla.
//
// Lleva TEXTO y no enlace, y es una decisión (D-044.52 §1): la referencia del adjunto es OPACA
// —nunca una URL— y hoy la API no publica ninguna ruta por la que descargarlo. Un `<a href>`
// construido sobre ella llevaría a ninguna parte, y un enlace roto es peor que una mención.
type borradorAdjunto struct {
	Texto string
	Audio bool
}

// borradorLinea es UN renglón del borrador: la parte que se lee y la parte que se teclea, juntas,
// porque en esta pantalla son la misma fila.
//
// Los valores editables van como TEXTO por lo mismo que en el formulario de líneas: es lo que la
// persona tecleó y hay que poder repintárselo tal cual cuando algo se rechaza.
type borradorLinea struct {
	// Numero es el número de fila 1-based ENTRE LAS EDITABLES, que es exactamente el índice con el
	// que viajarán los campos del formulario. La línea de envío no entra en la numeración porque
	// tampoco entra en el formulario.
	Numero int
	Clase  string
	// ClaseEtiqueta es cómo se llama esa clase de línea en la pantalla.
	ClaseEtiqueta string

	SKU             string
	Etiqueta        string
	Personalizacion string
	Cantidad        string
	// PrecioUnitario es el precio ya formateado, y CADENA VACÍA cuando la línea no lo tiene. Aquí
	// está el corazón del bloque: `null` y `0` llegan distintos desde el apiclient y tienen que
	// seguir distintos hasta el HTML. Un `printf "%.2f"` incondicional los colapsa en «0.00», que le
	// diría a la dueña que esa torta es gratis.
	PrecioUnitario string
	TienePrecio    bool

	// PendientePrecio es «esta línea es de las que hay que poner a precio». SOLO lo son las
	// `unmatched` sin precio: son las que el catálogo no supo resolver.
	PendientePrecio bool
	// NotaPendiente es el motivo cuando la línea NO tiene precio pero TAMPOCO cuenta como pendiente:
	// una `matched` con varias presentaciones espera a que se ELIJA (el precio existe, uno por
	// variante, en el catálogo) y el envío espera a confirmar zona. Contarlas juntas haría que la
	// pantalla dijera «3 líneas pendientes de precio» donde el §7.5 dice 1.
	NotaPendiente string

	Evidencia string
	Nota      string
	// Tamano es lo pedido sin colapsar («10-12 porciones») y Empaque la unidad de venta que trajo P4
	// («paquete de 30»). Viajan porque son lo que distingue dos líneas que se llaman igual.
	Tamano  string
	Empaque string

	Variantes []apiclient.IntakeVariantOption
	Match     *apiclient.IntakeLineMatch
	Adjuntos  []borradorAdjunto
}

// borradorView es el §7.5 entero: lo que la dueña lee para decidir.
type borradorView struct {
	// Revision es la revisión `interpreted` de la que sale todo esto.
	Revision int
	// Posteriores son las revisiones que vinieron DESPUÉS. La `interpreted` se congela cuando el LLM
	// interpreta y NO se reescribe cuando se corrige, así que con correcciones encima este bloque
	// enseña la lectura original: se avisa en vez de dejar creer que es lo vigente.
	Posteriores int

	Lineas []borradorLinea
	// Envio son las líneas que pone la plataforma. Van FUERA de la tabla editable porque no se editan
	// aquí —la plataforma rechaza su prefijo reservado— y sacarlas de la tabla es lo que permite que
	// el número de fila y el índice del formulario sean el mismo número.
	Envio []borradorLinea

	// TotalParcial es el total que manda LA PLATAFORMA, y es el total parcial del §7.5 sin que esta
	// capa sume nada (INV-13): `items` solo contiene las líneas resueltas —la `unmatched` ni siquiera
	// está—, así que ese número ya excluye exactamente lo que falta por poner a precio. Recalcularlo
	// aquí crearía una segunda autoridad sobre el dinero que divergiría de la primera.
	TotalParcial float64
	// PendientesPrecio es cuántas líneas esperan precio (las `unmatched` sin él).
	PendientesPrecio int
	// PendientesVariante y EnvioPendiente son las OTRAS dos ausencias de precio, contadas APARTE
	// porque no son lo mismo y la pantalla no puede sumarlas al conteo de arriba.
	PendientesVariante int
	EnvioPendiente     bool

	FechaEntrega string
	// 🔴 NO HAY `TextoOriginal` AQUÍ, y no es un olvido: el literal del cliente lo pinta el bloque de
	// comparación (§7.6) y solo él. Copiarlo también a esta vista daría DOS copias en memoria y dos
	// sitios en el HTML de un texto que una persona escribió por WhatsApp, y cada copia es un sitio
	// más del que se puede escapar.
	Analisis apiclient.IntakeAnalysis
	Adjuntos []borradorAdjunto

	// Preguntas son las preparadas y PreguntasConocidas si la plataforma llegó a publicar la clave:
	// ausente ⇒ el plan de la empresa no incluye `llm_intake`, que NO es lo mismo que «el LLM no
	// tenía nada que preguntar».
	Preguntas          []string
	PreguntasConocidas bool

	// Editable es si el estado admite corregir. Sale del mismo espejo que el formulario de líneas
	// (`estadoEditable`) y por la misma razón: la plataforma publica los destinos del ciclo de vida
	// pero no desde dónde se edita.
	Editable bool

	// Defectos son los problemas de la última tentativa de CORREGIR (vacío = no hubo).
	//
	// 🔴 Son los de ESTE formulario y no los del de líneas facturables, aunque los dos tipos sean el
	// mismo y los dos bloques estén en la misma página. Cruzarlos marcaría una fila del borrador con
	// el defecto de una línea facturable, que es un número señalando a otra cosa.
	Defectos []defectoLinea
}

// conLoTecleado devuelve al borrador lo que la persona escribió, con sus defectos marcados. Nil ⇒ no
// hubo repintado y el borrador se queda con lo que dice la plataforma.
//
// 🔑 SOLO PISA LOS CINCO CAMPOS EDITABLES, y por eso no reemplaza las filas como hace el formulario
// de líneas: cada renglón de aquí lleva además lo que NO se teclea —la clase, la evidencia, el
// match, las variantes, los adjuntos— y sustituirlo entero dejaría una tabla sin la mitad de lo que
// hace legible la interpretación.
//
// 🔴 Y SOLO SI LOS TAMAÑOS CUADRAN. Un envío con más o menos filas que las que hay en la revisión no
// se puede emparejar sin adivinar, y adivinar aquí significa poner un precio en la línea de otro
// artículo. Con tamaños distintos se prefiere perder lo tecleado —el aviso sigue diciendo qué
// pasó— a colocarlo mal, que es un fallo que nadie ve.
func (v *borradorView) conLoTecleado(filas []filaLinea, defectos []defectoLinea) {
	if v == nil || len(filas) == 0 {
		return
	}
	v.Defectos = defectos
	if len(filas) != len(v.Lineas) {
		return
	}
	for i := range v.Lineas {
		v.Lineas[i].SKU = filas[i].SKU
		v.Lineas[i].Etiqueta = filas[i].Etiqueta
		v.Lineas[i].Personalizacion = filas[i].Personalizacion
		v.Lineas[i].Cantidad = filas[i].Cantidad
		v.Lineas[i].PrecioUnitario = filas[i].PrecioUnitario
	}
}

// TotalText redacta el total parcial CON el conteo de lo que falta, que es lo que pide el §7.5: un
// número suelto no dice que sea parcial, y se leería como el precio final del pedido.
func (v *borradorView) TotalText() string {
	total := strconv.FormatFloat(v.TotalParcial, 'f', 2, 64)
	if v.PendientesPrecio == 0 {
		return "Total parcial: " + total + " (ninguna línea pendiente de precio)"
	}
	return "Total parcial: " + total + " (" +
		cuenta(v.PendientesPrecio, "línea pendiente de precio", "líneas pendientes de precio") + ")"
}

// VariantesText redacta las líneas que esperan a que se ELIJA presentación, y dice en voz alta por
// qué NO están en el conteo de arriba: su precio existe —hay uno por variante en el catálogo—, lo que
// falta es la elección. Vacío cuando no hay ninguna.
func (v *borradorView) VariantesText() string {
	if v.PendientesVariante == 0 {
		return ""
	}
	verbo := "espera"
	if v.PendientesVariante != 1 {
		verbo = "esperan"
	}
	return cuenta(v.PendientesVariante, "línea", "líneas") + " " + verbo + " a que elijas " +
		"presentación: no cuenta como línea pendiente de precio, porque el precio ya está en el " +
		"catálogo —falta elegir cuál—."
}

// PosterioresText avisa de que este bloque no es lo vigente. Vacío cuando es la última revisión.
//
// Se redacta en Go y no en la plantilla porque lleva un PLURAL: en el BFF esto era la única llamada
// de plantilla a `cuenta`, y una clave del FuncMap por un solo uso es una cadena que no compila nadie.
func (v *borradorView) PosterioresText() string {
	if v.Posteriores == 0 {
		return ""
	}
	return "Después de esta interpretación hay " + cuenta(v.Posteriores, "revisión", "revisiones") +
		" más: este bloque no se reescribe al corregir. Lo vigente son las líneas de la ficha de arriba."
}

// AnalisisText redacta quién interpretó. `Provider` sale CADENA VACÍA en la interpretación normal
// —solo lo rellenan las revisiones nacidas de un re-análisis—, y eso se dice como «no registrada» en
// vez de pintar un proveedor llamado «».
//
// 🔑 La redacción sale de `viaText` y no de un literal propio: el bloque de comparación dice lo mismo
// de este mismo dato unos centímetros más arriba, y dos frases distintas para el mismo hecho en la
// misma página harían dudar de si hablan de lo mismo.
func (v *borradorView) AnalisisText() string {
	text := "Interpretado por " + viaText(v.Analisis.Provider)
	if v.Analisis.Model != "" {
		text += " · modelo " + v.Analisis.Model
	}
	if v.Analisis.ReanalyzedFrom != nil {
		text += " · re-análisis de la revisión " + strconv.Itoa(*v.Analisis.ReanalyzedFrom)
	}
	return text
}

// HayAudio responde si el cliente mandó algo que se escucha, mirando tanto la cabecera del borrador
// como las líneas. Es lo que decide si sale el rótulo del audio.
func (v *borradorView) HayAudio() bool {
	for _, m := range v.Adjuntos {
		if m.Audio {
			return true
		}
	}
	for _, l := range v.Lineas {
		for _, m := range l.Adjuntos {
			if m.Audio {
				return true
			}
		}
	}
	return false
}

// borradorDe arma el §7.5 desde la ÚLTIMA revisión `interpreted` del detalle. Devuelve nil cuando no
// hay ninguna o cuando su payload no se puede leer: el borrador no se pinta a medias ni se sustituye
// por `items`, que es otra cosa (y que la pantalla sigue pintando arriba).
func borradorDe(detalle *apiclient.IntakeDetail) *borradorView {
	rev := detalle.LastRevisionOf(apiclient.RevisionKindInterpreted)
	if rev == nil {
		return nil
	}
	payload, err := apiclient.DecodeInterpretation(rev.Payload)
	if err != nil {
		return nil
	}

	view := &borradorView{
		Revision:           rev.RevisionNo,
		Posteriores:        detalle.RevisionsAfter(rev.RevisionNo),
		TotalParcial:       detalle.Total,
		FechaEntrega:       payload.DeliveryDate,
		Analisis:           payload.Analysis,
		Adjuntos:           adjuntosDe(payload.MediaRefs),
		Preguntas:          payload.Questions(),
		PreguntasConocidas: payload.QuestionsKnown(),
		Editable:           detalle.Status == estadoEditable,
	}
	for _, line := range payload.Lines {
		view.anade(lineaDe(line))
	}
	return view
}

// anade coloca la línea donde le toca y lleva las cuentas del §7.5 en el mismo sitio en que se decide
// la clase: si el reparto y el conteo vivieran separados, una clase nueva entraría en uno y no en el
// otro, y el conteo mentiría sin que nada fallara.
func (v *borradorView) anade(linea borradorLinea) {
	if linea.Clase == apiclient.LineKindShipping {
		v.Envio = append(v.Envio, linea)
		if !linea.TienePrecio {
			v.EnvioPendiente = true
		}
		return
	}
	linea.Numero = len(v.Lineas) + 1
	v.Lineas = append(v.Lineas, linea)
	switch {
	case linea.PendientePrecio:
		v.PendientesPrecio++
	case !linea.TienePrecio && len(linea.Variantes) > 0:
		v.PendientesVariante++
	}
}

// lineaDe traduce una línea del payload a lo que la pantalla necesita.
func lineaDe(line apiclient.IntakeDraftLine) borradorLinea {
	out := borradorLinea{
		Clase:           line.Kind,
		ClaseEtiqueta:   claseDeLineaLabel(line.Kind),
		SKU:             line.SKU,
		Etiqueta:        line.Label,
		Personalizacion: line.Customization,
		Cantidad:        strconv.Itoa(line.Qty),
		TienePrecio:     line.HasPrice(),
		Evidencia:       line.Evidence,
		Nota:            line.Note,
		Tamano:          rangoText(line.Range),
		Empaque:         empaqueText(line.UnitKind, line.PackageSize),
		Variantes:       line.VariantOptions,
		Match:           line.Match,
		Adjuntos:        adjuntosDe(line.MediaRefs),
	}
	if out.TienePrecio {
		// El precio se re-imprime con dos decimales, igual que la ficha de arriba: dos cifras
		// distintas para el mismo precio en la misma página harían dudar de cuál es.
		out.PrecioUnitario = strconv.FormatFloat(line.Price(), 'f', 2, 64)
		return out
	}
	// Sin precio NO se imprime ningún número: ni «0.00» ni «0». El hueco se queda vacío para que lo
	// rellene la dueña, y la pantalla dice por qué está vacío.
	switch {
	case line.Kind == apiclient.LineKindUnmatched:
		out.PendientePrecio = true
	case len(line.VariantOptions) > 0:
		out.NotaPendiente = "falta elegir presentación"
	default:
		out.NotaPendiente = "sin precio"
	}
	return out
}

// claseDeLineaLabel traduce la clase de línea al nombre que se ve. Una clase desconocida se devuelve
// TAL CUAL, misma doctrina que statusLabel: antes una clave cruda que una traducción inventada o una
// línea escondida.
func claseDeLineaLabel(clase string) string {
	switch clase {
	case apiclient.LineKindMatched:
		return "del catálogo"
	case apiclient.LineKindUnmatched:
		return "sin match"
	case apiclient.LineKindShipping:
		return "envío"
	}
	return clase
}

// rangoText redacta el tamaño pedido sin colapsarlo («10-12 porciones»): el rango es lo que el
// cliente dijo, y quedarse con un extremo decidiría por él.
func rangoText(r *apiclient.IntakeLineRange) string {
	if r == nil || (r.Min == 0 && r.Max == 0) {
		return ""
	}
	size := strconv.Itoa(r.Min)
	if r.Max != r.Min {
		size += "-" + strconv.Itoa(r.Max)
	}
	if r.Unit != "" {
		size += " " + r.Unit
	}
	return size
}

// empaqueText redacta la unidad de venta que trajo P4 («paquete de 30»). Sin ella, «un paquete de 30»
// se pierde en cuanto la línea toma el nombre del catálogo.
func empaqueText(unitKind string, packageSize int) string {
	if unitKind == "" && packageSize == 0 {
		return ""
	}
	if packageSize <= 0 {
		return unitKind
	}
	if unitKind == "" {
		return "de " + strconv.Itoa(packageSize)
	}
	return unitKind + " de " + strconv.Itoa(packageSize)
}

// adjuntosDe redacta los adjuntos. Un audio SIN etiqueta cae en el literal de la plataforma, y una
// clase que este cliente no conozca se nombra por su clave: callar un adjunto dejaría a la dueña
// creyendo que el cliente solo escribió.
func adjuntosDe(refs []apiclient.IntakeMediaRef) []borradorAdjunto {
	if len(refs) == 0 {
		return nil
	}
	out := make([]borradorAdjunto, 0, len(refs))
	for _, ref := range refs {
		out = append(out, borradorAdjunto{Texto: adjuntoText(ref), Audio: ref.IsAudio()})
	}
	return out
}

func adjuntoText(ref apiclient.IntakeMediaRef) string {
	if label := strings.TrimSpace(ref.Label); label != "" {
		return label
	}
	switch ref.Kind {
	case apiclient.MediaKindAudio, apiclient.MediaKindPTT, apiclient.MediaKindVoice:
		return etiquetaAudio
	case apiclient.MediaKindImage:
		return "🖼️ imagen del cliente"
	case apiclient.MediaKindVideo:
		return "🎬 vídeo del cliente"
	case apiclient.MediaKindDocument:
		return "📄 documento del cliente"
	}
	return "adjunto del cliente (" + ref.Kind + ")"
}
