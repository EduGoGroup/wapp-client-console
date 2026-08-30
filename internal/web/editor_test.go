package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/EduGoGroup/wapp-shared/ui"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
)

// editor_test.go vigila las dos pantallas del EDITOR (T6.3 · T6.4).
//
// 🔴 TRES de sus asertos vienen del BFF y NO LOS VIGILA NADIE MÁS EN EL ECOSISTEMA. Su fichero de
// origen (wapp-guardian-bff/internal/web/editor_test.go, 18 tests) se borra en T6.6, así que si aquí
// no estuvieran, desaparecerían con él sin que ningún gate se pusiera rojo:
//
//  1. el cuerpo de PUBLICAR se envuelve en `{definition}` — vive en apiclient/editor_test.go
//     (TestPublishFlow_LaDefinicionViajaBajoDefinitionYLaEmpresaNoViaja), que se escribió en T6.1;
//     aquí se comprueba el otro extremo, que lo que llega del textarea es lo que se publica;
//  2. el aviso de disparador SOMBREADO, que es la única cosa del ecosistema que pinta
//     `shadowed_by_event_list`;
//  3. que un `event_kind` residual del formulario NO VIAJE cuando el tipo no es `event_start`.

// --- Cuerpos del doble de la API ---

const (
	testFlowID      = "pedidos-panaderia"
	testTriggerID   = "44444444-4444-4444-8444-444444444444"
	testOtroTrigger = "55555555-5555-4555-8555-555555555555"
)

// flowsBody arma la respuesta de GET /api/v1/flows.
func flowsBody(flujos ...string) string { return "[" + strings.Join(flujos, ",") + "]" }

func flowJSON(id string, version int, creado string) string {
	return fmt.Sprintf(`{"flow_id":%q,"version":%d,"created_at":%q}`, id, version, creado)
}

// flowDefinitionBody es lo que devuelve GET /api/v1/flows/{id}: la definición CRUDA, sin envolver.
const flowDefinitionBody = `{"flow_id":"pedidos-panaderia","version":3,"initial":"inicio",` +
	`"nodes":{"inicio":{"type":"message","text":"Hola","next":null}}}`

// triggersBody arma la respuesta de GET /api/v1/triggers a partir de filas ya escritas en JSON, por
// el mismo motivo que sesionesBody: media docena de asertos de esta pantalla son sobre campos
// AUSENTES —un `keyword` que no viene, un `event_kind` vacío—, y una firma con parámetros obliga a
// mandar el cero de cada uno, que es justo lo contrario de ausente.
func triggersBody(filas ...string) string { return "[" + strings.Join(filas, ",") + "]" }

// disparadorSombreado es un `fallback` con la marca DERIVADA que calcula la plataforma
// (`shadowed_by_event_list`). Es el único sitio del ecosistema donde ese campo se pinta.
const disparadorSombreado = `{"trigger_id":"` + testTriggerID + `","kind":"fallback","match_type":"exact",` +
	`"flow_id":"menu-general","priority":10,"enabled":true,"shadowed_by_event_list":true}`

const disparadorNormal = `{"trigger_id":"` + testOtroTrigger + `","kind":"keyword","keyword":"hola",` +
	`"match_type":"exact","flow_id":"menu-general","priority":0,"enabled":true}`

// rutasEditor son las respuestas de fábrica del doble para las dos pantallas: todo va bien.
func rutasEditor() map[string]stubResponse {
	return map[string]stubResponse{
		rutaListadoDeEmpresas:          {http.StatusOK, unaEmpresa()},
		"GET /api/v1/flows":            {http.StatusOK, flowsBody(flowJSON(testFlowID, 3, "2026-08-01T10:00:00Z"))},
		"GET /api/v1/flows/{id}":       {http.StatusOK, flowDefinitionBody},
		"POST /api/v1/flows":           {http.StatusCreated, `{"flow_id":"` + testFlowID + `","version":4}`},
		"GET /api/v1/triggers":         {http.StatusOK, triggersBody(disparadorSombreado, disparadorNormal)},
		"POST /api/v1/triggers":        {http.StatusCreated, disparadorNormal},
		"DELETE /api/v1/triggers/{id}": {http.StatusNoContent, ""},
	}
}

// editorRouter monta el router con las respuestas de fábrica, sustituyendo las que se le pasen.
func editorRouter(t *testing.T, cambios map[string]stubResponse) (http.Handler, *stubAPI) {
	t.Helper()
	rutas := rutasEditor()
	for ruta, resp := range cambios {
		rutas[ruta] = resp
	}
	api := newStubAPI(t, rutas)
	return adminRouter(api), api
}

// bloque devuelve el trozo de HTML que va desde `desde` hasta `hasta`, fallando si no está.
func bloque(t *testing.T, html, desde, hasta string) string {
	t.Helper()
	ini := strings.Index(html, desde)
	if ini < 0 {
		t.Fatalf("no se encontró %q en el HTML servido", desde)
	}
	resto := html[ini:]
	fin := strings.Index(resto, hasta)
	if fin < 0 {
		t.Fatalf("no se encontró el cierre %q después de %q", hasta, desde)
	}
	return resto[:fin]
}

