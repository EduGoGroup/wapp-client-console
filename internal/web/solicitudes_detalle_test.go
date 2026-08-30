package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
)

// solicitudes_detalle_test.go vigila EL DETALLE de una solicitud (Plan 047 · T7.3): la ficha, la
// comparación original ↔ interpretado, el borrador con sus conteos, las acciones y el cambio de
// estado.
//
// 🔴 CUATRO de sus asertos vienen del BFF y su fichero de origen se borra en T7.7, así que si aquí no
// estuvieran desaparecerían con él sin que ningún gate se pusiera rojo:
//
//  1. que SIN PRECIO no se imprima ningún número —ni «0.00» ni «0»—, que es lo que el §7.5 prohíbe;
//  2. que el conteo de «pendientes de precio» NO sume las que esperan variante ni el envío;
//  3. que `Cache-Control: no-store` salga SIEMPRE, también por las salidas tempranas;
//  4. que un `?revision=` ilegible o inexistente NO tumbe la página.

// --- El cuerpo del doble: una solicitud rica, con los cuatro casos de línea ---

// interpretacionDeCampo es el payload de la revisión `interpreted`, con las CUATRO clases de línea
// que la pantalla tiene que distinguir: una con precio, una `unmatched` SIN precio (la que hay que
// poner a precio), una con variantes SIN precio (que NO cuenta como pendiente) y el envío.
//
// Es un objeto JSON embebido y no una cadena: `payload` viaja como `json.RawMessage`.
const interpretacionDeCampo = `{
	"version": 1,
	"source_text": "Hola! Quiero una torta de chocolate sin gluten para el sábado,\nunos tequeños y dos gaseosas",
	"analysis": {"provider": "", "model": "qwen3:1.7b", "source": "", "reanalyzed_from": null},
	"delivery_date": "2026-08-25",
	"media_refs": [{"ref": "opaco-1", "kind": "ptt", "label": ""}],
	"suggested_questions": ["¿Para cuántas personas es la torta?"],
	"lines": [
		{"kind": "matched", "sku": "TRT-CHO", "label": "Torta de chocolate", "qty": 1,
		 "unit_price": 21000, "customization": "sin gluten",
		 "range": {"min": 10, "max": 12, "unit": "porciones"},
		 "match": {"strategy": "sku", "confidence": 0.97},
		 "evidence": "una torta de chocolate sin gluten"},
		{"kind": "unmatched", "label": "Tequeños", "qty": 30, "unit_price": null,
		 "unit_kind": "paquete", "package_size": 30, "evidence": "unos tequeños"},
		{"kind": "matched", "label": "Gaseosa", "qty": 2, "unit_price": null,
		 "variant_options": [{"sku": "GAS-1L", "label": "1 litro", "price": 1500},
		                     {"sku": "GAS-2L", "label": "2 litros", "price": 2500}]},
		{"kind": "shipping", "sku": "_shipping", "label": "Envío", "qty": 1, "unit_price": null,
		 "note": "por confirmar zona"}
	]
}`

// revisionJSON arma UNA entrada del histórico.
func revisionJSON(no int, clase, porQuien, creada, payload string) string {
	return `{"revision_no": ` + itoa(no) + `, "kind": "` + clase + `", "created_by": "` + porQuien +
		`", "created_at": "` + creada + `", "payload": ` + payload + `}`
}

// solicitudDeCampo es el detalle tal como lo devuelve la API: `pending_approval` —el único estado que
// abre las acciones y la corrección—, con la línea del sistema entre los items y con una
// interpretación guardada.
func solicitudDeCampo(revisiones ...string) string {
	if len(revisiones) == 0 {
		revisiones = []string{revisionJSON(1, apiclient.RevisionKindInterpreted, apiclient.RevisionBySystem,
			"2026-08-20T10:05:00Z", interpretacionDeCampo)}
	}
	return `{
		"id": "` + testIntakeID + `", "contact_id": "` + testContactID + `",
		"session_id": "` + testIntakeSesio + `", "status": "pending_approval",
		"total": 21000, "customer_note": "Dejarlo en portería", "overdue": true,
		"created_at": "2026-08-20T10:00:00Z", "updated_at": "2026-08-21T15:30:00Z",
		"allowed_transitions": ["confirmed", "rejected"],
		"items": [
			{"sku": "TRT-CHO", "label": "Torta de chocolate", "customization": "sin gluten",
			 "qty": 1, "unit_price": 21000},
			{"sku": "_shipping", "label": "Envío", "customization": "", "qty": 1, "unit_price": 0}
		],
		"revisions": [` + strings.Join(revisiones, ",") + `]
	}`
}

// itoa evita arrastrar strconv a los literales de este fichero.
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// rutaDetalle es la URL del detalle de la solicitud de campo.
const rutaDetalle = rutaSolicitudes + "/" + testIntakeID

