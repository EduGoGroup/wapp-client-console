package apiclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const intakeDePrueba = "77777777-7777-4777-8777-777777777777"

// peticionCapturada es lo que un test necesita saber de lo que salió por el cable: el verbo, la ruta,
// la query y el cuerpo. Se guarda el cuerpo LEÍDO porque el `*http.Request` que conserva
// servidorQueResponde ya lo trae consumido.
type peticionCapturada struct {
	Method string
	Path   string
	// URI es lo que viajó POR EL CABLE, sin decodificar. Hay un aserto que solo se puede hacer aquí:
	// `URL.Path` viene ya decodificado, así que un `%2F` del cliente se lee ahí como una barra de
	// verdad y un id que reescribiera la ruta parecería haberlo conseguido cuando no salió de su
	// segmento.
	URI   string
	Query string
	Body  string
	Auth  string
}

// servidorDeBandeja levanta un upstream que contesta lo mismo a todo y guarda la última petición
// ENTERA (cuerpo incluido). El plazo del cliente general se deja corto a propósito —los tests no
// esperan a nadie— y el de inferencia queda en DefaultInferenceTimeout, que es lo que separa la
// sugerencia del resto.
func servidorDeBandeja(t *testing.T, status int, body string) (*Client, *peticionCapturada) {
	t.Helper()
	var ultima peticionCapturada
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		ultima = peticionCapturada{
			Method: r.Method, Path: r.URL.Path, URI: r.URL.RequestURI(), Query: r.URL.RawQuery,
			Body: string(raw), Auth: r.Header.Get("Authorization"),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != "" {
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, 5*time.Second), &ultima
}

// llamadasDeLaBandeja son las DIEZ operaciones del plano, cada una con el verbo y la ruta que le toca
// en la API pública. La tabla se escribe una vez y la reusan todos los tests de desenlace: un método
// que se dejara fuera de aquí se quedaría sin ninguno de ellos.
//
// 🔴 Fíjate en la FIRMA: ninguna recibe una empresa. No es que no se le pase — es que no hay
// parámetro (INV-04).
//
// 🔴 ReplaceIntakeItems y CorrectIntakeItems comparten verbo Y ruta a propósito: no existe ninguna
// `/correct` en la API, corregir es el MISMO PUT con `as_correction` puesto. Las dos entradas de la
// tabla son las dos llamadas, no dos endpoints.
var llamadasDeLaBandeja = []struct {
	nombre string
	verbo  string
	ruta   string
	llamar func(*Client) error
}{
	{"intakes.list", http.MethodGet, "/api/v1/intakes", func(c *Client) error {
		_, err := c.Intakes.ListIntakes(context.Background(), tokenDePrueba, IntakeFilter{})
		return err
	}},
	{"intakes.get", http.MethodGet, "/api/v1/intakes/" + intakeDePrueba, func(c *Client) error {
		_, err := c.Intakes.GetIntake(context.Background(), tokenDePrueba, intakeDePrueba)
		return err
	}},
	{"intakes.status", http.MethodPost, "/api/v1/intakes/" + intakeDePrueba + "/status", func(c *Client) error {
		_, err := c.Intakes.SetIntakeStatus(context.Background(), tokenDePrueba, intakeDePrueba, "confirmed")
		return err
	}},
	{"intakes.items.replace", http.MethodPut, "/api/v1/intakes/" + intakeDePrueba + "/items", func(c *Client) error {
		_, err := c.Intakes.ReplaceIntakeItems(context.Background(), tokenDePrueba, intakeDePrueba,
			[]IntakeItem{{SKU: "torta-1", Label: "Torta", Qty: 1, UnitPrice: 25}})
		return err
	}},
	{"intakes.items.correct", http.MethodPut, "/api/v1/intakes/" + intakeDePrueba + "/items", func(c *Client) error {
		_, err := c.Intakes.CorrectIntakeItems(context.Background(), tokenDePrueba, intakeDePrueba,
			[]IntakeItem{{SKU: "torta-1", Label: "Torta", Qty: 1, UnitPrice: 25}})
		return err
	}},
	{"intakes.discard", http.MethodPost, "/api/v1/intakes/discard", func(c *Client) error {
		_, err := c.Intakes.DiscardIntakes(context.Background(), tokenDePrueba, []string{intakeDePrueba})
		return err
	}},
	{"intakes.approve", http.MethodPost, "/api/v1/intakes/" + intakeDePrueba + "/approve", func(c *Client) error {
		_, err := c.Intakes.ApproveIntake(context.Background(), tokenDePrueba, intakeDePrueba, "Son 25,00")
		return err
	}},
	{"intakes.request-info", http.MethodPost, "/api/v1/intakes/" + intakeDePrueba + "/request-info", func(c *Client) error {
		_, err := c.Intakes.RequestIntakeInfo(context.Background(), tokenDePrueba, intakeDePrueba, "¿Para cuándo?")
		return err
	}},
	{"intakes.reanalyze", http.MethodPost, "/api/v1/intakes/" + intakeDePrueba + "/reanalyze", func(c *Client) error {
		_, err := c.Intakes.ReanalyzeIntake(context.Background(), tokenDePrueba, intakeDePrueba, "")
		return err
	}},
	{"intakes.quote-suggestion", http.MethodPost, "/api/v1/intakes/" + intakeDePrueba + "/quote-suggestion", func(c *Client) error {
		_, err := c.Intakes.SuggestIntakeQuote(context.Background(), tokenDePrueba, intakeDePrueba)
		return err
	}},
}

// TestIntakes_LasDiezVanASuVerboYSuRuta fija el contrato con la plataforma por el CABLE.
//
// Es el test que sobrevive a una refactorización: cambiar el struct de respuesta rompe la
// decodificación y se nota, pero equivocarse de ruta o de verbo compila, pasa los tipos y solo se ve
// en campo como un 404 inexplicable. Los diez endpoints están medidos contra el registro de rutas del
// cloud (`internal/publicapi/publicapi.go`), no contra lo que el plan afirmaba.
func TestIntakes_LasDiezVanASuVerboYSuRuta(t *testing.T) {
	t.Parallel()

	for _, caso := range llamadasDeLaBandeja {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			api, ultima := servidorDeBandeja(t, http.StatusOK, "{}")
			_ = caso.llamar(api)

			if ultima.Method != caso.verbo {
				t.Errorf("verbo = %s, want %s", ultima.Method, caso.verbo)
			}
			if ultima.Path != caso.ruta {
				t.Errorf("ruta = %s, want %s", ultima.Path, caso.ruta)
			}
			if ultima.Auth != "Bearer "+tokenDePrueba {
				t.Errorf("Authorization = %q", ultima.Auth)
			}
		})
	}
}