// contenidoDelTextarea devuelve lo que hay DENTRO del <textarea> de la definición.
func contenidoDelTextarea(t *testing.T, html string) string {
	t.Helper()
	area := bloque(t, html, `<textarea`, `</textarea>`)
	abre := strings.Index(area, ">")
	if abre < 0 {
		t.Fatal("el <textarea> del formulario de publicar no cierra su etiqueta de apertura")
	}
	return area[abre+1:]
}

// postSinCSRF manda el mismo POST que postFormWithCSRF pero SIN la mitad del double-submit: ni la
// cookie ni el campo. Es lo que hace un formulario al que alguien le haya quitado el token.
func postSinCSRF(t *testing.T, router http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(clientSessionCookie(t))
	router.ServeHTTP(rec, req)
	return rec
}

// --- FLUJOS (T6.3) ---

// TestFlujos_LaListaPintaLosFlujosDeLaEmpresa: el caso bueno, con la tabla del partial compartido.
func TestFlujos_LaListaPintaLosFlujosDeLaEmpresa(t *testing.T) {
	t.Parallel()

	router, _ := editorRouter(t, nil)
	rec := getWithSession(t, router, rutaFlujos)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /flujos status = %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}
	html := rec.Body.String()
	for _, quiero := range []string{`id="table-flujos"`, testFlowID, "v3", `href="/flujos/` + testFlowID + `"`,
		`href="/flujos/nuevo"`} {
		if !strings.Contains(html, quiero) {
			t.Errorf("la lista de flujos no trae %q", quiero)
		}
	}
	// 🔴 El distintivo del BFF NO se muda: allí decía que la pantalla migraba a ESTA consola.
	if strings.Contains(html, "PROVISIONAL") {
		t.Error("la pantalla de flujos de la consola trae el distintivo «PROVISIONAL» del BFF: aquí ya está en su casa")
	}
}

// TestFlujos_LaTablaConservaSusCuatroColumnas es la mitad NUEVA de la mutación de T6.2 —«quitar una
// columna del partial ⇒ rojo en la pantalla convertida Y en una nueva»—, y sin ella esa mutación
// solo estaría medio vigilada.
//
// Cuenta las columnas Y comprueba los nombres: un test que solo contara pasaría con las cuatro
// cabeceras cambiadas de sitio, y uno que solo mirara nombres pasaría con una quinta de más.
func TestFlujos_LaTablaConservaSusCuatroColumnas(t *testing.T) {
	t.Parallel()

	router, _ := editorRouter(t, nil)
	tabla := bloque(t, getWithSession(t, router, rutaFlujos).Body.String(), `id="table-flujos"`, `</thead>`)
	if n := strings.Count(tabla, `<th scope="col">`); n != 4 {
		t.Errorf("la tabla de flujos tiene %d columnas, want 4: %s", n, tabla)
	}
	for _, columna := range []string{"Flujo", "Versión", "Creado", "Acciones"} {
		if !strings.Contains(tabla, `<th scope="col">`+columna+`</th>`) {
			t.Errorf("la tabla de flujos perdió la columna %q", columna)
		}
	}
}

// TestFlujos_ElListadoQueFallaDEGRADAyNoTumbaLaPantalla.
//
// Se muda del BFF tal cual (`editor_handler.go:109`): un 502 aquí dejaría a la dueña sin forma de
// publicar nada justo cuando la plataforma va mal, y publicar NO necesita el listado.
func TestFlujos_ElListadoQueFallaDEGRADAyNoTumbaLaPantalla(t *testing.T) {
	t.Parallel()

	router, _ := editorRouter(t, map[string]stubResponse{
		"GET /api/v1/flows": {http.StatusInternalServerError, `{"error":"boom"}`},
	})
	rec := getWithSession(t, router, rutaFlujos)
	if rec.Code != http.StatusOK {
		t.Fatalf("un listado caído dio status %d; el modo degradado responde 200", rec.Code)
	}
	html := rec.Body.String()
	if !strings.Contains(html, `id="flujos-degradado"`) {
		t.Error("la pantalla degradada no avisa de que el listado no se pudo leer")
	}
	if !strings.Contains(html, `href="/flujos/nuevo"`) {
		t.Error("la pantalla degradada se quedó sin «Nuevo flujo»: publicar no necesita el listado")
	}
	if strings.Contains(html, "boom") {
		t.Error("el cuerpo del upstream acabó en pantalla")
	}
}

// TestFlujoNuevo_ElValorMagicoNoLlamaALaAPI.
//
// `/flujos/nuevo` pinta el formulario de alta SIN salir a la red (se muda del BFF, donde el valor
// mágico es `new`). El aserto que importa es el NEGATIVO: si esto llamara a la API, la plataforma
// respondería 404 —no existe ningún flujo llamado «nuevo»— y el alta sería inalcanzable.
func TestFlujoNuevo_ElValorMagicoNoLlamaALaAPI(t *testing.T) {
	t.Parallel()

	router, api := editorRouter(t, nil)
	rec := getWithSession(t, router, rutaFlujos+"/"+flujoNuevo)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /flujos/nuevo status = %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}
	for _, r := range api.Requests() {
		if strings.HasPrefix(r.Path, "/api/v1/flows/") {
			t.Errorf("/flujos/nuevo salió a la red: %s", r.Route())
		}
	}
	html := rec.Body.String()
	if !strings.Contains(html, "Nuevo flujo") {
		t.Error("el detalle no se pinta como alta")
	}
	if !strings.Contains(html, `name="is_new" value="1"`) {
		t.Error("el formulario no dice que es un alta")
	}
	// La definición de arranque tiene que ser JSON VÁLIDO: un ejemplo que su propio validador
	// rechazaría enseña a copiar lo que no funciona y convierte el primer intento en un 400.
	if !json.Valid([]byte(definicionDeArranque)) {
		t.Error("la definición de arranque del textarea NO es JSON válido")
	}
}

