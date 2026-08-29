package web

import (
	"regexp"
	"strings"
	"testing"
)

// Vigilancia de la BARRA SUPERIOR (arreglo del desbordamiento que introdujo T5.3).
//
// 🔴 QUÉ DEFECTO EXISTE Y POR QUÉ NINGÚN TEST DE LOS QUE HABÍA PODÍA VERLO. Al meter el selector de
// empresas en la barra, la fila pasó a pedir ~1.204 px. En un viewport de 1.154 px «Cerrar sesión»
// quedaba en 1.128..1.204, o sea 50 px FUERA del área visible: seguía en el HTML —y por eso todos
// los asertos de `strings.Contains(html, "Cerrar sesión")` seguían verdes— pero no se podía pulsar.
// Con dos empresas, eso deja a la persona sin forma de cerrar sesión desde la interfaz. El defecto es
// de PINTADO, y un aserto sobre el HTML no lo puede ver por construcción.
//
// 🔴 QUÉ VIGILA ESTO Y QUÉ NO. Un test de Go no mide píxeles: no abre un navegador, no calcula
// layout y NO puede afirmar que la barra quepa. Lo que sí puede es vigilar las tres condiciones que
// la medición en navegador demostró NECESARIAS, cada una tapando una franja de ancho distinta:
//
//	sin flex-wrap en .app-bar ............ desborda desde 1.200 px hacia abajo
//	sin flex-wrap en .app-bar__actions ... desborda 329 px a 768 px (con la barra ya envolviendo)
//	sin flex-wrap en .app-bar__tenant-form  desborda 45 px a 360 y 85 px a 320
//	sin cota ABSOLUTA en el <select> ..... desborda 136 px a 320 con un nombre de 55 caracteres
//
// Lo que queda FUERA de esta vigilancia, y hay que decirlo: (1) no se mide ni un píxel, así que un
// cambio que rompa la barra por una vía distinta —una fuente enorme, un `position: absolute`, un
// ancho fijo nuevo en una pieza— pasaría verde; (2) no se miran las hojas de `wapp-shared/ui`, que
// esta consola no controla y que también pintan dentro de la barra; (3) no se prueba ningún tema ni
// ningún navegador concreto. Esto es un candado sobre las reglas que se demostraron necesarias, no
// una prueba de que la barra quepa.

// reglaCSS son las declaraciones de UN bloque de app.css, ya sin comentarios.
type reglaCSS map[string]string

// cssDeLaConsola parsea la hoja PROPIA de esta consola (la que va embebida en el binario, `appCSS`,
// no una copia escrita a mano en el test: si la hoja cambia, esto cambia con ella).
//
// El parser es deliberadamente tonto —parte por llaves— y por eso comprueba PRIMERO que la hoja no
// tenga bloques anidados (`@media`, `@supports`, `@layer`). Si algún día los tiene, esto falla
// diciéndolo en vez de leer mal las reglas y darlas por buenas, que es como un candado se vuelve
// decorativo sin que nadie lo note.
func cssDeLaConsola(t *testing.T) map[string]reglaCSS {
	t.Helper()

	hoja := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(appCSS), "")
	for _, anidado := range []string{"@media", "@supports", "@layer", "@container"} {
		if strings.Contains(hoja, anidado) {
			t.Fatalf("app.css ya usa %s: este parser parte por llaves y leería mal las reglas. "+
				"Actualízalo antes de fiarte de lo que dice", anidado)
		}
	}

	reglas := make(map[string]reglaCSS)
	for _, bloque := range strings.Split(hoja, "}") {
		abre := strings.Index(bloque, "{")
		if abre < 0 {
			continue
		}
		decls := make(reglaCSS)
		for _, d := range strings.Split(bloque[abre+1:], ";") {
			prop, valor, ok := strings.Cut(d, ":")
			if !ok {
				continue
			}
			decls[strings.TrimSpace(prop)] = strings.TrimSpace(valor)
		}
		for _, sel := range strings.Split(bloque[:abre], ",") {
			sel = strings.TrimSpace(sel)
			if sel == "" {
				continue
			}
			// Una hoja puede repetir selector; nos quedamos con la unión de sus declaraciones,
			// que es lo que el navegador acaba aplicando.
			if previa, ya := reglas[sel]; ya {
				for k, v := range decls {
					previa[k] = v
				}
				continue
			}
			reglas[sel] = decls
		}
	}
	return reglas
}

// esFlex dice si un bloque monta un contenedor flex (los únicos a los que `flex-wrap` les significa
// algo).
func (r reglaCSS) esFlex() bool {
	return r["display"] == "flex" || r["display"] == "inline-flex"
}

var (
	reEtiqueta = regexp.MustCompile(`<(/?)([a-zA-Z][a-zA-Z0-9]*)([^>]*)>`)
	reClase    = regexp.MustCompile(`class="([^"]*)"`)
)