// TestIntakes_INV04_LaEmpresaNoViajaEnNingunaDeLasDiez.
//
// El tenant sale del Context Token. El aserto barre las TRES posiciones donde podría colarse —query,
// cuerpo y ruta—, y va aquí, en el paquete que ESCRIBE la petición: el test de internal/web recorre
// pantallas, y la bandeja todavía no tiene ninguna (esa es otra casilla). Sin este, los diez métodos
// entrarían en el repo sin que nadie afirmara el invariante sobre ellos.
func TestIntakes_INV04_LaEmpresaNoViajaEnNingunaDeLasDiez(t *testing.T) {
	t.Parallel()

	const empresa = "33333333-3333-4333-8333-333333333333"
	for _, caso := range llamadasDeLaBandeja {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			api, ultima := servidorDeBandeja(t, http.StatusOK, "{}")
			_ = caso.llamar(api)

			if strings.Contains(ultima.Query, "tenant_id") {
				t.Errorf("%s manda tenant_id en la query: %s", caso.nombre, ultima.Query)
			}
			if strings.Contains(ultima.Body, "tenant_id") {
				t.Errorf("%s manda tenant_id en el cuerpo: %s", caso.nombre, ultima.Body)
			}
			if strings.Contains(ultima.Path, empresa) || strings.Contains(ultima.Path, "tenant") {
				t.Errorf("%s lleva la empresa en la RUTA: %s", caso.nombre, ultima.Path)
			}
		})
	}
}

// paginaDeBandeja es la respuesta del listado con el TOTAL puesto: 137 coincidencias repartidas en
// páginas de 25, de las que esta es la SEGUNDA.
const paginaDeBandeja = `{
  "intakes": [{"id":"` + intakeDePrueba + `","contact_id":"c-1","session_id":"s-1",
               "status":"pending_approval","total":25.5,"customer_note":"en portería","overdue":true,
               "created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:05:00Z"}],
  "page": 2, "page_size": 25, "total": 137
}`

// TestListIntakes_PideLaPAGINA2YElTotalVuelve — el criterio de paginación de T7.1, y el test que la
// mutación declarada tiene que poner en rojo.
//
// 🔴 PIDE LA PÁGINA 2, Y ESA ES LA MITAD DEL TEST. Un test que pidiera la 1 pasaría con el bug
// puesto: «ignorar Page y mandar siempre la primera» produce exactamente la misma query que pedir la
// primera a propósito, así que la página 1 no puede distinguir el cliente bueno del roto. La
// paginación solo se prueba pidiendo algo que NO sea el default.
//
// Los cuatro asertos son distintos y ninguno sobra:
//   - `page=2` en la query es el que mata la mutación;
//   - `page_size=25` es su gemelo: quien borrara el segundo bloque de query() dejaría el primero
//     verde y la bandeja pediría páginas de 50 pensando que pide de 25;
//   - `Total` es lo que hace que exista un paginador: sin él no se puede saber si hay una página
//     siguiente, y una bandeja sin paginador esconde lo más antiguo —que es lo que lleva más
//     esperando— para siempre;
//   - y `Page`/`PageSize` de vuelta, porque el cliente NO los inventa a partir del filtro: los lee de
//     la respuesta, que es quien sabe qué aplicó de verdad la API.
func TestListIntakes_PideLaPAGINA2YElTotalVuelve(t *testing.T) {
	t.Parallel()

	api, ultima := servidorDeBandeja(t, http.StatusOK, paginaDeBandeja)
	page, err := api.Intakes.ListIntakes(context.Background(), tokenDePrueba, IntakeFilter{
		Page: 2, PageSize: 25, Status: "pending_approval", Sort: IntakeSortOldest,
	})
	if err != nil {
		t.Fatalf("ListIntakes devolvió error: %v", err)
	}

	q := ultima.Query
	if !strings.Contains(q, "page=2") {
		t.Errorf("la query NO pide la página 2: %q — el cliente está mandando siempre la primera", q)
	}
	if !strings.Contains(q, "page_size=25") {
		t.Errorf("la query no lleva page_size=25: %q", q)
	}
	// Y los filtros siguen viajando con sus nombres reales, que son los que lee parseIntakeFilter.
	for _, clave := range []string{"status=pending_approval", "sort=oldest"} {
		if !strings.Contains(q, clave) {
			t.Errorf("la query no lleva %q: %s", clave, q)
		}
	}

	if page.Total != 137 {
		t.Errorf("Total = %d, want 137 — sin el total no hay paginador", page.Total)
	}
	if page.Page != 2 || page.PageSize != 25 {
		t.Errorf("Page/PageSize = %d/%d, want 2/25", page.Page, page.PageSize)
	}
	if len(page.Intakes) != 1 || page.Intakes[0].ID != intakeDePrueba {
		t.Fatalf("la página no trae la solicitud: %+v", page.Intakes)
	}
	if !page.Intakes[0].Overdue || page.Intakes[0].CustomerNote != "en portería" {
		t.Errorf("la cabecera perdió campos: %+v", page.Intakes[0])
	}
}

// TestListIntakes_ElFiltroVacioNoMandaQuery: los ceros significan «sin filtro» y la API aplica sus
// defaults. Es el hermano del de arriba y lo que impide que alguien «arregle» la paginación mandando
// siempre `page=1`: eso dejaría de ser «lo que decida la API» y fijaría el default en el cliente.
func TestListIntakes_ElFiltroVacioNoMandaQuery(t *testing.T) {
	t.Parallel()

	api, ultima := servidorDeBandeja(t, http.StatusOK, `{"intakes":[],"page":1,"page_size":50,"total":0}`)
	if _, err := api.Intakes.ListIntakes(context.Background(), tokenDePrueba, IntakeFilter{}); err != nil {
		t.Fatalf("error: %v", err)
	}
	if ultima.Query != "" {
		t.Errorf("un filtro vacío mandó query: %q", ultima.Query)
	}
}