// detalleRouter monta el router con la solicitud de campo servida, sustituyendo lo que se le pase.
func detalleRouter(t *testing.T, cambios map[string]stubResponse) (http.Handler, *stubAPI) {
	t.Helper()
	rutas := rutasSolicitudes()
	rutas["GET /api/v1/intakes/{id}"] = stubResponse{http.StatusOK, solicitudDeCampo()}
	for ruta, resp := range cambios {
		rutas[ruta] = resp
	}
	api := newStubAPI(t, rutas)
	return adminRouter(api), api
}

// --- Criterio 1: líneas, estado, plazos y precios ---

// TestSolicitudDetalle_PintaLineasEstadoPlazosYPrecios es el criterio 1 de la casilla, sobre la
// solicitud de campo: la ficha con su estado y su total, la marca de retraso con SU plazo, la
// interpretación con las tres líneas y el formulario de corrección con la línea del sistema fuera.
func TestSolicitudDetalle_PintaLineasEstadoPlazosYPrecios(t *testing.T) {
	t.Parallel()
	router, api := detalleRouter(t, nil)

	rec := getWithSession(t, router, rutaDetalle)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()

	// El ESTADO, con el nombre de negocio y no con la clave del ciclo de vida.
	if !strings.Contains(out, "estado · por aprobar") {
		t.Error("la ficha no dice el estado con el nombre de negocio")
	}
	// El PLAZO: la marca `overdue` con las horas, y dicha como AVISO —no como estado—.
	if !strings.Contains(out, "sin responder hace más de 24 h") {
		t.Error("no se pinta la marca de retraso con su plazo")
	}
	if !strings.Contains(out, "no cambia su estado") {
		t.Error("la marca de retraso no dice que NO cambia el estado: se leería como uno")
	}
	// Las LÍNEAS facturables y su PRECIO.
	if !strings.Contains(out, `id="table-solicitud-items"`) || !strings.Contains(out, "Torta de chocolate") {
		t.Error("no se pinta la tabla de líneas facturables")
	}
	if !strings.Contains(out, "total · 21000.00") {
		t.Error("la ficha no pinta el total con dos decimales")
	}
	// La INTERPRETACIÓN, con lo que el catálogo no supo resolver.
	if !strings.Contains(out, `id="section-solicitud-borrador"`) || !strings.Contains(out, "Tequeños") {
		t.Errorf("no se pinta el borrador con la línea sin match. Body: %s", out)
	}
	// El TOTAL PARCIAL con su conteo: un número suelto se leería como el precio final.
	if !strings.Contains(out, "Total parcial: 21000.00 (1 línea pendiente de precio)") {
		t.Errorf("el total parcial no dice que lo es ni cuánto falta. Body: %s",
			bloque(t, out, `id="solicitud-total-parcial"`, "</p>"))
	}
	// La línea del SISTEMA no se ofrece en el formulario de corrección, pero se dice que está.
	if !strings.Contains(out, `id="form-solicitud-lineas"`) {
		t.Error("no se pinta el formulario de corrección de líneas")
	}
	if !strings.Contains(out, "La línea de envío la pone wApp") {
		t.Error("la línea del sistema desaparece sin decirlo: se daría por perdida")
	}

	// 🔴 NI UN `<no value>`. html/template no falla cuando una plantilla pide un campo que el dot no
	// tiene: escribe eso y sigue. En la pantalla más grande del repo —seis tarjetas y cuatro tablas
	// sobre cinco vistas anidadas— es el error más fácil de cometer y el más difícil de ver, porque
	// no rompe nada visible.
	if strings.Contains(out, "<no value>") {
		t.Errorf("la plantilla pide un campo que la vista no tiene. Body: %s", out)
	}

	// Y lo que se le pidió a la plataforma: UNA sola lectura del detalle.
	if !api.Called("GET /api/v1/intakes/" + testIntakeID) {
		t.Error("no se leyó el detalle de la solicitud")
	}
}

// --- Criterio 2: las fechas se LEEN ---

