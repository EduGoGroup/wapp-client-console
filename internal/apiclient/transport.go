// Package apiclient habla con la API PÚBLICA de la plataforma (:8103 /api/v1) en nombre del
// administrador del tenant que tiene la sesión abierta en la consola.
//
// 🔴 INV-04 — el tenant NO viaja. Ni en el cuerpo, ni en la query, ni en la ruta: la empresa sale del
// Context Token que la plataforma verifica en cada llamada, y por eso los métodos de este paquete no
// aceptan un `tenantID` — no hay dónde ponerlo por error, porque no existe el parámetro. Hay un test
// que lo afirma POR EL CABLE, inspeccionando la petición saliente.
//
// 🆕 Y TIENE EXACTAMENTE UNA EXCEPCIÓN, declarada aquí para que no envejezca escondida:
// `TenantsClient.SetActive` (POST /api/v1/auth/active-tenant), que sí acepta un `tenantID` y lo manda
// en el cuerpo. Es la elección de empresa de quien pertenece a varias, y es lo que permite que el
// CANJE no tenga que aceptar ninguna (INV-8): el porqué entero está en tenants.go, y la excepción va
// con test HERMANO y aserto POSITIVO en internal/web/inv04_test.go. Una segunda excepción no se añade
// sin pasar por ahí.
//
// 🔴 No hay —ni debe haber— un cliente del plano admin (:8100). Este paquete es el del perímetro del
// CLIENTE (ver internal/config).
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"
)

// defaultTimeout acota cada llamada si quien construye el cliente no da plazo. En la consola el
// valor efectivo sale de la config (WAPP_CONSOLE_UPSTREAM_TIMEOUT_SECS); esto es solo el suelo para
// que un cliente mal construido no se cuelgue para siempre.
const defaultTimeout = 15 * time.Second

// DefaultInferenceTimeout es el plazo del cliente de INFERENCIA (ver Transport.inference).
//
// 🔴 55s, y el número no es holgura por si acaso. Medido contra UAT el 2026-08-28 desde el BFF, la
// sugerencia de cotización tardó 24,8 / 28,4 / 29,7 / 35,5 segundos con el modelo ya cargado, y el
// cloud se da a sí mismo 48s para redactar (`pipeline.PlazoPorLlamadaSuelo`), así que el techo
// realista de la respuesta es ~48-50s: 55s = eso más el viaje y el cierre. El plazo general de esta
// consola son 15s (o los 20s que pone la config), y por ahí esa llamada no cabe: muere sin
// respuesta.
//
// ⚠️ Este plazo es UNO de los tres, y solo el de este cliente HTTP. Los otros dos son del servidor de
// la consola —el deadline por petición y el write deadline— y los trajo T7.6, que le dio a esa ruta
// los suyos: 58s y 60s, en `internal/web/solicitudes_plazos.go`. Los tres tienen que quedar en ORDEN
// —cliente < petición < escritura— para que, cuando la espera se pase, corte el cliente (que devuelve
// un error traducible a pantalla) y no el servidor (que cierra la conexión sin nada que pintar), así
// que ÉSTE es el más corto de los tres y subirlo sin mover los otros dos invierte el diseño. La
// constante gemela es `config.DefaultQuoteSuggestionTimeout`, y hay un test que compara las dos.
const DefaultInferenceTimeout = 55 * time.Second

// maxErrorBody acota lo que se lee del cuerpo de un no-2xx. El detalle del upstream NO se pinta al
// usuario (el código HTTP y el catálogo de flash deciden el texto), pero sí se registra, y un
// upstream que responda megabytes no debe poder llenar el log.
const maxErrorBody = 4 << 10