// TestIntakes_El409EsRecargarYNoYaExiste.
//
// Las CUATRO puertas que lo emiten comparten cuerpo y significado en el cloud («la solicitud cambió
// de estado; recárgala y reintenta»). Sin sentinela propio llegaría como ErrConflict, que en esta
// consola significa «ya existe» (un rol repetido) y lleva a un consejo que no sirve de nada aquí.
//
// El tercer aserto es el que mata la mutación por su otro lado: un *APIError conserva el status, así
// que si StatusCodeOf vuelve a devolver 409 es que el APIError sobrevivió al traductor.
func TestIntakes_El409EsRecargarYNoYaExiste(t *testing.T) {
	t.Parallel()

	puertas := map[string]func(*Client) error{
		"status":       llamadaPorNombre(t, "intakes.status"),
		"items":        llamadaPorNombre(t, "intakes.items.replace"),
		"approve":      llamadaPorNombre(t, "intakes.approve"),
		"request-info": llamadaPorNombre(t, "intakes.request-info"),
	}
	for nombre, llamar := range puertas {
		api, _ := servidorDeBandeja(t, http.StatusConflict,
			`{"error":"la solicitud cambió de estado; recárgala y reintenta"}`)
		err := llamar(api)
		if !errors.Is(err, ErrIntakeChanged) {
			t.Errorf("%s: err = %v, want ErrIntakeChanged", nombre, err)
		}
		if !errors.Is(err, ErrConflict) {
			t.Errorf("%s: ErrIntakeChanged debe ENVOLVER a ErrConflict", nombre)
		}
		if got := StatusCodeOf(err); got != 0 {
			t.Errorf("%s: el 409 llegó como *APIError desnudo (status %d)", nombre, got)
		}
	}
}

// TestIntakes_El403EsLaFEATUREYNoUnPermiso.
//
// Lo emite el middleware de entitlements del cloud en OCHO de las nueve rutas, antes del handler: con
// `cart_basic` apagada, TODO lo que la bandeja intente responde 403. Traducirlo a «sin permiso» sería
// mandar a pedirle permisos a quien ya los tiene; lo que falta es el plan.
func TestIntakes_El403EsLaFEATUREYNoUnPermiso(t *testing.T) {
	t.Parallel()

	for _, caso := range llamadasDeLaBandeja {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			api, _ := servidorDeBandeja(t, http.StatusForbidden,
				`{"error":"feature_not_enabled","feature":"cart_basic"}`)
			err := caso.llamar(api)

			falta, ok := FeatureNotEnabledOf(err)
			if !ok {
				t.Fatalf("err = %v, want *FeatureNotEnabledError", err)
			}
			if falta.Feature != "cart_basic" {
				t.Errorf("Feature = %q, want cart_basic — sin la clave no se sabe qué contratar", falta.Feature)
			}
			if !errors.Is(err, ErrForbidden) {
				t.Error("*FeatureNotEnabledError debe ENVOLVER a ErrForbidden")
			}
			if got := StatusCodeOf(err); got != 0 {
				t.Errorf("el 403 llegó como *APIError desnudo (status %d)", got)
			}
		})
	}
}

// TestIntakes_El403SinLaClaveSigueSiendoUnPermiso: el hermano NEGATIVO del de arriba.
//
// Sin él, quien tradujera TODO 403 a *FeatureNotEnabledError tendría el test anterior en verde y la
// pantalla diciendo «tu plan no lo incluye» ante un token al que de verdad le falta el scope — con
// una `Feature` vacía, que no lleva a ningún sitio.
func TestIntakes_El403SinLaClaveSigueSiendoUnPermiso(t *testing.T) {
	t.Parallel()

	api, _ := servidorDeBandeja(t, http.StatusForbidden, `{"error":"scope requerido"}`)
	_, err := api.Intakes.ListIntakes(context.Background(), tokenDePrueba, IntakeFilter{})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if _, ok := FeatureNotEnabledOf(err); ok {
		t.Error("un 403 sin `feature_not_enabled` se tradujo como capacidad que falta")
	}
}

// TestReplaceIntakeItems_ElRechazoTraeLaListaENTERADeDefectos.
//
// El 400 de la edición no es «revisa los datos»: es una lista con la que el dueño corrige sus líneas
// en UNA pasada. Colapsarla en ErrInvalidInput le haría arreglar de una en una.
func TestReplaceIntakeItems_ElRechazoTraeLaListaENTERADeDefectos(t *testing.T) {
	t.Parallel()

	const cuerpo = `{"error":"invalid_items","errors":[
		{"index":0,"field":"qty","message":"la cantidad debe ser mayor que cero"},
		{"index":2,"field":"sku","message":"ese código no está en tu catálogo"}]}`
	api, _ := servidorDeBandeja(t, http.StatusBadRequest, cuerpo)
	_, err := api.Intakes.ReplaceIntakeItems(context.Background(), tokenDePrueba, intakeDePrueba, nil)

	invalid, ok := InvalidItemsOf(err)
	if !ok {
		t.Fatalf("err = %v, want *InvalidItemsError", err)
	}
	if len(invalid.Defects) != 2 {
		t.Fatalf("llegaron %d defectos, want 2 — la lista se recortó", len(invalid.Defects))
	}
	if invalid.Defects[1].Index != 2 || invalid.Defects[1].Field != "sku" {
		t.Errorf("el segundo defecto perdió su posición o su campo: %+v", invalid.Defects[1])
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Error("*InvalidItemsError debe ENVOLVER a ErrInvalidInput")
	}
}

// TestReplaceIntakeItems_El422DiceDesdeDONDESiSeEdita: la consola no replica el ciclo de vida, así
// que si el 422 no trajera `editable_in` no habría manera de decirle al dueño qué hacer.
func TestReplaceIntakeItems_El422DiceDesdeDONDESiSeEdita(t *testing.T) {
	t.Parallel()

	api, _ := servidorDeBandeja(t, http.StatusUnprocessableEntity,
		`{"error":"not_editable","status":"confirmed","editable_in":["pending_approval"]}`)
	_, err := api.Intakes.CorrectIntakeItems(context.Background(), tokenDePrueba, intakeDePrueba,
		[]IntakeItem{{SKU: "x", Qty: 1}})

	notEditable, ok := NotEditableOf(err)
	if !ok {
		t.Fatalf("err = %v, want *NotEditableError", err)
	}
	if notEditable.Status != "confirmed" || len(notEditable.EditableIn) != 1 {
		t.Errorf("el 422 perdió el estado o los editables: %+v", notEditable)
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Error("*NotEditableError debe ENVOLVER a ErrInvalidInput")
	}
}

