package web

import (
	"bytes"
	"encoding/csv"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	sharedweb "github.com/EduGoGroup/wapp-shared/web"
)

// catalogo_test.go cubre la IMPORTACIÓN DE CATÁLOGO (Plan 047 · T8.2 · T8.3 · T8.4).
//
// 🔑 EL CRITERIO QUE SOSTIENE ESTE FICHERO, y por el que varios asertos miran la petición SALIENTE en
// vez de la respuesta: los dos pasos de esta pantalla comparten RUTA, PLANTILLA Y FORMULARIO, y solo
// los separa el `mode` que manda el botón pulsado. Un test que compruebe «el POST contestó» —o
// incluso «contestó 303»— pasa con los dos confundidos, y el desenlace de confundirlos es el catálogo
// entero de una empresa reemplazado sin que nadie lo aprobara. Lo único que los distingue de verdad
// es la query de la llamada al upstream.

// --- Rutas del doble ---

const (
	rutaImportJSON      = "POST /api/v1/catalog/import"
	rutaImportTabular   = "POST /api/v1/catalog/import/tabular"
	rutaPromptCatalogo  = "GET /api/v1/catalog/import/prompt"
	rutaRefsDeContenido = "GET /api/v1/tenant-content"
)

// promptDeLaPlataforma es el texto que sirve el doble. Es reconocible a propósito: uno de los
// criterios es que ese texto NO esté pegado en este repo, y con un lorem cualquiera el aserto no
// distinguiría una cosa de la otra.
const promptDeLaPlataforma = "Eres un asistente que convierte una lista de productos en el documento del contrato."

// diffDeCampo es una respuesta de COMPROBACIÓN con las cinco clases de cambio a la vez, para que la
// pantalla pinte sus tres tablas, su resumen y el aviso del catálogo vigente.
const diffDeCampo = `{"mode":"validate","ref":"catalogo","applied":false,"items":3,` +
	`"diff":{"price_changes":[{"sku":"PAN-01","label":"Pan de yema","old_price":1,"new_price":1.5}],` +
	`"added":[{"sku":"TOR-09","label":"Torta de chocolate"}],` +
	`"removed":[{"sku":"EMP-02","label":"Empanada de viento"}],` +
	`"changed_details":["QUE-03"],"unchanged":4,` +
	`"current_warnings":["el articulo _envio usa un sku reservado y el motor lo ignora"]}}`

// diffTabular es lo mismo por la puerta de la planilla: trae ADEMÁS el documento ya normalizado, que
// es lo que sostiene el segundo paso.
const documentoNormalizado = `{"format":"wapp.catalog_import","version":1,"catalog":{"categories":[]}}`

const diffTabularDeCampo = `{"mode":"validate","ref":"catalogo","applied":false,"items":3,` +
	`"diff":{"price_changes":[],"added":[],"removed":[],"changed_details":[],"unchanged":3,` +
	`"current_warnings":[]},"document":` + documentoNormalizado + `}`

const aplicadoDeCampo = `{"mode":"apply","ref":"catalogo","applied":true,"items":3,"archived_version":7,` +
	`"diff":{"price_changes":[],"added":[],"removed":[],"changed_details":[],"unchanged":3,` +
	`"current_warnings":[]}}`

// documentoPegado es un JSON mínimo pero con la forma del contrato.
const documentoPegado = `{"format":"wapp.catalog_import","version":1,"catalog":{"categories":[{"name":"Panes"}]}}`

// refsBody arma la respuesta de GET /api/v1/tenant-content.
func refsBody(refs ...string) string {
	filas := make([]string, 0, len(refs))
	for _, r := range refs {
		filas = append(filas, `{"ref":"`+r+`","created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-02T10:00:00Z"}`)
	}
	return "[" + strings.Join(filas, ",") + "]"
}

// rutasDelCatalogo son las del doble para esta pantalla, con el plan que incluye la capacidad.
func rutasDelCatalogo(extra map[string]stubResponse) map[string]stubResponse {
	rutas := map[string]stubResponse{
		"GET /api/v1/entitlements": {http.StatusOK, entitlementsBody("commerce", featureCatalogImport, "menu")},
		rutaListadoDeEmpresas:      {http.StatusOK, unaEmpresa()},
		rutaRefsDeContenido:        {http.StatusOK, refsBody("catalogo", "promociones")},
		rutaPromptCatalogo: {http.StatusOK,
			`{"format":"wapp.catalog_import","version":1,"prompt":"` + promptDeLaPlataforma + `"}`},
		rutaImportJSON:    {http.StatusOK, diffDeCampo},
		rutaImportTabular: {http.StatusOK, diffTabularDeCampo},
	}
	for ruta, resp := range extra {
		rutas[ruta] = resp
	}
	return rutas
}

// routerDelCatalogo monta el router contra un doble con las rutas de esta pantalla.
func routerDelCatalogo(t *testing.T, extra map[string]stubResponse) (*stubAPI, http.Handler) {
	t.Helper()
	api := newStubAPI(t, rutasDelCatalogo(extra))
	return api, adminRouter(api)
}

// ficheroSubido es la parte de fichero de un envío multipart.
type ficheroSubido struct {
	nombre    string
	contenido []byte
}