// TestFlujo_ElDetallePintaLaDefinicionQueSirveLaPlataforma.
func TestFlujo_ElDetallePintaLaDefinicionQueSirveLaPlataforma(t *testing.T) {
	t.Parallel()

	router, _ := editorRouter(t, nil)
	rec := getWithSession(t, router, rutaFlujos+"/"+testFlowID)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /flujos/{id} status = %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}
	dentro := contenidoDelTextarea(t, rec.Body.String())
	if !strings.Contains(dentro, "pedidos-panaderia") || !strings.Contains(dentro, "inicio") {
		t.Errorf("el textarea no trae la definición del flujo: %q", dentro)
	}
	if !strings.Contains(rec.Body.String(), `name="is_new" value="0"`) {
		t.Error("el detalle de un flujo existente se pinta como si fuera un alta")
	}
}

// TestFlujo_ElDetalleQueNoSePuedeAbrirVaALaListaConFlash.
//
// 🔧 SIMPLIFICACIÓN DECLARADA respecto al BFF: allí un 404/502 de GetFlow renderiza `flows.html` —la
// lista, no el detalle— con una llamada EXTRA a ListFlows dentro del camino de error. Aquí se
// redirige a la lista con su código de flash: el usuario acaba en la misma pantalla, la explicación
// sale del catálogo y el camino de error no hace una segunda llamada.
func TestFlujo_ElDetalleQueNoSePuedeAbrirVaALaListaConFlash(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre string
		status int
		want   string
	}{
		{"de otra empresa", http.StatusNotFound, flashNotInYourTenant},
		{"la plataforma no contesta", http.StatusInternalServerError, flashUpstreamUnavailable},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			router, _ := editorRouter(t, map[string]stubResponse{
				"GET /api/v1/flows/{id}": {caso.status, `{"error":"lo que sea"}`},
			})
			rec := getWithSession(t, router, rutaFlujos+"/"+testFlowID)
			if destino := redirectTarget(t, rec); destino != rutaFlujos+"?error="+caso.want {
				t.Fatalf("Location = %q, want %q", destino, rutaFlujos+"?error="+caso.want)
			}
		})
	}
}

// TestPublicarFlujo_ElExitoVaPorPRG es la MUTACIÓN (b): devolver 200 con HTML en la publicación buena
// en vez de 303 tiene que salir rojo.
//
// El éxito SÍ va por POST-redirect-GET (D-047.16): la petición salió y mutó, así que recargar
// publicaría una segunda versión idéntica del flujo.
func TestPublicarFlujo_ElExitoVaPorPRG(t *testing.T) {
	t.Parallel()

	router, api := editorRouter(t, nil)
	rec := postFormWithCSRF(router, rutaFlujos, url.Values{
		"flow_id":    {testFlowID},
		"is_new":     {"0"},
		"definition": {`{"flow_id":"pedidos-panaderia","version":4}`},
	}, clientSessionCookie(t))

	if destino := redirectTarget(t, rec); destino != rutaFlujos+"?success="+flashFlowPublished {
		t.Fatalf("Location = %q, want %q", destino, rutaFlujos+"?success="+flashFlowPublished)
	}
	// Y lo tecleado es lo que se publica: el cuerpo va envuelto en `{definition}` (el otro extremo de
	// este aserto está en apiclient/editor_test.go, y es uno de los tres huérfanos del BFF).
	cuerpo := api.Last(t, "POST /api/v1/flows").Body
	if !strings.Contains(cuerpo, `"definition"`) {
		t.Errorf("la publicación no envuelve la definición bajo `definition`: %s", cuerpo)
	}
	if !strings.Contains(cuerpo, "pedidos-panaderia") {
		t.Errorf("lo tecleado en el textarea no llegó a la plataforma: %s", cuerpo)
	}
}