// TestCorrectIntakeItems_EsElMISMOPUTConLaMarcaPuesta.
//
// No existe ninguna ruta `/correct` en la API: corregir es este PUT con `as_correction`. El aserto
// NEGATIVO de la edición normal es la otra mitad, y es el que importa: el campo lleva `omitempty`
// justamente para que el cuerpo del Plan 041 salga byte a byte como salía, y sin este aserto alguien
// podría quitar el `omitempty` sin que nada se pusiera rojo.
func TestCorrectIntakeItems_EsElMISMOPUTConLaMarcaPuesta(t *testing.T) {
	t.Parallel()

	items := []IntakeItem{{SKU: "torta-1", Label: "Torta", Qty: 1, UnitPrice: 25}}

	api, correccion := servidorDeBandeja(t, http.StatusOK, "{}")
	_, _ = api.Intakes.CorrectIntakeItems(context.Background(), tokenDePrueba, intakeDePrueba, items)
	if !strings.Contains(correccion.Body, `"as_correction":true`) {
		t.Errorf("la corrección no marcó as_correction: %s", correccion.Body)
	}

	api2, edicion := servidorDeBandeja(t, http.StatusOK, "{}")
	_, _ = api2.Intakes.ReplaceIntakeItems(context.Background(), tokenDePrueba, intakeDePrueba, items)
	if strings.Contains(edicion.Body, "as_correction") {
		t.Errorf("la edición del 041 dejó de salir byte a byte como salía: %s", edicion.Body)
	}
	if correccion.Path != edicion.Path || correccion.Method != edicion.Method {
		t.Errorf("las dos no van al mismo sitio: %s %s vs %s %s",
			correccion.Method, correccion.Path, edicion.Method, edicion.Path)
	}
}

// TestPutIntakeItems_LaListaVaciaViajaComoArregloYNoComoNull.
//
// Quitar todas las líneas es una edición legítima. Un `[]IntakeItem` nil serializaría a `null`, que
// la plataforma contesta con un 400 «no mandaste la clave» — y el dueño leería un error
// incomprensible ante una acción válida.
func TestPutIntakeItems_LaListaVaciaViajaComoArregloYNoComoNull(t *testing.T) {
	t.Parallel()

	api, ultima := servidorDeBandeja(t, http.StatusOK, "{}")
	_, _ = api.Intakes.ReplaceIntakeItems(context.Background(), tokenDePrueba, intakeDePrueba, nil)
	if !strings.Contains(ultima.Body, `"items":[]`) {
		t.Errorf("la lista vacía no viajó como []: %s", ultima.Body)
	}
}

// TestSetIntakeStatus_El422TraeLosDestinosPosibles: es la única respuesta de la API que publica el
// ciclo de vida. Sin ella el operador prueba estados a ciegas.
func TestSetIntakeStatus_El422TraeLosDestinosPosibles(t *testing.T) {
	t.Parallel()

	api, _ := servidorDeBandeja(t, http.StatusUnprocessableEntity,
		`{"error":"invalid_transition","status":"draft","requested":"confirmed",
		  "allowed":["pending_approval","abandoned"]}`)
	_, err := api.Intakes.SetIntakeStatus(context.Background(), tokenDePrueba, intakeDePrueba, "confirmed")

	invalid, ok := InvalidTransitionOf(err)
	if !ok {
		t.Fatalf("err = %v, want *InvalidTransitionError", err)
	}
	if invalid.Status != "draft" || invalid.Requested != "confirmed" || len(invalid.Allowed) != 2 {
		t.Errorf("el 422 perdió información: %+v", invalid)
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Error("*InvalidTransitionError debe ENVOLVER a ErrInvalidInput")
	}
}

// TestApproveIntake_LosDOS422SonHistoriasDISTINTAS.
//
// `/approve` emite dos 422 con el mismo código y consejos opuestos: `not_approvable` es «cambia de
// estado» y `invalid_transition` es «otro operador la movió, recarga». Un traductor que los mezclara
// tendría la mitad del test en verde y le daría al dueño el consejo del otro caso.
func TestApproveIntake_LosDOS422SonHistoriasDISTINTAS(t *testing.T) {
	t.Parallel()

	api, _ := servidorDeBandeja(t, http.StatusUnprocessableEntity,
		`{"error":"not_approvable","status":"confirmed","approvable_in":["pending_approval"]}`)
	_, err := api.Intakes.ApproveIntake(context.Background(), tokenDePrueba, intakeDePrueba, "Son 25,00")
	notApprovable, ok := NotApprovableOf(err)
	if !ok {
		t.Fatalf("not_approvable: err = %v, want *NotApprovableError", err)
	}
	if notApprovable.Status != "confirmed" || len(notApprovable.ApprovableIn) != 1 {
		t.Errorf("el 422 perdió información: %+v", notApprovable)
	}
	if _, esTransicion := InvalidTransitionOf(err); esTransicion {
		t.Error("`not_approvable` se tradujo como carrera entre operadores")
	}

	api2, _ := servidorDeBandeja(t, http.StatusUnprocessableEntity,
		`{"error":"invalid_transition","status":"abandoned","requested":"confirmed","allowed":[]}`)
	_, err2 := api2.Intakes.ApproveIntake(context.Background(), tokenDePrueba, intakeDePrueba, "Son 25,00")
	if _, ok := InvalidTransitionOf(err2); !ok {
		t.Fatalf("invalid_transition: err = %v, want *InvalidTransitionError", err2)
	}
	if _, esNoAprobable := NotApprovableOf(err2); esNoAprobable {
		t.Error("`invalid_transition` se tradujo como estado no aprobable")
	}
}

// TestApproveIntake_El400TraeLasLineasSinPrecio: TODAS de una vez, que es lo que el dueño necesita
// para arreglarlas en una pasada. Y la posición importa —la línea `unmatched` no tiene sku—.
func TestApproveIntake_El400TraeLasLineasSinPrecio(t *testing.T) {
	t.Parallel()

	api, _ := servidorDeBandeja(t, http.StatusBadRequest,
		`{"error":"lines_without_price","lines":[{"index":1,"label":"algo dulce"},{"index":3,"label":"bebidas"}]}`)
	_, err := api.Intakes.ApproveIntake(context.Background(), tokenDePrueba, intakeDePrueba, "Son 25,00")

	faltan, ok := LinesWithoutPriceOf(err)
	if !ok {
		t.Fatalf("err = %v, want *LinesWithoutPriceError", err)
	}
	if len(faltan.Lines) != 2 || faltan.Lines[1].Index != 3 || faltan.Lines[1].Label != "bebidas" {
		t.Errorf("las líneas llegaron mal: %+v", faltan.Lines)
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Error("*LinesWithoutPriceError debe ENVOLVER a ErrInvalidInput")
	}
}