// vacias son las etiquetas que no abren nivel: si se metieran en la pila, la pila nunca cerraría.
var vacias = map[string]bool{"input": true, "br": true, "img": true, "meta": true, "link": true, "hr": true}

// contenedoresConControles DERIVA del layout —no de una lista escrita a mano— qué clases envuelven
// algún control de la barra.
//
// 🔴 SE DERIVA A PROPÓSITO. Una lista escrita a mano envejece en silencio: el día que la barra gane
// un contenedor nuevo alrededor de un botón nuevo, ese contenedor no estaría en la lista, nadie lo
// notaría y el defecto de T5.3 volvería exactamente por donde vino. Derivándolo, un contenedor flex
// nuevo alrededor de un control nuevo entra solo en la vigilancia.
//
// «Control» es un `<a>` o un `<button>`: lo que hay que poder alcanzar con el ratón. Los
// contenedores que solo llevan texto o un icono —`.app-bar__titles`, `.app-bar__logo`— NO entran, y
// no es un olvido: el texto ya fluye por su cuenta y exigirles `flex-wrap` sería inventar una regla
// que la medición no respalda.
func contenedoresConControles(t *testing.T) (contenedores map[string]bool, controles []string) {
	t.Helper()

	crudo, err := templatesFS.ReadFile("templates/layouts/base.html")
	if err != nil {
		t.Fatalf("no se pudo leer el layout maestro: %v", err)
	}
	// Fuera las acciones de plantilla. Los COMENTARIOS van primero y con su propia expresión: dentro
	// llevan texto como `<select onchange>` y `{{ if .TenantID }}`, así que quitarlos con la regla
	// genérica dejaría trozos sueltos que el escáner leería como etiquetas de verdad.
	html := regexp.MustCompile(`(?s)\{\{/\*.*?\*/\}\}`).ReplaceAllString(string(crudo), "")
	html = regexp.MustCompile(`\{\{[^{}]*\}\}`).ReplaceAllString(html, "")

	ini := strings.Index(html, "<header")
	fin := strings.Index(html, "</header>")
	if ini < 0 || fin < 0 || fin < ini {
		t.Fatal("no se encontró el <header> de la barra en base.html: la derivación no puede mirar nada")
	}
	barra := html[ini : fin+len("</header>")]

	contenedores = make(map[string]bool)
	var pila [][]string // clases de cada nivel abierto
	for _, m := range reEtiqueta.FindAllStringSubmatch(barra, -1) {
		cierre, etiqueta, attrs := m[1] == "/", strings.ToLower(m[2]), m[3]
		if cierre {
			if len(pila) > 0 {
				pila = pila[:len(pila)-1]
			}
			continue
		}
		if etiqueta == "a" || etiqueta == "button" {
			// Un control: todo lo que esté ABIERTO por encima lo envuelve.
			for _, nivel := range pila {
				for _, c := range nivel {
					contenedores[c] = true
				}
			}
			controles = append(controles, etiqueta)
		}
		if vacias[etiqueta] || strings.HasSuffix(strings.TrimSpace(attrs), "/") {
			continue
		}
		var clases []string
		if c := reClase.FindStringSubmatch(attrs); c != nil {
			clases = strings.Fields(c[1])
		}
		pila = append(pila, clases)
	}
	return contenedores, controles
}