// postMultipartConCSRF manda el formulario de esta pantalla como lo manda el navegador: multipart,
// con el double-submit completo y, si se le da, con la parte del fichero.
//
// Vive aquí y no en el arnés común porque es el ÚNICO formulario de esta consola que sube algo: el
// resto son `x-www-form-urlencoded` y les basta postFormWithCSRF.
func postMultipartConCSRF(t *testing.T, router http.Handler, path string,
	campos url.Values, fichero *ficheroSubido, sess *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	csrf := mintCSRF(router)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField(sharedweb.CSRFFieldName, csrf.Value); err != nil {
		t.Fatalf("escribir el campo CSRF: %v", err)
	}
	for clave, valores := range campos {
		for _, v := range valores {
			if err := mw.WriteField(clave, v); err != nil {
				t.Fatalf("escribir el campo %q: %v", clave, err)
			}
		}
	}
	if fichero != nil {
		w, err := mw.CreateFormFile(campoArchivoCatalogo, fichero.nombre)
		if err != nil {
			t.Fatalf("crear la parte del fichero: %v", err)
		}
		if _, err := w.Write(fichero.contenido); err != nil {
			t.Fatalf("escribir el fichero: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("cerrar el multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(csrf)
	if sess != nil {
		req.AddCookie(sess)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// --- EL PASO 1 ---

// TestCatalogo_ElPaso1PintaSusDosEntradasSuSelectorYSusPlantillas.
func TestCatalogo_ElPaso1PintaSusDosEntradasSuSelectorYSusPlantillas(t *testing.T) {
	t.Parallel()
	_, router := routerDelCatalogo(t, nil)

	rec := getWithSession(t, router, rutaCatalogo)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200. Body: %s", rutaCatalogo, rec.Code, rec.Body.String())
	}
	out := rec.Body.String()

	// Las DOS entradas del paso 1: pegar y subir. Que falte una no rompe la otra, y por eso las dos
	// se afirman.
	for _, ancla := range []string{
		`id="section-catalogo-subir"`,
		`id="document"`,
		`type="file" id="file" name="file"`,
		`name="mode" value="validate"`,
	} {
		if !strings.Contains(out, ancla) {
			t.Errorf("el paso 1 no trae %s", ancla)
		}
	}
	// El selector de ref, con las DOS del tenant y exactamente una marcada.
	for _, opcion := range []string{`<option value="catalogo" selected>`, `<option value="promociones">`} {
		if !strings.Contains(out, opcion) {
			t.Errorf("el selector de ref no trae %s. Body: %s", opcion, out)
		}
	}
	if n := strings.Count(out, ` selected>`); n != 1 {
		t.Errorf("el selector trae %d opciones marcadas, want 1", n)
	}
	// Los tres enlaces de descarga, compuestos con la ruta que el router sirve de verdad.
	for _, formato := range []string{"json", "csv", "xlsx"} {
		if !strings.Contains(out, `href="`+rutaPlantillaCatalogo+`?format=`+formato+`"`) {
			t.Errorf("falta el enlace de la plantilla %q", formato)
		}
	}
	// Y el paso 2 NO está: no hay nada comprobado todavía.
	if strings.Contains(out, `id="section-catalogo-confirmar"`) {
		t.Error("el paso 1 ofrece el botón de aplicar sin haber comprobado nada")
	}
}

// TestCatalogo_ElPromptSaleDeLaPlataformaYNoDeEsteRepo.
//
// El texto está versionado junto al contrato en la plataforma. Una copia pegada aquí se quedaría
// vieja sin que nadie lo notara y le dictaría al asistente un formato que el validador ya rechaza.
func TestCatalogo_ElPromptSaleDeLaPlataformaYNoDeEsteRepo(t *testing.T) {
	t.Parallel()
	api, router := routerDelCatalogo(t, nil)

	out := getWithSession(t, router, rutaCatalogo).Body.String()
	if !strings.Contains(out, promptDeLaPlataforma) {
		t.Error("el prompt que pinta la pantalla no es el que sirvió la plataforma")
	}
	if !api.Called(rutaPromptCatalogo) {
		t.Error("la pantalla no pidió el prompt: lo estaría sacando de algún sitio de este repo")
	}
}

// TestCatalogo_SiElPromptNoCargaLaPantallaSigueSirviendo: es ayuda, no la operación.
func TestCatalogo_SiElPromptNoCargaLaPantallaSigueSirviendo(t *testing.T) {
	t.Parallel()
	_, router := routerDelCatalogo(t, map[string]stubResponse{
		rutaPromptCatalogo: {http.StatusInternalServerError, `{"error":"detalle interno"}`},
	})

	rec := getWithSession(t, router, rutaCatalogo)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (degradado)", rec.Code)
	}
	out := rec.Body.String()
	if !strings.Contains(out, `id="catalogo-prompt-caido"`) {
		t.Error("no se avisa de que el texto para el asistente no cargó")
	}
	if !strings.Contains(out, `id="form-catalogo-comprobar"`) {
		t.Error("el fallo del prompt se llevó por delante el formulario, que es lo único imprescindible")
	}
	if strings.Contains(out, "detalle interno") {
		t.Error("el cuerpo del upstream acabó en pantalla")
	}
}

// TestCatalogo_SinRefsSigueOfreciendoUna: una empresa nueva tiene que poder estrenar catálogo, y un
// desplegable vacío no dejaría importar nunca (defecto A3 del Plan 041).
func TestCatalogo_SinRefsSigueOfreciendoUna(t *testing.T) {
	t.Parallel()
	_, router := routerDelCatalogo(t, map[string]stubResponse{
		rutaRefsDeContenido: {http.StatusOK, `[]`},
	})

	out := getWithSession(t, router, rutaCatalogo).Body.String()
	if !strings.Contains(out, `<option value="`+refDeArranque+`" selected>`) {
		t.Errorf("sin refs no se ofrece la de arranque. Body: %s", out)
	}
}

// --- 🔑 LOS DOS PASOS, Y LO ÚNICO QUE LOS SEPARA ---

// TestCatalogo_ElBotonDecideElMODOdeLaPeticionSALIENTE es LA prueba de esta ola.
//
// 🔴 POR QUÉ MIRA LA QUERY DE LA LLAMADA Y NO EL CÓDIGO DE RESPUESTA: los dos pasos comparten ruta,
// plantilla y formulario. Un aserto sobre «el POST contestó» pasa con los dos confundidos, y el
// desenlace de confundirlos no es una pantalla fea — es el catálogo entero de una empresa reemplazado
// sin que nadie lo aprobara, sobre una operación que además borra lo que no venga en el documento.
//
// Los DOS casos van en el MISMO test a propósito: el par es el que prueba algo. Con solo el de
// comprobar, un `mode` clavado a «validate» pasaría (y el botón de aplicar no aplicaría nunca); con
// solo el de aplicar, un `mode` clavado a «apply» pasaría (y comprobar escribiría).
func TestCatalogo_ElBotonDecideElMODOdeLaPeticionSALIENTE(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre    string
		modo      string
		respuesta stubResponse
		wantModo  string
	}{
		{"comprobar", "validate", stubResponse{http.StatusOK, diffDeCampo}, "validate"},
		{"aplicar", "apply", stubResponse{http.StatusOK, aplicadoDeCampo}, "apply"},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			api, router := routerDelCatalogo(t, map[string]stubResponse{rutaImportJSON: caso.respuesta})

			postMultipartConCSRF(t, router, rutaCatalogo, url.Values{
				campoModoCatalogo:      {caso.modo},
				campoRefCatalogo:       {"catalogo"},
				campoDocumentoCatalogo: {documentoPegado},
			}, nil, clientSessionCookie(t))

			req := api.Last(t, rutaImportJSON)
			if got := req.Query.Get("mode"); got != caso.wantModo {
				t.Fatalf("el botón %q mandó mode=%q, want %q: los dos pasos están confundidos",
					caso.modo, got, caso.wantModo)
			}
			// Y NO hubo una segunda llamada con el otro modo, que es como se colaría un apply
			// «de propina» detrás de un validate.
			for _, r := range api.Requests() {
				if r.Route() == rutaImportJSON && r.Query.Get("mode") != caso.wantModo {
					t.Errorf("además del %q salió una llamada con mode=%q", caso.wantModo, r.Query.Get("mode"))
				}
			}
		})
	}
}