// TestRequestIntakeInfo_NoTieneNiNotApprovableNiLineasSinPrecio.
//
// Medido contra `writeRequestInfoError` del cloud: esta puerta emite 400 en prosa (pregunta vacía),
// 404, 422 `invalid_transition` y 409. Este test es el que impide que alguien le atribuya los
// rechazos de `/approve` porque comparten el traductor: con la lista de precios inventada, la
// pantalla mandaría al dueño a poner precios en una acción que no los pide.
func TestRequestIntakeInfo_NoTieneNiNotApprovableNiLineasSinPrecio(t *testing.T) {
	t.Parallel()

	api, _ := servidorDeBandeja(t, http.StatusBadRequest, `{"error":"la pregunta no puede estar vacía"}`)
	_, err := api.Intakes.RequestIntakeInfo(context.Background(), tokenDePrueba, intakeDePrueba, "")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if _, ok := LinesWithoutPriceOf(err); ok {
		t.Error("el 400 en prosa se tradujo como líneas sin precio")
	}
	// 🔴 Y el motivo del upstream NO viaja: quien lo pintara dejaría que la plataforma decida qué
	// dice esta consola (misma doctrina que EditorClient).
	if strings.Contains(err.Error(), "la pregunta no puede estar vacía") {
		t.Errorf("el texto del upstream se coló en el error: %v", err)
	}
}

// TestDiscardIntakes_ElExitoNoSignificaQueSeDescartara.
//
// Un lote MIXTO es el caso NORMAL. Quien mire solo el `err == nil` le dirá al dueño que se descartó
// todo cuando puede no haberse descartado nada, y el descarte es irreversible.
func TestDiscardIntakes_ElExitoNoSignificaQueSeDescartara(t *testing.T) {
	t.Parallel()

	api, ultima := servidorDeBandeja(t, http.StatusOK,
		`{"discarded":["a"],"skipped":[{"intake_id":"b","reason":"live_event"},
		  {"intake_id":"c","reason":"un motivo que este cliente no conoce"}]}`)
	out, err := api.Intakes.DiscardIntakes(context.Background(), tokenDePrueba, []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(out.Discarded) != 1 || len(out.Skipped) != 2 {
		t.Fatalf("el desglose llegó mal: %+v", out)
	}
	if out.Skipped[0].Reason != DiscardSkipLiveEvent {
		t.Errorf("la razón conocida se perdió: %+v", out.Skipped[0])
	}
	// Una clave desconocida se entrega TAL CUAL: callarla dejaría al dueño creyendo que esa sí se
	// descartó.
	if out.Skipped[1].Reason == "" {
		t.Error("una razón desconocida se descartó en silencio")
	}
	if !strings.Contains(ultima.Body, `"intake_ids":["a","b","c"]`) {
		t.Errorf("el lote no viajó como lista explícita de ids: %s", ultima.Body)
	}
}

// TestDiscardIntakes_LaListaVaciaViajaComoArreglo: `[]` dice «no se seleccionó nada» y `null` dice
// «este cliente no supo armar la petición». Las dos las contesta la plataforma con el mismo 400, así
// que lo que cambia es lo que lee quien mire el cuerpo después.
func TestDiscardIntakes_LaListaVaciaViajaComoArreglo(t *testing.T) {
	t.Parallel()

	api, ultima := servidorDeBandeja(t, http.StatusOK, `{"discarded":[],"skipped":[]}`)
	if _, err := api.Intakes.DiscardIntakes(context.Background(), tokenDePrueba, nil); err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(ultima.Body, `"intake_ids":[]`) {
		t.Errorf("la lista nil viajó como null: %s", ultima.Body)
	}
}

// TestReanalyzeIntake_LosSEISMotivosNombradosSeSeparan.
//
// El código HTTP NO basta: el 400 son dos historias y el 422 son tres, y solo las separa la clave
// `error`. Tratarlas igual llevaría al paywall a quien lo que le falta es una credencial.
//
// 🆕 `text_too_long` es el SEXTO y NO viene del BFF —allí caía en el error genérico y la dueña leía
// «inténtalo de nuevo» ante una transcripción larga—. Está medido en el cloud
// (`publicapi/reanalyze.go`) y se traduce porque su consejo, «recorta hasta N», no lo da ningún otro.
func TestReanalyzeIntake_LosSEISMotivosNombradosSeSeparan(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre    string
		status    int
		cuerpo    string
		comprobar func(*testing.T, error)
	}{
		{"feature_not_enabled", http.StatusForbidden,
			`{"error":"feature_not_enabled","feature":"api_llm"}`,
			func(t *testing.T, err error) {
				falta, ok := FeatureNotEnabledOf(err)
				if !ok || falta.Feature != "api_llm" {
					t.Fatalf("err = %v, want *FeatureNotEnabledError{api_llm}", err)
				}
			}},
		{"llm_credentials_missing", http.StatusUnprocessableEntity,
			`{"error":"llm_credentials_missing","via":"api"}`,
			func(t *testing.T, err error) {
				falta, ok := LLMCredentialsMissingOf(err)
				if !ok || falta.Via != "api" {
					t.Fatalf("err = %v, want *LLMCredentialsMissingError{api}", err)
				}
				if _, esFeature := FeatureNotEnabledOf(err); esFeature {
					t.Error("«configura tus credenciales» se tradujo como «compra el plan»")
				}
			}},
		{"source_unavailable", http.StatusUnprocessableEntity,
			`{"error":"source_unavailable","reason":"never_stored"}`,
			func(t *testing.T, err error) {
				fuente, ok := SourceUnavailableOf(err)
				if !ok || fuente.Reason != SourceNeverStored {
					t.Fatalf("err = %v, want *SourceUnavailableError{never_stored}", err)
				}
				if fuente.Purged() {
					t.Error("`never_stored` se leyó como una pérdida: nunca fue una promesa")
				}
			}},
		{"reanalysis_in_progress", http.StatusUnprocessableEntity,
			`{"error":"reanalysis_in_progress","job_id":"job-9"}`,
			func(t *testing.T, err error) {
				enCurso, ok := ReanalysisInProgressOf(err)
				if !ok || enCurso.JobID != "job-9" {
					t.Fatalf("err = %v, want *ReanalysisInProgressError{job-9}", err)
				}
			}},
		{"invalid_via", http.StatusBadRequest,
			`{"error":"invalid_via","via":"api","configured_via":"local"}`,
			func(t *testing.T, err error) {
				invalid, ok := InvalidViaOf(err)
				if !ok || invalid.Via != "api" {
					t.Fatalf("err = %v, want *InvalidViaError{api}", err)
				}
			}},
		{"text_too_long", http.StatusBadRequest,
			`{"error":"text_too_long","runes":9000,"max":4000}`,
			func(t *testing.T, err error) {
				tooLong, ok := TextTooLongOf(err)
				if !ok {
					t.Fatalf("err = %v, want *TextTooLongError", err)
				}
				if tooLong.Runes != 9000 || tooLong.Max != 4000 {
					t.Errorf("sin las cifras el dueño no sabe cuánto recortar: %+v", tooLong)
				}
				if _, esVia := InvalidViaOf(err); esVia {
					t.Error("el texto largo se tradujo como vía inválida: comparten el 400")
				}
			}},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			api, _ := servidorDeBandeja(t, caso.status, caso.cuerpo)
			_, err := api.Intakes.ReanalyzeIntake(context.Background(), tokenDePrueba, intakeDePrueba, "hola")
			caso.comprobar(t, err)
			if got := StatusCodeOf(err); got != 0 {
				t.Errorf("%s llegó como *APIError desnudo (status %d)", caso.nombre, got)
			}
		})
	}
}