// Los cinco desenlaces que las pantallas distinguen. Son SENTINELAS y no códigos sueltos porque el
// handler decide el mensaje con errors.Is: un `switch` sobre números repartido por los handlers es
// exactamente lo que se desincroniza cuando se añade una pantalla.
var (
	// ErrUnauthorized es el 401: el Context Token no vale (caducó o lo revocaron). Quien lo recibe
	// reintenta UNA vez tras refrescar (AuthHandler.withAuthRetry) y, si persiste, expulsa a /login.
	ErrUnauthorized = errors.New("apiclient: no autorizado")

	// ErrForbidden es el 403: el token es válido pero al usuario le falta el scope
	// (members.read/write, roles.read/write, entitlements.read) o no trae empresa.
	ErrForbidden = errors.New("apiclient: sin permiso")

	// ErrNotFound es el 404.
	//
	// 🔴 AQUÍ UN 404 NO SIGNIFICA «no existe». La plataforma responde 404 —y no 403— cuando el UUID
	// pertenece a OTRA empresa, y lo hace a propósito: distinguir los dos casos le diría al llamante
	// que ese identificador existe en algún sitio. Por eso el texto que ve el usuario no puede ser
	// «no encontrado» a secas; ver el catálogo de flash de internal/web.
	ErrNotFound = errors.New("apiclient: no existe en tu empresa")

	// ErrConflict es el 409 genérico: el recurso ya existe (por ejemplo, un rol con ese nombre).
	ErrConflict = errors.New("apiclient: conflicto")

	// ErrInvalidInput es el 400: la plataforma rechazó los datos (campo vacío, formato inválido).
	ErrInvalidInput = errors.New("apiclient: entrada inválida")
)

// ErrMemberOfAnotherTenant es el 409 del plano de MIEMBROS, y no es «ya está»: significa que esa
// persona pertenece a otra empresa.
//
// La guarda es de MD-055.2 y nació sin ser una regla de administración: mientras el canje no supo
// elegir empresa, una segunda membresía le ROMPÍA el login a esa persona (ErrMultipleTenants), así
// que añadirla no le daba una empresa más — se la quitaba.
//
// 🆕 Ese motivo ya no vale: el canje sabe elegir (Plan 047 · Ola 5, D-047.14) y esta consola tiene
// selector. Lo que queda del 409 es una decisión COMERCIAL —el alta de una segunda empresa la honra
// el entitlement `multi_empresa` (T5.2)—, así que el sentinela sigue existiendo y su texto sigue
// siendo el mismo: lo que cambia es que ahora significa «tu plan no lo incluye», no «le romperías
// el acceso».
//
// Envuelve a ErrConflict, así que quien solo quiera saber «hubo conflicto» sigue funcionando con
// errors.Is(err, ErrConflict); quien quiera dar el mensaje exacto pregunta por este.
var ErrMemberOfAnotherTenant = fmt.Errorf("apiclient: la persona ya pertenece a otra empresa: %w", ErrConflict)

// ErrPersonUnknown es el 404 del ALTA de un miembro, y es la ÚNICA operación de esta consola donde
// un 404 sí significa «no existe».
//
// El motivo es que el alta no pregunta por un recurso de la empresa: pregunta por una persona del
// PADRÓN. La plataforma consulta identity con su credencial M2M y, si ese UUID no está allí,
// responde 404 — sin frontera de tenant que proteger, porque no hay nada al otro lado. Y es el
// desenlace más probable de esa pantalla: quien pega un identificador se equivoca de carácter.
//
// 🔴 ENVUELVE a ErrNotFound —igual que ErrMemberOfAnotherTenant envuelve a ErrConflict—, así que
// quien solo quiera saber «hubo un 404» sigue funcionando con errors.Is(err, ErrNotFound). La
// consecuencia es que el ORDEN manda en cualquier switch que los distinga: preguntar antes por el
// genérico se come este significado y el usuario lee «no pertenece a tu empresa» ante un UUID que
// no existe en ninguna. Ver flashCodeFor en internal/web.
var ErrPersonUnknown = fmt.Errorf("apiclient: esa persona no existe en el proveedor de identidad: %w", ErrNotFound)

// APIError es un fallo con el status del upstream, para los códigos que no tienen sentinela (5xx y
// cualquier otro inesperado). StatusCodeOf lo extrae.
type APIError struct {
	Op         string
	StatusCode int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("apiclient: %s devolvió status %d", e.Op, e.StatusCode)
}

// StatusCodeOf devuelve el status HTTP del upstream si el error lo lleva (0 si no).
func StatusCodeOf(err error) int {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	return 0
}