// TestPublicarFlujo_ElJSONINVALIDOrepintaConLoTecleadoINTACTO.
//
// 🔴 ES LA MUTACIÓN (c) Y EL ASERTO QUE JUSTIFICA D-047.16 ENTERA: repintar el 400 con el textarea
// VACÍO tiene que salir ROJO. Sin él, la decisión se cumple «de palabra» —el código de estado es
// 400— y se incumple de hecho: el usuario perdió la definición igual.
//
// Y el otro medio, tan importante como el primero: NO se llama a la plataforma. Si se llamara,
// habría mutación y el 400 sin redirect dejaría un F5 capaz de publicar dos veces.
func TestPublicarFlujo_ElJSONINVALIDOrepintaConLoTecleadoINTACTO(t *testing.T) {
	t.Parallel()

	tecleado := "{\n  \"flow_id\": \"pedidos-panaderia\",\n  \"nodes\": ESTO_NO_ES_JSON,\n}"
	router, api := editorRouter(t, nil)
	rec := postFormWithCSRF(router, rutaFlujos, url.Values{
		"flow_id":    {testFlowID},
		"is_new":     {"0"},
		"definition": {tecleado},
	}, clientSessionCookie(t))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 repintando (D-047.16: la validación local NO redirige)", rec.Code)
	}
	if api.Called("POST /api/v1/flows") {
		t.Fatal("el JSON inválido llegó a la plataforma: la validación local tiene que cortar ANTES")
	}

	dentro := contenidoDelTextarea(t, rec.Body.String())
	if strings.TrimSpace(dentro) == "" {
		t.Fatal("el 400 repintó con el TEXTAREA VACÍO: el usuario perdió la definición entera, que es " +
			"exactamente lo que D-047.16 existe para impedir")
	}
	for _, trozo := range []string{"pedidos-panaderia", "ESTO_NO_ES_JSON"} {
		if !strings.Contains(dentro, trozo) {
			t.Errorf("el textarea repintado perdió %q; devolvió: %q", trozo, dentro)
		}
	}
	if !strings.Contains(rec.Body.String(), flashError(flashFlowInvalidJSON)) {
		t.Error("el repintado no explica por qué se rechazó")
	}
}

// TestPublicarFlujo_ElErrorDeLaAPIvaPorPRG: la otra mitad de D-047.16. Aquí la petición SALIÓ, así
// que pudo mutar y el redirect es lo que impide que un F5 lo repita.
func TestPublicarFlujo_ElErrorDeLaAPIvaPorPRG(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre string
		status int
		want   string
	}{
		{"la definición no valida contra el esquema", http.StatusBadRequest, flashInvalidInput},
		{"la plataforma no contesta", http.StatusBadGateway, flashUpstreamUnavailable},
		// El 409 HOY NO LO EMITE la plataforma (publicar versiona N+1 sin comprobar contra qué se
		// editaba). Se prueba contra el doble, que es donde sí se puede.
		{"alguien publicó entre medias", http.StatusConflict, flashFlowVersionConflict},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			router, _ := editorRouter(t, map[string]stubResponse{
				"POST /api/v1/flows": {caso.status, `{"error":"detalle del validador"}`},
			})
			rec := postFormWithCSRF(router, rutaFlujos, url.Values{
				"flow_id": {testFlowID}, "is_new": {"0"}, "definition": {`{"flow_id":"x"}`},
			}, clientSessionCookie(t))

			destino := redirectTarget(t, rec)
			if destino != rutaFlujos+"?error="+caso.want {
				t.Fatalf("Location = %q, want %q", destino, rutaFlujos+"?error="+caso.want)
			}
			// Y el detalle del upstream no acaba en pantalla: el texto sale del catálogo.
			if out := getWithSession(t, router, destino).Body.String(); strings.Contains(out, "detalle del validador") {
				t.Error("el cuerpo del upstream acabó en pantalla")
			}
		})
	}
}

// TestPublicarFlujo_ElFormularioLLEVAcsrf es la MUTACIÓN (a): quitar el `csrf_token` del formulario
// de publicar tiene que salir rojo.
//
// Va en DOS mitades porque una sola no basta: el aserto sobre el HTML caza el campo borrado, y el
// POST sin token caza que la defensa esté de verdad enchufada en esta ruta (un `<form>` con un token
// que nadie comprueba pasaría la primera mitad).
func TestPublicarFlujo_ElFormularioLLEVAcsrf(t *testing.T) {
	t.Parallel()

	router, _ := editorRouter(t, nil)
	form := bloque(t, getWithSession(t, router, rutaFlujos+"/"+flujoNuevo).Body.String(),
		`id="form-publicar"`, "</form>")
	if !strings.Contains(form, `name="csrf_token" value="`) {
		t.Errorf("el formulario de publicar no incrusta el token CSRF: %s", form)
	}
	if strings.Contains(form, `name="csrf_token" value=""`) {
		t.Error("el formulario de publicar incrusta un token CSRF vacío")
	}

	// Y sin token, la ruta rechaza. El POST se hace a mano: postFormWithCSRF lo pondría.
	rec := postSinCSRF(t, router, rutaFlujos, url.Values{"definition": {`{}`}})
	if rec.Code == http.StatusSeeOther || rec.Code == http.StatusOK {
		t.Errorf("POST /flujos sin token CSRF respondió %d: la defensa no cubre esta ruta", rec.Code)
	}
}

// --- DISPARADORES (T6.4) ---

