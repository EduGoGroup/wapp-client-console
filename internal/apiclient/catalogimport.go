package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

// catalogImportPath es la raíz del import de catálogo en la API pública (Plan 041 · T3.3). La
// planilla entra por `/tabular` (T3.4) y sale por el MISMO validador, el mismo diff y el mismo
// versionado: no hay dos definiciones de «catálogo correcto» que puedan separarse con el tiempo.
const catalogImportPath = "/api/v1/catalog/import"

// tenantContentPath lista las refs de contenido del tenant. Vive aquí y no en un cliente propio: ver
// CatalogImportClient.ListTenantContentRefs.
const tenantContentPath = "/api/v1/tenant-content"

// catalogTabularFormField es el campo del formulario multipart donde la plataforma espera el fichero
// (`tabularFormField` del cloud). Nombre FIJO por contrato: la plataforma no acepta «cualquier parte
// que parezca un archivo», así que equivocarlo no sube el fichero equivocado — no sube ninguno.
const catalogTabularFormField = "file"

// defaultCatalogUploadFilename nombra la parte cuando quien sube no dio nombre. La parte necesita
// uno; el resultado no depende de él (el formato se reconoce por el CONTENIDO).
const defaultCatalogUploadFilename = "catalogo"

// CatalogImportMode es la modalidad del import, y va como TIPO PROPIO y no como un `bool` ni como un
// `string` suelto por dos razones que se ven en el sitio de la llamada:
//   - un `apply bool` en la firma se lee «true» y no dice qué hace ese true, justo en la única
//     llamada de esta consola que puede reemplazar el catálogo entero de una empresa;
//   - siendo un tipo distinto de `string`, cruzarlo con el parámetro `ref` —que va justo al lado— no
//     compila. Con dos `string` compilaría y escribiría en la ref llamada «apply».
type CatalogImportMode string

// Las DOS modalidades. El cloud, si no le mandas ninguna, asume `validate`; esa red de seguridad se
// respeta pero no se usa: una petición que PUEDE escribir el catálogo del tenant no debe depender de
// un default para no escribirlo, y por eso el modo viaja siempre explícito y un modo desconocido se
// rechaza aquí en vez de dejarlo degradar en silencio.
const (
	// CatalogModeValidate enseña qué cambiaría. No escribe NADA, y la respuesta lo confirma con
	// `applied:false`.
	CatalogModeValidate CatalogImportMode = "validate"
	// CatalogModeApply escribe. Re-valida stateless (vuelve a leer, validar y diffear lo que llega en
	// ESA llamada): no hay ticket ni sesión que «confirme» un validate anterior.
	CatalogModeApply CatalogImportMode = "apply"
)

// Formatos de la plantilla descargable. Los tres son la MISMA información: el JSON es el contrato
// —y es lo que se pega en un asistente junto al prompt—, el CSV y el XLSX son la planilla canónica
// para quien prefiere una hoja de cálculo.
const (
	CatalogTemplateJSON = "json"
	CatalogTemplateCSV  = "csv"
	CatalogTemplateXLSX = "xlsx"
)

// errValidationFailed es la clave `error` con la que el cloud envuelve TODO rechazo de contenido del
// import: el documento que no valida, la planilla que no se deja leer y —ojo— también el fichero
// demasiado grande del camino tabular (ver catalogImportError).
//
// La otra clave que emite este plano es `feature_not_enabled`, y esa NO se declara aquí: es del
// middleware de entitlements y ya está en intakes.go, que es el mismo literal para el mismo cuerpo.
// Dos constantes para una clave del cloud es exactamente cómo una de las dos envejece sola.
const errValidationFailed = "validation_failed"

// maxCatalogErrorBody acota lo que se lee de un rechazo del import. Muy por encima del maxErrorBody
// general (4 KiB) por lo mismo que en la bandeja: el 400 no trae un motivo suelto sino la lista
// COMPLETA de defectos —hasta 500 artículos pueden fallar—, y recortarla dejaría al dueño del
// negocio arreglando solo los primeros y volviendo a subir para descubrir el resto.
const maxCatalogErrorBody = 64 << 10

// maxCatalogTemplateBytes acota la plantilla descargable. Es un documento de ejemplo con cuatro
// artículos, así que 4 MiB sobra; el tope existe para que un upstream que conteste cualquier cosa no
// se lleve por delante la memoria de la consola.
const maxCatalogTemplateBytes int64 = 4 << 20