// TestCatalogo_ComprobarRepintaConElDiffYNoRedirige (D-047.16: no mutó nada).
func TestCatalogo_ComprobarRepintaConElDiffYNoRedirige(t *testing.T) {
	t.Parallel()
	_, router := routerDelCatalogo(t, nil)

	rec := postMultipartConCSRF(t, router, rutaCatalogo, url.Values{
		campoModoCatalogo:      {"validate"},
		campoRefCatalogo:       {"catalogo"},
		campoDocumentoCatalogo: {documentoPegado},
	}, nil, clientSessionCookie(t))

	if rec.Code != http.StatusOK {
		t.Fatalf("comprobar status = %d, want 200 repintando (D-047.16). Body: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	for _, ancla := range []string{
		`id="section-catalogo-diff"`,
		`id="section-catalogo-confirmar"`,
		`id="table-catalogo-bajas"`,
		`id="table-catalogo-precios"`,
		`id="table-catalogo-altas"`,
		`name="mode" value="apply"`,
		// El aviso del catálogo VIGENTE: lo que ya estaba mal y no sale en la comparación.
		`id="catalogo-avisos-vigentes"`,
		"_envio usa un sku reservado",
	} {
		if !strings.Contains(out, ancla) {
			t.Errorf("el diff repintado no trae %s", ancla)
		}
	}
	// El paso 1 desaparece mientras hay un diff esperando: dejar el cuadro de texto ahí invitaría a
	// editar lo que ya se comprobó.
	if strings.Contains(out, `id="section-catalogo-subir"`) {
		t.Error("el paso 1 sigue pintado con un diff esperando confirmación")
	}
}

// TestCatalogo_AplicarRedirigeConFlashYNoRepinta (D-047.16: sí pudo mutar ⇒ PRG).
func TestCatalogo_AplicarRedirigeConFlashYNoRepinta(t *testing.T) {
	t.Parallel()
	_, router := routerDelCatalogo(t, map[string]stubResponse{
		rutaImportJSON: {http.StatusOK, aplicadoDeCampo},
	})

	rec := postMultipartConCSRF(t, router, rutaCatalogo, url.Values{
		campoModoCatalogo:      {"apply"},
		campoRefCatalogo:       {"catalogo"},
		campoDocumentoCatalogo: {documentoNormalizado},
	}, nil, clientSessionCookie(t))

	if destino := redirectTarget(t, rec); destino != rutaCatalogo+"?success="+flashCatalogoAplicado {
		t.Fatalf("Location = %q, want %q", destino, rutaCatalogo+"?success="+flashCatalogoAplicado)
	}
	// Y el texto dice lo que hay que saber: que lo anterior no se borró.
	msg := flashSuccess(flashCatalogoAplicado)
	if !strings.Contains(msg, "archivó") {
		t.Errorf("el éxito no dice que el catálogo anterior quedó archivado: %q", msg)
	}
}

// TestCatalogo_AplicarRedirigeTambienCuandoLaApiFalla.
//
// Es la otra mitad de la regla y la que se olvida: un 502 al aplicar NO dice si el catálogo quedó
// reemplazado, así que repintarlo invitaría a un F5 que lo reenviaría. Y el aviso no puede decir «no
// se ha cambiado nada», porque no se sabe.
func TestCatalogo_AplicarRedirigeTambienCuandoLaApiFalla(t *testing.T) {
	t.Parallel()
	_, router := routerDelCatalogo(t, map[string]stubResponse{
		rutaImportJSON: {http.StatusBadGateway, `{"error":"upstream"}`},
	})

	rec := postMultipartConCSRF(t, router, rutaCatalogo, url.Values{
		campoModoCatalogo:      {"apply"},
		campoRefCatalogo:       {"catalogo"},
		campoDocumentoCatalogo: {documentoNormalizado},
	}, nil, clientSessionCookie(t))

	if destino := redirectTarget(t, rec); destino != rutaCatalogo+"?error="+flashCatalogoNoAplicado {
		t.Fatalf("Location = %q, want %q", destino, rutaCatalogo+"?error="+flashCatalogoNoAplicado)
	}
	msg := flashError(flashCatalogoNoAplicado)
	if strings.Contains(msg, "no se ha cambiado nada") {
		t.Errorf("el aviso de aplicar afirma que no se cambió nada, y no se sabe: %q", msg)
	}
	if !strings.Contains(msg, "VUELVE A COMPROBAR") {
		t.Errorf("el aviso no dice qué hacer antes de repetir: %q", msg)
	}
}

// TestCatalogo_UnEnvioSinBotonCaeEnCOMPROBAR.
//
// 🔴 No es un caso de laboratorio: un formulario enviado con Enter viaja SIN el `name`/`value` del
// botón, porque eso solo lo manda el botón que se pulsa. El lado por el que se puede fallar aquí es
// uno solo, y es éste: convertir un Enter en un reemplazo del catálogo entero no lo arregla ningún
// aviso posterior.
func TestCatalogo_UnEnvioSinBotonCaeEnCOMPROBAR(t *testing.T) {
	t.Parallel()
	api, router := routerDelCatalogo(t, nil)

	postMultipartConCSRF(t, router, rutaCatalogo, url.Values{
		campoRefCatalogo:       {"catalogo"},
		campoDocumentoCatalogo: {documentoPegado},
	}, nil, clientSessionCookie(t))

	if got := api.Last(t, rutaImportJSON).Query.Get("mode"); got != string("validate") {
		t.Fatalf("un envío sin botón salió con mode=%q, want validate", got)
	}
}

// TestCatalogo_UnModoConValorDesconocidoNoSaleALaRed: eso no lo produce ningún botón de esta
// pantalla, así que degradarlo en silencio escondería que alguien manda algo que no se ofrece.
func TestCatalogo_UnModoConValorDesconocidoNoSaleALaRed(t *testing.T) {
	t.Parallel()
	api, router := routerDelCatalogo(t, nil)

	rec := postMultipartConCSRF(t, router, rutaCatalogo, url.Values{
		campoModoCatalogo:      {"aplicar"},
		campoDocumentoCatalogo: {documentoPegado},
	}, nil, clientSessionCookie(t))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 repintando", rec.Code)
	}
	if api.Called(rutaImportJSON) || api.Called(rutaImportTabular) {
		t.Error("un modo desconocido gastó el viaje a la plataforma")
	}
	if !strings.Contains(rec.Body.String(), flashError(flashCatalogoModoDesconocido)) {
		t.Error("no se pintó el aviso del modo desconocido")
	}
}

