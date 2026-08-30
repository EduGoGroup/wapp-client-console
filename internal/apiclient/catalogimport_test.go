package apiclient

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// peticionDeCatalogo es lo que hace falta saber de lo que salió por el cable en el import. A
// diferencia de peticionCapturada de la bandeja, guarda el cuerpo en BYTES y las cabeceras que
// deciden el formato: aquí lo que se prueba no es solo adónde se fue, es EN QUÉ FORMA.
type peticionDeCatalogo struct {
	Method      string
	Path        string
	Query       string
	Auth        string
	ContentType string
	Accept      string
	Body        []byte
	// Llamadas cuenta cuántas veces se llegó al servidor: es lo que distingue «lo rechazó el cloud»
	// de «se rechazó aquí y no se gastó el viaje».
	Llamadas atomic.Int32
}

// servidorDeCatalogo levanta un upstream que contesta siempre lo mismo (con las cabeceras que se le
// den) y guarda la última petición entera.
func servidorDeCatalogo(t *testing.T, status int, cabeceras map[string]string, body []byte) (*Client, *peticionDeCatalogo) {
	t.Helper()
	var ultima peticionDeCatalogo
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		ultima.Method, ultima.Path = r.Method, r.URL.Path
		ultima.Query, ultima.Auth = r.URL.RawQuery, r.Header.Get("Authorization")
		ultima.ContentType, ultima.Accept = r.Header.Get("Content-Type"), r.Header.Get("Accept")
		ultima.Body = raw
		ultima.Llamadas.Add(1)
		for k, v := range cabeceras {
			w.Header().Set(k, v)
		}
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		if len(body) > 0 {
			_, _ = w.Write(body)
		}
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, 5*time.Second), &ultima
}

// csvDePrueba es una planilla REAL: cabecera, cuatro filas, acentos y eñes, punto y coma dentro de un
// texto y un precio con decimales. Los acentos no son adorno — son lo que se pierde si alguien
// recodifica el fichero por el camino, y por eso el aserto del multipart compara BYTE A BYTE.
const csvDePrueba = "categoria,nombre,precio,unidad\n" +
	"Almuerzos,Milanesa con puré,12.50,unidad\n" +
	"Almuerzos,Ñoquis caseros; con salsa,10.00,porción\n" +
	"Postres,Budín de pan,4.75,porción\n" +
	"Bebidas,Limonada (jarra 1 L),6.20,jarra\n"

// xlsxDePrueba arma un ZIP mínimo VÁLIDO, que es lo que un .xlsx es por dentro.
//
// Se genera aquí y no se mete un binario en el repo por dos motivos: el cloud reconoce el formato por
// la FIRMA DEL ZIP y no por la extensión —así que lo que hace falta es un fichero que empiece por
// `PK\x03\x04`, no una hoja de cálculo de verdad—, y un binario de prueba en el árbol es algo que
// nadie vuelve a mirar.
func xlsxDePrueba(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatalf("armar el xlsx de prueba: %v", err)
	}
	if _, err := w.Write([]byte(`<?xml version="1.0"?><sheetData>` +
		`<row><c t="str"><v>Ñoquis caseros</v></c><c><v>10.00</v></c></row>` +
		`<row><c t="str"><v>Budín de pan</v></c><c><v>4.75</v></c></row>` +
		`</sheetData>`)); err != nil {
		t.Fatalf("escribir el xlsx de prueba: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("cerrar el xlsx de prueba: %v", err)
	}
	return buf.Bytes()
}

// documentoDePrueba es el JSON del contrato tal como lo escribiría (o lo generaría un asistente para)
// el dueño del negocio: INDENTADO y con los campos en su orden. Que viaje así, y no reserializado,
// es parte del contrato de ImportCatalog.
const documentoDePrueba = `{
  "format": "wapp.catalog_import",
  "version": 1,
  "catalog": {
    "categories": [
      {"name": "Almuerzos", "items": [
        {"sku": "mila-1", "label": "Milanesa con puré", "price": 12.50}
      ]}
    ]
  }
}`

const refDePrueba = "catalogo"

