package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	flowDePrueba    = "44444444-4444-4444-8444-444444444444"
	triggerDePrueba = "55555555-5555-4555-8555-555555555555"
)

// definicionDePrueba es un flujo mínimo. No se valida aquí —eso lo hace la plataforma—; lo que
// importa es que sea JSON y que llegue INTACTO al otro lado.
const definicionDePrueba = `{"flow_id":"pedidos","nodes":[]}`

// llamadasDelEditor son las SEIS operaciones del plano, cada una con el verbo y la ruta que le toca
// en la API pública. La tabla se escribe una vez y la reusan todos los tests de desenlace: un método
// que se dejara fuera de aquí se quedaría sin ninguno de ellos.
//
// 🔴 Fíjate en la FIRMA: ninguna recibe una empresa. No es que no se le pase — es que no hay
// parámetro (INV-04).
var llamadasDelEditor = []struct {
	nombre string
	verbo  string
	ruta   string
	llamar func(*Client) error
}{
	{"flows.list", http.MethodGet, "/api/v1/flows", func(c *Client) error {
		_, err := c.Editor.ListFlows(context.Background(), tokenDePrueba)
		return err
	}},
	{"flows.get", http.MethodGet, "/api/v1/flows/" + flowDePrueba, func(c *Client) error {
		_, err := c.Editor.GetFlow(context.Background(), tokenDePrueba, flowDePrueba)
		return err
	}},
	{"flows.publish", http.MethodPost, "/api/v1/flows", func(c *Client) error {
		_, err := c.Editor.PublishFlow(context.Background(), tokenDePrueba, []byte(definicionDePrueba))
		return err
	}},
	{"triggers.list", http.MethodGet, "/api/v1/triggers", func(c *Client) error {
		_, err := c.Editor.ListTriggers(context.Background(), tokenDePrueba)
		return err
	}},
	{"triggers.create", http.MethodPost, "/api/v1/triggers", func(c *Client) error {
		_, err := c.Editor.CreateTrigger(context.Background(), tokenDePrueba,
			CreateTriggerRequest{Kind: "keyword", Keyword: "hola", MatchType: "exact"})
		return err
	}},
	{"triggers.delete", http.MethodDelete, "/api/v1/triggers/" + triggerDePrueba, func(c *Client) error {
		return c.Editor.DeleteTrigger(context.Background(), tokenDePrueba, triggerDePrueba)
	}},
}

// TestEditor_LasSeisVanASuVerboYSuRuta fija el contrato con la plataforma por el CABLE.
//
// Es el test que sobrevive a una refactorización: cambiar el struct de respuesta rompe la
// decodificación y se nota, pero equivocarse de ruta o de verbo compila, pasa los tipos y solo se ve
// en campo como un 404 inexplicable.
func TestEditor_LasSeisVanASuVerboYSuRuta(t *testing.T) {
	t.Parallel()

	for _, caso := range llamadasDelEditor {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			api, ultima := servidorQueResponde(t, http.StatusNoContent, "")
			_ = caso.llamar(api)

			if ultima.Method != caso.verbo {
				t.Errorf("verbo = %s, want %s", ultima.Method, caso.verbo)
			}
			if ultima.URL.Path != caso.ruta {
				t.Errorf("ruta = %s, want %s", ultima.URL.Path, caso.ruta)
			}
			if got := ultima.Header.Get("Authorization"); got != "Bearer "+tokenDePrueba {
				t.Errorf("Authorization = %q", got)
			}
			// INV-04 por el cable, en las seis: la empresa no viaja ni siquiera de contrabando en
			// la query.
			if ultima.URL.Query().Has("tenant_id") {
				t.Errorf("%s manda tenant_id en la query: %s", caso.nombre, ultima.URL.RawQuery)
			}
		})
	}
}