// CatalogImportMaxBytesDefault es el techo POR DEFECTO que el cloud aplica al documento y al fichero
// subido (`catalogimport.DefaultMaxJSONBytes`, 1 MiB).
//
// 🔴 Es un ESPEJO y no la autoridad: quien mide y rechaza es la plataforma, y allí el número es
// configurable (`WAPP_TENANT_CONTENT_MAX_BYTES`). Vive aquí para que la pantalla pueda decir la cifra
// cuando el rechazo llega SIN ella —que es justo lo que pasa por el camino tabular, ver
// CatalogTooLargeError—, no para decidir nada.
const CatalogImportMaxBytesDefault int64 = 1 << 20

// ErrCatalogFormatUnsupported rechaza un formato de plantilla que no está en la lista blanca, y lo
// hace ANTES de gastar el viaje. No es un problema de la plataforma sino de la petición, así que
// envuelve a ErrInvalidInput: quien solo quiera saber «lo rechazaron» sigue funcionando con
// errors.Is(err, ErrInvalidInput).
var ErrCatalogFormatUnsupported = fmt.Errorf("apiclient: ese formato de plantilla no existe: %w", ErrInvalidInput)

// ErrCatalogModeUnknown rechaza un modo que no es validate ni apply, también sin gastar el viaje.
// Existe porque el cero-valor de CatalogImportMode es la cadena vacía, y con ella el cloud aplicaría
// su default —validate—: el dueño pulsaría «aplicar» y no se aplicaría nada, sin que nada fallara.
var ErrCatalogModeUnknown = fmt.Errorf("apiclient: modo de import desconocido: %w", ErrInvalidInput)

// ErrCatalogTemplateMismatch es el upstream contestando algo que NO es el formato que se le pidió.
//
// 🔴 NO ES UN SENTINELA MÁS: es la mitad que le faltaba a la regla del origen —«ninguna cabecera
// ajena decide cómo guarda el navegador un archivo servido desde este origen»—. Ver catalogMediaType.
// No envuelve a ninguno de los generales a propósito: no es un rechazo de lo que se pidió
// (ErrInvalidInput) ni una ausencia (ErrNotFound), es la plataforma respondiendo otra cosa, y quien
// lo traduzca tiene que poder decir eso y no «revisa lo que escribiste».
var ErrCatalogTemplateMismatch = errors.New("apiclient: la plantilla no llegó en el formato que se pidió")

// CatalogUpload es el fichero que el dueño del negocio eligió en su pantalla, camino del import
// tabular.
//
// 🔴 El ContentType es lo que DECLARÓ quien subió (el navegador), no lo que el fichero es. No decide
// nada: la plataforma reconoce CSV y XLSX por el contenido —la firma de un ZIP— precisamente porque
// ni la extensión ni el tipo declarado son de fiar. Viaja porque es la verdad de lo que se subió y
// porque el único sitio donde queda escrito es la parte del multipart.
type CatalogUpload struct {
	Filename    string
	ContentType string
	Content     []byte
}

// CatalogImportResult es la respuesta de las DOS puertas del import. La plataforma contesta el MISMO
// objeto en validate y en apply —`Applied` es la única diferencia semántica— para que la pantalla
// pinte el diff con un solo camino de código.
//
// ⚠️ El éxito es SIEMPRE 200, nunca 201, también cuando se escribió el catálogo.
type CatalogImportResult struct {
	Mode string `json:"mode"`
	// Ref es la ref de contenido donde vive (o viviría) el catálogo. Viene de la RESPUESTA y no se
	// fija aquí: el default es de la plataforma y copiarlo sería tener dos.
	Ref string `json:"ref"`
	// Applied dice si el catálogo se escribió de verdad. En validate es SIEMPRE false: es la garantía
	// de que mirar no cambia nada.
	Applied bool `json:"applied"`
	// Items es cuántos artículos trae el documento subido: la cifra con la que el dueño reconoce que
	// subió el fichero que quería antes de leer el diff.
	Items int         `json:"items"`
	Diff  CatalogDiff `json:"diff"`
	// ArchivedVersion es el número con el que quedó guardado el catálogo ANTERIOR. Cero cuando no se
	// archivó nada: en validate (no se escribe) y en el primer import de una ref.
	ArchivedVersion int `json:"archived_version"`
	// Document es el documento leído y NORMALIZADO (el sobre entero: format, version, source y
	// catalog). Solo lo trae el camino TABULAR: quien sube un JSON ya lo tiene.
	//
	// 🔴 Va como RawMessage y no como un struct tipado a propósito, y es lo que sostiene la
	// confirmación en dos pasos: el paso 2 reenvía ESTOS MISMOS BYTES a ImportCatalog, así que se
	// aplica exactamente lo que se enseñó en el diff. Deserializarlo aquí para volver a serializarlo
	// obligaría a esta consola a conocer el contrato del catálogo —que es dominio de la plataforma— y
	// cualquier campo nuevo se perdería en la traducción sin que nadie lo notara.
	Document json.RawMessage `json:"document,omitempty"`
}