// TestImportCatalogTabular_ElFicheroViajaEnMULTIPARTYLlegaByteAByte.
//
// 🔴 ES EL TEST DE LA MUTACIÓN DECLARADA DE T8.1: si alguien cambia la subida por un JSON con el
// fichero en base64, esto se pone rojo. Y se pone rojo por el ASERTO y no porque «la llamada deje de
// funcionar»: mirar que la llamada devuelve 200 no distingue las dos implementaciones —el servidor de
// prueba contesta 200 a cualquier cosa—, así que lo que se inspecciona es la petición SALIENTE.
//
// Los cuatro asertos son distintos y ninguno sobra:
//   - el Content-Type es `multipart/form-data` CON boundary (es lo que mata la mutación);
//   - el campo se llama `file` (el nombre lo fija el contrato: con otro, el cloud dice que no llegó
//     ningún archivo);
//   - el nombre y el tipo del fichero viajan en la parte (con CreateFormFile el tipo sería siempre
//     `application/octet-stream` y se perdería);
//   - y lo que llega es BYTE A BYTE lo que se mandó: los acentos del CSV y la firma ZIP del XLSX
//     están justo para que un recodificado por el camino no pase inadvertido.
//
// 🔴 Y va con CONTENIDO de verdad: un fichero de 0 bytes pasaría con casi cualquier bug —un sobre
// vacío es indistinguible de un sobre bien armado sin nada dentro—, así que el test empieza
// comprobando que lo que sube pesa algo.
func TestImportCatalogTabular_ElFicheroViajaEnMULTIPARTYLlegaByteAByte(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre      string
		filename    string
		contentType string
		contenido   []byte
	}{
		{"csv", "catálogo de agosto.csv", "text/csv", []byte(csvDePrueba)},
		{"xlsx", "catalogo.xlsx",
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", xlsxDePrueba(t)},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			if len(caso.contenido) < 100 {
				t.Fatalf("el fichero de prueba pesa %d bytes: con tan poco, este test no prueba nada",
					len(caso.contenido))
			}
			api, pet := servidorDeCatalogo(t, http.StatusOK, nil, []byte(`{"mode":"validate"}`))
			if _, err := api.Catalog.ImportCatalogTabular(context.Background(), tokenDePrueba,
				CatalogUpload{Filename: caso.filename, ContentType: caso.contentType, Content: caso.contenido},
				CatalogModeValidate, refDePrueba); err != nil {
				t.Fatalf("la subida falló: %v", err)
			}

			tipo, params, err := mime.ParseMediaType(pet.ContentType)
			if err != nil {
				t.Fatalf("el Content-Type saliente no se deja parsear (%q): %v", pet.ContentType, err)
			}
			if tipo != "multipart/form-data" {
				t.Fatalf("el fichero NO viajó en un multipart: Content-Type = %q", pet.ContentType)
			}
			if params["boundary"] == "" {
				t.Fatal("el multipart salió sin boundary: el otro lado no puede separar las partes")
			}

			mr := multipart.NewReader(bytes.NewReader(pet.Body), params["boundary"])
			parte, err := mr.NextPart()
			if err != nil {
				t.Fatalf("el sobre no trae ninguna parte: %v", err)
			}
			if got := parte.FormName(); got != "file" {
				t.Errorf("el campo se llama %q, y el contrato dice \"file\"", got)
			}
			if got := parte.FileName(); got != caso.filename {
				t.Errorf("filename = %q, want %q", got, caso.filename)
			}
			if got := parte.Header.Get("Content-Type"); got != caso.contentType {
				t.Errorf("el tipo de la parte = %q, want %q", got, caso.contentType)
			}
			recibido, err := io.ReadAll(parte)
			if err != nil {
				t.Fatalf("leer la parte: %v", err)
			}
			if !bytes.Equal(recibido, caso.contenido) {
				t.Errorf("el fichero NO llegó intacto: %d bytes de %d", len(recibido), len(caso.contenido))
			}
			if _, err := mr.NextPart(); !errors.Is(err, io.EOF) {
				t.Errorf("el sobre trae más de una parte (err=%v); el cloud solo mira una", err)
			}
		})
	}
}

// TestImportCatalog_ElDocumentoViajaCRUDOYNoEnBase64.
//
// 🔴 La trampa que este test existe para cazar: newAuthedRequest serializa con json.Marshal, y
// json.Marshal de unos `[]byte` es su BASE64 entre comillas. Mandar el documento por ahí compila,
// viaja y el cloud contesta un 400 incomprensible. Por eso ImportCatalog va por newBodyRequest, y por
// eso aquí se compara el cuerpo saliente byte a byte Y se busca explícitamente el base64.
//
// El documento va indentado a propósito: si alguien lo decodificara y lo volviera a serializar, el
// cuerpo seguiría siendo un JSON equivalente pero NO el mismo texto — y lo que se validó dejaría de
// ser lo que el dueño vio.
func TestImportCatalog_ElDocumentoViajaCRUDOYNoEnBase64(t *testing.T) {
	t.Parallel()

	api, pet := servidorDeCatalogo(t, http.StatusOK, nil, []byte(`{"mode":"apply","applied":true}`))
	if _, err := api.Catalog.ImportCatalog(context.Background(), tokenDePrueba,
		[]byte(documentoDePrueba), CatalogModeApply, refDePrueba); err != nil {
		t.Fatalf("el import falló: %v", err)
	}

	if string(pet.Body) != documentoDePrueba {
		t.Errorf("el documento NO viajó tal cual.\nsalió: %s", pet.Body)
	}
	enBase64 := base64.StdEncoding.EncodeToString([]byte(documentoDePrueba))
	if bytes.Contains(pet.Body, []byte(enBase64)) {
		t.Error("el documento viajó en BASE64: alguien lo pasó por json.Marshal")
	}
	if pet.ContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", pet.ContentType)
	}
}

// llamadasDelCatalogo son las CINCO operaciones del plano, cada una con su verbo y su ruta medidos
// contra el registro del cloud (`registerCatalogImport` y el bloque de tenant-content de
// publicapi.go), no contra lo que dijera el plan.
//
// 🔴 Fíjate en las firmas: ninguna recibe una empresa. No es que no se le pase — es que no hay
// parámetro (INV-04).
var llamadasDelCatalogo = []struct {
	nombre string
	verbo  string
	ruta   string
	llamar func(*Client) error
}{
	{"import", http.MethodPost, "/api/v1/catalog/import", func(c *Client) error {
		_, err := c.Catalog.ImportCatalog(context.Background(), tokenDePrueba,
			[]byte(documentoDePrueba), CatalogModeValidate, refDePrueba)
		return err
	}},
	{"import.tabular", http.MethodPost, "/api/v1/catalog/import/tabular", func(c *Client) error {
		_, err := c.Catalog.ImportCatalogTabular(context.Background(), tokenDePrueba,
			CatalogUpload{Filename: "catalogo.csv", ContentType: "text/csv", Content: []byte(csvDePrueba)},
			CatalogModeValidate, refDePrueba)
		return err
	}},
	{"template", http.MethodGet, "/api/v1/catalog/import/template", func(c *Client) error {
		_, err := c.Catalog.GetCatalogTemplate(context.Background(), tokenDePrueba, CatalogTemplateCSV)
		return err
	}},
	{"prompt", http.MethodGet, "/api/v1/catalog/import/prompt", func(c *Client) error {
		_, err := c.Catalog.GetCatalogPrompt(context.Background(), tokenDePrueba)
		return err
	}},
	{"tenant-content", http.MethodGet, "/api/v1/tenant-content", func(c *Client) error {
		_, err := c.Catalog.ListTenantContentRefs(context.Background(), tokenDePrueba)
		return err
	}},
}