// --- LA PLANILLA Y EL SEGUNDO PASO ---

// TestCatalogo_LaPlanillaEntraPorTabularYElPaso2SalePorLaPuertaJSON.
//
// 🔑 Es el diseño entero del segundo paso: un .xlsx es binario y no cabe en un campo oculto, y volver
// a pedir el fichero dejaría subir uno DISTINTO del que se confirmó. Por eso la plataforma devuelve
// el documento ya normalizado y el paso 2 reenvía ESE, por la puerta JSON, con la MISMA ref.
func TestCatalogo_LaPlanillaEntraPorTabularYElPaso2SalePorLaPuertaJSON(t *testing.T) {
	t.Parallel()
	api, router := routerDelCatalogo(t, nil)
	sess := clientSessionCookie(t)

	rec := postMultipartConCSRF(t, router, rutaCatalogo, url.Values{
		campoModoCatalogo: {"validate"},
		campoRefCatalogo:  {"promociones"},
	}, &ficheroSubido{nombre: "catalogo.csv", contenido: []byte("sku;articulo;precio\nPAN-01;Pan;1.50\n")}, sess)

	if rec.Code != http.StatusOK {
		t.Fatalf("comprobar la planilla status = %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}
	tabular := api.Last(t, rutaImportTabular)
	if tabular.Query.Get("mode") != "validate" || tabular.Query.Get("ref") != "promociones" {
		t.Fatalf("la planilla salió con mode=%q ref=%q", tabular.Query.Get("mode"), tabular.Query.Get("ref"))
	}
	if api.Called(rutaImportJSON) {
		t.Error("la planilla salió TAMBIÉN por la puerta JSON: la puerta se elige por el contenido")
	}

	// El documento NORMALIZADO viaja oculto, y la ref con él.
	out := rec.Body.String()
	if !strings.Contains(out, `name="ref" value="catalogo"`) {
		t.Errorf("el paso 2 no arrastra la ref que devolvió la plataforma. Body: %s", out)
	}
	if !strings.Contains(out, "wapp.catalog_import") {
		t.Error("el paso 2 no lleva el documento normalizado en su oculto")
	}

	// Y el segundo paso, tal como lo mandaría el navegador desde ese formulario.
	postMultipartConCSRF(t, router, rutaCatalogo, url.Values{
		campoModoCatalogo:      {"apply"},
		campoRefCatalogo:       {"catalogo"},
		campoDocumentoCatalogo: {documentoNormalizado},
	}, nil, sess)

	aplicado := api.Last(t, rutaImportJSON)
	if aplicado.Query.Get("mode") != "apply" {
		t.Fatalf("el paso 2 salió con mode=%q, want apply", aplicado.Query.Get("mode"))
	}
	if aplicado.Body != documentoNormalizado {
		t.Errorf("el paso 2 mandó un documento distinto del que se enseñó:\n got %s\nwant %s",
			aplicado.Body, documentoNormalizado)
	}
}

// TestCatalogo_LaPuertaSeEligePorElCONTENIDOyNoPorLaExtension: renombrar un fichero no debe cambiar
// por dónde entra, porque el nombre lo cambia cualquiera sin tocar el fichero.
func TestCatalogo_LaPuertaSeEligePorElCONTENIDOyNoPorLaExtension(t *testing.T) {
	t.Parallel()
	api, router := routerDelCatalogo(t, nil)

	// Un JSON del contrato con nombre de planilla, y con el BOM que escriben algunos editores de
	// Windows por delante: sin saltarlo se iría por la puerta de las planillas.
	postMultipartConCSRF(t, router, rutaCatalogo, url.Values{campoModoCatalogo: {"validate"}},
		&ficheroSubido{nombre: "catalogo.csv", contenido: append([]byte("\xef\xbb\xbf"), documentoPegado...)},
		clientSessionCookie(t))

	if !api.Called(rutaImportJSON) {
		t.Error("un JSON con extensión .csv NO entró por la puerta JSON: se está mirando el nombre")
	}
	if api.Called(rutaImportTabular) {
		t.Error("un JSON con extensión .csv entró por la puerta de las planillas")
	}
}

// --- 🔑 LOS DEFECTOS, ANCLADOS A SU SITIO ---

// TestCatalogo_CadaDefectoVaConSuUBICACION.
//
// 🔴 EL ASERTO ES EL PAR, no que el texto aparezca. Lo que se pierde al romper la traducción es la
// CORRESPONDENCIA sitio↔defecto: un test que solo buscara la cadena del motivo pasaría con los dos
// defectos colgando de la fila 1, y eso manda a corregir la línea que no era — un fallo que no rompe
// nada y que solo se descubre corrigiendo la que no es. Es el mismo criterio que `defectosRemotos`
// en las líneas de una solicitud.
//
// Las DOS puertas van en el mismo test porque localizan de formas DISTINTAS y las dos tienen que
// funcionar: la planilla trae la FILA ya en el sistema de quien la llenó (cabecera = 1, no se le suma
// nada) y el JSON trae índices en base 0 que sí hay que traducir. Con solo una, la otra podría estar
// contando mal para siempre.
func TestCatalogo_CadaDefectoVaConSuUBICACION(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre  string
		ruta    string
		cuerpo  string
		fichero *ficheroSubido
		quiere  []string
	}{
		{
			nombre: "planilla: manda la fila y no se le suma nada",
			ruta:   rutaImportTabular,
			cuerpo: `{"error":"validation_failed","errors":[` +
				`{"row":4,"field":"precio","reason":"el precio no se pudo leer"},` +
				`{"row":7,"field":"cantidad","reason":"la unidad viene vacia"}]}`,
			fichero: &ficheroSubido{nombre: "catalogo.csv", contenido: []byte("sku;precio\nPAN;x\n")},
			quiere: []string{
				`<li class="link-list__item">Fila 4 · <code class="mono">precio</code>: el precio no se pudo leer</li>`,
				`<li class="link-list__item">Fila 7 · <code class="mono">cantidad</code>: la unidad viene vacia</li>`,
			},
		},
		{
			nombre: "JSON: los indices se traducen a base 1 y los dos ausentes no son lo mismo",
			ruta:   rutaImportJSON,
			cuerpo: `{"error":"validation_failed","errors":[` +
				`{"category_index":1,"item_index":2,"field":"price","reason":"el precio no puede ser negativo"},` +
				`{"category_index":0,"field":"name","reason":"la categoria no tiene nombre"},` +
				`{"field":"format","reason":"el documento no declara el formato"}]}`,
			quiere: []string{
				`<li class="link-list__item">Categoría 2 · artículo 3 · <code class="mono">price</code>: el precio no puede ser negativo</li>`,
				`<li class="link-list__item">Categoría 1 · <code class="mono">name</code>: la categoria no tiene nombre</li>`,
				`<li class="link-list__item">Todo el documento · <code class="mono">format</code>: el documento no declara el formato</li>`,
			},
		},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			_, router := routerDelCatalogo(t, map[string]stubResponse{
				caso.ruta: {http.StatusBadRequest, caso.cuerpo},
			})

			rec := postMultipartConCSRF(t, router, rutaCatalogo, url.Values{
				campoModoCatalogo:      {"validate"},
				campoRefCatalogo:       {"catalogo"},
				campoDocumentoCatalogo: {documentoPegado},
			}, caso.fichero, clientSessionCookie(t))

			// 400 REPINTANDO: el import es todo-o-nada, así que este rechazo no escribió nada.
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 repintando. Body: %s", rec.Code, rec.Body.String())
			}
			out := rec.Body.String()
			for _, linea := range caso.quiere {
				if !strings.Contains(out, linea) {
					t.Errorf("falta el defecto ENTERO con su ubicación:\n%s\nBody: %s", linea, out)
				}
			}
			// El encabezado sale del catálogo de flash, no del upstream.
			if !strings.Contains(out, flashError(flashCatalogoDefectos)) {
				t.Error("falta el encabezado que dice que no se tocó nada")
			}
			if strings.Contains(out, "validation_failed") {
				t.Error("la clave cruda del upstream acabó en pantalla")
			}
		})
	}
}