// Transport es la base HTTP compartida por los clientes de dominio: URL base, http.Client con plazo
// y la construcción de peticiones autenticadas.
type Transport struct {
	baseURL string
	http    *http.Client

	// inference es el cliente de las llamadas que ESPERAN A UN MODELO. Hoy hay UNA sola
	// —IntakesClient.SuggestIntakeQuote— y un test estructural vigila que siga siendo una sola.
	//
	// 🔴 EXISTE PORQUE http.Client.Timeout NO SE PUEDE SOBRESCRIBIR POR PETICIÓN: es un campo del
	// cliente, no del request, y entre el plazo del cliente y el del contexto gana SIEMPRE el menor.
	// Un ctx de 58s sobre un cliente de 15s se sigue cortando a los 15s, así que el único modo de
	// darle a UNA llamada un plazo mayor sin dárselo a todas es que esa llamada use OTRO cliente.
	//
	// Comparte el RoundTripper (los dos van con Transport nil == http.DefaultTransport), así que
	// comparte el pool de conexiones con el general: lo que cambia es el plazo, no el cable.
	inference *http.Client
}

// NewTransport construye el transporte contra la API pública. Un timeout <= 0 cae a defaultTimeout:
// un cliente sin plazo es un cuelgue esperando a ocurrir.
func NewTransport(baseURL string, timeout time.Duration) *Transport {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Transport{
		baseURL:   strings.TrimRight(baseURL, "/"),
		http:      &http.Client{Timeout: timeout},
		inference: &http.Client{Timeout: DefaultInferenceTimeout},
	}
}

// statusError traduce un status no-2xx al sentinela que le corresponde.
//
// El 409 NO se traduce aquí a ErrMemberOfAnotherTenant: ese significado es del plano de miembros y
// lo pone su propio traductor (members.go). Aquí el 409 es «conflicto» a secas.
func statusError(op string, status int) error {
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("%s: %w", op, ErrUnauthorized)
	case http.StatusForbidden:
		return fmt.Errorf("%s: %w", op, ErrForbidden)
	case http.StatusNotFound:
		return fmt.Errorf("%s: %w", op, ErrNotFound)
	case http.StatusConflict:
		return fmt.Errorf("%s: %w", op, ErrConflict)
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return fmt.Errorf("%s: %w", op, ErrInvalidInput)
	default:
		return &APIError{Op: op, StatusCode: status}
	}
}

// do ejecuta la petición y devuelve la respuesta solo si es 2xx; en otro caso drena, cierra y
// traduce el status con `translate` (statusError salvo que el dominio tenga uno propio).
func (t *Transport) do(req *http.Request, op string, translate func(string, int) error) (*http.Response, error) {
	resp, err := t.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("apiclient: %s: %w", op, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status := resp.StatusCode
		drainClose(resp.Body)
		return nil, translate(op, status)
	}
	return resp, nil
}

// doTyped es `do` para los planos cuyo no-2xx trae un CUERPO que hay que leer para saber QUÉ pasó.
//
// La diferencia con `do` es una sola y es la razón de que existan los dos: `do` drena el cuerpo y le
// pasa al traductor solo el status, porque en el plano de administración el código HTTP es toda la
// información. En la bandeja NO lo es —el 400 de aprobar puede ser «faltan precios» (con la lista
// entera) o «falta el texto», y el 422 puede ser «este estado no aprueba» o «otro operador la
// movió», y a los cuatro solo los separa la clave `error` del cuerpo—, así que el traductor tiene
// que verlo.
//
// El traductor recibe la respuesta VIVA y este método la cierra después: quien traduzca lee lo que
// necesite (acotado) y no se ocupa del cierre.
func (t *Transport) doTyped(req *http.Request, op string, translate func(string, *http.Response) error) (*http.Response, error) {
	return t.doWith(t.http, req, op, translate)
}

// doInference es doTyped por el cliente de plazo largo. ÚNICO llamante permitido:
// IntakesClient.SuggestIntakeQuote (ver Transport.inference); hay un test estructural que lo cuenta.
func (t *Transport) doInference(req *http.Request, op string, translate func(string, *http.Response) error) (*http.Response, error) {
	return t.doWith(t.inference, req, op, translate)
}