// TestCatalogo_LasCINCOVanASuVerboYSuRuta fija el contrato con la plataforma por el CABLE: cambiar el
// struct de respuesta rompe la decodificación y se nota, pero equivocarse de ruta o de verbo compila,
// pasa los tipos y solo se ve en campo como un 404 inexplicable.
func TestCatalogo_LasCINCOVanASuVerboYSuRuta(t *testing.T) {
	t.Parallel()

	for _, caso := range llamadasDelCatalogo {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			api, pet := servidorDeCatalogo(t, http.StatusOK, nil, []byte(`{}`))
			_ = caso.llamar(api)

			if pet.Method != caso.verbo {
				t.Errorf("verbo = %s, want %s", pet.Method, caso.verbo)
			}
			if pet.Path != caso.ruta {
				t.Errorf("ruta = %s, want %s", pet.Path, caso.ruta)
			}
			if pet.Auth != "Bearer "+tokenDePrueba {
				t.Errorf("Authorization = %q", pet.Auth)
			}
		})
	}
}

// TestCatalogo_INV04_LaEmpresaNoViajaEnNingunaDeLasCINCO: el tenant sale del Context Token. El aserto
// barre las TRES posiciones donde podría colarse —query, cuerpo y ruta— y va aquí, en el paquete que
// ESCRIBE la petición.
func TestCatalogo_INV04_LaEmpresaNoViajaEnNingunaDeLasCINCO(t *testing.T) {
	t.Parallel()

	const empresa = "33333333-3333-4333-8333-333333333333"
	for _, caso := range llamadasDelCatalogo {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			api, pet := servidorDeCatalogo(t, http.StatusOK, nil, []byte(`{}`))
			_ = caso.llamar(api)

			if strings.Contains(pet.Query, "tenant_id") {
				t.Errorf("%s manda tenant_id en la query: %s", caso.nombre, pet.Query)
			}
			if bytes.Contains(pet.Body, []byte("tenant_id")) {
				t.Errorf("%s manda tenant_id en el cuerpo", caso.nombre)
			}
			if strings.Contains(pet.Path, empresa) || strings.Contains(pet.Path, "tenant_id") {
				t.Errorf("%s lleva la empresa en la RUTA: %s", caso.nombre, pet.Path)
			}
		})
	}
}

// TestImportCatalog_ElModoViajaSIEMPREExplicitoYLaRefSoloSiSeFija.
//
// El modo explícito es una decisión de seguridad: el cloud, sin parámetro, asume `validate`, y
// depender de ese default para NO escribir el catálogo de una empresa es depender de que nadie lo
// cambie nunca. La ref, al revés: vacía se deja al default de la plataforma, porque copiarlo aquí
// sería tener dos.
func TestImportCatalog_ElModoViajaSIEMPREExplicitoYLaRefSoloSiSeFija(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre string
		modo   CatalogImportMode
		ref    string
		quiero string
	}{
		{"validate con ref", CatalogModeValidate, "menu-2026", "mode=validate&ref=menu-2026"},
		{"apply con ref", CatalogModeApply, "menu-2026", "mode=apply&ref=menu-2026"},
		{"sin ref", CatalogModeApply, "", "mode=apply"},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			api, pet := servidorDeCatalogo(t, http.StatusOK, nil, []byte(`{}`))
			_, _ = api.Catalog.ImportCatalog(context.Background(), tokenDePrueba,
				[]byte(documentoDePrueba), caso.modo, caso.ref)
			if pet.Query != caso.quiero {
				t.Errorf("query = %q, want %q", pet.Query, caso.quiero)
			}
		})
	}
}

// TestCatalogo_LoQueSeRechazaAQUINoGastaElViaje: las tres puertas que esta consola cierra ella misma.
//
// 🔴 El aserto que importa es el CONTADOR: sin él, un cliente que mandara la petición y devolviera el
// error después seguiría en verde, y el modo vacío llegaría al cloud —que aplicaría su default y
// contestaría 200 sin haber escrito nada—.
func TestCatalogo_LoQueSeRechazaAQUINoGastaElViaje(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre string
		llamar func(*Client) error
		quiero error
		porQue string
	}{
		{"modo vacío", func(c *Client) error {
			_, err := c.Catalog.ImportCatalog(context.Background(), tokenDePrueba,
				[]byte(documentoDePrueba), "", refDePrueba)
			return err
		}, ErrCatalogModeUnknown, "el cero-valor del modo es la cadena vacía, y el cloud la lee como validate"},
		{"formato de plantilla inventado", func(c *Client) error {
			_, err := c.Catalog.GetCatalogTemplate(context.Background(), tokenDePrueba, "xls")
			return err
		}, ErrCatalogFormatUnsupported, "«xls» tecleado a las prisas no puede degradar a otra cosa"},
		{"documento vacío", func(c *Client) error {
			_, err := c.Catalog.ImportCatalog(context.Background(), tokenDePrueba, nil, CatalogModeApply, refDePrueba)
			return err
		}, ErrInvalidInput, "un documento vacío no es un import, es una llamada mal armada"},
		{"fichero de 0 bytes", func(c *Client) error {
			_, err := c.Catalog.ImportCatalogTabular(context.Background(), tokenDePrueba,
				CatalogUpload{Filename: "catalogo.csv", Content: nil}, CatalogModeApply, refDePrueba)
			return err
		}, ErrInvalidInput, "una subida de cero bytes es una subida que no llegó a ocurrir"},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			api, pet := servidorDeCatalogo(t, http.StatusOK, nil, []byte(`{}`))
			err := caso.llamar(api)
			if !errors.Is(err, caso.quiero) {
				t.Errorf("error = %v, want %v (%s)", err, caso.quiero, caso.porQue)
			}
			if n := pet.Llamadas.Load(); n != 0 {
				t.Errorf("se gastó el viaje %d veces: %s", n, caso.porQue)
			}
		})
	}
}