// TestReanalyzeIntake_LaViaNOViaja (D-044.51): el cuerpo lleva `text` y nada más. Mandar la vía sería
// convertir un acto de configuración —con su consentimiento— en un parámetro de una llamada suelta
// que puede mandar el texto de un cliente a un tercero de pago.
func TestReanalyzeIntake_LaViaNOViaja(t *testing.T) {
	t.Parallel()

	api, conTexto := servidorDeBandeja(t, http.StatusOK, `{"intake_id":"x","revision_no":4,"job_id":"j","via":"local","status":"processing"}`)
	out, err := api.Intakes.ReanalyzeIntake(context.Background(), tokenDePrueba, intakeDePrueba, "una transcripción")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if strings.Contains(conTexto.Body, `"via"`) {
		t.Errorf("el cuerpo manda la vía: %s", conTexto.Body)
	}
	if !strings.Contains(conTexto.Body, `"text":"una transcripción"`) {
		t.Errorf("el material extra no viajó: %s", conTexto.Body)
	}
	// El 200 anuncia una revisión que TODAVÍA NO EXISTE, y `status` es siempre «processing».
	if out.RevisionNo != 4 || out.Status != "processing" {
		t.Errorf("la respuesta llegó mal: %+v", out)
	}

	// Y sin material extra la clave no se manda (omitempty): el caso corriente es «regenera según el
	// origen», no «suma una cadena vacía».
	api2, sinTexto := servidorDeBandeja(t, http.StatusOK, "{}")
	_, _ = api2.Intakes.ReanalyzeIntake(context.Background(), tokenDePrueba, intakeDePrueba, "")
	if strings.Contains(sinTexto.Body, "text") {
		t.Errorf("el texto vacío viajó igualmente: %s", sinTexto.Body)
	}
}

// TestSuggestIntakeQuote_ElRespaldoSobrioNOEsUnError.
//
// Con el modelo caído esta puerta responde 200 con el texto determinista y su motivo. Quien tratara
// `deterministic` como un fallo escondería una cotización perfectamente utilizable.
func TestSuggestIntakeQuote_ElRespaldoSobrioNOEsUnError(t *testing.T) {
	t.Parallel()

	api, ultima := servidorDeBandeja(t, http.StatusOK,
		`{"rendered_text":"Torta 25,00\nTotal 25,00","source":"deterministic","fallback_reason":"llm_fallo"}`)
	out, err := api.Intakes.SuggestIntakeQuote(context.Background(), tokenDePrueba, intakeDePrueba)
	if err != nil {
		t.Fatalf("el respaldo sobrio llegó como error: %v", err)
	}
	if out.FromLLM() {
		t.Error("FromLLM() dijo que la escribió el modelo")
	}
	if out.FallbackReason != QuoteFallbackLLMFailed || out.RenderedText == "" {
		t.Errorf("la sugerencia llegó incompleta: %+v", out)
	}
	// NO LLEVA CUERPO, y eso es el contrato: el cloud ni siquiera lo lee.
	if ultima.Body != "" {
		t.Errorf("la sugerencia mandó cuerpo: %q", ultima.Body)
	}
}