// doWith es el viaje compartido por doTyped y doInference: lo único que cambia entre los dos es el
// http.Client, y por tanto el plazo.
func (t *Transport) doWith(client *http.Client, req *http.Request, op string, translate func(string, *http.Response) error) (*http.Response, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("apiclient: %s: %w", op, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer drainClose(resp.Body)
		return nil, translate(op, resp)
	}
	return resp, nil
}

// contentTypeJSON es el tipo de casi todo lo que sale de aquí. Va como constante porque desde T8.1
// hay cuerpos que NO lo son —el documento de catálogo crudo y el sobre multipart— y el literal
// repetido en tres sitios es justo lo que se desincroniza.
const contentTypeJSON = "application/json"

// newAuthedRequest arma la petición con el Context Token de la sesión, serializando `payload` a JSON.
//
// `path` ya viene con sus segmentos escapados por quien lo compone (ver pathSegment): este método no
// re-escapa nada, porque hacerlo dos veces rompe los UUID con guiones tanto como no hacerlo ninguna.
//
// 🔴 NO LE PASES UN `[]byte` PENSANDO QUE MANDAS ESOS BYTES: `json.Marshal` de un slice de bytes es
// su BASE64 entre comillas, así que el otro lado recibiría una cadena donde espera un documento. Eso
// compila, viaja y solo se ve en campo. Para un cuerpo ya serializado está newBodyRequest.
func (t *Transport) newAuthedRequest(ctx context.Context, method, path string, payload any, accessToken string) (*http.Request, error) {
	var raw []byte
	if payload != nil {
		var err error
		raw, err = json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("apiclient: serializar %s: %w", path, err)
		}
	}
	// Sin cuerpo no se marca el tipo: un GET con `Content-Type` y sin nada dentro es ruido.
	contentType := ""
	if raw != nil {
		contentType = contentTypeJSON
	}
	return t.newBodyRequest(ctx, method, path, contentType, raw, accessToken)
}