// TestCatalogo_ElDocumentoPEGADOvuelveAlFormularioTrasUnRechazo (la otra mitad de D-047.16: el 400
// «de palabra» no basta si además le borra a la dueña lo que acababa de pegar).
func TestCatalogo_ElDocumentoPEGADOvuelveAlFormularioTrasUnRechazo(t *testing.T) {
	t.Parallel()
	_, router := routerDelCatalogo(t, map[string]stubResponse{
		rutaImportJSON: {http.StatusBadRequest,
			`{"error":"validation_failed","errors":[{"field":"format","reason":"falta el formato"}]}`},
	})

	rec := postMultipartConCSRF(t, router, rutaCatalogo, url.Values{
		campoModoCatalogo:      {"validate"},
		campoRefCatalogo:       {"promociones"},
		campoDocumentoCatalogo: {documentoPegado},
	}, nil, clientSessionCookie(t))

	out := rec.Body.String()
	if !strings.Contains(out, "wapp.catalog_import") {
		t.Errorf("el documento pegado no volvió al formulario. Body: %s", out)
	}
	if !strings.Contains(out, `<option value="promociones" selected>`) {
		t.Error("la ref elegida no volvió marcada: el reintento se compararía contra otra")
	}
}

// TestCatalogo_LosRechazosLOCALESnoGastanViaje.
func TestCatalogo_LosRechazosLOCALESnoGastanViaje(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre string
		campos url.Values
		quiere string
	}{
		{"sin nada que mandar", url.Values{campoModoCatalogo: {"validate"}}, flashCatalogoSinDocumento},
		{"pegado que no es JSON",
			url.Values{campoModoCatalogo: {"validate"}, campoDocumentoCatalogo: {"sku;precio\nPAN;1"}},
			flashCatalogoNoEsJSON},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			api, router := routerDelCatalogo(t, nil)

			rec := postMultipartConCSRF(t, router, rutaCatalogo, caso.campos, nil, clientSessionCookie(t))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 repintando", rec.Code)
			}
			if api.Called(rutaImportJSON) || api.Called(rutaImportTabular) {
				t.Error("un rechazo local gastó el viaje a la plataforma")
			}
			if !strings.Contains(rec.Body.String(), flashError(caso.quiere)) {
				t.Errorf("no se pintó el aviso %q", caso.quiere)
			}
		})
	}
}

// --- EL GATE POR RUTA ---

// TestCatalogo_ElGateCortaLasTRESrutasYnoSaleALaRed.
//
// Esconder el enlace decide lo que se PINTA, no lo que se PUEDE: quien teclea la URL entra igual. Y
// son TRES rutas y no una porque en el origen cada handler repetía su propio `if` y acabaron
// respondiendo cosas distintas ante la misma ausencia de plan.
func TestCatalogo_ElGateCortaLasTRESrutasYnoSaleALaRed(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, map[string]stubResponse{
		"GET /api/v1/entitlements": {http.StatusOK, entitlementsBody("basic", "menu")},
		rutaListadoDeEmpresas:      {http.StatusOK, unaEmpresa()},
	})
	router := adminRouter(api)
	sess := clientSessionCookie(t)

	for _, ruta := range []string{rutaCatalogo, rutaPlantillaCatalogo + "?format=csv"} {
		rec := getWithSession(t, router, ruta)
		if rec.Code != http.StatusForbidden {
			t.Errorf("GET %s sin la capacidad status = %d, want 403", ruta, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `id="section-catalogo-sin-plan"`) {
			t.Errorf("GET %s no pintó la pantalla vacía que explica el plan", ruta)
		}
	}
	rec := postMultipartConCSRF(t, router, rutaCatalogo, url.Values{
		campoModoCatalogo: {"apply"}, campoDocumentoCatalogo: {documentoPegado},
	}, nil, sess)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST sin la capacidad status = %d, want 403. Body: %s", rec.Code, rec.Body.String())
	}

	// Y NINGUNA de las tres llegó a la plataforma.
	for _, r := range api.Requests() {
		if strings.HasPrefix(r.Path, "/api/v1/catalog") || r.Path == "/api/v1/tenant-content" {
			t.Errorf("el gate dejó salir %s", r.Route())
		}
	}
}