// TestSuggestIntakeQuote_VaPorElClienteDePlazoLARGO.
//
// 🔴 El test que demuestra el MECANISMO y no solo la intención. El cliente general se construye con
// 80 ms y el de inferencia se queda en DefaultInferenceTimeout; contra un upstream que tarda 400 ms,
// el detalle MUERE por plazo y la sugerencia COMPLETA. Sin el cliente aparte los dos morirían, que es
// exactamente lo que pasaba en el BFF antes del Plan 047 · T2.4: esta llamada tardó 24,8 / 28,4 /
// 29,7 / 35,5 segundos medida contra UAT y el plazo general de esta consola son 15-20 s.
func TestSuggestIntakeQuote_VaPorElClienteDePlazoLARGO(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(400 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"rendered_text":"Son 25,00","source":"llm"}`))
	}))
	t.Cleanup(srv.Close)
	api := New(srv.URL, 80*time.Millisecond)

	if _, err := api.Intakes.GetIntake(context.Background(), tokenDePrueba, intakeDePrueba); err == nil {
		t.Fatal("el detalle NO murió por plazo con un cliente de 80 ms: el test no está midiendo nada")
	}
	out, err := api.Intakes.SuggestIntakeQuote(context.Background(), tokenDePrueba, intakeDePrueba)
	if err != nil {
		t.Fatalf("la sugerencia murió por el plazo general: %v", err)
	}
	if !out.FromLLM() {
		t.Errorf("la respuesta llegó mal: %+v", out)
	}
}

// TestSuggestIntakeQuote_ElClienteDeInferenciaTieneUNSoloLlamante.
//
// Candado ESTRUCTURAL, y existe porque la regla no la vigila ningún tipo: `doInference` es un método
// como otro cualquiera y usarlo desde una segunda llamada compila, pasa los tests y le regala 55 s a
// algo que no los necesita —lo que convierte un upstream lento en una pantalla colgada—. Se cuenta
// sobre el TEXTO del paquete por lo mismo que el resto de invariantes estructurales de esta casa: un
// test de conducta por llamada envejece en cuanto se añade la número once.
func TestSuggestIntakeQuote_ElClienteDeInferenciaTieneUNSoloLlamante(t *testing.T) {
	t.Parallel()

	ficheros, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("no se pudo listar el paquete: %v", err)
	}
	llamantes := map[string]int{}
	for _, f := range ficheros {
		// transport.go es donde se DECLARA, y los tests no cuentan: lo que se vigila es el código de
		// producción que lo USA.
		if f == "transport.go" || strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("leer %s: %v", f, err)
		}
		if n := strings.Count(string(raw), "doInference("); n > 0 {
			llamantes[f] = n
		}
	}
	if len(llamantes) != 1 || llamantes["intakes_quote.go"] != 1 {
		t.Errorf("doInference tiene %v llamantes; el único permitido es SuggestIntakeQuote en intakes_quote.go", llamantes)
	}
}

// detalleConBorrador es un detalle con las DOS caras: las líneas resueltas (`items`) y la revisión
// `interpreted` con el borrador, donde vive la línea `unmatched` que `items` ni siquiera trae.
const detalleConBorrador = `{
  "id":"` + intakeDePrueba + `","contact_id":"c-1","status":"pending_approval","total":25,
  "items":[{"sku":"torta-1","label":"Torta","customization":"sin nueces","qty":1,"unit_price":25}],
  "allowed_transitions":["confirmed","abandoned"],
  "revisions":[
    {"revision_no":1,"kind":"cart","created_by":"system","created_at":"2026-08-01T10:00:00Z"},
    {"revision_no":2,"kind":"interpreted","created_by":"system","created_at":"2026-08-01T10:01:00Z",
     "payload":{"version":1,"source_text":"quiero una torta y algo dulce",
       "lines":[{"kind":"matched","sku":"torta-1","label":"Torta","qty":1,"unit_price":25},
                {"kind":"unmatched","label":"algo dulce","qty":1,"unit_price":null}],
       "suggested_questions":["¿Para cuántas personas?"]}},
    {"revision_no":3,"kind":"corrected","created_by":"owner","created_at":"2026-08-01T10:02:00Z"}
  ]}`

// TestGetIntake_ElBorradorSaleDeLasREVISIONESYNoDeItems.
//
// 🔴 La razón por la que el modelo trae las dos cosas: `items` son las líneas RESUELTAS y su
// `unit_price` es un float que no sabe decir «todavía no hay precio»; la línea `unmatched` —que es
// justo la que el dueño tiene que atender— NI SIQUIERA APARECE ahí. Una pantalla construida sobre
// `items` enseñaría un pedido completo al que le falta la mitad.
func TestGetIntake_ElBorradorSaleDeLasREVISIONESYNoDeItems(t *testing.T) {
	t.Parallel()

	api, _ := servidorDeBandeja(t, http.StatusOK, detalleConBorrador)
	detail, err := api.Intakes.GetIntake(context.Background(), tokenDePrueba, intakeDePrueba)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if len(detail.Items) != 1 || detail.Items[0].Customization != "sin nueces" {
		t.Errorf("las líneas resueltas llegaron mal: %+v", detail.Items)
	}
	if detail.AllowedTransitions == nil || len(*detail.AllowedTransitions) != 2 {
		t.Fatalf("allowed_transitions llegó mal: %v", detail.AllowedTransitions)
	}

	rev := detail.LastRevisionOf(RevisionKindInterpreted)
	if rev == nil || rev.RevisionNo != 2 {
		t.Fatalf("no se encontró la interpretación: %+v", rev)
	}
	borrador, err := DecodeInterpretation(rev.Payload)
	if err != nil {
		t.Fatalf("DecodeInterpretation: %v", err)
	}
	if len(borrador.Lines) != 2 {
		t.Fatalf("el borrador llegó con %d líneas, want 2", len(borrador.Lines))
	}
	// La `unmatched` es la que `items` no trae, y su precio es NULL —«lo pone el dueño»—, que no es
	// lo mismo que 0 —«va de regalo»—.
	sinPrecio := borrador.Lines[1]
	if sinPrecio.Kind != LineKindUnmatched || sinPrecio.HasPrice() {
		t.Errorf("la línea sin precio se leyó mal: %+v", sinPrecio)
	}
	if !borrador.QuestionsKnown() || len(borrador.Questions()) != 1 {
		t.Errorf("las preguntas preparadas llegaron mal: known=%v %v",
			borrador.QuestionsKnown(), borrador.Questions())
	}
	// Y hay UNA revisión posterior: el borrador que se pinta ya no es lo último que pasó.
	if got := detail.RevisionsAfter(rev.RevisionNo); got != 1 {
		t.Errorf("RevisionsAfter(2) = %d, want 1", got)
	}
	if rev.LiteralPruned() {
		t.Error("una revisión sin literal_pruned_at se leyó como podada")
	}
}

// TestIntakeDetail_SinAllowedTransitionsNoEsUnEstadoTERMINAL.
//
// La distinción que el puntero existe para conservar: `null` es «no se sabe» (un servidor anterior a
// cloud a804943) y `[]` es «terminal, no admite cambios». Colapsarlas haría que la pantalla dijera
// «no hay acciones» ante un servidor que simplemente no publicó el campo.
func TestIntakeDetail_SinAllowedTransitionsNoEsUnEstadoTERMINAL(t *testing.T) {
	t.Parallel()

	api, _ := servidorDeBandeja(t, http.StatusOK, `{"id":"x","items":[]}`)
	sinCampo, err := api.Intakes.GetIntake(context.Background(), tokenDePrueba, intakeDePrueba)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if sinCampo.AllowedTransitions != nil {
		t.Error("la clave ausente no dejó el puntero en nil")
	}

	api2, _ := servidorDeBandeja(t, http.StatusOK, `{"id":"x","items":[],"allowed_transitions":[]}`)
	terminal, err := api2.Intakes.GetIntake(context.Background(), tokenDePrueba, intakeDePrueba)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if terminal.AllowedTransitions == nil {
		t.Fatal("`[]` se leyó como clave ausente: se perdió el estado terminal")
	}
	if len(*terminal.AllowedTransitions) != 0 {
		t.Errorf("AllowedTransitions = %v, want []", *terminal.AllowedTransitions)
	}
}

// TestIntakes_ElRestoDeLos4xxLlegaComoSentinelaEnLasDiez.
//
// El aserto NO es «devuelve error»: es qué sentinela, en las diez operaciones, más la comprobación de
// que no queda ningún *APIError debajo. El 401 va con ellos porque es el que dispara el reintento con
// token refrescado: si llegara como *APIError, una sesión caducada expulsaría al login en vez de
// renovarse sola.
func TestIntakes_ElRestoDeLos4xxLlegaComoSentinelaEnLasDiez(t *testing.T) {
	t.Parallel()

	codigos := []struct {
		status int
		quiero error
	}{
		{http.StatusUnauthorized, ErrUnauthorized},
		{http.StatusNotFound, ErrNotFound},
		{http.StatusBadRequest, ErrInvalidInput},
	}
	for _, caso := range llamadasDeLaBandeja {
		for _, codigo := range codigos {
			api, _ := servidorDeBandeja(t, codigo.status, "")
			err := caso.llamar(api)
			if !errors.Is(err, codigo.quiero) {
				t.Errorf("%s con %d: err = %v, want %v", caso.nombre, codigo.status, err, codigo.quiero)
			}
			if got := StatusCodeOf(err); got != 0 {
				t.Errorf("%s con %d llegó como *APIError desnudo", caso.nombre, codigo.status)
			}
		}
	}
}

// TestIntakes_ElIdentificadorNoPuedeREESCRIBIRLaRuta: el id llega de un formulario, y sin escapar un
// valor con `/` alcanzaría OTRO endpoint de la API pública con el token del usuario.
func TestIntakes_ElIdentificadorNoPuedeREESCRIBIRLaRuta(t *testing.T) {
	t.Parallel()

	api, ultima := servidorDeBandeja(t, http.StatusOK, "{}")
	_, _ = api.Intakes.GetIntake(context.Background(), tokenDePrueba, "../../api/v1/tenants")

	// 🔴 EL ASERTO VA SOBRE LA URI, NO SOBRE URL.Path: el servidor entrega el Path ya DECODIFICADO,
	// así que las barras escapadas (`%2F`) vuelven a leerse ahí como barras y este test saldría rojo
	// con el escape puesto y correcto. Lo que decide si el id se salió de su segmento es lo que viajó
	// por el cable.
	if strings.Contains(ultima.URI, "/tenants") {
		t.Errorf("el id alcanzó otro recurso de la API: %s", ultima.URI)
	}
	if got := strings.Count(ultima.URI, "/"); got != 4 {
		t.Errorf("el identificador añadió %d segmentos a la ruta: %s", got-4, ultima.URI)
	}
}

// TestIntakes_ElCuerpoDeUnRechazoNoLlegaAPintarse: la doctrina de esta casa (ver EditorClient). Lo que
// viaja son los cuerpos ESTRUCTURADOS —defectos, estados permitidos, líneas sin precio—; la prosa que
// escribe el cloud se drena y no sale por ningún error.
func TestIntakes_ElCuerpoDeUnRechazoNoLlegaAPintarse(t *testing.T) {
	t.Parallel()

	const prosaDelUpstream = "no se pudieron listar las solicitudes: pq: relation does not exist"
	api, _ := servidorDeBandeja(t, http.StatusInternalServerError,
		`{"error":"`+prosaDelUpstream+`"}`)
	_, err := api.Intakes.ListIntakes(context.Background(), tokenDePrueba, IntakeFilter{})
	if err == nil {
		t.Fatal("un 500 no dio error")
	}
	if strings.Contains(err.Error(), "relation does not exist") {
		t.Errorf("el detalle del upstream viaja en el error: %v", err)
	}
	if got := StatusCodeOf(err); got != http.StatusInternalServerError {
		t.Errorf("el 5xx debe seguir llegando como *APIError con su status, got %d", got)
	}
}

// llamadaPorNombre busca una operación en llamadasDeLaBandeja. Falla el test si no está: una tabla que
// se renombra sin actualizar a quien la usa dejaría los tests de desenlace midiendo nada.
func llamadaPorNombre(t *testing.T, nombre string) func(*Client) error {
	t.Helper()
	for _, caso := range llamadasDeLaBandeja {
		if caso.nombre == nombre {
			return caso.llamar
		}
	}
	t.Fatalf("no hay ninguna llamada %q en llamadasDeLaBandeja", nombre)
	return nil
}

// TestIntakeFilter_LosNombresDeLaQuerySonLosQueLeeElCLOUD.
//
// Medidos contra `publicapi.parseIntakeFilter`: `from`, `to`, `status`, `session`, `sort`, `page` y
// `page_size`. Una clave que la API no reconoce NO da error: se ignora, y el listado devuelve la
// primera página del filtro entero como si nadie hubiera pedido nada.
func TestIntakeFilter_LosNombresDeLaQuerySonLosQueLeeElCLOUD(t *testing.T) {
	t.Parallel()

	q := IntakeFilter{
		From: "2026-08-01", To: "2026-08-31", Status: "needs_info", Session: "s-1",
		Sort: IntakeSortNewest, Page: 3, PageSize: 10,
	}.query()

	for _, clave := range []string{
		"from=2026-08-01", "to=2026-08-31", "status=needs_info", "session=s-1",
		"sort=newest", "page=3", "page_size=10",
	} {
		if !strings.Contains(q, clave) {
			t.Errorf("la query no lleva %q: %s", clave, q)
		}
	}
	// Y no inventa ninguna: lo que la API no espera se ignora en silencio, así que una clave de más
	// no da error — devuelve la primera página como si nadie hubiera filtrado.
	valores, err := url.ParseQuery(strings.TrimPrefix(q, "?"))
	if err != nil {
		t.Fatalf("query ilegible %q: %v", q, err)
	}
	if len(valores) != 7 {
		t.Errorf("la query lleva %d claves, want 7: %s", len(valores), q)
	}
}