// TestFormato_FechaConviertePreservaYDeclaraElHuso es el unitario del helper.
//
// 🔴 EL ASERTO QUE MATA LA MUTACIÓN es el primero: devolver el valor crudo desde `fecha` tiene que
// poner esto en rojo. Por eso no basta con «contiene el día»: se compara la cadena ENTERA, que es lo
// único que distingue «se formateó» de «se devolvió tal cual».
func TestFormato_FechaConviertePreservaYDeclaraElHuso(t *testing.T) {
	t.Parallel()

	for _, caso := range []struct {
		nombre string
		dentro string
		quiere string
	}{
		{"RFC3339 en UTC", "2026-08-20T10:00:00Z", "20/08/2026 10:00 UTC"},
		// 🔑 Un instante con desplazamiento se CONVIERTE, no se recorta: quedarse con «15:30» y
		// escribirle «UTC» al lado sería exactamente la mentira que este helper viene a impedir.
		{"con desplazamiento, se convierte", "2026-08-20T15:30:00+05:00", "20/08/2026 10:30 UTC"},
		{"con fracción de segundo", "2026-08-20T10:00:00.123456Z", "20/08/2026 10:00 UTC"},
		// Una fecha SIN hora no afirma ningún instante: no se le pone huso.
		{"fecha sola, sin huso", "2026-08-25", "25/08/2026"},
		{"vacío devuelve vacío", "", ""},
		{"espacios alrededor", "  2026-08-20T10:00:00Z  ", "20/08/2026 10:00 UTC"},
		// Ilegible ⇒ TAL CUAL, misma doctrina que statusLabel: antes el dato crudo que esconderlo.
		{"ilegible se devuelve crudo", "el sábado", "el sábado"},
	} {
		if got := fecha(caso.dentro); got != caso.quiere {
			t.Errorf("%s: fecha(%q) = %q, want %q", caso.nombre, caso.dentro, got, caso.quiere)
		}
	}

	// ANTI-VACUIDAD: el formato ELEGIDO no es el de entrada. Si alguien cambiara el layout por
	// RFC3339, los asertos de arriba se caerían — pero este dice por qué en una línea.
	if fecha("2026-08-20T10:00:00Z") == "2026-08-20T10:00:00Z" {
		t.Fatal("fecha() devuelve el valor crudo: el helper dejó de formatear nada")
	}
}

// TestSolicitudDetalle_LasFechasSeLeenFormateadasYNoCrudas es el criterio 2, POR EL CABLE.
//
// El unitario de arriba prueba la función; éste prueba que la PANTALLA la usa. Son dos cosas
// distintas y la segunda es la que se rompe sola: `fecha` vive en el FuncMap, o sea en una CADENA de
// plantilla que no compila nadie, y una plantilla que se olvidara de invocarla volcaría el ISO-8601
// con todo en verde.
func TestSolicitudDetalle_LasFechasSeLeenFormateadasYNoCrudas(t *testing.T) {
	t.Parallel()
	router, _ := detalleRouter(t, nil)

	out := getWithSession(t, router, rutaDetalle).Body.String()

	// Las tres fechas de esta pantalla: creada, actualizada y la de la revisión.
	for _, quiere := range []string{
		"Creada 20/08/2026 10:00 UTC",
		"actualizada 21/08/2026 15:30 UTC",
		"Guardada 20/08/2026 10:05 UTC",
	} {
		if !strings.Contains(out, quiere) {
			t.Errorf("la pantalla no escribe %q. Body: %s", quiere, out)
		}
	}

	// 🔴 EL GEMELO NEGATIVO, que es lo que mata la mutación: ni un solo ISO-8601 crudo en el HTML.
	// Sin él, un helper que devolviera el valor tal cual dejaría los asertos de arriba en rojo… pero
	// una plantilla que pintara LAS DOS formas seguiría verde.
	for _, crudo := range []string{"2026-08-20T10:00:00Z", "2026-08-21T15:30:00Z", "2026-08-20T10:05:00Z"} {
		if strings.Contains(out, crudo) {
			t.Errorf("la pantalla vuelca la fecha cruda %q: es lo que devuelve el JSON, no algo que "+
				"una persona lea", crudo)
		}
	}

	// Y el huso se DICE, porque esta consola no puede saber el de quien mira (sin JS, ADR-0035): un
	// instante sin huso es una hora que no se sabe de dónde es.
	if !strings.Contains(out, "UTC") {
		t.Error("la pantalla escribe instantes sin decir en qué huso están")
	}
}

// --- El literal del cliente no se cachea ---

// TestSolicitudDetalle_NoSeCacheaElLiteralDelCliente.
//
// 🔴 Ésta es la única página de la consola que pinta en claro lo que una persona escribió por
// WhatsApp, y llega YA DESCIFRADA del cloud. La cabecera se comprueba en las CUATRO salidas de la
// función —no solo en la buena— porque el defecto que se evita es exactamente ése: una cabecera
// colgada de la rama que pinta el literal es una cabecera que un día no sale.
func TestSolicitudDetalle_NoSeCacheaElLiteralDelCliente(t *testing.T) {
	t.Parallel()

	router, _ := detalleRouter(t, nil)
	rec := getWithSession(t, router, rutaDetalle)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want «no-store» en el camino bueno", got)
	}
	// Anti-vacuidad: la página que se acaba de medir SÍ trae el literal del cliente. Sin esto, el
	// aserto de arriba podría estar mirando una pantalla vacía.
	if !strings.Contains(rec.Body.String(), "Quiero una torta de chocolate sin gluten") {
		t.Fatal("la captura no trae el literal del cliente: este test no está protegiendo nada")
	}

	// Salida temprana 1: SIN EMPRESA, que ni siquiera sale a la red.
	sinTenant := sessionCookieFor(t, testUserID, "")
	if got := getConCookie(router, rutaDetalle, sinTenant).Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q sin empresa, want «no-store»", got)
	}

	// Salida temprana 2: la solicitud NO SE PUEDE ABRIR, que se va por un 303 a la bandeja.
	routerCaido, _ := detalleRouter(t, map[string]stubResponse{
		"GET /api/v1/intakes/{id}": {http.StatusNotFound, `{"error":"not_found"}`},
	})
	rec404 := getWithSession(t, routerCaido, rutaDetalle)
	if got := rec404.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q en el 303 a la bandeja, want «no-store»", got)
	}

	// Salida temprana 3: el GATE por plan, que corta antes del handler. No es de esta función, pero
	// se mide igual: si mañana el 403 pintara el detalle, esta cabecera tendría que seguir estando.
	routerSinPlan, _ := detalleRouter(t, map[string]stubResponse{
		"GET /api/v1/entitlements": {http.StatusOK, entitlementsBody("basic", "menu")},
	})
	if code := getWithSession(t, routerSinPlan, rutaDetalle).Code; code != http.StatusForbidden {
		t.Errorf("el detalle sin cart_basic respondió %d, want 403: el gate del grupo no cubre el :id", code)
	}
}