// TestCatalogo_ElGateEsFAILCLOSED: si el plan no se puede leer, no se entra.
func TestCatalogo_ElGateEsFAILCLOSED(t *testing.T) {
	t.Parallel()
	_, router := routerDelCatalogo(t, map[string]stubResponse{
		"GET /api/v1/entitlements": {http.StatusInternalServerError, `{"error":"detalle interno"}`},
	})

	rec := getWithSession(t, router, rutaCatalogo)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("con el plan sin resolver status = %d, want 403 (fail-closed)", rec.Code)
	}
	if strings.Contains(rec.Body.String(), `id="section-catalogo-subir"`) {
		t.Error("con el plan sin resolver se pintó el formulario: el gate se abrió por un fallo del upstream")
	}
}

// TestCatalogo_ElEnlaceDeLaBarraNOestaGateado y el de la PORTADA SÍ.
//
// 🔴 Son dos decisiones distintas y hay que afirmarlas juntas, porque el aserto de ausencia de una se
// lo comería la otra: el enlace del layout sale en TODAS las páginas —incluida la portada— así que
// «sin la capacidad no aparece /importar-catalogo» sería falso mirando el HTML entero. Lo que se gatea
// es la TARJETA de la portada, y quien decide de verdad es el gate por ruta.
func TestCatalogo_ElEnlaceDeLaBarraNOestaGateadoYElDeLaPortadaSI(t *testing.T) {
	t.Parallel()

	conPlan := newStubAPI(t, map[string]stubResponse{
		"GET /api/v1/entitlements": {http.StatusOK, entitlementsBody("commerce", featureCatalogImport)},
		rutaListadoDeEmpresas:      {http.StatusOK, unaEmpresa()},
	})
	sinPlan := newStubAPI(t, map[string]stubResponse{
		"GET /api/v1/entitlements": {http.StatusOK, entitlementsBody("basic", "menu")},
		rutaListadoDeEmpresas:      {http.StatusOK, unaEmpresa()},
	})

	conOut := getWithSession(t, adminRouter(conPlan), "/").Body.String()
	sinOut := getWithSession(t, adminRouter(sinPlan), "/").Body.String()

	// La BARRA: en las dos. El precio aceptado es un 403 explicativo al pulsarlo.
	for nombre, out := range map[string]string{"con plan": conOut, "sin plan": sinOut} {
		if !strings.Contains(out, `>Catálogo</a>`) {
			t.Errorf("%s: la barra no ofrece «Catálogo»; el enlace del layout NO va gateado", nombre)
		}
	}
	// La TARJETA de la portada: solo con la capacidad.
	if !strings.Contains(conOut, `id="link-importar-catalogo"`) {
		t.Error("con la capacidad, la portada no ofrece el enlace real a la pantalla")
	}
	if strings.Contains(sinOut, `id="link-importar-catalogo"`) {
		t.Error("sin la capacidad, la portada ofrece el enlace: el gate de la tarjeta no cierra")
	}
}

// --- T8.3 · LA DESCARGA ---

// plantillaCSVdeCampo es lo que sirve la plataforma para `?format=csv`.
const plantillaCSVdeCampo = "sku,articulo,precio,unidad\nPAN-01,Pan de yema,1.50,unidad\n"

