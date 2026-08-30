package web

import (
	"strconv"
	"strings"
	"time"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
)

// solicitudes_acciones.go son LAS ACCIONES QUE LE HABLAN AL CLIENTE (Plan 044 · T4.2/T4.3/T4.4 y
// Plan 047 · T2.4), mudadas de las VISTAS de `intakes_actions.go` e `intakes_quote.go` del BFF
// (Plan 047 · T7.3).
//
// 📌 Aquí viajan SOLO las vistas: los cuatro handlers (aprobar, corregir, pedir información y sugerir
// la respuesta) y los mappers de sus rechazos son T7.4, T7.5 y T7.6. Esta casilla pinta los
// formularios; quien los atiende llega después.
//
// Es una vista PROPIA y no una fila más del bloque de estado a propósito: estas acciones LE HABLAN AL
// CLIENTE por WhatsApp y dejan revisión; el desplegable de estado solo mueve la etiqueta del ciclo de
// vida y no le escribe a nadie. Confundirlos sería ofrecer «responderle al cliente» donde no se
// responde.

// paywallSugerencia es lo que se lee cuando el plan no incluye la redacción automática. Va aparte del
// de Regenerar aunque lleve a contratar LO MISMO: son dos acciones distintas, y decirlo con las
// palabras de la otra dejaría a la dueña buscando un botón que no es el que tiene delante.
const paywallSugerencia = "El plan de tu empresa no incluye el análisis con IA (`" + featureLLMIntake +
	"`), así que la plataforma no puede redactar la respuesta. La solicitud se responde igual: el " +
	"campo de abajo trae la propuesta que arma esta consola con las líneas, y se edita a mano."

// sugerenciaView es el botón «Sugerir la respuesta» y POR QUÉ no se puede pulsar.
//
// El botón NUNCA se esconde, igual que Regenerar y por lo mismo: sin `llm_intake` sale DESHABILITADO
// con la razón delante. El gate DURO de esta pantalla sigue siendo `cart_basic`, que corta la ruta
// entera antes de renderizar (solicitudes_gate.go).
type sugerenciaView struct {
	Habilitado bool
	// Razon es por qué NO se puede pulsar (vacío cuando Habilitado).
	Razon string
	// Paywall es si el motivo es del PLAN, que decide si el aviso lleva a contratar o a otro sitio.
	Paywall bool
	// MaxEspera es CUÁNTO espera esta página antes de rendirse, en palabras y ya redondeado.
	//
	// 🔴 SALE DEL PLAZO CONFIGURADO, no de un número escrito en la plantilla, y esa es toda la razón
	// de que el campo exista. En el BFF el aviso decía «unos segundos» porque se redactó cuando la
	// espera moría a los 15 s; en cuanto la ruta pasó a esperar de verdad, la frase se quedó
	// mintiendo —lo medido fueron 24,8-35,5 s— sin que nada fallara. Colgado del plazo, mover el
	// plazo mueve el texto.
	MaxEspera string
}

// accionesView son las acciones tal como las pinta la plantilla.
type accionesView struct {
	// RespuestaPropuesta es la cotización PROPUESTA, que la dueña edita antes de mandarla. Es una
	// propuesta y nada más: lo que la revisión guarda es byte a byte lo que se envió, y su autora es
	// ella (D-044.19).
	RespuestaPropuesta string
	// Pregunta es la propuesta, tomada de las que preparó el LLM. Nunca sale sola (INV-1): esto es lo
	// que aparece en el formulario, no lo que se manda.
	Pregunta string
	// Preguntas son todas las preparadas, para poder copiar otra.
	Preguntas []string
	// PreguntasConocidas es falso cuando la plataforma NO publicó la clave, o sea cuando el plan no
	// incluye `llm_intake`. No es lo mismo que «no había nada que preguntar», y la pantalla lo dice
	// distinto.
	PreguntasConocidas bool
	// PendientesPrecio es cuántas líneas siguen sin precio. Con una sola, la aprobación va a salir
	// rechazada, y decirlo ANTES ahorra el viaje y explica por qué.
	PendientesPrecio int
	// HayBorrador es si hay un borrador que corregir. Sin él NO se ofrece el botón «Corregir»: lo
	// manda el formulario del borrador, y un botón que apunta a un formulario que no está en la
	// página no hace nada — que es peor que no ofrecerlo, porque parece que sí.
	HayBorrador bool
	// Sugerencia es la acción de OTRA naturaleza: no le habla al cliente, no escribe en la solicitud
	// y no la mueve de estado. Solo redacta una propuesta y la deja en el campo de aquí al lado. Vive
	// en esta vista —y no en una tarjeta propia— porque su resultado ES este formulario: separarla
	// dejaría un botón lejos del campo que precarga.
	Sugerencia sugerenciaView
}