// --- El desenlace malo manda a la bandeja ---

// TestSolicitudDetalle_UnaSolicitudQueNoSePuedeAbrirMandaALaBandeja es la diferencia declarada contra
// el BFF, que pintaba un detalle vacío con un párrafo y ningún camino de salida.
//
// Es el precedente de esta casa, escrito en ShowFlowDetail: ahí están las que sí existen, y el aviso
// explica cuál de los casos fue.
func TestSolicitudDetalle_UnaSolicitudQueNoSePuedeAbrirMandaALaBandeja(t *testing.T) {
	t.Parallel()

	for _, caso := range []struct {
		nombre string
		resp   stubResponse
		quiere string
	}{
		{"no es de tu empresa", stubResponse{http.StatusNotFound, `{"error":"not_found"}`}, flashNotInYourTenant},
		{"la plataforma no contesta", stubResponse{http.StatusBadGateway, `{"error":"upstream"}`}, flashUpstreamUnavailable},
	} {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			router, _ := detalleRouter(t, map[string]stubResponse{"GET /api/v1/intakes/{id}": caso.resp})

			rec := getWithSession(t, router, rutaDetalle)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303. Body: %s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Location"); got != rutaSolicitudes+"?error="+caso.quiere {
				t.Errorf("Location = %q, want la bandeja con el aviso %q", got, caso.quiere)
			}
		})
	}
}

// --- El §7.5: sin precio no se imprime ningún número ---

// TestSolicitudDetalle_SinPrecioNoSeImprimeUnCero.
//
// 🔴 Es la regla dura del §7.5, y el defecto que evita es concreto: un `printf "%.2f"` incondicional
// convierte el «todavía no hay precio» en un «0.00», que le dice a la dueña que esa torta es gratis.
// El hueco se queda vacío, con «¿precio?» a la vista.
func TestSolicitudDetalle_SinPrecioNoSeImprimeUnCero(t *testing.T) {
	t.Parallel()
	router, _ := detalleRouter(t, nil)

	out := getWithSession(t, router, rutaDetalle).Body.String()
	borrador := bloque(t, out, `id="section-solicitud-borrador"`, `id="section-solicitud-acciones"`)

	// La línea CON precio lo enseña con dos decimales.
	if !strings.Contains(borrador, `value="21000.00"`) {
		t.Errorf("la línea con precio no lo re-imprime con dos decimales. Bloque: %s", borrador)
	}
	// Las dos SIN precio dejan el campo vacío y dicen «¿precio?».
	if !strings.Contains(borrador, `placeholder="¿precio?"`) {
		t.Error("una línea sin precio no ofrece el hueco marcado")
	}
	if strings.Contains(borrador, `value="0.00"`) {
		t.Error("una línea sin precio se imprimió como «0.00»: eso dice que es gratis (§7.5)")
	}
	// Y las dos ausencias se dicen DISTINTO: una espera precio, la otra espera que se elija variante.
	if !strings.Contains(borrador, "falta elegir presentación") {
		t.Error("la línea con variantes no dice que lo que falta es ELEGIR, no poner precio")
	}
}

// TestSolicitudDetalle_ElConteoDePendientesNoMezclaLasTresAusencias.
//
// 🔴 Son TRES ausencias de precio distintas y solo UNA cuenta: la `unmatched`. La que espera variante
// ya tiene precio en el catálogo —falta elegir cuál— y el envío depende de la zona del cliente.
// Sumarlas diría «3 líneas pendientes de precio» donde el §7.5 dice 1, y la dueña buscaría dos
// precios que no le tocan.
func TestSolicitudDetalle_ElConteoDePendientesNoMezclaLasTresAusencias(t *testing.T) {
	t.Parallel()
	router, _ := detalleRouter(t, nil)

	out := getWithSession(t, router, rutaDetalle).Body.String()

	if !strings.Contains(out, "(1 línea pendiente de precio)") {
		t.Errorf("el conteo de pendientes no es 1. Body: %s", bloque(t, out, `id="solicitud-total-parcial"`, "</p>"))
	}
	if strings.Contains(out, "líneas pendientes de precio)") {
		t.Error("el conteo pluralizó: está sumando las que esperan variante o el envío")
	}
	if !strings.Contains(out, "espera a que elijas presentación") {
		t.Error("la línea que espera variante no se cuenta aparte")
	}
	if !strings.Contains(out, "El envío está por confirmar zona") {
		t.Error("el envío sin precio no se cuenta aparte")
	}
}