// respuestaTabular es el 200 del camino tabular: el mismo objeto que el JSON MÁS `document`, que es
// la planilla ya traducida al contrato.
const respuestaTabular = `{
  "mode":"validate","ref":"catalogo","applied":false,"items":2,
  "diff":{"price_changes":[{"sku":"mila-1","label":"Milanesa","old_price":11.5,"new_price":12.5}],
          "added":[{"sku":"noqui-1","label":"Ñoquis"}],"removed":[],
          "changed_details":["mila-1"],"unchanged":7,"current_warnings":["sku reservado: _shipping"]},
  "document":{"format":"wapp.catalog_import","version":1,"catalog":{"categories":[]}}
}`

// TestImportCatalogTabular_El200TraeElDocumentoParaElPASO2.
//
// 🔴 El `document` es lo que sostiene la confirmación en dos pasos: el paso 2 reenvía ESOS MISMOS
// BYTES al import JSON, así que se aplica exactamente lo que se enseñó en el diff. Por eso el aserto
// no es «llega el documento», es que llega SIN PASAR POR UN STRUCT: si alguien lo tipara aquí,
// cualquier campo que la plataforma añada se perdería en la traducción y el paso 2 aplicaría un
// documento distinto del que se validó.
func TestImportCatalogTabular_El200TraeElDocumentoParaElPASO2(t *testing.T) {
	t.Parallel()

	api, _ := servidorDeCatalogo(t, http.StatusOK, nil, []byte(respuestaTabular))
	out, err := api.Catalog.ImportCatalogTabular(context.Background(), tokenDePrueba,
		CatalogUpload{Filename: "catalogo.csv", ContentType: "text/csv", Content: []byte(csvDePrueba)},
		CatalogModeValidate, refDePrueba)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if out.Applied {
		t.Error("un validate volvió con applied=true: mirar cambió algo")
	}
	if out.Items != 2 {
		t.Errorf("items = %d, want 2", out.Items)
	}
	if len(out.Diff.PriceChanges) != 1 || out.Diff.PriceChanges[0].NewPrice != 12.5 {
		t.Errorf("los cambios de precio llegaron mal: %+v", out.Diff.PriceChanges)
	}
	if len(out.Diff.CurrentWarnings) != 1 {
		t.Errorf("los avisos del catálogo VIGENTE se perdieron: %+v", out.Diff.CurrentWarnings)
	}
	// Y el documento se puede reenviar tal cual: es un JSON válido y completo, no un fragmento.
	const esperado = `{"format":"wapp.catalog_import","version":1,"catalog":{"categories":[]}}`
	if compacto := string(bytes.Join(bytes.Fields(out.Document), nil)); compacto != esperado {
		t.Errorf("el documento del paso 2 llegó alterado:\n%s", out.Document)
	}
}

// TestCatalogo_El400TraeLaListaENTERADeDefectos.
//
// La pantalla no enseña un motivo: enseña una lista con la que el dueño corrige su fichero, así que
// lo que se prueba es que llega COMPLETA y con sus ubicaciones. Los punteros de índice son la mitad
// del aserto: `nil` y `0` no significan lo mismo —uno es «no es de ninguna categoría», el otro es «la
// primera»— y un `int` los colapsaría sin que nada fallase.
func TestCatalogo_El400TraeLaListaENTERADeDefectos(t *testing.T) {
	t.Parallel()

	const cuerpo = `{"error":"validation_failed","errors":[
      {"row":4,"field":"precio","reason":"el precio tiene que ser un número"},
      {"category_index":1,"item_index":2,"field":"price","reason":"falta el precio"},
      {"field":"format","reason":"el documento no dice qué es"}]}`

	api, _ := servidorDeCatalogo(t, http.StatusBadRequest, nil, []byte(cuerpo))
	_, err := api.Catalog.ImportCatalog(context.Background(), tokenDePrueba,
		[]byte(documentoDePrueba), CatalogModeValidate, refDePrueba)

	invalido, ok := CatalogImportInvalidOf(err)
	if !ok {
		t.Fatalf("el 400 no llegó como rechazo por contenido: %v", err)
	}
	if len(invalido.Errors) != 3 {
		t.Fatalf("llegaron %d defectos de 3: recortar la lista deja al dueño arreglando solo los primeros",
			len(invalido.Errors))
	}
	if invalido.Errors[0].Row != 4 || invalido.Errors[0].CategoryIndex != nil {
		t.Errorf("el defecto del camino TABULAR llegó mal: %+v", invalido.Errors[0])
	}
	segundo := invalido.Errors[1]
	if segundo.CategoryIndex == nil || *segundo.CategoryIndex != 1 ||
		segundo.ItemIndex == nil || *segundo.ItemIndex != 2 {
		t.Errorf("los índices del camino JSON llegaron mal: %+v", segundo)
	}
	if invalido.Errors[2].CategoryIndex != nil || invalido.Errors[2].ItemIndex != nil {
		t.Error("un defecto de CABECERA llegó con índices: nil es «no es de ninguna categoría»")
	}
	// Y sigue siendo un 400 para quien solo quiera saber que lo rechazaron.
	if !errors.Is(err, ErrInvalidInput) {
		t.Error("el rechazo por contenido dejó de ser una entrada inválida")
	}
}

