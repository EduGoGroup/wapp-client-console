package web

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

// solicitudes_gate_test.go vigila EL GATE POR RUTA (Plan 047 · T7.2), que es lo que nace en esa
// casilla y lo único de esta pantalla que no tiene precedente en este repo.
//
// 🔴 TODOS los asertos de aquí son sobre el CÓDIGO DE RESPUESTA de una petición DIRECTA, nunca sobre
// la ausencia del enlace en el HTML. Esconder un enlace es lo que esta consola ya sabía hacer, y es
// justo lo que no basta: quien teclea la URL entra igual.

// sinCartBasic devuelve el plan de un tenant que NO tiene la bandeja contratada. `catalog_import`
// va dentro a propósito: así el plan está RESUELTO y con features, y lo que falla es la feature
// concreta y no la resolución — que es el otro motivo por el que el gate podría cerrar.
func sinCartBasic() map[string]stubResponse {
	return map[string]stubResponse{
		"GET /api/v1/entitlements": {http.StatusOK, entitlementsBody("basic", featureCatalogImport, "menu")},
	}
}

// TestGate_UnTenantSinCartBasicNOENTRAPorLaURL es el criterio 3 de la casilla, y el test que la
// MUTACIÓN declarada tiene que poner en rojo: quitar el gate de la ruta y dejar solo el del nav.
//
// El aserto es el 403, y va con dos más que impiden que pase «de palabra»: que la bandeja NO se haya
// pedido a la plataforma (el corte es ANTES de renderizar, no después) y que el HTML no traiga ni
// una fila.
func TestGate_UnTenantSinCartBasicNOENTRAPorLaURL(t *testing.T) {
	t.Parallel()
	router, api := solicitudesRouter(t, sinCartBasic())

	rec := getWithSession(t, router, rutaSolicitudes)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET %s sin cart_basic = %d, want 403. 🔴 Este es el criterio de la casilla: el gate "+
			"por RUTA, no el del nav. Body: %s", rutaSolicitudes, rec.Code, rec.Body.String())
	}
	if api.Called("GET /api/v1/intakes") {
		t.Error("se pidió la bandeja a la plataforma pese a no tener la feature: el corte llegó tarde")
	}
	out := rec.Body.String()
	if strings.Contains(out, `id="section-listado"`) || strings.Contains(out, `id="form-descarte"`) {
		t.Error("el 403 sirvió la bandeja igualmente: el gate decide lo que se PINTA y no solo el código")
	}
	// Y lo que sí trae: la explicación de qué falta, que no es un permiso.
	if !strings.Contains(out, `id="section-sin-plan"`) {
		t.Errorf("el 403 no explica que el plan no incluye la bandeja. Body: %s", out)
	}
	if !strings.Contains(out, "no es cosa de tus permisos") && !strings.Contains(out, "No es cosa de tus permisos") {
		t.Error("la explicación no descarta el diagnóstico falso («no tienes permiso»)")
	}
}

// TestGate_ElPOSTDelDescarteTAMBIENCorta.
//
// 🔑 Es la mitad que el BFF tenía repartida en cinco `if` copiados, y por eso su GET (200) y sus
// POST (403) acabaron respondiendo distinto ante la misma ausencia de feature sin que nadie lo
// decidiera. Con el middleware sobre el grupo, GET y POST responden lo MISMO por construcción.
func TestGate_ElPOSTDelDescarteTAMBIENCorta(t *testing.T) {
	t.Parallel()
	router, api := solicitudesRouter(t, sinCartBasic())

	rec := postFormWithCSRF(router, rutaSolicitudesDescartar, url.Values{
		"intake_id": {testIntakeID},
		"action":    {"discard"},
	}, clientSessionCookie(t))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST %s sin cart_basic = %d, want 403", rutaSolicitudesDescartar, rec.Code)
	}
	if api.Called("POST /api/v1/intakes/discard") {
		t.Fatal("🔴 se descartó un lote sin la feature: el gate no cortó el POST")
	}
}