// --- La navegación por revisiones ---

// TestSolicitudDetalle_LaRevisionDeLaQueryNuncaTumbaLaPagina.
//
// 🔴 Un enlace viejo o tecleado a mano no puede dejar a la dueña sin su solicitud. Un valor ilegible
// o menor que 1 vale lo mismo que no mandar nada —se mira la última— y una revisión que NO EXISTE se
// DICE, en vez de redirigir en silencio a otra cosa.
func TestSolicitudDetalle_LaRevisionDeLaQueryNuncaTumbaLaPagina(t *testing.T) {
	t.Parallel()

	dos := []string{
		revisionJSON(1, apiclient.RevisionKindInterpreted, apiclient.RevisionBySystem,
			"2026-08-20T10:05:00Z", interpretacionDeCampo),
		revisionJSON(2, apiclient.RevisionKindInterpreted, apiclient.RevisionByOwner,
			"2026-08-21T09:00:00Z", `{"version":1,"source_text":"lo mismo pero de vainilla",
			"analysis":{"provider":"local","model":"qwen3:1.7b","reanalyzed_from":1},
			"lines":[{"kind":"matched","sku":"TRT-VAI","label":"Torta de vainilla","qty":1,"unit_price":19000}]}`),
	}
	router, _ := detalleRouter(t, map[string]stubResponse{
		"GET /api/v1/intakes/{id}": {http.StatusOK, solicitudDeCampo(dos...)},
	})

	// Sin query: la ÚLTIMA, y con la navegación entre las dos.
	ultima := getWithSession(t, router, rutaDetalle).Body.String()
	if !strings.Contains(ultima, "revisión 2 · vía local — la que ves") {
		t.Errorf("sin ?revision= no se está mirando la última. Body: %s",
			bloque(t, ultima, `id="section-solicitud-revisiones"`, "</div>"))
	}
	if !strings.Contains(ultima, `href="/solicitudes/`+testIntakeID+`?revision=1"`) {
		t.Error("no hay enlace a la revisión anterior: la navegación sin JS depende de él")
	}

	// Con ?revision=1: ESA, y el borrador sigue saliendo de la ÚLTIMA (navegar es leer, no cambiar
	// lo que se corrige).
	primera := getWithSession(t, router, rutaDetalle+"?revision=1").Body.String()
	if !strings.Contains(primera, "revisión 1 · "+viaDesconocida+" — la que ves") {
		t.Error("?revision=1 no cambió la interpretación que se mira")
	}
	if !strings.Contains(primera, "Interpretación · revisión 2") {
		t.Error("el borrador dejó de salir de la ÚLTIMA revisión al navegar por el histórico")
	}

	// Ilegible y fuera de rango: la página SIGUE EN PIE, mirando la última.
	for _, query := range []string{"?revision=abc", "?revision=0", "?revision=-3", "?revision="} {
		rec := getWithSession(t, router, rutaDetalle+query)
		if rec.Code != http.StatusOK {
			t.Errorf("%q respondió %d, want 200: un enlace tecleado a mano no puede tumbar la página",
				query, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "revisión 2 · vía local — la que ves") {
			t.Errorf("%q no cayó en la última revisión", query)
		}
	}

	// Una revisión que no existe se DICE, y se sigue viendo la última.
	inexistente := getWithSession(t, router, rutaDetalle+"?revision=9")
	if inexistente.Code != http.StatusOK {
		t.Fatalf("?revision=9 respondió %d, want 200", inexistente.Code)
	}
	if !strings.Contains(inexistente.Body.String(), `id="solicitud-revision-inexistente"`) {
		t.Errorf("una revisión inexistente no se dice: se cayó en la última en silencio. Body: %s",
			inexistente.Body.String())
	}
	if !strings.Contains(inexistente.Body.String(), "revisión 2 · vía local — la que ves") {
		t.Error("tras avisar de la revisión inexistente no se está mirando la última")
	}
}

// --- Los tres casos del puntero de transiciones ---

// TestSolicitudDetalle_LosTresCasosDelPunteroDeTransiciones es el unitario de transicionesDe.
//
// 🔴 Los tres salen del PUNTERO, y por eso el campo es puntero: `nil` —la plataforma no manda el
// campo— es «no se sabe», y la lista VACÍA es «terminal». Colapsarlos en un slice haría que un
// servidor viejo se leyera como un estado final, que es una pantalla diciendo «ya no admite cambios»
// sobre una solicitud que sí los admite.
func TestSolicitudDetalle_LosTresCasosDelPunteroDeTransiciones(t *testing.T) {
	t.Parallel()

	terminal := []string{}
	publicadas := []string{"confirmed", "rejected"}

	for _, caso := range []struct {
		nombre       string
		publicadas   *[]string
		desdeRechazo []string
		quiere       []string
		conocidas    bool
		tarde        bool
	}{
		{"las publica el detalle", &publicadas, nil, publicadas, true, false},
		{"terminal: lista vacía", &terminal, nil, nil, true, false},
		// La lista vacía es TERMINAL aunque haya destinos de un rechazo previo: la fuente buena manda.
		{"terminal manda sobre el rechazo", &terminal, []string{"confirmed"}, nil, true, false},
		{"no se sabe: sin campo y sin rechazo", nil, nil, nil, false, false},
		{"llegan tarde, del 422", nil, []string{"confirmed"}, []string{"confirmed"}, true, true},
	} {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			vista := transicionesDe(&apiclient.IntakeDetail{AllowedTransitions: caso.publicadas}, caso.desdeRechazo)

			if len(vista.Transiciones) != len(caso.quiere) {
				t.Fatalf("Transiciones = %v, want %v", vista.Transiciones, caso.quiere)
			}
			for i, quiere := range caso.quiere {
				if vista.Transiciones[i] != quiere {
					t.Errorf("Transiciones[%d] = %q, want %q", i, vista.Transiciones[i], quiere)
				}
			}
			if vista.Conocidas != caso.conocidas {
				t.Errorf("Conocidas = %t, want %t", vista.Conocidas, caso.conocidas)
			}
			if vista.DesdeRechazo != caso.tarde {
				t.Errorf("DesdeRechazo = %t, want %t: que llegó tarde se dice, no se disimula",
					vista.DesdeRechazo, caso.tarde)
			}
		})
	}
}