// TestCatalogo_El403EsLaFEATUREYNoUnPermiso: con el plan sin `catalog_import`, TODO lo que esta
// pantalla intente responde 403 desde el middleware, antes del handler. «Sin permiso» sería un
// diagnóstico falso: el permiso está, lo que falta es el plan.
func TestCatalogo_El403EsLaFEATUREYNoUnPermiso(t *testing.T) {
	t.Parallel()

	const cuerpo = `{"error":"feature_not_enabled","feature":"catalog_import"}`
	for _, caso := range llamadasDelCatalogo {
		if caso.nombre == "tenant-content" {
			// Esa puerta NO lleva el gate: su 403 es un permiso de verdad. Tiene su propio test.
			continue
		}
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			api, _ := servidorDeCatalogo(t, http.StatusForbidden, nil, []byte(cuerpo))
			err := caso.llamar(api)

			falta, ok := FeatureNotEnabledOf(err)
			if !ok {
				t.Fatalf("el 403 no llegó como capacidad que falta: %v", err)
			}
			if falta.Feature != "catalog_import" {
				t.Errorf("feature = %q, want catalog_import", falta.Feature)
			}
			if !errors.Is(err, ErrForbidden) {
				t.Error("dejó de ser un 403 para quien solo mira el sentinela general")
			}
		})
	}
}

// TestCatalogo_El403SinLaClaveSigueSiendoUnPermiso: un 403 que NO trae `feature_not_enabled` es lo
// que siempre fue —falta el scope content.write/content.read— y no puede convertirse en «contrata el
// plan».
func TestCatalogo_El403SinLaClaveSigueSiendoUnPermiso(t *testing.T) {
	t.Parallel()

	api, _ := servidorDeCatalogo(t, http.StatusForbidden, nil, []byte(`{"error":"forbidden"}`))
	_, err := api.Catalog.ImportCatalog(context.Background(), tokenDePrueba,
		[]byte(documentoDePrueba), CatalogModeValidate, refDePrueba)

	if _, ok := FeatureNotEnabledOf(err); ok {
		t.Error("un 403 sin la clave se leyó como capacidad que falta")
	}
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("error = %v, want ErrForbidden", err)
	}
}

// TestCatalogo_El413DelTABULARNoEsUnaPlanillaMalLlenada.
//
// 🔴 LA ASIMETRÍA REAL DEL CLOUD, y el motivo de que catalogImportError mire el STATUS antes que la
// forma del cuerpo. Los dos caminos rechazan por tamaño con 413, pero con cuerpos distintos: el JSON
// manda `max_bytes` aparte y el TABULAR lo envuelve en `validation_failed` con un defecto del campo
// «archivo» y la cifra solo dentro de la frase. Traduciendo por la forma —que es lo natural, y lo que
// hace el BFF— el tabular acabaría como lista de defectos y el dueño leería «revisa la fila del
// archivo» ante un fichero cuyo único problema es que pesa demasiado.
func TestCatalogo_El413DelTABULARNoEsUnaPlanillaMalLlenada(t *testing.T) {
	t.Parallel()

	t.Run("camino JSON: trae la cifra", func(t *testing.T) {
		t.Parallel()
		const cuerpo = `{"error":"el documento excede el tamaño máximo de 1048576 bytes","max_bytes":1048576}`
		api, _ := servidorDeCatalogo(t, http.StatusRequestEntityTooLarge, nil, []byte(cuerpo))
		_, err := api.Catalog.ImportCatalog(context.Background(), tokenDePrueba,
			[]byte(documentoDePrueba), CatalogModeApply, refDePrueba)

		grande, ok := CatalogTooLargeOf(err)
		if !ok {
			t.Fatalf("el 413 no llegó como rechazo por tamaño: %v", err)
		}
		if grande.MaxBytes != 1048576 {
			t.Errorf("max_bytes = %d, want 1048576", grande.MaxBytes)
		}
	})

	t.Run("camino TABULAR: disfrazado de validation_failed", func(t *testing.T) {
		t.Parallel()
		const cuerpo = `{"error":"validation_failed","errors":[
          {"field":"archivo","reason":"el archivo excede el tamaño máximo de 1048576 bytes"}]}`
		api, _ := servidorDeCatalogo(t, http.StatusRequestEntityTooLarge, nil, []byte(cuerpo))
		_, err := api.Catalog.ImportCatalogTabular(context.Background(), tokenDePrueba,
			CatalogUpload{Filename: "gordo.csv", ContentType: "text/csv", Content: []byte(csvDePrueba)},
			CatalogModeApply, refDePrueba)

		if _, ok := CatalogImportInvalidOf(err); ok {
			t.Fatal("el 413 tabular se leyó como planilla inválida: la pantalla diría «revisa la fila»")
		}
		grande, ok := CatalogTooLargeOf(err)
		if !ok {
			t.Fatalf("el 413 tabular no llegó como rechazo por tamaño: %v", err)
		}
		// Y aquí la cifra NO viene: por eso existe CatalogImportMaxBytesDefault.
		if grande.MaxBytes != 0 {
			t.Errorf("max_bytes = %d, y por este camino el cloud no lo publica", grande.MaxBytes)
		}
		if !errors.Is(err, ErrInvalidInput) {
			t.Error("el 413 no es un fallo del servidor: para la pantalla es lo que se subió")
		}
	})
}