// TestGate_ElMISMOCodigoEnGETYEnPOST fija por escrito lo que el BFF perdió: que la respuesta no
// dependa del verbo. Comparar los dos códigos en un solo test es lo que hace que una divergencia
// futura se vea como divergencia y no como dos tests independientes que alguien ajusta por separado.
func TestGate_ElMISMOCodigoEnGETYEnPOST(t *testing.T) {
	t.Parallel()
	router, _ := solicitudesRouter(t, sinCartBasic())

	get := getWithSession(t, router, rutaSolicitudes).Code
	post := postFormWithCSRF(router, rutaSolicitudesDescartar, url.Values{
		"intake_id": {testIntakeID},
	}, clientSessionCookie(t)).Code

	if get != post {
		t.Errorf("sin la feature, el GET responde %d y el POST %d: es exactamente la divergencia que "+
			"el `if` copiado del BFF produjo", get, post)
	}
}

// TestGate_ConLaFeatureLaPantallaSEABRE es el positivo, y no es de adorno: sin él, los negativos de
// arriba seguirían verdes con un gate que cerrara SIEMPRE, que es la otra forma de romper esto.
func TestGate_ConLaFeatureLaPantallaSEABRE(t *testing.T) {
	t.Parallel()
	router, api := solicitudesRouter(t, nil)

	rec := getWithSession(t, router, rutaSolicitudes)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s CON cart_basic = %d, want 200. Body: %s", rutaSolicitudes, rec.Code, rec.Body.String())
	}
	if !api.Called("GET /api/v1/intakes") {
		t.Error("con la feature no se llegó a pedir la bandeja")
	}
	if !strings.Contains(rec.Body.String(), `id="section-listado"`) {
		t.Error("con la feature no se sirvió el listado")
	}
}

// TestGate_FailClosedSiElPlanNOSEPUEDELEER es el criterio 4.
//
// 🔴 El fail-closed es POR CONSTRUCCIÓN y no por una rama: resolveEntitlements devuelve la vista
// CERO cuando falla, su mapa `enabled` es nil, y leer un mapa nil da false. Lo que este test protege
// es que nadie añada el `if ent.Resolved` que lo abriría — el único camino por el que un fallo del
// upstream se convertiría en acceso.
func TestGate_FailClosedSiElPlanNOSEPUEDELEER(t *testing.T) {
	t.Parallel()

	for nombre, respuesta := range map[string]stubResponse{
		"el catálogo de planes no contesta": {http.StatusBadGateway, `{"error":"upstream"}`},
		"el usuario no puede consultarlo":   {http.StatusForbidden, `{"error":"forbidden"}`},
		"la respuesta es ilegible":          {http.StatusOK, `no soy json`},
	} {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			router, api := solicitudesRouter(t, map[string]stubResponse{
				"GET /api/v1/entitlements": respuesta,
			})

			rec := getWithSession(t, router, rutaSolicitudes)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("con «%s» el gate respondió %d, want 403: la puerta tiene que quedar CERRADA",
					nombre, rec.Code)
			}
			if api.Called("GET /api/v1/intakes") {
				t.Error("se pidió la bandeja con el plan sin resolver")
			}
		})
	}
}

// TestGate_ElPlanSePreguntaUNASOLAVEZPorPeticion.
//
// 🔑 Es el segundo fleco obligatorio del contrato del gate: el middleware SIEMBRA la vista en el
// contexto y el handler la REUTILIZA. Sin eso, cada carga de la bandeja pagaría DOS llamadas a
// /entitlements —esta consola las resuelve sin caché, una por petición—, y el coste se duplicaría en
// silencio: la pantalla se vería igual.
func TestGate_ElPlanSePreguntaUNASOLAVEZPorPeticion(t *testing.T) {
	t.Parallel()
	router, api := solicitudesRouter(t, nil)

	getWithSession(t, router, rutaSolicitudes)

	veces := 0
	for _, r := range api.Requests() {
		if r.Route() == "GET /api/v1/entitlements" {
			veces++
		}
	}
	if veces != 1 {
		t.Errorf("se preguntó por el plan %d veces en una sola petición, want 1: el handler no está "+
			"reutilizando la vista que sembró el gate", veces)
	}
}