// CatalogDiff responde a la única pregunta que el dueño se hace antes de aplicar: qué le va a pasar a
// su catálogo. Se calcula por sku en la plataforma; aquí solo se transporta.
type CatalogDiff struct {
	PriceChanges []CatalogPriceChange `json:"price_changes"`
	Added        []CatalogItemRef     `json:"added"`
	// Removed son los artículos que dejan de venderse en cuanto se aplique.
	Removed []CatalogItemRef `json:"removed"`
	// ChangedDetails son los sku a los que les cambió algo que no es el precio (variantes, tags,
	// componentes, etiqueta…). La v1 del diff dice QUÉ cambió, no qué campo.
	ChangedDetails []string `json:"changed_details"`
	Unchanged      int      `json:"unchanged"`
	// CurrentWarnings es lo que el catálogo VIGENTE ya tenía mal y el motor ignora en silencio. NO es
	// decorativo: esos artículos no están en el lado viejo de la comparación, así que no aparecen en
	// Removed aunque desaparezcan de verdad. Sin enseñarlos, un artículo con sku reservado se esfuma y
	// nada lo dice.
	CurrentWarnings []string `json:"current_warnings"`
}

// CatalogPriceChange es un artículo que cambia de precio. La etiqueta es la NUEVA: la pantalla enseña
// el catálogo que viene, no el que se va.
type CatalogPriceChange struct {
	SKU      string  `json:"sku"`
	Label    string  `json:"label"`
	OldPrice float64 `json:"old_price"`
	NewPrice float64 `json:"new_price"`
}

// CatalogItemRef identifica un artículo en las listas de altas y bajas. La etiqueta va junto al sku
// porque un sku suelto no le dice nada al dueño del negocio.
type CatalogItemRef struct {
	SKU   string `json:"sku"`
	Label string `json:"label"`
}