// TestCatalogo_ElRestoDeLos4xxLlegaComoSentinelaEnLasCINCO: lo que no tiene cuerpo nombrado sigue
// mapeado por status, en las cinco puertas.
func TestCatalogo_ElRestoDeLos4xxLlegaComoSentinelaEnLasCINCO(t *testing.T) {
	t.Parallel()

	casos := []struct {
		status int
		quiero error
	}{
		{http.StatusUnauthorized, ErrUnauthorized},
		{http.StatusNotFound, ErrNotFound},
		{http.StatusBadRequest, ErrInvalidInput},
	}
	for _, llamada := range llamadasDelCatalogo {
		for _, caso := range casos {
			api, _ := servidorDeCatalogo(t, caso.status, nil, []byte(`{"error":"lo que sea"}`))
			if err := llamada.llamar(api); !errors.Is(err, caso.quiero) {
				t.Errorf("%s con %d dio %v, want %v", llamada.nombre, caso.status, err, caso.quiero)
			}
		}
	}
}

// TestGetCatalogTemplate_LosTRESFormatosVuelvenENTEROS.
//
// La plantilla es lo único que esta consola descarga y no es HTML, así que hay tres cosas que probar
// y las tres importan: los bytes (una plantilla a medias se llenaría y la rechazaría el import
// entero), el tipo (con el que el navegador decide qué hace con el fichero) y el NOMBRE, que sale
// parseado del `Content-Disposition` del upstream y no de un `strings.Split` a mano.
func TestGetCatalogTemplate_LosTRESFormatosVuelvenENTEROS(t *testing.T) {
	t.Parallel()

	casos := []struct {
		formato     string
		contentType string
		contenido   string
	}{
		{CatalogTemplateJSON, "application/json; charset=utf-8", `{"format":"wapp.catalog_import"}` + "\n"},
		{CatalogTemplateCSV, "text/csv; charset=utf-8", csvDePrueba},
		{CatalogTemplateXLSX,
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "PK\x03\x04 planilla"},
	}
	for _, caso := range casos {
		t.Run(caso.formato, func(t *testing.T) {
			t.Parallel()
			nombre := "catalogo-plantilla." + caso.formato
			api, pet := servidorDeCatalogo(t, http.StatusOK, map[string]string{
				"Content-Type":        caso.contentType,
				"Content-Disposition": `attachment; filename="` + nombre + `"`,
			}, []byte(caso.contenido))

			tpl, err := api.Catalog.GetCatalogTemplate(context.Background(), tokenDePrueba, caso.formato)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if string(tpl.Content) != caso.contenido {
				t.Errorf("la plantilla llegó alterada: %q", tpl.Content)
			}
			if tpl.ContentType != caso.contentType {
				t.Errorf("ContentType = %q, want %q", tpl.ContentType, caso.contentType)
			}
			if tpl.Filename != nombre {
				t.Errorf("Filename = %q, want %q (parseado del Content-Disposition)", tpl.Filename, nombre)
			}
			if pet.Query != "format="+caso.formato {
				t.Errorf("query = %q, want format=%s", pet.Query, caso.formato)
			}
			if pet.Accept != caso.contentType {
				t.Errorf("Accept = %q: se pidió JSON para una descarga que no lo es", pet.Accept)
			}
		})
	}
}

// TestGetCatalogTemplate_ElTIPOloDECIDEestaCONSOLAyNoElUPSTREAM.
//
// 🔴 ES LA MISMA REGLA QUE EL NOMBRE DEL FICHERO, elevada al tipo: lo que sale por el cable lo sirve
// ESTE origen, así que el navegador lo trata como nuestro — y quien sabe qué se pidió es esta consola,
// no el que responde. La cabecera del upstream se COMPRUEBA; jamás se reenvía.
//
// Las dos direcciones van juntas a propósito:
//   - coincide ⇒ sale el tipo de la LISTA BLANCA (no el del upstream, aunque digan casi lo mismo);
//   - no coincide, o no viene ⇒ NO se sirve la descarga.
//
// Sin el negativo, un `ContentType` copiado del upstream pasaría el positivo siempre que el cloud
// contestara bien, que es justo lo que no se puede dar por hecho para siempre.
func TestGetCatalogTemplate_ElTIPOloDECIDEestaCONSOLAyNoElUPSTREAM(t *testing.T) {
	t.Parallel()

	t.Run("coincide: manda la lista blanca y no la cabecera de enfrente", func(t *testing.T) {
		t.Parallel()
		// El upstream dice lo mismo pero SIN el charset. Es el mismo tipo, así que se sirve — y lo
		// que sale es el de la lista blanca, con su charset.
		api, _ := servidorDeCatalogo(t, http.StatusOK, map[string]string{
			"Content-Type": "text/csv",
		}, []byte(csvDePrueba))

		tpl, err := api.Catalog.GetCatalogTemplate(context.Background(), tokenDePrueba, CatalogTemplateCSV)
		if err != nil {
			t.Fatalf("un charset distinto no es otro formato: %v", err)
		}
		if tpl.ContentType != "text/csv; charset=utf-8" {
			t.Errorf("ContentType = %q: se reenvió la cabecera del upstream en vez de la lista blanca",
				tpl.ContentType)
		}
	})

	casos := []struct {
		nombre      string
		contentType string
	}{
		{"otro formato", "application/json; charset=utf-8"},
		{"una página de error", "text/html; charset=utf-8"},
		{"cabecera ilegible", "no es un tipo; ; ;"},
		{"sin cabecera", ""},
	}
	for _, caso := range casos {
		t.Run("no coincide: "+caso.nombre, func(t *testing.T) {
			t.Parallel()
			cabeceras := map[string]string{}
			if caso.contentType != "" {
				cabeceras["Content-Type"] = caso.contentType
			}
			api, _ := servidorDeCatalogo(t, http.StatusOK, cabeceras, []byte("lo que sea"))

			tpl, err := api.Catalog.GetCatalogTemplate(context.Background(), tokenDePrueba, CatalogTemplateCSV)
			if !errors.Is(err, ErrCatalogTemplateMismatch) {
				t.Fatalf("pidiendo CSV y recibiendo %q el error fue %v, want ErrCatalogTemplateMismatch",
					caso.contentType, err)
			}
			if tpl != nil {
				t.Errorf("se devolvió una plantilla igualmente: %+v", tpl)
			}
		})
	}
}