// newBodyRequest arma la petición autenticada con un cuerpo YA SERIALIZADO y el Content-Type que le
// toque. Es el único sitio del paquete donde nace un *http.Request; newAuthedRequest es este mismo
// con el cuerpo pasado antes por json.Marshal.
//
// Existe porque no todo lo que sale de aquí es un objeto JSON, y son dos casos concretos, los dos del
// import de catálogo: el documento, que viaja CRUDO —tal como lo escribió el dueño del negocio, sin
// reindentar ni reordenar campos, porque lo que se valida tiene que ser lo que él vio—, y el sobre
// multipart de la planilla, que trae su boundary en el tipo.
//
// Un `raw` nil deja la petición SIN cuerpo (no con un cuerpo vacío): es lo que necesitan los GET.
func (t *Transport) newBodyRequest(ctx context.Context, method, path, contentType string, raw []byte, accessToken string) (*http.Request, error) {
	var body io.Reader
	if raw != nil {
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("apiclient: construir petición %s: %w", path, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", contentTypeJSON)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	return req, nil
}

// filePart es UNA parte de fichero de un formulario multipart: en qué campo va, cómo se llama, qué
// dice ser y qué lleva dentro.
//
// Va como struct y no como cuatro parámetros sueltos porque tres de ellos son cadenas consecutivas:
// con nombres, cruzar el nombre del campo con el del fichero no compila; sin ellos compila, sube, y
// el otro lado contesta «no llegó ningún archivo» sin que nada apunte a por qué.
type filePart struct {
	// Field es el nombre del campo del formulario, y lo fija el CONTRATO del endpoint —nunca el
	// fichero—: la plataforma solo mira una parte y con un nombre concreto.
	Field string
	// Filename es el nombre con el que se anuncia el fichero. Es informativo: la plataforma reconoce
	// el formato por el CONTENIDO, no por la extensión.
	Filename string
	// ContentType es el tipo declarado de la parte. Vacío ⇒ no se declara ninguno.
	ContentType string
	// Content son los bytes TAL CUAL. Aquí no se recodifica, ni se reordena, ni se mira por dentro.
	Content []byte
}

// quoteEscaper protege las comillas de las cabeceras de la parte, igual que hace `mime/multipart`
// por dentro en CreateFormFile. Se pierde al armar la cabecera a mano (ver newMultipartRequest), y
// sin él un nombre de fichero con una comilla parte la cabecera en dos.
var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

// newMultipartRequest arma la petición de una SUBIDA DE FICHERO: compone el sobre multipart con UNA
// parte y devuelve el request listo, con su Content-Type y su boundary ya puestos.
//
// 🔴 EL SOBRE SE COMPONE AQUÍ Y EN NINGÚN OTRO SITIO, y esa es una regla de la casilla T8.1, no una
// preferencia: quien atiende el formulario de la pantalla no debe ver un `multipart.Writer`. Si el
// armado viviera en el handler, la forma de lo que viaja por el cable la decidiría cada pantalla que
// suba algo, y el día que haya una segunda subida habrá dos formas distintas de mandar un fichero.
// Hay un test estructural en internal/web que lo vigila: ese paquete no importa `mime/multipart`.
//
// La cabecera de la parte se escribe a mano en vez de con CreateFormFile por UNA razón concreta:
// CreateFormFile declara SIEMPRE `application/octet-stream`, y entonces el tipo del fichero que el
// dueño subió se pierde en el único sitio donde queda escrito. El tipo no decide nada al otro lado
// —la plataforma mira el contenido—, y justo por eso inventarlo sería poner una mentira donde no
// hace falta ninguna.
func (t *Transport) newMultipartRequest(ctx context.Context, method, path string, part filePart, accessToken string) (*http.Request, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`,
		quoteEscaper.Replace(part.Field), quoteEscaper.Replace(part.Filename)))
	if part.ContentType != "" {
		h.Set("Content-Type", part.ContentType)
	}
	w, err := mw.CreatePart(h)
	if err != nil {
		return nil, fmt.Errorf("apiclient: armar el formulario de %s: %w", path, err)
	}
	if _, err := w.Write(part.Content); err != nil {
		return nil, fmt.Errorf("apiclient: escribir el fichero de %s: %w", path, err)
	}
	// El cierre escribe el delimitador FINAL. Sin él no llega un fichero vacío: llega un sobre
	// truncado, que el otro lado no puede parsear y contesta como si no se hubiera subido nada.
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("apiclient: cerrar el formulario de %s: %w", path, err)
	}
	// FormDataContentType es lo que lleva el boundary, y el boundary solo lo conoce este writer.
	return t.newBodyRequest(ctx, method, path, mw.FormDataContentType(), buf.Bytes(), accessToken)
}

// decodeJSON lee el cuerpo 2xx en `out` y cierra siempre.
func decodeJSON(resp *http.Response, op string, out any) error {
	defer drainClose(resp.Body)
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("apiclient: %s: decodificar respuesta: %w", op, err)
	}
	return nil
}

// readBytes lee el cuerpo 2xx ENTERO y cierra siempre. Es el hermano de decodeJSON para lo que NO
// es JSON: hoy, la plantilla descargable del catálogo (un CSV o un XLSX).
//
// 🔴 Una respuesta más larga que `max` es un ERROR y no un recorte, y la diferencia importa: media
// plantilla no se distingue de una entera al mirarla, así que se descargaría, se llenaría y la
// rechazaría el import entero. El +1 del LimitReader es lo que permite NOTAR el exceso en vez de
// entregar justo `max` bytes creyendo que era todo.
func readBytes(resp *http.Response, op string, max int64) ([]byte, error) {
	defer drainClose(resp.Body)
	raw, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("apiclient: %s: leer respuesta: %w", op, err)
	}
	if int64(len(raw)) > max {
		return nil, fmt.Errorf("apiclient: %s: la respuesta excede %d bytes", op, max)
	}
	return raw, nil
}

// discard consume y cierra el cuerpo de una respuesta que no trae datos (204).
func discard(resp *http.Response) {
	drainClose(resp.Body)
}

// drainClose vacía y cierra el cuerpo para que la conexión vuelva al pool en vez de quedar colgada.
func drainClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxErrorBody))
	_ = body.Close()
}