// TestPublishFlow_El409EsConflictoDeVersionYNoUnErrorCualquiera.
//
// 🔴 ESTE es el test de la mutación declarada (T6.1): si PublishFlow devolviera el *APIError crudo
// en vez del sentinela, la pantalla no tendría cómo distinguir «alguien publicó antes que tú» de
// «la plataforma se cayó», y caería en la rama por defecto del traductor de flash — que es
// exactamente el defecto que la Ola 5 encontró en campo (un 409 pintando «Verifica tus
// credenciales», que era falso).
//
// Los tres asertos son distintos a propósito, y ninguno sobra:
//   - el PRIMERO dice QUÉ sentinela es. Un `err != nil` a secas pasaría con cualquier error, que es
//     justo la trampa del extremo trivial;
//   - el SEGUNDO dice que lo ENVUELVE y no lo reemplaza, para que quien solo mire «hubo conflicto»
//     siga funcionando;
//   - el TERCERO es el que mata la mutación por su otro lado: un *APIError conserva el status, así
//     que StatusCodeOf devolvería 409. Si vuelve a devolver 409, es que el APIError sobrevivió.
func TestPublishFlow_El409EsConflictoDeVersionYNoUnErrorCualquiera(t *testing.T) {
	t.Parallel()
	api, _ := servidorQueResponde(t, http.StatusConflict, "")

	_, err := api.Editor.PublishFlow(context.Background(), tokenDePrueba, []byte(definicionDePrueba))
	if !errors.Is(err, ErrFlowVersionConflict) {
		t.Fatalf("err = %v, want ErrFlowVersionConflict", err)
	}
	if !errors.Is(err, ErrConflict) {
		t.Error("ErrFlowVersionConflict debe ENVOLVER a ErrConflict")
	}
	if got := StatusCodeOf(err); got != 0 {
		t.Errorf("el 409 llegó como *APIError desnudo (status %d): el handler no puede traducirlo", got)
	}
	if errors.Is(err, ErrTriggerDuplicate) {
		t.Error("el 409 de publicar se tradujo como «regla duplicada»")
	}
}

// TestCreateTrigger_El409EsDuplicadoYNoElGenerico es el gemelo por el otro plano.
//
// Sin él, quien tradujera TODOS los 409 del editor a ErrFlowVersionConflict tendría el test de
// arriba en verde y la pantalla de disparadores diciendo «ese flujo cambió mientras lo editabas»
// ante una regla repetida.
func TestCreateTrigger_El409EsDuplicadoYNoElGenerico(t *testing.T) {
	t.Parallel()
	api, _ := servidorQueResponde(t, http.StatusConflict, "")

	_, err := api.Editor.CreateTrigger(context.Background(), tokenDePrueba,
		CreateTriggerRequest{Kind: "keyword", Keyword: "hola"})
	if !errors.Is(err, ErrTriggerDuplicate) {
		t.Fatalf("err = %v, want ErrTriggerDuplicate", err)
	}
	if !errors.Is(err, ErrConflict) {
		t.Error("ErrTriggerDuplicate debe ENVOLVER a ErrConflict")
	}
	if errors.Is(err, ErrFlowVersionConflict) {
		t.Error("el 409 de crear una regla se tradujo como conflicto de versión de flujo")
	}
}

// TestCreateTrigger_El422NoEsUnaEntradaInvalidaCualquiera.
//
// 🔴 Este es el único de los tres desenlaces propios del editor que la plataforma SÍ devuelve hoy
// (Plan 054 · T2.7, D-054.8): la regla está bien formada y aun así no se puede guardar, porque un
// `fallback` sin red de `event_start` deja al contacto sin respuesta (MD-054.2).
//
// statusError mete 400 y 422 en el mismo cajón, así que sin traductor propio la dueña leería
// «revisa los datos» ante un formulario cuyos datos están todos bien — el problema no está en el
// formulario, está en el flujo. El aserto de abajo lo separa del 400 REAL, que es lo que hace que
// este test no sea una tautología.
func TestCreateTrigger_El422NoEsUnaEntradaInvalidaCualquiera(t *testing.T) {
	t.Parallel()

	api, _ := servidorQueResponde(t, http.StatusUnprocessableEntity, "")
	_, err := api.Editor.CreateTrigger(context.Background(), tokenDePrueba, CreateTriggerRequest{Kind: "fallback"})
	if !errors.Is(err, ErrTriggerWithoutEventStart) {
		t.Fatalf("422: err = %v, want ErrTriggerWithoutEventStart", err)
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Error("ErrTriggerWithoutEventStart debe ENVOLVER a ErrInvalidInput")
	}

	// Y el 400 sigue siendo un 400: si alguien tradujera los dos igual, el test de arriba seguiría
	// verde y la pantalla explicaría un JSON mal formado como un problema de red de eventos.
	api400, _ := servidorQueResponde(t, http.StatusBadRequest, "")
	_, err400 := api400.Editor.CreateTrigger(context.Background(), tokenDePrueba, CreateTriggerRequest{Kind: "keyword"})
	if !errors.Is(err400, ErrInvalidInput) {
		t.Fatalf("400: err = %v, want ErrInvalidInput", err400)
	}
	if errors.Is(err400, ErrTriggerWithoutEventStart) {
		t.Error("el 400 se tradujo como «la regla dejaría la conversación sin salida»")
	}
}