// TestGate_SinEmpresaNoSePreguntaPorElPlanYNoSaleUn403.
//
// 🔴 Es la excepción declarada del gate, y el motivo es que un 403 ahí sería un DIAGNÓSTICO FALSO: a
// esa sesión no le falta un plan, le falta una empresa. `GET /api/v1/entitlements` responde 401 sin
// tenant, así que preguntarlo costaría un refresco contra identity en cada carga para acabar en la
// vista cero. Quien se lo explica es el parcial `sin_empresa`, igual que en las otras ocho pantallas.
//
// Esto NO abre ninguna puerta, y el test lo comprueba: sin empresa no se pide la bandeja.
func TestGate_SinEmpresaNoSePreguntaPorElPlanYNoSaleUn403(t *testing.T) {
	t.Parallel()
	// El listado de empresas va VACÍO: es el caso de quien se registró y todavía no pertenece a
	// ninguna, que es el que pinta el canje. Con empresas entre las que elegir, el parcial pinta el
	// selector, que es la otra mitad del mismo estado (T5.3).
	router, api := solicitudesRouter(t, map[string]stubResponse{
		rutaListadoDeEmpresas: {http.StatusOK, tenantsBody()},
	})

	rec := getConCookie(router, rutaSolicitudes, sessionCookieFor(t, testUserID, ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s sin empresa = %d, want 200 (el parcial `sin_empresa`)", rutaSolicitudes, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `id="form-canjear"`) {
		t.Error("la pantalla no ofrece el canje: quien no tiene empresa se queda sin la salida")
	}
	for _, ruta := range []string{"GET /api/v1/entitlements", "GET /api/v1/intakes"} {
		if api.Called(ruta) {
			t.Errorf("sin empresa se llamó a %s, cuya respuesta ya se sabe", ruta)
		}
	}
}

// TestGate_ElPOSTSinEmpresaNoEscribeNada: la otra mitad del anterior. Va por 303 a la bandeja —que
// le explica lo que le pasa— y no llega a tocar la API.
func TestGate_ElPOSTSinEmpresaNoEscribeNada(t *testing.T) {
	t.Parallel()
	router, api := solicitudesRouter(t, nil)

	rec := postFormWithCSRF(router, rutaSolicitudesDescartar, url.Values{
		"intake_id": {testIntakeID},
		"action":    {"discard"},
	}, sessionCookieFor(t, testUserID, ""))

	if destino := redirectTarget(t, rec); destino != rutaSolicitudes {
		t.Errorf("Location = %q, want %q", destino, rutaSolicitudes)
	}
	if api.Called("POST /api/v1/intakes/discard") {
		t.Fatal("se descartó un lote desde una sesión sin empresa")
	}
}

// TestGate_ElGateYLaPlantillaHablanDeLaMISMAFeature.
//
// 🔴 El gate de la plantilla vive en una CADENA que no compila nadie —`{{ if $.Entitlements.Has
// "cart_basic" }}`—, así que cambiar la constante de Go y olvidarse del `.html` deja los dos
// apuntando a features distintas con `vet`, el linter y todos los tests en verde. Lo único que se
// vería es una pantalla que se pinta cuando no debe. Es el mismo candado que la portada tiene desde
// el Plan 040, aplicado a la pantalla que ahora TAMBIÉN corta la ruta.
func TestGate_ElGateYLaPlantillaHablanDeLaMISMAFeature(t *testing.T) {
	t.Parallel()

	crudo, err := templatesFS.ReadFile("templates/pages/" + plantillaSolicitudes)
	if err != nil {
		t.Fatalf("leer la plantilla de la bandeja: %v", err)
	}
	pantalla := string(crudo)

	gate := `{{ else if $.Entitlements.Has "` + featureCartBasic + `" }}`
	if !strings.Contains(pantalla, gate) {
		t.Fatalf("la bandeja no gatea por %q; el gate que tiene no coincide con la constante de Go",
			featureCartBasic)
	}
	if n := strings.Count(pantalla, `$.Entitlements.Has `); n != 1 {
		t.Errorf("la bandeja tiene %d gates por feature, want 1: cada uno más es un sitio donde "+
			"desincronizarse", n)
	}
}

// TestGate_ElUNICO403DeLaConsolaEsElDeLaFeature.
//
// 🔴 T7.2 INAUGURÓ el 403 en este repo: antes, `grep StatusForbidden` sobre internal/web (sin tests)
// daba CERO — la consola solo emitía 400 (validación local) y un 404. El candado no prohíbe que
// aparezca otro; obliga a que aparezca A PROPÓSITO, pasando por aquí, en vez de colarse como el
// código con el que alguien traduzca el 403 de un upstream (que en esta casa se traduce a flash, no
// a status).
//
// 🔒 SEGUNDO EMISOR, DECLARADO (T7.4): `solicitudes_comparacion.go`, cuando falta `llm_intake` al
// pedir una regeneración. Es EL MISMO HECHO que corta el middleware —una capacidad que el plan no
// incluye— dicho desde el otro lado de la puerta: el gate del grupo solo cubre `cart_basic`, porque
// `llm_intake` lo exige el servicio de la plataforma y no su middleware, así que la bandeja abre y
// esa acción no. Tiene que responder LO MISMO que el gate, o la consola diría dos cosas distintas
// sobre «tu plan no lo incluye» sin que nadie lo hubiera decidido.
//
// 🔒 TERCER EMISOR, DECLARADO (T7.6): `solicitudes_sugerencia.go`, cuando falta `llm_intake` al pedir
// la respuesta redactada. Es EXACTAMENTE el mismo hecho que el segundo —la misma capacidad, la misma
// puerta que el gate del grupo no cubre— sobre la otra acción que la necesita, así que responde lo
// mismo por la misma razón. Que sean dos ficheros y no uno es porque son dos acciones con vistas
// distintas, no dos criterios.
//
// Lo que sí cambia entre los tres es qué se pinta con ese 403, y también es deliberado: el gate sirve
// la pantalla VACÍA (no hay nada que conservar) y las otras dos REPINTAN el detalle —la regeneración
// con el material extra tecleado dentro; la sugerencia sin nada que conservar, porque es un botón,
// pero con el botón deshabilitado y su razón delante—. El código de estado y el repintado son
// decisiones independientes.
func TestGate_ElUNICO403DeLaConsolaEsElDeLaFeature(t *testing.T) {
	t.Parallel()

	emisores := ficherosConStatusForbidden(t)
	want := map[string]bool{
		"solicitudes_gate.go":        true,
		"solicitudes_comparacion.go": true,
		"solicitudes_sugerencia.go":  true,
	}
	for _, f := range emisores {
		if !want[f] {
			t.Errorf("%s emite un 403 y no es el gate por feature: si es deliberado, añádelo a este "+
				"candado y escribe por qué", f)
		}
	}
	if len(emisores) == 0 {
		t.Error("nadie emite ya un 403: el gate por ruta dejó de cortar")
	}
}

// ficherosConStatusForbidden devuelve los .go de producción de este paquete que nombran
// `http.StatusForbidden`.
//
// Mira el CÓDIGO FUENTE y no el HTML servido a propósito: lo que se vigila es dónde se DECIDE
// responder 403, y eso no se puede observar por el cable —una pantalla que devolviera 403 desde otro
// sitio se vería igual—. Se excluyen los tests: ahí el literal es lo que se espera, no lo que se
// emite.
func ficherosConStatusForbidden(t *testing.T) []string {
	t.Helper()
	entradas, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("no se pudo listar el paquete: %v", err)
	}
	var out []string
	for _, e := range entradas {
		nombre := e.Name()
		if e.IsDir() || !strings.HasSuffix(nombre, ".go") || strings.HasSuffix(nombre, "_test.go") {
			continue
		}
		cuerpo, err := os.ReadFile(nombre)
		if err != nil {
			t.Fatalf("no se pudo leer %s: %v", nombre, err)
		}
		if strings.Contains(string(cuerpo), "http.StatusForbidden") {
			out = append(out, nombre)
		}
	}
	return out
}