// TestDisparadores_LaListaPintaLasReglasYsuTablaConservaSusNueveColumnas.
func TestDisparadores_LaListaPintaLasReglasYsuTablaConservaSusNueveColumnas(t *testing.T) {
	t.Parallel()

	router, _ := editorRouter(t, nil)
	rec := getWithSession(t, router, rutaDisparadores)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /disparadores status = %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}
	html := rec.Body.String()
	if strings.Contains(html, "PROVISIONAL") {
		t.Error("la pantalla de disparadores trae el distintivo «PROVISIONAL» del BFF")
	}

	tabla := bloque(t, html, `id="table-disparadores"`, `</thead>`)
	if n := strings.Count(tabla, `<th scope="col">`); n != 9 {
		t.Errorf("la tabla de disparadores tiene %d columnas, want 9: %s", n, tabla)
	}
	for _, columna := range []string{"Tipo", "Tipo de evento", "Palabra clave", "Coincidencia", "Flujo",
		"Prioridad", "Sesión", "Activo", "Acciones"} {
		if !strings.Contains(tabla, `<th scope="col">`+columna+`</th>`) {
			t.Errorf("la tabla de disparadores perdió la columna %q", columna)
		}
	}
	// El borrado es un POST con CSRF, no un enlace: un GET que borra lo dispara cualquier precarga.
	if !strings.Contains(html, `action="/disparadores/`+testTriggerID+`/borrar"`) {
		t.Error("la fila no ofrece el borrado por POST")
	}
}

// TestDisparadores_ElAvisoDeSOMBREADOsePinta.
//
// 🔴 HUÉRFANO RESCATADO DEL BFF. `shadowed_by_event_list` la calcula la plataforma (D-043.20 /
// REQ-27b) para los `fallback` a los que la lista de eventos del despachador se ofrece antes, y esta
// celda es LA ÚNICA REFERENCIA VIVA AL CAMPO en todo el ecosistema fuera de su declaración. Si la
// mudanza se la dejara por el camino, la marca seguiría viajando por el cable sin que nadie la viera
// nunca y la dueña seguiría creyendo que ese fallback contesta.
//
// El aserto NEGATIVO —que el disparador NO sombreado no lleve el aviso— importa igual: un aviso que
// saliera en todas las filas no estaría diciendo nada.
func TestDisparadores_ElAvisoDeSOMBREADOsePinta(t *testing.T) {
	t.Parallel()

	router, _ := editorRouter(t, nil)
	html := getWithSession(t, router, rutaDisparadores).Body.String()

	// 🔴 EL LITERAL SE ADAPTÓ A LO QUE ESTA PANTALLA PINTA, y decirlo importa: el aserto de origen
	// medía `chip--error">sombreado`, y esa clase es del CSS PROPIO del BFF — en `wapp-shared/ui` no
	// existe `wapp-chip--error`. Copiarlo tal cual habría dejado un test que no puede pasar; medir
	// solo el texto habría dejado uno que pasa con el distintivo BORRADO, quedando el párrafo suelto.
	// Se mide el par: la clase del componente compartido Y la palabra.
	if !strings.Contains(html, `wapp-chip--danger">sombreado`) {
		t.Error("el disparador sombreado no lleva su distintivo: el chip es lo que se ve de un vistazo en la tabla")
	}
	if !strings.Contains(html, "no llega a dispararse") {
		t.Error("la pantalla marca el sombreado pero no dice qué significa: quien lo lea no sabrá qué hacer")
	}
	// Y la clase EXISTE de verdad en la hoja compartida. Sin esto, un `wapp-chip--eror` pasaría el
	// aserto de arriba y se serviría SIN ESTILO: el aviso estaría en el HTML y no se vería en la
	// pantalla, que es la forma más silenciosa de perder esta funcionalidad.
	hoja, err := ui.GetCSS("wapp-components.css")
	if err != nil {
		t.Fatalf("no se pudo leer la hoja de componentes compartida: %v", err)
	}
	if !strings.Contains(string(hoja), ".wapp-chip--danger") {
		t.Error("el distintivo de sombreado usa una clase que NO está en wapp-shared/ui: se serviría sin estilo")
	}
	if n := strings.Count(html, "no llega a dispararse"); n != 1 {
		t.Errorf("el aviso de sombreado sale %d veces y solo UNA regla lo está: no distingue nada", n)
	}

	// Y sin la marca, el aviso no aparece: el gemelo que impide que esto pase con cualquier listado.
	sinMarca, _ := editorRouter(t, map[string]stubResponse{
		"GET /api/v1/triggers": {http.StatusOK, triggersBody(disparadorNormal)},
	})
	if out := getWithSession(t, sinMarca, rutaDisparadores).Body.String(); strings.Contains(out, "sombreado") {
		t.Error("el aviso de sombreado sale en un listado sin ninguna regla sombreada")
	}
}

// TestDisparadores_ElListadoQueFallaDEGRADA: mismo contrato que el de flujos.
func TestDisparadores_ElListadoQueFallaDEGRADA(t *testing.T) {
	t.Parallel()

	router, _ := editorRouter(t, map[string]stubResponse{
		"GET /api/v1/triggers": {http.StatusInternalServerError, `{"error":"boom"}`},
	})
	rec := getWithSession(t, router, rutaDisparadores)
	if rec.Code != http.StatusOK {
		t.Fatalf("un listado caído dio status %d; el modo degradado responde 200", rec.Code)
	}
	html := rec.Body.String()
	if !strings.Contains(html, `id="disparadores-degradado"`) {
		t.Error("la pantalla degradada no avisa de que el listado no se pudo leer")
	}
	if !strings.Contains(html, `id="form-crear-disparador"`) {
		t.Error("la pantalla degradada se quedó sin el formulario de alta")
	}
}