// TestEditor_ElRestoDeLos4xxLlegaComoSentinelaEnLasSeis.
//
// El criterio de T6.1 nombra 404/409/403, y el 401 va con ellos porque es el que dispara el
// reintento con token refrescado (AuthHandler.withAuthRetry): si llegara como *APIError, la sesión
// caducada expulsaría al login en vez de renovarse sola.
//
// El aserto NO es «devuelve error»: es qué sentinela, en las seis operaciones, más la comprobación
// de que no queda ningún *APIError debajo.
func TestEditor_ElRestoDeLos4xxLlegaComoSentinelaEnLasSeis(t *testing.T) {
	t.Parallel()

	codigos := []struct {
		status int
		quiero error
	}{
		{http.StatusUnauthorized, ErrUnauthorized},
		{http.StatusForbidden, ErrForbidden},
		{http.StatusNotFound, ErrNotFound},
	}

	for _, caso := range llamadasDelEditor {
		for _, codigo := range codigos {
			api, _ := servidorQueResponde(t, codigo.status, "")
			err := caso.llamar(api)
			if !errors.Is(err, codigo.quiero) {
				t.Errorf("%s con %d: err = %v, want %v", caso.nombre, codigo.status, err, codigo.quiero)
			}
			if got := StatusCodeOf(err); got != 0 {
				t.Errorf("%s: el %d llegó como *APIError desnudo", caso.nombre, codigo.status)
			}
		}
	}
}

// TestEditor_LosCodigosSinSentinelaConservanElStatus: un 500 o un 503 no cambian lo que la pantalla
// hace —los dos caen al aviso genérico—, pero el número tiene que llegar al log para poder
// distinguirlos ahí. Y ninguno puede disfrazarse de sentinela.
func TestEditor_LosCodigosSinSentinelaConservanElStatus(t *testing.T) {
	t.Parallel()

	sentinelas := []error{
		ErrUnauthorized, ErrForbidden, ErrNotFound, ErrConflict, ErrInvalidInput,
		ErrFlowVersionConflict, ErrTriggerDuplicate, ErrTriggerWithoutEventStart,
	}
	for _, caso := range llamadasDelEditor {
		for _, status := range []int{http.StatusInternalServerError, http.StatusServiceUnavailable, http.StatusBadGateway} {
			api, _ := servidorQueResponde(t, status, "")
			err := caso.llamar(api)
			if got := StatusCodeOf(err); got != status {
				t.Errorf("%s: StatusCodeOf del %d = %d", caso.nombre, status, got)
			}
			for _, s := range sentinelas {
				if errors.Is(err, s) {
					t.Errorf("%s: el %d se tradujo a %v; debía quedar como *APIError", caso.nombre, status, s)
				}
			}
		}
	}
}

// TestEditor_ElCuerpoDelUpstreamNoViajaDentroDelError.
//
// El BFF del que se muda este plano llevaba el cuerpo del no-2xx hasta la pantalla (su
// RejectionError) y lo pintaba tal cual. Aquí no: el desenlace lo deciden el sentinela y el catálogo
// de flash, y el cuerpo se drena acotado (maxErrorBody = 4 KB) sin llegar a ningún sitio.
//
// El aserto va sobre el TEXTO del error porque es lo único que un handler podría acabar pintando por
// descuido —un `%v` en una plantilla— y porque es lo que se ve en el log.
func TestEditor_ElCuerpoDelUpstreamNoViajaDentroDelError(t *testing.T) {
	t.Parallel()

	const delator = "definicion-invalida-nodo-secreto-42"
	for _, caso := range llamadasDelEditor {
		for _, status := range []int{http.StatusBadRequest, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError} {
			api, _ := servidorQueResponde(t, status, `{"error":"`+delator+`"}`)
			err := caso.llamar(api)
			if err == nil {
				t.Fatalf("%s con %d no devolvió error", caso.nombre, status)
			}
			if strings.Contains(err.Error(), delator) {
				t.Errorf("%s con %d propaga el cuerpo del upstream: %v", caso.nombre, status, err)
			}
		}
	}
}