// TestSolicitudDetalle_ElDesplegableSoloOfreceLoQueAutorizaLaPlataforma, por el cable.
func TestSolicitudDetalle_ElDesplegableSoloOfreceLoQueAutorizaLaPlataforma(t *testing.T) {
	t.Parallel()
	router, _ := detalleRouter(t, nil)

	out := getWithSession(t, router, rutaDetalle).Body.String()
	estado := bloque(t, out, `id="section-solicitud-estado"`, "</div>")

	if !strings.Contains(estado, `<option value="confirmed">confirmado</option>`) {
		t.Errorf("el desplegable no ofrece el destino que publica la plataforma. Bloque: %s", estado)
	}
	// 🔴 Y NO ofrece nada más: la lista llega hecha desde la API y esta pantalla no la amplía.
	if strings.Contains(estado, `value="needs_info"`) || strings.Contains(estado, `value="settled"`) {
		t.Error("el desplegable ofrece destinos que la plataforma no autorizó: hay una tabla local")
	}

	// Sin el campo publicado se DECLARA la ausencia, no se finge un estado final.
	routerViejo, _ := detalleRouter(t, map[string]stubResponse{
		"GET /api/v1/intakes/{id}": {http.StatusOK,
			strings.Replace(solicitudDeCampo(), `"allowed_transitions": ["confirmed", "rejected"],`, "", 1)},
	})
	sinCampo := getWithSession(t, routerViejo, rutaDetalle).Body.String()
	if !strings.Contains(sinCampo, `id="solicitud-estado-sin-destinos"`) {
		t.Error("sin `allowed_transitions` la pantalla no declara que no lo sabe")
	}
	if strings.Contains(sinCampo, `id="solicitud-estado-terminal"`) {
		t.Error("sin `allowed_transitions` la pantalla dijo «estado final»: es el colapso que el puntero evita")
	}
}

// --- El plan: lo que se puede leer y lo que no se puede pulsar ---