// TestBarra_TodoContenedorFlexConControlesPuedeFLUIR es el candado del arreglo.
//
// El invariante: si un contenedor de la barra es flex y dentro tiene algo que pulsar, tiene que poder
// pasar a varias líneas. Un flex que no envuelve reparte el ancho que hay entre las piezas que hay, y
// cuando no llega, lo que sobra sale del área visible en vez de bajar de línea — que es LITERALMENTE
// lo que le pasó a «Cerrar sesión».
//
// Y fluir es la única salida robusta aquí porque el ancho que pide esta barra NO TIENE COTA: el
// nombre de la empresa lo escribe el cliente.
func TestBarra_TodoContenedorFlexConControlesPuedeFLUIR(t *testing.T) {
	t.Parallel()

	reglas := cssDeLaConsola(t)
	conControles, controles := contenedoresConControles(t)

	// ANTI-VACUIDAD 1: la derivación encontró controles de verdad. Sin esto, un escáner que no
	// entendiera el layout devolvería el conjunto vacío y este test pasaría sin mirar nada.
	if len(controles) < 5 {
		t.Fatalf("la derivación solo vio %d controles en la barra (%v): no está leyendo el layout",
			len(controles), controles)
	}
	// ANTI-VACUIDAD 2: los dos contenedores estructurales están. Si la barra se reorganiza y dejan de
	// estar, esto avisa en vez de callarse.
	for _, imprescindible := range []string{"app-bar", "app-bar__actions"} {
		if !conControles[imprescindible] {
			t.Fatalf("la derivación no ve controles dentro de .%s: la barra cambió y esta vigilancia "+
				"dejó de mirar donde tenía que mirar", imprescindible)
		}
	}

	var vigilados, exentos []string
	for sel, regla := range reglas {
		clase, esClaseSuelta := strings.CutPrefix(sel, ".")
		if !esClaseSuelta || !strings.HasPrefix(clase, "app-bar") || !regla.esFlex() {
			continue
		}
		if !conControles[clase] {
			exentos = append(exentos, sel)
			continue
		}
		vigilados = append(vigilados, sel)
		if regla["flex-wrap"] != "wrap" {
			t.Errorf("%s es un contenedor flex de la barra CON controles dentro y no puede fluir "+
				"(flex-wrap=%q). Lo que no quepa saldrá del área visible en vez de bajar de línea: "+
				"es el defecto que dejó «Cerrar sesión» fuera de la barra a 1.154 px",
				sel, regla["flex-wrap"])
		}
	}

	if len(vigilados) < 2 {
		t.Fatalf("solo %d contenedores flex de la barra bajo vigilancia (%v): el cruce plantilla↔CSS "+
			"no está cuadrando", len(vigilados), vigilados)
	}
	// ANTI-VACUIDAD 3: el filtro DISCRIMINA. Si no exceptuara a nadie, «todo contenedor con
	// controles» sería indistinguible de «todo contenedor», y el test no estaría diciendo nada sobre
	// dónde está el peligro. `.app-bar__logo` y `.app-bar__titles` son flex y NO llevan controles.
	if len(exentos) == 0 {
		t.Error("ningún contenedor flex de la barra queda exento: el criterio no distingue " +
			"«envuelve controles» de «es flex», así que no está afirmando lo que dice afirmar")
	}
}

// TestBarra_LaAlturaDeLaBarraNoEsFIJA. Envolver con una altura fija no arregla nada: la segunda línea
// se pinta FUERA de la caja y el botón sigue sin verse, solo que ahora por abajo en vez de por la
// derecha. `min-height` conserva los 64 px de siempre cuando todo cabe en una línea (medido: 64 px
// hasta 1.400 px de viewport) y deja crecer cuando no.
func TestBarra_LaAlturaDeLaBarraNoEsFIJA(t *testing.T) {
	t.Parallel()

	barra, hay := cssDeLaConsola(t)[".app-bar"]
	if !hay {
		t.Fatal("no hay regla .app-bar en app.css")
	}
	if alto, fijada := barra["height"]; fijada {
		t.Errorf(".app-bar fija height: %s. Con flex-wrap eso RECORTA la segunda línea, que es el "+
			"defecto que se venía a arreglar. Usa min-height", alto)
	}
	if barra["min-height"] == "" {
		t.Error(".app-bar se quedó sin min-height: la barra perdió su altura de siempre cuando todo cabe")
	}
}

// TestBarra_ElSelectorDeEmpresaConservaSuCotaDeAnchoABSOLUTA.
//
// 🔴 ESTE ES EL ASERTO MENOS OBVIO DE LOS TRES, y sale de una medición que sorprendió. El ancho
// natural de un `<select>` lo fija su opción más larga, y el nombre de la empresa lo escribe el
// cliente: sin cota, un nombre de 55 caracteres pide 432 px y la barra desborda 136 px a 320.
//
// La cota tiene que ser una LONGITUD ABSOLUTA. Cambiarla por `min(14rem, 100%)` —que parece un
// endurecimiento— vuelve la cota indefinida mientras el navegador calcula el ancho intrínseco: el
// desplegable se ve recortado exactamente igual y la barra desborda otra vez esos mismos 136 px.
// Se ve idéntico y no lo es, así que se vigila el VALOR y no solo la presencia.
func TestBarra_ElSelectorDeEmpresaConservaSuCotaDeAnchoABSOLUTA(t *testing.T) {
	t.Parallel()

	sel, hay := cssDeLaConsola(t)[".app-bar__tenant-select"]
	if !hay {
		t.Fatal("no hay regla .app-bar__tenant-select en app.css: el selector de la barra se quedó sin cota de ancho")
	}
	cota := sel["max-width"]
	if !regexp.MustCompile(`^[0-9]+(\.[0-9]+)?(rem|em|px|ch)$`).MatchString(cota) {
		t.Errorf("la cota de ancho del selector de empresa es %q y tiene que ser una longitud "+
			"ABSOLUTA (rem/em/px/ch). Un porcentaje, un min()/clamp() con porcentaje o `none` dejan "+
			"que el nombre que elija el cliente decida el ancho de la barra: medido, 136 px de "+
			"desbordamiento a 320 px con un nombre de 55 caracteres", cota)
	}
}