// TestEditor_UnCuerpoEnormeNoSeLeeEntero: la otra mitad del acotado. Un upstream que respondiera
// megabytes ante un 500 no puede colgar la petición ni llenar el log; drainClose lee como mucho
// maxErrorBody y cierra.
func TestEditor_UnCuerpoEnormeNoSeLeeEntero(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		bloque := strings.Repeat("x", 4096)
		for range 64 { // 256 KB si el cliente los pidiera todos.
			if _, err := w.Write([]byte(bloque)); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)

	_, err := New(srv.URL, 5*time.Second).Editor.ListFlows(context.Background(), tokenDePrueba)
	if err == nil {
		t.Fatal("un 500 no devolvió error")
	}
	if len(err.Error()) > maxErrorBody {
		t.Errorf("el error mide %d bytes: el cuerpo del upstream se está colando", len(err.Error()))
	}
}

// TestEditor_LosListadosNuncaSonNil: la API devuelve siempre arreglo, pero un `null` mal servido
// dejaría un slice nil. La plantilla pintaría lo mismo («no hay ninguno»), así que el fallo no se
// vería hasta que alguien haga `range` sobre él en Go.
func TestEditor_LosListadosNuncaSonNil(t *testing.T) {
	t.Parallel()
	api, _ := servidorQueResponde(t, http.StatusOK, "null")

	flujos, err := api.Editor.ListFlows(context.Background(), tokenDePrueba)
	if err != nil || flujos == nil {
		t.Errorf("ListFlows con null = (%v, %v), want arreglo vacío", flujos, err)
	}
	reglas, err := api.Editor.ListTriggers(context.Background(), tokenDePrueba)
	if err != nil || reglas == nil {
		t.Errorf("ListTriggers con null = (%v, %v), want arreglo vacío", reglas, err)
	}
}

// TestPublishFlow_LaDefinicionViajaBajoDefinitionYLaEmpresaNoViaja.
//
// El contrato del endpoint es `{"definition": <el flujo entero>}`, no el flujo en la raíz del
// cuerpo: mandarlo plano da un 400 «definition es requerida» que desde la pantalla se lee como «tu
// JSON está mal», y es mentira.
//
// Y el flujo tiene que llegar INTACTO: se reenvía como json.RawMessage justo para que un campo que
// esta consola no modela no se pierda por el camino.
func TestPublishFlow_LaDefinicionViajaBajoDefinitionYLaEmpresaNoViaja(t *testing.T) {
	t.Parallel()

	var cuerpo string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		cuerpo = string(raw)
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"flow_id":"pedidos","version":7}`))
	}))
	t.Cleanup(srv.Close)

	res, err := New(srv.URL, 5*time.Second).Editor.PublishFlow(context.Background(), tokenDePrueba,
		[]byte(`{"flow_id":"pedidos","nodes":[],"campo_que_la_consola_no_modela":true}`))
	if err != nil {
		t.Fatalf("PublishFlow devolvió %v", err)
	}
	if res.FlowID != "pedidos" || res.Version != 7 {
		t.Errorf("resultado = %+v, want {pedidos 7}", res)
	}

	var enviado struct {
		Definition json.RawMessage `json:"definition"`
	}
	if err := json.Unmarshal([]byte(cuerpo), &enviado); err != nil {
		t.Fatalf("el cuerpo no es JSON: %s", cuerpo)
	}
	if len(enviado.Definition) == 0 {
		t.Fatalf("la definición no viaja bajo `definition`: %s", cuerpo)
	}
	if !strings.Contains(string(enviado.Definition), "campo_que_la_consola_no_modela") {
		t.Errorf("la definición perdió un campo por el camino: %s", enviado.Definition)
	}
	if strings.Contains(cuerpo, "tenant_id") {
		t.Errorf("el cuerpo de publicar lleva tenant_id (INV-04): %s", cuerpo)
	}
}

// TestCreateTrigger_ElCuerpoLlevaLaReglaYNoLaEmpresa, y omite lo vacío: un `event_kind: ""` enviado
// en una regla `keyword` es un campo que la plataforma no espera ahí.
func TestCreateTrigger_ElCuerpoLlevaLaReglaYNoLaEmpresa(t *testing.T) {
	t.Parallel()

	var cuerpo string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		cuerpo = string(raw)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"trigger_id":"` + triggerDePrueba + `","kind":"keyword","keyword":"hola","match_type":"exact","enabled":true}`))
	}))
	t.Cleanup(srv.Close)

	regla, err := New(srv.URL, 5*time.Second).Editor.CreateTrigger(context.Background(), tokenDePrueba,
		CreateTriggerRequest{Kind: "keyword", Keyword: "hola", MatchType: "exact", FlowID: flowDePrueba, Priority: 10})
	if err != nil {
		t.Fatalf("CreateTrigger devolvió %v", err)
	}
	if regla.TriggerID != triggerDePrueba || !regla.Enabled {
		t.Errorf("regla = %+v", regla)
	}
	if !strings.Contains(cuerpo, `"kind":"keyword"`) || !strings.Contains(cuerpo, `"keyword":"hola"`) {
		t.Errorf("el cuerpo no lleva la regla: %s", cuerpo)
	}
	if strings.Contains(cuerpo, "event_kind") {
		t.Errorf("una regla keyword mandó `event_kind` vacío: %s", cuerpo)
	}
	if strings.Contains(cuerpo, "tenant_id") {
		t.Errorf("el cuerpo lleva tenant_id (INV-04): %s", cuerpo)
	}
}