// accionesDe arma las acciones. Devuelve nil cuando el estado no las admite: un botón que la
// plataforma va a rechazar no se ofrece (misma regla que el desplegable de un estado terminal y que
// el formulario de líneas).
func accionesDe(detalle *apiclient.IntakeDetail, borrador *borradorView, ent entitlementsView,
	espera time.Duration) *accionesView {
	if detalle.Status != estadoEditable {
		return nil
	}
	view := &accionesView{
		RespuestaPropuesta: propuestaDeRespuesta(detalle, borrador),
		HayBorrador:        borrador != nil,
		Sugerencia:         sugerenciaDe(ent, espera),
	}
	if borrador != nil {
		view.Preguntas = borrador.Preguntas
		view.PreguntasConocidas = borrador.PreguntasConocidas
		view.PendientesPrecio = borrador.PendientesPrecio
		if len(borrador.Preguntas) > 0 {
			view.Pregunta = borrador.Preguntas[0]
		}
	}
	return view
}

// sugerenciaDe decide si el botón se puede pulsar, y si no, POR QUÉ.
//
// Solo hay UN motivo que se pueda anticipar aquí —la capacidad—, y no es un hueco: los otros dos
// desenlaces malos (líneas sin precio y solicitud sin líneas) dependen del estado del borrador EN LA
// PLATAFORMA, y adivinarlos desde el espejo local sería apagar el botón por una foto vieja. Llegan
// como RECHAZO, y su traducción es de T7.6.
func sugerenciaDe(ent entitlementsView, espera time.Duration) sugerenciaView {
	view := sugerenciaView{MaxEspera: esperaText(espera)}
	if !ent.Has(featureLLMIntake) {
		view.Paywall = true
		view.Razon = paywallSugerencia
		return view
	}
	view.Habilitado = true
	return view
}

// esperaText pone en palabras lo que la página espera antes de rendirse.
//
// Redondea a propósito: el aviso lo lee una persona que está decidiendo si quedarse mirando la
// pantalla, y «57 segundos» no le sirve mejor que «un minuto» — pero «unos segundos» sí le sirve
// PEOR, porque describe una espera que no es la que va a tener. El corte del minuto es donde la
// unidad deja de ayudar.
func esperaText(espera time.Duration) string {
	if espera <= 0 {
		// Sin plazo conocido no se inventa una magnitud: se dice lo único cierto, que espera a que
		// llegue. Es el caso de una Config armada a mano (los tests), no el de producción.
		return "que la plataforma conteste"
	}
	if espera < time.Minute {
		return strconv.Itoa(int(espera.Round(time.Second)/time.Second)) + " segundos"
	}
	minutos := int(espera.Round(time.Minute) / time.Minute)
	if minutos <= 1 {
		return "un minuto"
	}
	return strconv.Itoa(minutos) + " minutos"
}

// propuestaDeRespuesta redacta la cotización que se le propone a la dueña.
//
// Si la revisión ya trae un texto compuesto, ése; si no, se arma con las líneas del borrador. Y se
// arma SIN hacer una sola cuenta: los precios se copian tal cual y el total es el que manda la
// plataforma (INV-13). Multiplicar aquí crearía una segunda autoridad sobre el dinero, y el día que
// las dos discreparan el cliente tendría delante la de esta pantalla.
func propuestaDeRespuesta(detalle *apiclient.IntakeDetail, borrador *borradorView) string {
	if borrador == nil {
		return ""
	}
	if rev := detalle.LastRevisionOf(apiclient.RevisionKindInterpreted); rev != nil {
		if texto := strings.TrimSpace(rev.RenderedText); texto != "" {
			return texto
		}
	}

	var b strings.Builder
	b.WriteString("Tu pedido:\n")
	for _, linea := range borrador.Lineas {
		b.WriteString("- " + linea.Cantidad + " × " + linea.Etiqueta)
		if linea.Tamano != "" {
			b.WriteString(" (" + linea.Tamano + ")")
		}
		if linea.Personalizacion != "" {
			b.WriteString(" · " + linea.Personalizacion)
		}
		if linea.TienePrecio {
			b.WriteString(" — " + linea.PrecioUnitario + " c/u")
		} else {
			b.WriteString(" — pendiente de precio")
		}
		b.WriteString("\n")
	}
	for _, linea := range borrador.Envio {
		b.WriteString("- " + linea.Etiqueta)
		if linea.TienePrecio {
			b.WriteString(" — " + linea.PrecioUnitario)
		} else if linea.Nota != "" {
			b.WriteString(" — " + linea.Nota)
		}
		b.WriteString("\n")
	}
	if borrador.FechaEntrega != "" {
		b.WriteString("Entrega: " + borrador.FechaEntrega + "\n")
	}
	if detalle.CustomerNote != "" {
		b.WriteString("Indicación: " + detalle.CustomerNote + "\n")
	}
	b.WriteString("Total: " + strconv.FormatFloat(detalle.Total, 'f', 2, 64))
	return b.String()
}