// stubDeDescarga levanta un doble que responde a la descarga con las CABECERAS EXACTAS del cloud.
//
// 🔴 Va aparte de newStubAPI a propósito: aquel doble contesta `application/json` a todo, y esta es
// la única llamada de la consola cuya respuesta NO es JSON. Con el doble general, el test no podría
// distinguir un CSV servido bien de uno servido con el tipo equivocado, que es justo lo que hay que
// probar.
func stubDeDescarga(t *testing.T, status int, contentType, disposition, cuerpo string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/entitlements", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(entitlementsBody("commerce", featureCatalogImport)))
	})
	mux.HandleFunc(rutaListadoDeEmpresas, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(unaEmpresa()))
	})
	mux.HandleFunc("GET /api/v1/catalog/import/prompt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"format":"wapp.catalog_import","version":1,"prompt":"` + promptDeLaPlataforma + `"}`))
	})
	mux.HandleFunc("GET /api/v1/tenant-content", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(refsBody("catalogo")))
	})
	mux.HandleFunc("GET /api/v1/catalog/import/template", func(w http.ResponseWriter, _ *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		if disposition != "" {
			w.Header().Set("Content-Disposition", disposition)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(cuerpo))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestCatalogo_LaPlantillaSeSirveComoFicheroYnoPasaPorElRenderizador.
//
// 🔴 Es la ÚNICA respuesta de esta consola que no es HTML, y por eso se afirman las tres cosas: el
// tipo (un CSV servido como `text/html` lo abriría el navegador en vez de guardarlo), el nombre con
// el que se guarda, y que el cuerpo son los BYTES y no una página con ellos dentro. La tercera es la
// que caza «pasó por el renderizador»: con layout, el fichero descargado no se abriría.
func TestCatalogo_LaPlantillaSeSirveComoFicheroYnoPasaPorElRenderizador(t *testing.T) {
	t.Parallel()
	srv := stubDeDescarga(t, http.StatusOK, "text/csv; charset=utf-8",
		`attachment; filename="catalogo-plantilla.csv"`, plantillaCSVdeCampo)
	router := NewRouter(testConfig(srv.URL, "http://127.0.0.1:8200"))

	rec := getWithSession(t, router, rutaPlantillaCatalogo+"?format=csv")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/csv; charset=utf-8", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="catalogo-plantilla.csv"` {
		t.Errorf("Content-Disposition = %q: sin nombre de fichero el navegador la guarda como la ruta", got)
	}

	// NO pasó por el renderizador: ni layout, ni nonce, ni el par de avisos.
	cuerpo := rec.Body.String()
	for _, rastro := range []string{"<!DOCTYPE html>", "<html", "app-bar", "wapp-snackbar", "csrf_token"} {
		if strings.Contains(cuerpo, rastro) {
			t.Errorf("la descarga trae %q: pasó por el renderizador y el fichero no se abriría", rastro)
		}
	}

	// Y EL FICHERO SE ABRE, y es la plantilla: se parsea como CSV y trae su cabecera.
	if cuerpo != plantillaCSVdeCampo {
		t.Errorf("los bytes no son los que sirvió la plataforma:\n got %q\nwant %q", cuerpo, plantillaCSVdeCampo)
	}
	filas, err := csv.NewReader(strings.NewReader(cuerpo)).ReadAll()
	if err != nil {
		t.Fatalf("lo descargado no se abre como CSV: %v", err)
	}
	if len(filas) < 2 || filas[0][0] != "sku" {
		t.Errorf("lo descargado no es la plantilla: %v", filas)
	}
}

// TestCatalogo_LaConsolaNOreenviaElTipoQueDiceElUPSTREAM.
//
// 🔴 EL DEFECTO QUE ESTO FIJA, y que estuvo vivo entre T8.1 y este arreglo: el tipo de la descarga
// salía del upstream cuando su cabecera se dejaba parsear, y la lista blanca quedaba de reserva. Es
// justo la inversión de la regla del origen —«ninguna cabecera ajena decide cómo guarda el navegador
// un archivo servido desde este origen»—, y no depende de que la plataforma sea de fiar hoy: depende
// de que el navegador trata ese fichero como venido de NUESTRO origen. Quien sabe qué se pidió es
// esta consola.
//
// Medido antes del arreglo: pidiendo `format=csv` con el upstream contestando `application/json`, la
// consola servía `application/json`. Ahora no sirve NADA: repinta con su aviso.
func TestCatalogo_LaConsolaNOreenviaElTipoQueDiceElUPSTREAM(t *testing.T) {
	t.Parallel()
	srv := stubDeDescarga(t, http.StatusOK, "application/json",
		`attachment; filename="catalogo-plantilla.csv"`, `{"esto":"no es la planilla"}`)
	router := NewRouter(testConfig(srv.URL, "http://127.0.0.1:8200"))

	rec := getWithSession(t, router, rutaPlantillaCatalogo+"?format=csv")

	// NO se reenvía la cabecera ajena…
	if strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Errorf("la consola reenvió el Content-Type del upstream: %q", rec.Header().Get("Content-Type"))
	}
	// …y tampoco se sirve la descarga con el tipo bueno puesto encima, que sería peor: bytes
	// desconocidos etiquetados como si fueran la planilla.
	if rec.Header().Get("Content-Disposition") != "" {
		t.Error("se ofreció como descarga algo que no es el formato que se pidió")
	}
	if strings.Contains(rec.Body.String(), "no es la planilla") {
		t.Error("los bytes del upstream llegaron al navegador igualmente")
	}
	if !strings.Contains(rec.Body.String(), flashError(flashCatalogoPlantillaInesperada)) {
		t.Errorf("no se pintó el aviso propio del formato inesperado. Body: %s", rec.Body.String())
	}
	// Y NO cae en el genérico, que aquí daría el único consejo que seguro no sirve.
	if strings.Contains(rec.Body.String(), flashError(flashCatalogoPlantillaFallo)) {
		t.Error("el desacuerdo de formato se explicó como un fallo pasajero: reintentar no lo arregla")
	}
}

// TestCatalogo_LaPlantillaConUnFormatoQueNoExisteNoGastaViaje.
func TestCatalogo_LaPlantillaConUnFormatoQueNoExisteNoGastaViaje(t *testing.T) {
	t.Parallel()
	api, router := routerDelCatalogo(t, nil)

	rec := getWithSession(t, router, rutaPlantillaCatalogo+"?format=pdf")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if api.Called("GET /api/v1/catalog/import/template") {
		t.Error("un formato que no existe gastó el viaje a la plataforma")
	}
	if !strings.Contains(rec.Body.String(), flashError(flashCatalogoFormatoDesconocido)) {
		t.Error("no se pintó el aviso del formato desconocido")
	}
	if rec.Header().Get("Content-Disposition") != "" {
		t.Error("un rechazo se ofreció como descarga")
	}
}

// TestCatalogo_LaPlantillaQueLaPlataformaNoSIRVEnoDescargaNada, y su aviso NO remite al prompt: con
// las rutas del import sin montar, ese texto tampoco carga.
func TestCatalogo_LaPlantillaQueLaPlataformaNoSIRVEnoDescargaNada(t *testing.T) {
	t.Parallel()
	srv := stubDeDescarga(t, http.StatusNotFound, "application/json", "", `{"error":"not found"}`)
	router := NewRouter(testConfig(srv.URL, "http://127.0.0.1:8200"))

	rec := getWithSession(t, router, rutaPlantillaCatalogo+"?format=json")
	if rec.Header().Get("Content-Disposition") != "" {
		t.Error("un 404 del upstream se sirvió como descarga")
	}
	if !strings.Contains(rec.Body.String(), flashError(flashCatalogoPlantillaAusente)) {
		t.Errorf("no se pintó el aviso del 404. Body: %s", rec.Body.String())
	}
	if strings.Contains(flashError(flashCatalogoPlantillaAusente), "asistente") {
		t.Error("el aviso del 404 remite al texto para el asistente, que con las rutas sin montar tampoco carga")
	}
}

// --- T8.4 · LOS DOS TECHOS ---