// TestSolicitudDetalle_SinLlmIntakeLosDosBotonesSalenApagadosConSuRazon.
//
// 🔴 Es el plan REAL de UAT: `cart_basic` sin `llm_intake`. La solicitud se abre y se lee ENTERA, y
// los dos botones que llamarían al modelo salen DESHABILITADOS con la razón delante. Esconderlos
// dejaría a la dueña sin saber que existen ni por qué no los tiene.
func TestSolicitudDetalle_SinLlmIntakeLosDosBotonesSalenApagadosConSuRazon(t *testing.T) {
	t.Parallel()
	// 🔑 SIN la feature, la plataforma BORRA la clave `suggested_questions` del payload —no la deja
	// en `[]`—, justamente para que «no la has contratado» y «no había nada que preguntar» no se
	// confundan. El doble reproduce las dos mitades de esa realidad: el plan sin `llm_intake` Y el
	// payload sin la clave. Servir uno sin el otro probaría una combinación que no existe en campo.
	sinPreguntas := strings.Replace(interpretacionDeCampo,
		`"suggested_questions": ["¿Para cuántas personas es la torta?"],`, "", 1)
	router, _ := detalleRouter(t, map[string]stubResponse{
		"GET /api/v1/entitlements": {http.StatusOK, entitlementsBody("pro", featureCartBasic)},
		"GET /api/v1/intakes/{id}": {http.StatusOK, solicitudDeCampo(
			revisionJSON(1, apiclient.RevisionKindInterpreted, apiclient.RevisionBySystem,
				"2026-08-20T10:05:00Z", sinPreguntas))},
	})

	rec := getWithSession(t, router, rutaDetalle)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: sin llm_intake la bandeja se lee igual", rec.Code)
	}
	out := rec.Body.String()

	// La pantalla se lee entera: el gate duro es `cart_basic`, no éste.
	if !strings.Contains(out, `id="section-solicitud-borrador"`) {
		t.Fatal("sin llm_intake se perdió el borrador: se está cortando con la feature equivocada")
	}
	// Los DOS botones existen y están apagados, cada uno con SU razón.
	for _, ancla := range []string{`id="solicitud-regenerar-razon"`, `id="solicitud-sugerir-razon"`} {
		if !strings.Contains(out, ancla) {
			t.Errorf("falta la razón de %s: un botón apagado sin motivo no dice nada", ancla)
		}
	}
	if n := strings.Count(out, "disabled"); n < 2 {
		t.Errorf("solo %d controles deshabilitados, want al menos 2 (Regenerar y Sugerir)", n)
	}
	// Y la clave AUSENTE de preguntas no se dice como «no había nada que preguntar».
	if !strings.Contains(out, `id="solicitud-sin-preguntas-por-plan"`) {
		t.Error("sin llm_intake la pantalla no distingue «no lo has contratado» de «no había preguntas»")
	}

	// El gemelo: CON la feature, los dos se pueden pulsar y no se pinta ninguna razón.
	conPlan, _ := detalleRouter(t, nil)
	completa := getWithSession(t, conPlan, rutaDetalle).Body.String()
	for _, ancla := range []string{`id="solicitud-regenerar-razon"`, `id="solicitud-sugerir-razon"`} {
		if strings.Contains(completa, ancla) {
			t.Errorf("con llm_intake sigue apareciendo %s: el motivo no depende del plan", ancla)
		}
	}
}

// --- La comparación ---

// TestSolicitudDetalle_LaComparacionPoneElOriginalAlLadoDeLoInterpretado.
//
// La discrepancia tiene que leerse SIN abrir nada más: es el caso de las hamburguesas —1 pedida, 3
// interpretadas—, y aquí son 33 unidades en 3 líneas contra un texto que pide tres cosas.
func TestSolicitudDetalle_LaComparacionPoneElOriginalAlLadoDeLoInterpretado(t *testing.T) {
	t.Parallel()
	router, _ := detalleRouter(t, nil)

	out := getWithSession(t, router, rutaDetalle).Body.String()

	if !strings.Contains(out, `id="section-solicitud-original"`) ||
		!strings.Contains(out, `id="section-solicitud-entendido"`) {
		t.Fatalf("no se pintan las dos columnas de la comparación. Body: %s", out)
	}
	if !strings.Contains(out, "33 unidades interpretadas en 3 líneas") {
		t.Errorf("no se dice cuántas unidades se interpretaron: es la mitad de la comparación. Body: %s",
			bloque(t, out, `id="section-solicitud-entendido"`, "</div>"))
	}
	// 🔴 El envío NO cuenta entre «lo que se entendió»: lo pone wApp, no sale del texto del cliente.
	// Con él serían 34 en 4.
	if strings.Contains(out, "34 unidades interpretadas") {
		t.Error("el envío se contó entre lo interpretado: infla la discrepancia con una línea que no pidió el cliente")
	}
	// La vía NO se inventa: `provider` viene vacío en la revisión 1 y eso se dice «no registrada».
	if !strings.Contains(out, viaDesconocida) {
		t.Error("una revisión sin `provider` no dice que la vía no consta")
	}
	if strings.Contains(out, "LLM local") {
		t.Error("la pantalla afirma «LLM local» sobre una revisión que no registró vía")
	}
	// El rol es un ROL, nunca una persona.
	if !strings.Contains(out, "la dejó el sistema (rol `system`)") {
		t.Error("no se dice qué ROL dejó la revisión")
	}
	// Y sin literal guardado, la columna dice POR QUÉ en vez de quedarse vacía.
	sinLiteral := strings.Replace(interpretacionDeCampo,
		`"source_text": "Hola! Quiero una torta de chocolate sin gluten para el sábado,\nunos tequeños y dos gaseosas",`, "", 1)
	routerSinLiteral, _ := detalleRouter(t, map[string]stubResponse{
		"GET /api/v1/intakes/{id}": {http.StatusOK, solicitudDeCampo(
			revisionJSON(1, apiclient.RevisionKindInterpreted, apiclient.RevisionBySystem,
				"2026-08-20T10:05:00Z", sinLiteral))},
	})
	mudo := getWithSession(t, routerSinLiteral, rutaDetalle).Body.String()
	if !strings.Contains(mudo, "nunca se guardó") {
		t.Errorf("sin literal la columna no explica por qué. Body: %s",
			bloque(t, mudo, `id="section-solicitud-original"`, "</div>"))
	}
}