// CatalogImportFieldError localiza UN defecto del documento. Es la forma EXACTA de
// `catalogimport.ImportFieldError` del cloud.
//
// ⚠️ Los dos caminos rellenan campos DISTINTOS, y es del contrato, no un descuido: por el tabular
// viaja `Row` —la fila que la hoja enseña en su margen, cabecera = 1— y los índices van vacíos; por
// el JSON es al revés, porque quien sube un JSON no ha visto ninguna fila. `Row` en cero significa
// «no es de una fila concreta».
//
// `CategoryIndex`/`ItemIndex` son punteros porque sus dos ausencias no significan lo mismo: sin
// CategoryIndex el defecto es de la cabecera o del documento entero; con CategoryIndex y sin
// ItemIndex, de la categoría y no de un artículo suyo. Un `int` colapsaría las dos con la posición 0.
//
// ⚠️ `Reason` es prosa ESCRITA POR EL CLOUD, y se conserva por lo mismo que `ItemDefect.Message` de
// la bandeja: es lo único que dice qué le pasa a ESE artículo. La doctrina de la casa sigue siendo
// que el texto de la pantalla sale del catálogo de flash; esto es un dato de una lista, no el
// desenlace.
type CatalogImportFieldError struct {
	Row           int  `json:"row,omitempty"`
	CategoryIndex *int `json:"category_index,omitempty"`
	ItemIndex     *int `json:"item_index,omitempty"`
	// Field es el campo del contrato ("price", "variants[1].price") por el camino JSON, y el nombre de
	// la COLUMNA en español ("precio", "categoria") por el tabular. El valor especial "archivo" es el
	// fallo de LECTURA de la planilla: no llegó fichero, el XLSX está corrupto, no hay hoja «Catalogo».
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// CatalogImportInvalidError es el rechazo por documento o planilla inválidos: TODOS los defectos en
// una sola respuesta, porque el validador del cloud acumula en vez de cortar en el primero.
//
// Va como tipo propio y no como un sentinela pelado porque la pantalla no enseña un motivo: enseña
// una lista con la que el dueño corrige su fichero. Y cuando llega, la plataforma NO escribió nada
// —el import es todo-o-nada—, que es la condición que D-047.16 pide para repintar el formulario en
// vez de redirigir.
type CatalogImportInvalidError struct {
	Errors []CatalogImportFieldError
}

func (e *CatalogImportInvalidError) Error() string {
	return fmt.Sprintf("apiclient: el catálogo tiene %d problemas", len(e.Errors))
}

// Unwrap devuelve ErrInvalidInput: es un 400. Mismo criterio que InvalidItemsError en la bandeja.
func (e *CatalogImportInvalidError) Unwrap() error { return ErrInvalidInput }

// CatalogImportInvalidOf extrae el rechazo por contenido inválido (nil, false si no lo es).
func CatalogImportInvalidOf(err error) (*CatalogImportInvalidError, bool) {
	var invalid *CatalogImportInvalidError
	if errors.As(err, &invalid) {
		return invalid, true
	}
	return nil, false
}

// CatalogTooLargeError es el 413: el documento o el fichero no caben.
//
// 🔴 NECESITA TIPO PROPIO POR UNA ASIMETRÍA REAL DEL CLOUD, medida contra el handler. El 413 del
// camino JSON llega como `{"error":"…excede el tamaño máximo de N bytes","max_bytes":N}`; el del
// camino TABULAR llega envuelto en `{"error":"validation_failed","errors":[{"field":"archivo",…}]}`
// y SIN el campo `max_bytes` —la cifra solo está dentro de la frase—. Dos formas, un solo
// significado. Sin este tipo, y traduciendo por la FORMA del cuerpo como haría cualquiera, el
// tabular acabaría en CatalogImportInvalidError: el dueño leería «revisa la fila del archivo» ante un
// fichero que lo único que tiene es que pesa demasiado.
//
// MaxBytes es 0 cuando el cloud no publicó la cifra (o sea: siempre, por el camino tabular). Para ese
// caso está CatalogImportMaxBytesDefault, que es un espejo del default y no la autoridad.
type CatalogTooLargeError struct {
	MaxBytes int64
}

func (e *CatalogTooLargeError) Error() string {
	if e.MaxBytes > 0 {
		return fmt.Sprintf("apiclient: el catálogo excede el máximo de %d bytes", e.MaxBytes)
	}
	return "apiclient: el catálogo excede el máximo de bytes"
}

// Unwrap devuelve ErrInvalidInput: para la pantalla es un rechazo de lo que se subió, no un fallo del
// servidor. El 413 no tiene sentinela general (statusError lo dejaría como *APIError), y ese era el
// motivo de que hasta ahora un fichero demasiado grande llegara como «el servidor devolvió 413».
func (e *CatalogTooLargeError) Unwrap() error { return ErrInvalidInput }

// CatalogTooLargeOf extrae el rechazo por tamaño (nil, false si no lo es).
func CatalogTooLargeOf(err error) (*CatalogTooLargeError, bool) {
	var tooLarge *CatalogTooLargeError
	if errors.As(err, &tooLarge) {
		return tooLarge, true
	}
	return nil, false
}

// CatalogPrompt es el prompt-plantilla que el dueño del negocio pega en SU asistente junto con la
// plantilla y su lista de productos.
//
// SE PIDE, NO SE COPIA: el texto está versionado junto al contrato en la plataforma y por eso lo
// sirve un endpoint. Pegado en una plantilla HTML de este repo sería una segunda fuente que envejece
// sola y acabaría dictándole al asistente un formato que el validador ya no acepta. `Version` viaja
// para que la pantalla pueda notar que el prompt se quedó en una versión anterior.
type CatalogPrompt struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	Prompt  string `json:"prompt"`
}

// CatalogTemplate es la plantilla descargable, lista para servírsela al navegador.
type CatalogTemplate struct {
	Content     []byte
	ContentType string
	// Filename es el nombre con el que el navegador la guardará. Sale de la cabecera del upstream
	// PARSEADA, con una comprobación de por medio: ver catalogAttachmentName.
	Filename string
}