// TestCrearDisparador_ElExitoVaPorPRG.
func TestCrearDisparador_ElExitoVaPorPRG(t *testing.T) {
	t.Parallel()

	router, api := editorRouter(t, nil)
	rec := postFormWithCSRF(router, rutaDisparadores, url.Values{
		"kind": {"keyword"}, "keyword": {"hola"}, "flow_id": {"menu-general"},
		"match_type": {"exact"}, "priority": {"7"},
	}, clientSessionCookie(t))

	if destino := redirectTarget(t, rec); destino != rutaDisparadores+"?success="+flashTriggerCreated {
		t.Fatalf("Location = %q, want %q", destino, rutaDisparadores+"?success="+flashTriggerCreated)
	}
	cuerpo := api.Last(t, "POST /api/v1/triggers").Body
	for _, quiero := range []string{`"kind":"keyword"`, `"keyword":"hola"`, `"flow_id":"menu-general"`, `"priority":7`} {
		if !strings.Contains(cuerpo, quiero) {
			t.Errorf("el cuerpo del alta no lleva %s: %s", quiero, cuerpo)
		}
	}
}

// TestCrearDisparador_ElEventKindRESIDUALnoViaja.
//
// 🔴 HUÉRFANO RESCATADO DEL BFF, y es una invariante de FORMA DEL CUERPO que nadie más mide: el
// `<select>` de tipo de evento conserva el valor del envío anterior, así que el navegador manda un
// `event_kind` residual cuando alguien prueba un `event_start` y luego cambia el tipo a `keyword`.
// Mandarlo le pediría a la plataforma que guardara un tipo de evento en una regla que no arranca
// ninguno.
//
// El GEMELO POSITIVO va en el mismo test a propósito: sin él, un handler que no mandara NUNCA el
// `event_kind` —rompiendo `event_start` por completo— pasaría el aserto negativo.
func TestCrearDisparador_ElEventKindRESIDUALnoViaja(t *testing.T) {
	t.Parallel()

	router, api := editorRouter(t, nil)
	sess := clientSessionCookie(t)

	// NEGATIVO: el tipo no es event_start y el formulario trae un event_kind residual.
	postFormWithCSRF(router, rutaDisparadores, url.Values{
		"kind": {"keyword"}, "keyword": {"hola"}, "flow_id": {"menu-general"},
		"event_kind": {"menu"}, "match_type": {"exact"},
	}, sess)
	if cuerpo := api.Last(t, "POST /api/v1/triggers").Body; strings.Contains(cuerpo, "event_kind") {
		t.Errorf("un event_kind RESIDUAL viajó en un disparador de tipo keyword: %s", cuerpo)
	}

	// POSITIVO: en event_start sí viaja, o esta regla no podría crearse nunca.
	postFormWithCSRF(router, rutaDisparadores, url.Values{
		"kind": {"event_start"}, "keyword": {"pedido"}, "event_kind": {"cart"}, "match_type": {"exact"},
	}, sess)
	if cuerpo := api.Last(t, "POST /api/v1/triggers").Body; !strings.Contains(cuerpo, `"event_kind":"cart"`) {
		t.Errorf("el event_kind NO viajó en un event_start: %s", cuerpo)
	}
}

// TestCrearDisparador_ElCatalogoDeEventosNoOfreceCartLLM.
//
// Se muda del BFF tal cual: los cuatro tipos de fábrica del despachador (D-043.3, INV-07) y
// `cart_llm` FUERA. No es un olvido — depende del plan de la empresa, y esta pantalla no gatea por
// plan —, así que el aserto es sobre la ausencia y va con su positivo al lado.
func TestCrearDisparador_ElCatalogoDeEventosNoOfreceCartLLM(t *testing.T) {
	t.Parallel()

	router, _ := editorRouter(t, nil)
	selector := bloque(t, getWithSession(t, router, rutaDisparadores).Body.String(), `id="event_kind"`, "</select>")
	for _, tipo := range []string{"menu", "cart", "survey", "media"} {
		if !strings.Contains(selector, `value="`+tipo+`"`) {
			t.Errorf("el catálogo de tipos de evento perdió %q", tipo)
		}
	}
	if strings.Contains(selector, "cart_llm") {
		t.Error("el catálogo ofrece cart_llm, que depende del plan y esta pantalla no gatea por plan")
	}
	// Y el validador de Go dice lo mismo que el desplegable: un catálogo que solo viviera en el HTML
	// se saltaría con un envío a mano.
	if validEventKinds["cart_llm"] {
		t.Error("validEventKinds acepta cart_llm")
	}
}