// --- Sin interpretación ---

// TestSolicitudDetalle_SinInterpretacionSeDiceYNoSeFingeUnBorradorVacio.
func TestSolicitudDetalle_SinInterpretacionSeDiceYNoSeFingeUnBorradorVacio(t *testing.T) {
	t.Parallel()
	router, _ := detalleRouter(t, map[string]stubResponse{
		"GET /api/v1/intakes/{id}": {http.StatusOK,
			strings.Replace(solicitudDeCampo(), `"revisions": [`+revisionJSON(1,
				apiclient.RevisionKindInterpreted, apiclient.RevisionBySystem,
				"2026-08-20T10:05:00Z", interpretacionDeCampo)+`]`, `"revisions": []`, 1)},
	})

	rec := getWithSession(t, router, rutaDetalle)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	out := rec.Body.String()

	if !strings.Contains(out, `id="section-solicitud-sin-borrador"`) {
		t.Errorf("sin revisión interpretada no se dice que no hay borrador. Body: %s", out)
	}
	if strings.Contains(out, `id="section-solicitud-comparacion"`) {
		t.Error("se pintó la comparación sin ninguna interpretación que comparar")
	}
	// La ficha SIGUE: lo que hay son las líneas facturables, y se enseñan.
	if !strings.Contains(out, `id="table-solicitud-items"`) {
		t.Error("sin borrador se perdió también la ficha: es lo único que quedaba")
	}
	// Y el botón «Corregir» no se ofrece: apuntaría a un formulario que no está en la página.
	if !strings.Contains(out, `id="solicitud-sin-borrador-que-corregir"`) {
		t.Error("sin borrador se sigue ofreciendo «Corregir», que no haría nada")
	}
}

// --- Los formularios que todavía no tienen handler ---

// TestSolicitudDetalle_LosSieteFormulariosApuntanAlaRutaQueRegistraranLasCasillasSiguientes.
//
// 📌 T7.3 pintó los SIETE formularios sin registrar ninguno de sus POST. Este test fija los destinos
// para que quien traiga cada handler registre la ruta que el HTML ya está pidiendo: un `action` y un
// registro que se escriben por separado se desalinean, y el desenlace es un 404 que ningún gate ve
// venir.
//
// 🔑 EL GEMELO NEGATIVO FUE LA SEGUNDA MITAD, y por eso el test se movía en vez de ampliarse: afirmaba
// también qué rutas NO existían todavía. T7.4 registró las CUATRO que no le hablan al cliente y este
// test cayó —que es exactamente el aviso que se quería—; T7.5 registró LAS DOS QUE SÍ LE HABLAN y
// volvió a caer por lo mismo; T7.6 registró la última, la que cuesta una inferencia, y lo tiró por
// tercera y última vez. Ya no queda ninguna: la lista negativa se vació y lo que sobrevive es la
// mitad positiva, que sigue siendo la que importa —un formulario apuntando a una ruta que el router
// escribe distinto es un 404 que ningún gate ve venir—.
func TestSolicitudDetalle_LosSieteFormulariosApuntanAlaRutaQueRegistraranLasCasillasSiguientes(t *testing.T) {
	t.Parallel()
	router, _ := detalleRouter(t, nil)

	out := getWithSession(t, router, rutaDetalle).Body.String()
	base := rutaSolicitudes + "/" + testIntakeID

	for _, sufijo := range []string{
		sufijoEstado, sufijoLineas, sufijoCorregir, sufijoAprobar,
		sufijoPedirInfo, sufijoRegenerar, sufijoSugerir,
	} {
		if !strings.Contains(out, `action="`+base+sufijo+`"`) {
			t.Errorf("ningún formulario apunta a %q. Body: %s", base+sufijo, out)
		}
	}

	registradas := make(map[string]bool)
	for _, ruta := range NewRouter(offlineConfig()).Routes() {
		if ruta.Method == http.MethodPost {
			registradas[ruta.Path] = true
		}
	}
	// LAS SIETE tienen handler: las cuatro que no le hablan al cliente (T7.4), las dos que sí (T7.5) y
	// la que cuesta una inferencia (T7.6). Un formulario que apunte a una ruta sin registrar da un 404
	// del router, y el aserto de arriba no lo distingue de una que funciona.
	for _, sufijo := range []string{
		sufijoEstado, sufijoLineas, sufijoCorregir, sufijoRegenerar, sufijoAprobar, sufijoPedirInfo,
		sufijoSugerir,
	} {
		if !registradas[rutaSolicitudes+rutaSolicitudDetalle+sufijo] {
			t.Errorf("la ruta POST %q no está registrada: su formulario apunta a un 404", sufijo)
		}
	}
}