// TenantContentRef es una ref de contenido del tenant, sin el blob. Alimenta el selector del paso 1
// del import: sin él la pantalla solo podría importar a la ref fija «catalogo».
type TenantContentRef struct {
	Ref       string `json:"ref"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// catalogTemplateFormats es la lista blanca de formatos, con lo que esta consola pone de su parte si
// el upstream no lo dice o lo dice mal.
var catalogTemplateFormats = map[string]struct {
	contentType string
	filename    string
}{
	CatalogTemplateJSON: {"application/json; charset=utf-8", "catalogo-plantilla.json"},
	CatalogTemplateCSV:  {"text/csv; charset=utf-8", "catalogo-plantilla.csv"},
	CatalogTemplateXLSX: {
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"catalogo-plantilla.xlsx",
	},
}

// CatalogImportClient sirve la CARGA MASIVA del catálogo de la empresa del token: la plantilla que se
// descarga, el prompt que se pega en un asistente, el documento que se sube y la planilla que se
// sube.
//
// 🔴 Ninguno de sus métodos acepta un `tenantID`, y no es una omisión que haya que recordar: no
// existe el parámetro donde ponerlo (INV-04). La empresa sale del Context Token.
//
// ⚠️ CUATRO de sus cinco puertas exigen la feature `catalog_import` —el cloud las corta con 403 y
// `{"error":"feature_not_enabled"}` desde el middleware, antes del handler—. La quinta,
// ListTenantContentRefs, NO la exige: solo pide el scope `content.read`. Es una asimetría del cloud,
// verificada en su registro de rutas, y tiene consecuencia: un 403 en el listado de refs es un
// problema de PERMISOS de verdad, no del plan.
//
// Y esa quinta puerta vive aquí, en el cliente del import, en vez de en un TenantContentClient
// propio, por dos motivos: es lo único que esta consola consume de ese plano —existe solo para llenar
// el selector de ref del paso 1—, y un cliente entero de tenant-content sería una invitación
// permanente a añadirle el PUT y el DELETE del blob crudo, que es escritura directa sobre el
// contenido del tenant sin pasar por ninguna validación de catálogo.
type CatalogImportClient struct {
	t *Transport
}

// NewCatalogImportClient construye el cliente del import de catálogo.
func NewCatalogImportClient(t *Transport) *CatalogImportClient { return &CatalogImportClient{t: t} }

// ImportCatalog manda el documento a POST /api/v1/catalog/import y devuelve lo que pasaría (con
// CatalogModeValidate) o lo que pasó (con CatalogModeApply) con el catálogo de la empresa.
//
// 🔴 EL DOCUMENTO VIAJA CRUDO Y SIN TOCAR: no se reserializa, no se reindenta y no se le quitan
// campos. Lo que el dueño vio en pantalla es exactamente lo que la plataforma valida, y es también lo
// que hace fiel el paso 2 cuando lo que se reenvía es el `Document` que devolvió el camino tabular.
// Por eso NO pasa por newAuthedRequest —que lo mandaría en base64— sino por newBodyRequest.
//
// `ref` vacía deja mandar al default de la plataforma; con valor va explícita, y el paso 2 tiene que
// aplicar sobre la MISMA ref contra la que se calculó el diff del paso 1 (la ref no viaja dentro del
// documento: el documento es portátil por contrato).
//
// Errores: *CatalogImportInvalidError (400 con la lista entera), *CatalogTooLargeError (413),
// *FeatureNotEnabledError (403 `catalog_import`), ErrCatalogModeUnknown y los sentinelas generales.
func (c *CatalogImportClient) ImportCatalog(ctx context.Context, accessToken string,
	document []byte, mode CatalogImportMode, ref string) (*CatalogImportResult, error) {
	const op = "catalog.import"
	query, err := catalogImportQuery(mode, ref)
	if err != nil {
		return nil, err
	}
	// Un documento vacío lo rechazaría el cloud con un 400 que no dice gran cosa; dicho aquí, el
	// fallo apunta a quien armó la llamada y no gasta el viaje.
	if len(document) == 0 {
		return nil, fmt.Errorf("apiclient: %s: el documento está vacío: %w", op, ErrInvalidInput)
	}

	req, err := c.t.newBodyRequest(ctx, http.MethodPost, catalogImportPath+query,
		contentTypeJSON, document, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.doTyped(req, op, catalogImportError)
	if err != nil {
		return nil, err
	}
	var out CatalogImportResult
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ImportCatalogTabular sube la planilla (CSV o XLSX) a POST /api/v1/catalog/import/tabular.
//
// Es el MISMO import por otra puerta: mismo validador, mismo diff, mismo versionado y la misma
// respuesta, con `Document` —la planilla ya traducida al JSON del contrato— como único añadido. Lo
// que cambia es DÓNDE queda el defecto: los errores salen ubicados por FILA, que es lo que tiene
// delante quien llenó la hoja.
//
// El fichero va SIN TOCAR: ni se reordena, ni se recodifica, ni se mira por dentro. El formato NO se
// declara —la plataforma lo reconoce por el contenido—, así que el nombre viaja solo porque la parte
// del multipart lo pide y no decide nada.
//
// 🔴 El sobre multipart lo arma el Transport (newMultipartRequest), no este método y mucho menos el
// handler de la pantalla: ver el porqué allí.
//
// Errores: los mismos que ImportCatalog. Ojo con el 413: por esta puerta llega disfrazado de
// `validation_failed` y aun así sale como *CatalogTooLargeError (ver catalogImportError).
func (c *CatalogImportClient) ImportCatalogTabular(ctx context.Context, accessToken string,
	file CatalogUpload, mode CatalogImportMode, ref string) (*CatalogImportResult, error) {
	const op = "catalog.import.tabular"
	query, err := catalogImportQuery(mode, ref)
	if err != nil {
		return nil, err
	}
	// Un fichero de cero bytes no es una planilla vacía que la plataforma vaya a explicar bien: es una
	// subida que no llegó a ocurrir (el `input file` sin elegir nada, la lectura que falló antes).
	if len(file.Content) == 0 {
		return nil, fmt.Errorf("apiclient: %s: el archivo está vacío: %w", op, ErrInvalidInput)
	}

	req, err := c.t.newMultipartRequest(ctx, http.MethodPost, catalogImportPath+"/tabular"+query,
		filePart{
			Field:       catalogTabularFormField,
			Filename:    catalogUploadFilename(file.Filename),
			ContentType: file.ContentType,
			Content:     file.Content,
		}, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.doTyped(req, op, catalogImportError)
	if err != nil {
		return nil, err
	}
	var out CatalogImportResult
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetCatalogTemplate descarga la plantilla de ejemplo de GET /api/v1/catalog/import/template.
//
// LA SIRVE EL BACKEND, y ese es el punto entero: sale de los MISMOS structs del contrato, así que no
// puede describir un contrato que el servidor ya no acepta. Una plantilla estática en esta consola se
// quedaría vieja el día que suba la versión del contrato y repartiría documentos que el import
// rechaza en bloque.
//
// El formato se valida contra la lista blanca ANTES de gastar el viaje, y de ahí sale también con qué
// se responde si el upstream no dice nada útil.
func (c *CatalogImportClient) GetCatalogTemplate(ctx context.Context,
	accessToken, format string) (*CatalogTemplate, error) {
	const op = "catalog.template"
	esperado, ok := catalogTemplateFormats[format]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrCatalogFormatUnsupported, format)
	}

	req, err := c.t.newAuthedRequest(ctx, http.MethodGet,
		catalogImportPath+"/template?"+url.Values{"format": {format}}.Encode(), nil, accessToken)
	if err != nil {
		return nil, err
	}
	// Es la única llamada de esta consola cuyo 200 NO es JSON en dos de sus tres formatos. El cloud no
	// mira el Accept —decide por el `format` del query—, pero pedir JSON y esperar un XLSX sería una
	// mentira gratis en la única cabecera que dice qué se espera.
	req.Header.Set("Accept", esperado.contentType)

	resp, err := c.t.doTyped(req, op, catalogImportError)
	if err != nil {
		return nil, err
	}
	// La comprobación va ANTES de leer el cuerpo: si lo que hay al otro lado no es lo que se pidió, no
	// hay ninguna razón para traerse sus megabytes primero. El cuerpo se drena igual para que la
	// conexión vuelva al pool en vez de quedar colgada.
	tipo, err := catalogMediaType(resp.Header.Get("Content-Type"), esperado.contentType)
	if err != nil {
		drainClose(resp.Body)
		return nil, fmt.Errorf("apiclient: %s: %w", op, err)
	}
	content, err := readBytes(resp, op, maxCatalogTemplateBytes)
	if err != nil {
		return nil, err
	}
	return &CatalogTemplate{
		Content:     content,
		ContentType: tipo,
		Filename:    catalogAttachmentName(resp.Header.Get("Content-Disposition"), esperado.filename),
	}, nil
}

// GetCatalogPrompt pide el prompt-plantilla a GET /api/v1/catalog/import/prompt.
//
// Cuelga de la misma capacidad que el import, así que un 403 aquí significa lo mismo que allí.
func (c *CatalogImportClient) GetCatalogPrompt(ctx context.Context, accessToken string) (*CatalogPrompt, error) {
	const op = "catalog.prompt"
	req, err := c.t.newAuthedRequest(ctx, http.MethodGet, catalogImportPath+"/prompt", nil, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.doTyped(req, op, catalogImportError)
	if err != nil {
		return nil, err
	}
	var out CatalogPrompt
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	if out.Prompt == "" {
		// Un prompt vacío pintado como texto copiable es peor que decir que no se pudo cargar: el
		// dueño copiaría la nada y le echaría la culpa a su asistente.
		return nil, fmt.Errorf("apiclient: %s: respuesta sin texto", op)
	}
	return &out, nil
}

// ListTenantContentRefs lista las refs de contenido de la empresa vía GET /api/v1/tenant-content, sin
// los blobs. Es lo que llena el selector de ref del paso 1 del import.
//
// 🔴 Es la ÚNICA de las cinco que NO va gateada por `catalog_import` (el cloud no le pone
// RequireFeature, solo el scope `content.read`), así que aquí no puede llegar un
// *FeatureNotEnabledError: un 403 por esta puerta es un permiso que falta de verdad. Por eso traduce
// con el genérico de la casa y no con catalogImportError.
func (c *CatalogImportClient) ListTenantContentRefs(ctx context.Context, accessToken string) ([]TenantContentRef, error) {
	const op = "catalog.tenant-content"
	req, err := c.t.newAuthedRequest(ctx, http.MethodGet, tenantContentPath, nil, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.do(req, op, statusError)
	if err != nil {
		return nil, err
	}
	var out []TenantContentRef
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	if out == nil {
		// `[]` y `null` los distingue quien mire el resultado, y para la pantalla significan lo mismo:
		// no hay refs. Devolver nil obligaría a cada llamante a acordarse.
		out = []TenantContentRef{}
	}
	return out, nil
}

// catalogImportQuery arma el query de las dos puertas del import: el modo SIEMPRE explícito y la ref
// solo cuando el llamante la fija.
func catalogImportQuery(mode CatalogImportMode, ref string) (string, error) {
	if mode != CatalogModeValidate && mode != CatalogModeApply {
		return "", fmt.Errorf("%w: %q", ErrCatalogModeUnknown, mode)
	}
	q := url.Values{}
	q.Set("mode", string(mode))
	if ref != "" {
		q.Set("ref", ref)
	}
	return "?" + q.Encode(), nil
}

// catalogUploadFilename deja el nombre de la parte en algo que se pueda escribir en una cabecera.
//
// El nombre llega de quien sube —o sea, del navegador— y NO decide nada al otro lado, pero acaba
// dentro de una cabecera del multipart. Se queda con el último segmento (un nombre con rutas no
// aporta), tira los caracteres de control —una cabecera con un salto de línea dentro no es un nombre
// raro, es otra cabecera— y acota el largo. Sin nada utilizable, el nombre por defecto.
func catalogUploadFilename(nombre string) string {
	if i := strings.LastIndexAny(nombre, `/\`); i >= 0 {
		nombre = nombre[i+1:]
	}
	nombre = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, nombre)
	nombre = strings.TrimSpace(nombre)
	if nombre == "" || nombre == "." || nombre == ".." {
		return defaultCatalogUploadFilename
	}
	if len(nombre) > 100 {
		nombre = nombre[:100]
	}
	return nombre
}

// catalogMediaType decide con qué Content-Type sale la descarga, y la respuesta es SIEMPRE la misma:
// el de la LISTA BLANCA, o sea el del formato que esta consola pidió. Lo que hace con la cabecera del
// upstream es COMPROBARLA, nunca reenviarla; si no coincide con lo pedido, devuelve
// ErrCatalogTemplateMismatch y la descarga no llega a servirse.
//
// 🔴 EL PORQUÉ, Y NO ES UNA PREFERENCIA DE ESTILO. La regla del origen —«ninguna cabecera ajena
// decide cómo guarda el navegador un archivo servido desde este origen»— NO depende de que el
// upstream sea de fiar hoy: depende de que el navegador trata ese fichero como venido de NUESTRO
// origen. Recanonizar con FormatMediaType evita la inyección de cabeceras, pero eso no es lo que la
// regla protege; la regla protege QUIÉN DECIDE. Y quien sabe qué se pidió es esta consola, no el que
// responde. Es exactamente el mismo criterio que ya se le aplica al nombre del fichero
// (catalogAttachmentName), que también se comprueba en vez de creerse.
//
// 🔑 SE COMPARA EL TIPO PELADO Y NO LA CABECERA ENTERA: el `charset` es un parámetro y puede diferir
// sin que cambie QUÉ se sirvió, que es la única pregunta que esta comprobación hace. `ParseMediaType`
// ya devuelve el tipo en minúsculas, así que la comparación no necesita normalizar nada más.
//
// 🔴 Y UNA CABECERA AUSENTE O ILEGIBLE CUENTA COMO NO COINCIDIR, en vez de dejarla pasar con el tipo
// bueno puesto por nosotros. La diferencia importa: sin cabecera no hay confirmación de que lo que
// viene sea la plantilla, y servir bytes desconocidos ETIQUETADOS como `text/csv` es justo el
// desenlace que esto evita. No es un riesgo teórico contra el cloud de hoy —siempre la manda, junto
// con el Content-Length—, así que la estrictez no cuesta ningún caso real.
func catalogMediaType(raw, esperado string) (string, error) {
	tipo, _, err := mime.ParseMediaType(raw)
	if err != nil || tipo == "" {
		return "", fmt.Errorf("%w: el upstream no dijo qué servía (%q)", ErrCatalogTemplateMismatch, raw)
	}
	// El esperado es una constante de la lista blanca y siempre se deja parsear; si algún día no, es
	// un fallo de la lista y no del upstream, y sale por el mismo sitio antes de servir nada.
	quiere, _, err := mime.ParseMediaType(esperado)
	if err != nil {
		return "", fmt.Errorf("%w: la lista blanca trae un tipo ilegible (%q)", ErrCatalogTemplateMismatch, esperado)
	}
	if tipo != quiere {
		return "", fmt.Errorf("%w: se pidió %q y llegó %q", ErrCatalogTemplateMismatch, quiere, tipo)
	}
	// Coincide ⇒ sale el de la lista blanca, no el del upstream. Con `charset` distinto los dos son
	// «lo mismo» para esta comprobación, y el que vale es el nuestro.
	return esperado, nil
}

// catalogAttachmentName saca el nombre del fichero del `Content-Disposition` del upstream.
//
// Se parsea con mime.ParseMediaType y no partiendo la cadena a mano porque esa cabecera tiene
// gramática —comillas, parámetros, la forma extendida `filename*` de la RFC 2231— y un `strings.Split`
// por comillas acierta hasta el primer nombre con un punto y coma dentro.
//
// 🔴 Y ADEMÁS SE COMPRUEBA, porque este valor no se queda aquí: acaba en el `Content-Disposition` que
// esta consola le pone al navegador (T8.2). Un nombre con rutas, con comillas o con caracteres de
// control convertiría una cabecera nuestra en dos. Cuando no pasa la comprobación —o cuando no viene—
// se usa el del formato pedido, que es exactamente el que el cloud manda hoy.
func catalogAttachmentName(raw, esperado string) string {
	_, params, err := mime.ParseMediaType(raw)
	if err != nil {
		return esperado
	}
	nombre := params["filename"]
	if !nombreDeDescargaSeguro(nombre) {
		return esperado
	}
	return nombre
}

// nombreDeDescargaSeguro responde si un nombre se puede escribir tal cual en una cabecera y guardar
// como fichero: un nombre a secas, sin rutas, sin comillas, sin caracteres de control y de un largo
// razonable.
func nombreDeDescargaSeguro(nombre string) bool {
	if nombre == "" || nombre == "." || nombre == ".." {
		return false
	}
	if len(nombre) > 100 || !utf8.ValidString(nombre) {
		return false
	}
	if strings.ContainsAny(nombre, "/\\\"") {
		return false
	}
	for _, r := range nombre {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// catalogErrorBody es todo lo que las puertas del import pueden traer en un no-2xx. Se decodifica UNA
// vez y con él decide el traductor.
type catalogErrorBody struct {
	Error string `json:"error"`
	// validation_failed
	Errors []CatalogImportFieldError `json:"errors"`
	// feature_not_enabled
	Feature string `json:"feature"`
	// el 413 del camino JSON — y SOLO el de ese camino
	MaxBytes int64 `json:"max_bytes"`
}

// readCatalogError lee el cuerpo de un no-2xx acotado. Un cuerpo ilegible deja todo en blanco: el
// status sigue siendo la información principal y el llamante tiene su sentinela genérico.
func readCatalogError(resp *http.Response) catalogErrorBody {
	var body catalogErrorBody
	_ = json.NewDecoder(io.LimitReader(resp.Body, maxCatalogErrorBody)).Decode(&body)
	return body
}

// catalogImportError traduce un no-2xx de las cuatro puertas del import.
//
// 🔴 SE DECIDE POR EL STATUS Y LUEGO POR LA CLAVE, Y ESE ORDEN ES EL CONTRATO. Lo natural sería mirar
// primero la FORMA del cuerpo —«si trae lista de defectos, es un documento inválido»—, y con este
// cloud eso está mal: el 413 del camino TABULAR viene envuelto en `validation_failed` con un defecto
// del campo «archivo», así que por la forma es indistinguible de una planilla mal llenada. Mirando
// antes el 413, las dos formas del mismo significado acaban en el mismo sitio.
func catalogImportError(op string, resp *http.Response) error {
	body := readCatalogError(resp)
	switch resp.StatusCode {
	case http.StatusRequestEntityTooLarge:
		return &CatalogTooLargeError{MaxBytes: body.MaxBytes}
	case http.StatusForbidden:
		if body.Error == errFeatureNotEnabled {
			return &FeatureNotEnabledError{Feature: body.Feature}
		}
	case http.StatusBadRequest:
		// La clave Y la forma, como en la bandeja: la clave es lo que el cloud publica hoy y la lista
		// es lo que hace útil el rechazo. Aceptar las dos no cuesta nada y evita que un servidor de
		// otra versión convierta la lista de defectos en un «revisa los datos» genérico.
		if body.Error == errValidationFailed || len(body.Errors) > 0 {
			return &CatalogImportInvalidError{Errors: body.Errors}
		}
	}
	return statusError(op, resp.StatusCode)
}