// TestCrearDisparador_ElRECHAZOlocalRepintaConLosOCHOcamposINTACTOS es la MUTACIÓN (e): devolver el
// formulario vacío en el 400 de validación tiene que salir rojo.
//
// Es el hermano exacto del test del textarea, con la misma razón detrás (D-047.16): la validación es
// local, no hubo mutación, y un 303 obligaría a quien se dejó UN campo a volver a escribir los otros
// siete.
func TestCrearDisparador_ElRECHAZOlocalRepintaConLosOCHOcamposINTACTOS(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre string
		form   url.Values
		want   string
	}{
		{
			// keyword sin flow_id: el rechazo más frecuente de esta pantalla.
			nombre: "keyword sin flujo",
			form: url.Values{
				"kind": {"keyword"}, "keyword": {"hola-panaderia"}, "flow_id": {""},
				"event_kind": {"menu"}, "match_type": {"contains"}, "session_id": {"s-9999"},
				"message": {"hasta luego"}, "priority": {"12"},
			},
			want: flashTriggerKeywordIncomplete,
		},
		{
			// La prioridad no entera se juzga ANTES que el resto y también repinta.
			nombre: "prioridad que no es un número",
			form: url.Values{
				"kind": {"keyword"}, "keyword": {"hola-panaderia"}, "flow_id": {"menu-general"},
				"event_kind": {"menu"}, "match_type": {"contains"}, "session_id": {"s-9999"},
				"message": {"hasta luego"}, "priority": {"doce"},
			},
			want: flashTriggerPriorityNotInteger,
		},
		{
			nombre: "event_start sin tipo de evento",
			form: url.Values{
				"kind": {"event_start"}, "keyword": {"hola-panaderia"}, "flow_id": {"menu-general"},
				"event_kind": {""}, "match_type": {"contains"}, "session_id": {"s-9999"},
				"message": {"hasta luego"}, "priority": {"12"},
			},
			want: flashTriggerEventStartNoKind,
		},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			router, api := editorRouter(t, nil)
			rec := postFormWithCSRF(router, rutaDisparadores, caso.form, clientSessionCookie(t))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 repintando (D-047.16). Body: %s", rec.Code, rec.Body.String())
			}
			if api.Called("POST /api/v1/triggers") {
				t.Fatal("el rechazo local llegó a la plataforma: la validación tiene que cortar ANTES")
			}

			form := bloque(t, rec.Body.String(), `id="form-crear-disparador"`, "</form>")
			// Los OCHO campos, uno a uno. Los que van en <select> se comprueban por su `selected`,
			// que es lo que decide qué ve el usuario.
			for _, quiero := range []string{
				`value="` + caso.form.Get("kind") + `" selected`,
				`value="hola-panaderia"`,
				`value="` + caso.form.Get("flow_id") + `"`,
				`value="contains" selected`,
				`value="s-9999"`,
				`value="hasta luego"`,
				`value="` + caso.form.Get("priority") + `"`,
			} {
				if !strings.Contains(form, quiero) {
					t.Errorf("el formulario repintado perdió %q", quiero)
				}
			}
			// El octavo campo, el tipo de evento, según lo que se hubiera elegido.
			if ek := caso.form.Get("event_kind"); ek != "" && !strings.Contains(form, `value="`+ek+`" selected`) {
				t.Errorf("el formulario repintado perdió el tipo de evento %q", ek)
			}
			if !strings.Contains(rec.Body.String(), flashError(caso.want)) {
				t.Errorf("el repintado no explica el rechazo (%s)", caso.want)
			}
			// Y la tabla sigue ahí: repintar sin ella se lee como «se han borrado los disparadores».
			if !strings.Contains(rec.Body.String(), `id="table-disparadores"`) {
				t.Error("el repintado perdió la tabla de disparadores")
			}
		})
	}
}

// TestCrearDisparador_El422LLEGAcomoCodigoPROPIO es la MUTACIÓN (d): hacer que el 422 caiga en la
// rama por defecto del traductor tiene que salir rojo.
//
// 🔑 Es el ÚNICO de los tres desenlaces propios del editor que la plataforma devuelve HOY
// (`msg422DurableSinRed`, D-054.8 / MD-054.2): la regla está bien formada y aun así no se puede
// guardar, porque dejaría al contacto sin salida. `statusError` mete 400 y 422 en el MISMO
// ErrInvalidInput, así que por el genérico la pantalla diría «revisa lo que escribiste» ante un
// formulario cuyos datos están todos bien. Es el defecto de campo de la Ola 5 con otro número.
//
// El GEMELO —lo que el traductor genérico habría dicho— va al lado: sin él, alguien que «simplifique»
// flashCodeForEditor no rompería nada visible.
func TestCrearDisparador_El422LLEGAcomoCodigoPROPIO(t *testing.T) {
	t.Parallel()

	router, _ := editorRouter(t, map[string]stubResponse{
		"POST /api/v1/triggers": {http.StatusUnprocessableEntity,
			`{"error":"un trigger durable necesita una red de event_start"}`},
	})
	rec := postFormWithCSRF(router, rutaDisparadores, url.Values{
		"kind": {"fallback"}, "flow_id": {"menu-general"}, "match_type": {"exact"},
	}, clientSessionCookie(t))

	destino := redirectTarget(t, rec)
	if destino != rutaDisparadores+"?error="+flashTriggerWithoutEventStart {
		t.Fatalf("Location = %q, want %q", destino, rutaDisparadores+"?error="+flashTriggerWithoutEventStart)
	}
	// El texto tiene que decir que el formulario está BIEN y qué falta fuera de él.
	out := getWithSession(t, router, destino).Body.String()
	if !strings.Contains(out, "event_start") {
		t.Error("el aviso del 422 no dice qué falta: quien lo lea buscará el error en el formulario")
	}
	if strings.Contains(out, flashError(flashInvalidInput)) {
		t.Error("el 422 se explicó como «revisa lo que escribiste», y los datos estaban todos bien")
	}
	if strings.Contains(out, "un trigger durable necesita") {
		t.Error("el cuerpo del upstream acabó en pantalla")
	}

	// EL GEMELO: esto es lo que el traductor genérico habría dicho.
	err422 := fmt.Errorf("triggers.create: %w", apiclient.ErrTriggerWithoutEventStart)
	if got := flashCodeFor(err422); got != flashInvalidInput {
		t.Errorf("el genérico dio %q para el 422, want %q — si ya lo distingue, este test dejó de probar nada", got, flashInvalidInput)
	}
	if got := flashCodeForEditor(err422); got != flashTriggerWithoutEventStart {
		t.Errorf("flashCodeForEditor(422) = %q, want %q", got, flashTriggerWithoutEventStart)
	}
}