// TestCatalogo_UnFicheroPorEncimaDelTECHOdeLaPLATAFORMAseRechazaConSuCIFRA.
//
// Es la comprobación de NEGOCIO, la que el origen NO tenía: sin ella este caso llegaba como el 413
// del cloud, que por el camino tabular viene envuelto en `validation_failed` con `{"field":"archivo"}`
// y SIN el campo `max_bytes` — un rechazo por tamaño que no nombra ningún tamaño.
func TestCatalogo_UnFicheroPorEncimaDelTECHOdeLaPLATAFORMAseRechazaConSuCIFRA(t *testing.T) {
	t.Parallel()
	api, router := routerDelCatalogo(t, nil)

	rec := postMultipartConCSRF(t, router, rutaCatalogo, url.Values{campoModoCatalogo: {"validate"}},
		&ficheroSubido{nombre: "catalogo.csv", contenido: bytes.Repeat([]byte("a"), 2<<20)},
		clientSessionCookie(t))

	// Pasa el techo del SOBRE (4 MiB) y lo para la comprobación de negocio: 400 REPINTANDO.
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 repintando. Body: %s", rec.Code, rec.Body.String())
	}
	if api.Called(rutaImportTabular) || api.Called(rutaImportJSON) {
		t.Error("un fichero por encima del techo gastó el viaje a la plataforma")
	}
	out := rec.Body.String()
	if !strings.Contains(out, flashError(flashCatalogoArchivoGrande)) {
		t.Errorf("no se pintó el aviso del tamaño. Body: %s", out)
	}
	// Sigue siendo la PANTALLA, con su formulario: no un error suelto.
	if !strings.Contains(out, `id="form-catalogo-comprobar"`) {
		t.Error("el rechazo por tamaño se llevó por delante el formulario")
	}
}

// TestCatalogo_ElTECHOdelFICHEROyElTEXTOqueLoNombraNoPuedenSepararse.
//
// El catálogo de flash traduce códigos a textos FIJOS y no interpola datos, así que la cifra está
// escrita a mano en el texto. Esto es lo único que impide que alguien mueva la constante y deje el
// aviso diciendo un número que ya no es el que se comparó.
func TestCatalogo_ElTECHOdelFICHEROyElTEXTOqueLoNombraNoPuedenSepararse(t *testing.T) {
	t.Parallel()

	if maxArchivoCatalogo != 1<<20 {
		t.Fatalf("maxArchivoCatalogo = %d: el texto de flashCatalogoArchivoGrande dice «1 MB» y "+
			"habría que cambiarlo con él", maxArchivoCatalogo)
	}
	if msg := flashError(flashCatalogoArchivoGrande); !strings.Contains(msg, "1 MB") {
		t.Errorf("el aviso del tamaño no dice la cifra: %q", msg)
	}
	// Y la pantalla la dice ANTES de que alguien elija un fichero, que es cuando sirve de algo.
	raw, err := templatesFS.ReadFile("templates/pages/catalogo.html")
	if err != nil {
		t.Fatalf("leer la plantilla: %v", err)
	}
	if !strings.Contains(string(raw), "1 MB") {
		t.Error("el formulario no dice el tamaño máximo antes de subir nada")
	}
}

// TestCatalogo_UnCUERPOporEncimaDelTECHOdelSOBREloParaUnaPAGINAyNoUnCorteEnSeco.
//
// 🔴 SON DOS TECHOS DISTINTOS Y ÉSTE ES EL OTRO (T8.4): el de arriba mide el FICHERO contra lo que la
// plataforma honra; éste mide el SOBRE de la petición y protege el PROCESO. Se prueban por separado
// porque se rompen por separado — quitar este middleware deja el de arriba respondiendo, y el rechazo
// pasaría de 413 a 400 sin que nadie lo notara.
//
// Lo que el criterio exige demostrar es que esto NO es ni un corte en seco ni un 500: un 413 con una
// página que dice qué pasó, cuál es el techo y por dónde se vuelve.
func TestCatalogo_UnCUERPOporEncimaDelTECHOdelSOBREloParaUnaPAGINAyNoUnCorteEnSeco(t *testing.T) {
	t.Parallel()
	api, router := routerDelCatalogo(t, nil)

	rec := postMultipartConCSRF(t, router, rutaCatalogo, url.Values{campoModoCatalogo: {"validate"}},
		&ficheroSubido{nombre: "catalogo.csv", contenido: bytes.Repeat([]byte("a"), 5<<20)},
		clientSessionCookie(t))

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: lo paró otra cosa (o nadie). Body: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	// NI CORTE EN SECO NI 500: una página HTML, con el techo dicho y una salida.
	if !strings.Contains(out, `id="section-cuerpo-grande"`) {
		t.Fatalf("el 413 no pintó la página del rechazo. Body: %s", out)
	}
	if !strings.Contains(out, "4 MB") {
		t.Error("la página del 413 no dice cuál es el techo")
	}
	if !strings.Contains(out, `href="`+rutaCatalogo+`"`) {
		t.Error("la página del 413 no ofrece por dónde volver: es un callejón sin salida")
	}
	if !strings.Contains(out, "No se ha tocado nada") {
		t.Error("la página del 413 no afirma que el catálogo no cambió, que es lo primero que se pregunta")
	}
	// Y no llegó ni al gate: el tope corta ANTES de leer el cuerpo, que es su razón de ser.
	if len(api.Requests()) != 0 {
		t.Errorf("el techo dejó salir %v", routesOf(api.Requests()))
	}
}

// TestCatalogo_ElTECHOdelSOBREnoSeAplicaAotrasRutasNiAlosGET: es por ruta EXACTA y solo en los
// métodos que mutan, que es lo que evita meterle un techo por la puerta de atrás a una pantalla ajena.
func TestCatalogo_ElTECHOdelSOBREnoSeAplicaAotrasRutasNiAlosGET(t *testing.T) {
	t.Parallel()
	_, router := routerDelCatalogo(t, nil)

	// Un GET a la propia ruta, con la misma sesión: no lo toca.
	if rec := getWithSession(t, router, rutaCatalogo); rec.Code != http.StatusOK {
		t.Errorf("GET %s status = %d, want 200", rutaCatalogo, rec.Code)
	}
	// Y un POST GRANDE a OTRA ruta pasa el techo (y muere donde le toque, no aquí).
	grande := url.Values{"definition": {strings.Repeat("x", 5<<20)}, "flow_id": {"f-1"}, "is_new": {"0"}}
	rec := postFormWithCSRF(router, rutaFlujos, grande, clientSessionCookie(t))
	if rec.Code == http.StatusRequestEntityTooLarge {
		t.Error("el techo del catálogo se aplicó a /flujos: es por ruta EXACTA")
	}
}