// TestGetFlow_DevuelveLaDefinicionCrudaYNoUnStruct: la consola no modela flujos —los pinta y los
// reenvía—, así que lo que sale de aquí tiene que ser el JSON tal cual. Un struct intermedio perdería
// cada nodo que el Motor añada sin que esta consola se entere.
func TestGetFlow_DevuelveLaDefinicionCrudaYNoUnStruct(t *testing.T) {
	t.Parallel()

	const definicion = `{"flow_id":"pedidos","nodes":[{"id":"n1","type":"cart"}],"nuevo_campo":42}`
	api, ultima := servidorQueResponde(t, http.StatusOK, definicion)

	raw, err := api.Editor.GetFlow(context.Background(), tokenDePrueba, flowDePrueba)
	if err != nil {
		t.Fatalf("GetFlow devolvió %v", err)
	}
	var vuelta any
	if err := json.Unmarshal(raw, &vuelta); err != nil {
		t.Fatalf("lo devuelto no es JSON: %s", raw)
	}
	if !strings.Contains(string(raw), "nuevo_campo") {
		t.Errorf("la definición perdió un campo: %s", raw)
	}
	if ultima.URL.Path != "/api/v1/flows/"+flowDePrueba {
		t.Errorf("ruta = %s", ultima.URL.Path)
	}
}

// TestEditor_LosIdentificadoresDeLaRutaSeEscapan.
//
// El `flow_id` y el `trigger_id` llegan de un formulario (un `<input type=hidden>` de la tabla). Sin
// escapar, un valor con `/` reescribiría la ruta y la petición acabaría en OTRO endpoint de la API
// pública CON EL TOKEN DEL USUARIO. El BFF los concatenaba en crudo; aquí van por pathSegment.
//
// 🔴 El aserto va sobre `RequestURI` —lo que VIAJA POR EL CABLE— y no sobre `URL.Path`, que es la
// versión ya decodificada por el servidor y daría rojo con el código correcto.
func TestEditor_LosIdentificadoresDeLaRutaSeEscapan(t *testing.T) {
	t.Parallel()

	const malicioso = "../../v1/tenants/otra-empresa"

	casos := []struct {
		nombre string
		base   string
		llamar func(*Client)
	}{
		{"flows.get", "/api/v1/flows/", func(c *Client) {
			_, _ = c.Editor.GetFlow(context.Background(), tokenDePrueba, malicioso)
		}},
		{"triggers.delete", "/api/v1/triggers/", func(c *Client) {
			_ = c.Editor.DeleteTrigger(context.Background(), tokenDePrueba, malicioso)
		}},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			api, ultima := servidorQueResponde(t, http.StatusNoContent, "")
			caso.llamar(api)

			if !strings.HasPrefix(ultima.RequestURI, caso.base) {
				t.Fatalf("la petición dejó de apuntar a su recurso: %s", ultima.RequestURI)
			}
			if got, want := strings.Count(ultima.RequestURI, "/"), strings.Count(caso.base, "/"); got != want {
				t.Errorf("el identificador añadió %d segmentos: %s", got-want, ultima.RequestURI)
			}
			if strings.Contains(ultima.RequestURI, "/tenants/") {
				t.Errorf("la ruta alcanza otro recurso de la API: %s", ultima.RequestURI)
			}
		})
	}
}

// TestEditor_EstaRegistradoConNombreEnElClient: el criterio pide campo CON NOMBRE, no embebido. Si
// alguien lo embebiera, `api.ListFlows(...)` compilaría hoy y dejaría de compilar el día que otro
// cliente traiga su propio `ListFlows` — un fallo diferido y a distancia.
func TestEditor_EstaRegistradoConNombreEnElClient(t *testing.T) {
	t.Parallel()

	api := New("http://localhost:0", 5*time.Second)
	if api.Editor == nil {
		t.Fatal("Client.Editor es nil: New no lo construye")
	}
}