// TestBorrarDisparador_SIEMPREvaPorPRGyElClienteTraduceAdelete.
//
// 🔧 El borrado NO tiene formulario que perder —es un `<form>` de un solo botón dentro de la fila—,
// así que la excepción de D-047.16 no aplica: sus DOS desenlaces van por 303 + flash. Repintar solo
// cambiaría el código de estado.
//
// Y el verbo: el navegador manda POST y es el cliente de la API quien lo convierte en DELETE. Se
// comprueba sobre la petición que sale, no sobre la firma.
func TestBorrarDisparador_SIEMPREvaPorPRGyElClienteTraduceAdelete(t *testing.T) {
	t.Parallel()

	t.Run("éxito", func(t *testing.T) {
		t.Parallel()
		router, api := editorRouter(t, nil)
		rec := postFormWithCSRF(router, rutaDisparadores+"/"+testTriggerID+"/borrar", url.Values{},
			clientSessionCookie(t))
		if destino := redirectTarget(t, rec); destino != rutaDisparadores+"?success="+flashTriggerDeleted {
			t.Fatalf("Location = %q, want %q", destino, rutaDisparadores+"?success="+flashTriggerDeleted)
		}
		req := api.Last(t, "DELETE /api/v1/triggers/"+testTriggerID)
		if req.Method != http.MethodDelete {
			t.Errorf("el borrado salió como %s: el cliente no tradujo el POST del navegador a DELETE", req.Method)
		}
	})

	t.Run("la regla ya no existe", func(t *testing.T) {
		t.Parallel()
		router, _ := editorRouter(t, map[string]stubResponse{
			"DELETE /api/v1/triggers/{id}": {http.StatusNotFound, `{"error":"no encontrado"}`},
		})
		rec := postFormWithCSRF(router, rutaDisparadores+"/"+testTriggerID+"/borrar", url.Values{},
			clientSessionCookie(t))
		if destino := redirectTarget(t, rec); destino != rutaDisparadores+"?error="+flashNotInYourTenant {
			t.Fatalf("Location = %q, want %q", destino, rutaDisparadores+"?error="+flashNotInYourTenant)
		}
	})
}

// --- El nav (lado consola de T6.5) ---

// TestNav_LasDosPantallasDelEditorEstanEnLaBarra.
//
// El aserto va sobre el HTML RENDERIZADO y no sobre la plantilla, y los enlaces se comprueban contra
// las rutas que el router SIRVE de verdad: un enlace a una ruta que su propia casa no sirve es un 404
// con forma de menú.
func TestNav_LasDosPantallasDelEditorEstanEnLaBarra(t *testing.T) {
	t.Parallel()

	router, _ := editorRouter(t, nil)
	html := getWithSession(t, router, "/").Body.String()
	barra := bloque(t, html, "<nav", "</nav>")

	for etiqueta, ruta := range map[string]string{"Flujos": rutaFlujos, "Disparadores": rutaDisparadores} {
		if !strings.Contains(barra, `href="`+ruta+`"`) {
			t.Errorf("la barra no ofrece %q (%s)", etiqueta, ruta)
		}
		if !strings.Contains(barra, ">"+etiqueta+"</a>") {
			t.Errorf("la barra no rotula el enlace como %q", etiqueta)
		}
	}
	// Y «Cerrar sesión» sigue ahí: es lo que la barra expulsó la última vez que creció (T5.3).
	// ⚠️ Esto NO demuestra que quepa —un test de Go no mide píxeles—; los candados de ancho están en
	// barra_test.go y la comprobación de verdad es en un navegador.
	if !strings.Contains(barra, "Cerrar sesión") {
		t.Error("la barra se quedó sin «Cerrar sesión»")
	}

	// Sin empresa no se ofrecen: las dos pantallas responderían 403 desde la plataforma, y un enlace
	// que lleva a una explicación de por qué no funciona es peor que no ofrecerlo.
	sin := getConCookie(router, "/", sessionCookieFor(t, testUserID, "")).Body.String()
	if strings.Contains(bloque(t, sin, "<nav", "</nav>"), `href="`+rutaFlujos+`"`) {
		t.Error("la barra ofrece Flujos a una sesión sin empresa")
	}
}