// TestGetCatalogTemplate_UnNombreQueNoEsUnNOMBRENoSeUsa.
//
// 🔴 El nombre parseado no se queda aquí: acaba en el `Content-Disposition` que ESTA consola le pone
// al navegador. Un nombre con rutas, con comillas o con un salto de línea dentro no es un nombre
// raro — es una segunda cabecera. Cuando no pasa la comprobación se usa el del formato pedido, que es
// justo el que el cloud manda hoy.
func TestGetCatalogTemplate_UnNombreQueNoEsUnNOMBRENoSeUsa(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre      string
		disposition string
	}{
		{"con ruta", `attachment; filename="../../etc/passwd"`},
		{"sin filename", `attachment`},
		{"cabecera ilegible", `no es una cabecera; ; ;`},
		{"vacía", ``},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			api, _ := servidorDeCatalogo(t, http.StatusOK, map[string]string{
				"Content-Type":        "text/csv; charset=utf-8",
				"Content-Disposition": caso.disposition,
			}, []byte(csvDePrueba))

			tpl, err := api.Catalog.GetCatalogTemplate(context.Background(), tokenDePrueba, CatalogTemplateCSV)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if tpl.Filename != "catalogo-plantilla.csv" {
				t.Errorf("Filename = %q; con una cabecera así manda el nombre del formato pedido", tpl.Filename)
			}
		})
	}
}

// TestGetCatalogTemplate_UnaPlantillaENORMEEsUnErrorYNoUnRecorte: media plantilla no se distingue de
// una entera al mirarla, así que se descargaría, se llenaría y la rechazaría el import.
func TestGetCatalogTemplate_UnaPlantillaENORMEEsUnErrorYNoUnRecorte(t *testing.T) {
	t.Parallel()

	gorda := bytes.Repeat([]byte("a"), int(maxCatalogTemplateBytes)+1)
	api, _ := servidorDeCatalogo(t, http.StatusOK, map[string]string{
		"Content-Type": "text/csv; charset=utf-8",
	}, gorda)

	if _, err := api.Catalog.GetCatalogTemplate(context.Background(), tokenDePrueba, CatalogTemplateCSV); err == nil {
		t.Fatal("una plantilla por encima del tope se entregó recortada en vez de fallar")
	}
}

// TestGetCatalogPrompt_UnPromptVACIONoEsUnExito: pintado como texto copiable, el dueño copiaría la
// nada y le echaría la culpa a su asistente.
func TestGetCatalogPrompt_UnPromptVACIONoEsUnExito(t *testing.T) {
	t.Parallel()

	t.Run("con texto", func(t *testing.T) {
		t.Parallel()
		api, _ := servidorDeCatalogo(t, http.StatusOK, nil,
			[]byte(`{"format":"wapp.catalog_import","version":1,"prompt":"Convierte esta lista…"}`))
		out, err := api.Catalog.GetCatalogPrompt(context.Background(), tokenDePrueba)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if out.Version != 1 || out.Format != "wapp.catalog_import" {
			t.Errorf("la versión del contrato llegó mal: %+v", out)
		}
	})

	t.Run("sin texto", func(t *testing.T) {
		t.Parallel()
		api, _ := servidorDeCatalogo(t, http.StatusOK, nil,
			[]byte(`{"format":"wapp.catalog_import","version":1,"prompt":""}`))
		if _, err := api.Catalog.GetCatalogPrompt(context.Background(), tokenDePrueba); err == nil {
			t.Fatal("un prompt vacío pasó por bueno")
		}
	})
}

// TestListTenantContentRefs_NoLleveGateYNuncaEsNIL.
//
// Las dos mitades del contrato de esta puerta: es la ÚNICA de las cinco sin `RequireFeature`, así que
// su 403 es un permiso de verdad y NO puede llegar como capacidad que falta; y su lista vacía es un
// arreglo, no nil, para que la pantalla no tenga que acordarse.
func TestListTenantContentRefs_NoLleveGateYNuncaEsNIL(t *testing.T) {
	t.Parallel()

	t.Run("lista", func(t *testing.T) {
		t.Parallel()
		api, _ := servidorDeCatalogo(t, http.StatusOK, nil, []byte(
			`[{"ref":"catalogo","created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-02T10:00:00Z"},
              {"ref":"menu-2026"}]`))
		refs, err := api.Catalog.ListTenantContentRefs(context.Background(), tokenDePrueba)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if len(refs) != 2 || refs[0].Ref != "catalogo" || refs[0].UpdatedAt == "" {
			t.Errorf("las refs llegaron mal: %+v", refs)
		}
	})

	t.Run("vacía", func(t *testing.T) {
		t.Parallel()
		api, _ := servidorDeCatalogo(t, http.StatusOK, nil, []byte(`null`))
		refs, err := api.Catalog.ListTenantContentRefs(context.Background(), tokenDePrueba)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if refs == nil {
			t.Error("un `null` del upstream llegó como nil en vez de como arreglo vacío")
		}
	})

	t.Run("su 403 es un PERMISO", func(t *testing.T) {
		t.Parallel()
		// Aunque el cuerpo trajera la clave de la feature —que no la trae, porque esa ruta no pasa por
		// el middleware—, esta puerta no la traduce: decirle al dueño que contrate un plan ante un
		// scope que falta lo mandaría a pagar por algo que ya tiene.
		api, _ := servidorDeCatalogo(t, http.StatusForbidden, nil,
			[]byte(`{"error":"feature_not_enabled","feature":"catalog_import"}`))
		_, err := api.Catalog.ListTenantContentRefs(context.Background(), tokenDePrueba)
		if _, ok := FeatureNotEnabledOf(err); ok {
			t.Error("el listado de refs tradujo su 403 como capacidad que falta")
		}
		if !errors.Is(err, ErrForbidden) {
			t.Errorf("error = %v, want ErrForbidden", err)
		}
	})
}

// TestCatalogUploadFilename_UnNombreQueRompeLaCABECERASeLimpia: el nombre lo elige quien sube, no
// decide nada al otro lado, y aun así acaba escrito dentro del sobre multipart.
func TestCatalogUploadFilename_UnNombreQueRompeLaCABECERASeLimpia(t *testing.T) {
	t.Parallel()

	casos := []struct{ dado, quiero string }{
		{"catalogo.csv", "catalogo.csv"},
		{`C:\Users\dueña\Escritorio\catálogo.xlsx`, "catálogo.xlsx"},
		{"/tmp/catalogo.csv", "catalogo.csv"},
		{"cata\r\nlogo.csv", "catalogo.csv"},
		{"", defaultCatalogUploadFilename},
		{"   ", defaultCatalogUploadFilename},
		{"..", defaultCatalogUploadFilename},
	}
	for _, caso := range casos {
		if got := catalogUploadFilename(caso.dado); got != caso.quiero {
			t.Errorf("catalogUploadFilename(%q) = %q, want %q", caso.dado, got, caso.quiero)
		}
	}
}

// TestMultipart_SoloLoArmaElTRANSPORT.
//
// Candado ESTRUCTURAL, y la mitad de dentro del criterio de T8.1 («el multipart se construye en el
// Transport»). La otra mitad —que el handler no lo vea— vive en internal/web, porque es sobre otro
// paquete. Se cuenta sobre el TEXTO del paquete por lo mismo que el candado de doInference: un
// segundo sitio que arme sobres compila, pasa los tests y produce una forma distinta de mandar un
// fichero, que es justo lo que se descubre cuando ya hay dos pantallas subiendo cosas.
func TestMultipart_SoloLoArmaElTRANSPORT(t *testing.T) {
	t.Parallel()

	ficheros, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("no se pudo listar el paquete: %v", err)
	}
	if len(ficheros) < 10 {
		t.Fatalf("el glob encontró %d ficheros: este test no está mirando el paquete", len(ficheros))
	}
	armadores := map[string]int{}
	for _, f := range ficheros {
		// Los tests no cuentan: ahí el multipart se LEE para comprobar lo que salió, no se arma.
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("leer %s: %v", f, err)
		}
		if n := strings.Count(string(raw), "multipart.NewWriter("); n > 0 {
			armadores[f] = n
		}
	}
	if len(armadores) != 1 || armadores["transport.go"] != 1 {
		t.Errorf("el sobre multipart se arma en %v; el único sitio permitido es transport.go", armadores)
	}
}

// TestCatalogo_EstaRegistradoConNombreEnElClient: el criterio pide campo CON NOMBRE, no embebido.
func TestCatalogo_EstaRegistradoConNombreEnElClient(t *testing.T) {
	t.Parallel()

	api := New("http://localhost:0", 5*time.Second)
	if api.Catalog == nil {
		t.Fatal("Client.Catalog es nil: New no lo construye")
	}
}

// TestCatalogo_NingunaLlamadaVaPorElClienteDeINFERENCIA.
//
// El plazo de 55s es de la única llamada que espera a que un MODELO redacte. Aquí no hay ninguno: se
// sube un fichero de 1 MiB como mucho y el cloud lo parsea, lo valida y lo diffea sin salir de su
// proceso. Regalarle esos 55s a una subida convertiría un upstream lento en una pantalla colgada casi
// un minuto. El aserto lo hace por el CABLE —el plazo del cliente general se pone en 80 ms y la
// llamada tiene que morir— porque `http.Client.Timeout` no se puede leer desde fuera del Transport.
func TestCatalogo_NingunaLlamadaVaPorElClienteDeINFERENCIA(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(400 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	api := New(srv.URL, 80*time.Millisecond)

	for _, caso := range llamadasDelCatalogo {
		if err := caso.llamar(api); err == nil {
			t.Errorf("%s sobrevivió a un plazo de 80 ms: va por el cliente de inferencia", caso.nombre)
		}
	}
}
