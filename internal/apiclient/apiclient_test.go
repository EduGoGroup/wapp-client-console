package apiclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	tokenDePrueba = "context-token-de-prueba"
	userDePrueba  = "22222222-2222-4222-8222-222222222222"
	roleDePrueba  = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

// servidorQueResponde levanta un upstream que contesta siempre lo mismo y guarda la última petición.
func servidorQueResponde(t *testing.T, status int, body string) (*Client, *http.Request) {
	t.Helper()
	var ultima http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ultima = *r
		w.WriteHeader(status)
		if body != "" {
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, 5*time.Second), &ultima
}

// TestStatusError_CadaCodigoTieneSuSentinela: los handlers deciden el mensaje con errors.Is, así que
// un status que se quedara sin traducir caería al genérico y el usuario leería «inténtalo de nuevo»
// ante un problema de permisos.
func TestStatusError_CadaCodigoTieneSuSentinela(t *testing.T) {
	t.Parallel()

	casos := []struct {
		status int
		quiero error
	}{
		{http.StatusUnauthorized, ErrUnauthorized},
		{http.StatusForbidden, ErrForbidden},
		{http.StatusNotFound, ErrNotFound},
		{http.StatusConflict, ErrConflict},
		{http.StatusBadRequest, ErrInvalidInput},
		{http.StatusUnprocessableEntity, ErrInvalidInput},
	}
	for _, caso := range casos {
		api, _ := servidorQueResponde(t, caso.status, "")
		_, err := api.Roles.List(context.Background(), tokenDePrueba)
		if !errors.Is(err, caso.quiero) {
			t.Errorf("status %d dio %v, want %v", caso.status, err, caso.quiero)
		}
	}
}

// TestStatusError_LosCodigosSinSentinelaConservanElStatus: un 500 o un 503 no tienen sentinela porque
// no cambian lo que la pantalla hace, pero el número tiene que llegar al log.
func TestStatusError_LosCodigosSinSentinelaConservanElStatus(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusInternalServerError, http.StatusServiceUnavailable, http.StatusTeapot} {
		api, _ := servidorQueResponde(t, status, "")
		_, err := api.Roles.List(context.Background(), tokenDePrueba)
		if got := StatusCodeOf(err); got != status {
			t.Errorf("StatusCodeOf del %d = %d", status, got)
		}
		for _, sentinela := range []error{ErrUnauthorized, ErrForbidden, ErrNotFound, ErrConflict, ErrInvalidInput} {
			if errors.Is(err, sentinela) {
				t.Errorf("el %d se tradujo a %v; debía quedar como *APIError", status, sentinela)
			}
		}
	}
}

// TestMembers_El409TieneSuPropioSignificado (MD-055.2).
//
// En el plano de MIEMBROS un 409 no es «ya existe»: es «esa persona ya pertenece a otra empresa», y
// una segunda membresía le rompería el canje del token. Este caso lo afirma sobre la BAJA, que pasa
// por el mismo writeDomainError del servidor; el alta —que es quien más lo dispara— tiene su propio
// traductor y su propio caso más abajo.
func TestMembers_El409TieneSuPropioSignificado(t *testing.T) {
	t.Parallel()
	api, _ := servidorQueResponde(t, http.StatusConflict, "")

	err := api.Members.Remove(context.Background(), tokenDePrueba, userDePrueba)
	if !errors.Is(err, ErrMemberOfAnotherTenant) {
		t.Fatalf("err = %v, want ErrMemberOfAnotherTenant", err)
	}
	// Y sigue siendo un conflicto para quien solo mire eso: lo ENVUELVE, no lo reemplaza.
	if !errors.Is(err, ErrConflict) {
		t.Error("ErrMemberOfAnotherTenant debe envolver a ErrConflict")
	}
}

// TestMembersAdd_El404EsQueLaPersonaNoExisteYNoUnaFronteraDeEmpresa.
//
// 🔴 Es la EXCEPCIÓN del repo. En todas las demás operaciones un 404 significa «ese identificador es
// de otra empresa» y la consola tiene que conservar la ambigüedad. En el alta la plataforma está
// preguntando por el PADRÓN de identity, no por un recurso del tenant: si el UUID no está allí, no
// hay nada al otro lado que proteger. Traducirlo al sentinela genérico haría que el fallo más
// frecuente de la pantalla —un UUID mal pegado— se explicara como un problema de permisos.
func TestMembersAdd_El404EsQueLaPersonaNoExisteYNoUnaFronteraDeEmpresa(t *testing.T) {
	t.Parallel()
	api, _ := servidorQueResponde(t, http.StatusNotFound, `{"error":"usuario no encontrado"}`)

	err := api.Members.Add(context.Background(), tokenDePrueba, userDePrueba)
	if !errors.Is(err, ErrPersonUnknown) {
		t.Fatalf("err = %v, want ErrPersonUnknown", err)
	}
	// Y sigue siendo un 404 para quien solo mire eso: lo ENVUELVE, no lo reemplaza. De ahí que el
	// orden de las ramas mande en quien los distinga (ver flashCodeFor).
	if !errors.Is(err, ErrNotFound) {
		t.Error("ErrPersonUnknown debe envolver a ErrNotFound")
	}
}

// TestMembersRemove_SuS404SigueSiendoFronteraDeEmpresa es el gemelo del caso de arriba, y sin él
// aquello no probaría nada: si alguien tradujera TODOS los 404 del plano a ErrPersonUnknown, el
// test del alta seguiría verde y la baja empezaría a decirle al usuario que la persona no existe.
func TestMembersRemove_SuS404SigueSiendoFronteraDeEmpresa(t *testing.T) {
	t.Parallel()
	api, _ := servidorQueResponde(t, http.StatusNotFound, "")

	err := api.Members.Remove(context.Background(), tokenDePrueba, userDePrueba)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if errors.Is(err, ErrPersonUnknown) {
		t.Error("el 404 de la baja se tradujo como «esa persona no existe»: ahí es frontera de empresa")
	}
}

// TestMembersAdd_El409EsPersonaDeOtraEmpresa (MD-055.2): el alta es quien de verdad lo dispara.
func TestMembersAdd_El409EsPersonaDeOtraEmpresa(t *testing.T) {
	t.Parallel()
	api, _ := servidorQueResponde(t, http.StatusConflict, "")

	if err := api.Members.Add(context.Background(), tokenDePrueba, userDePrueba); !errors.Is(err, ErrMemberOfAnotherTenant) {
		t.Fatalf("err = %v, want ErrMemberOfAnotherTenant", err)
	}
}

// TestMembersAdd_LosCodigosDeIdentityNoTienenSentinelaPeroConservanElStatus.
//
// El alta es la única operación de esta consola que puede recibir 502 y 503: la plataforma consulta
// identity con su credencial M2M —502 si no acredita a la persona en `wapp.bff`, 503 si identity no
// responde o no hay cliente M2M—. Ninguno cambia lo que la pantalla hace (los dos caen al aviso
// genérico), pero el número tiene que llegar al log para poder distinguirlos ahí.
func TestMembersAdd_LosCodigosDeIdentityNoTienenSentinelaPeroConservanElStatus(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusInternalServerError} {
		api, _ := servidorQueResponde(t, status, `{"error":"identity_no_configurado"}`)
		err := api.Members.Add(context.Background(), tokenDePrueba, userDePrueba)
		if got := StatusCodeOf(err); got != status {
			t.Errorf("StatusCodeOf del %d = %d", status, got)
		}
		for _, sentinela := range []error{ErrPersonUnknown, ErrNotFound, ErrMemberOfAnotherTenant, ErrConflict} {
			if errors.Is(err, sentinela) {
				t.Errorf("el %d se tradujo a %v; debía quedar como *APIError", status, sentinela)
			}
		}
	}
}

// TestMembersAdd_LaPersonaViajaEnElCuerpoYLaEmpresaNoViaja: el contrato del endpoint es
// `{"user_id":"…"}` sobre POST /api/v1/members, y el tenant NO va (INV-04) — sale del token.
func TestMembersAdd_LaPersonaViajaEnElCuerpoYLaEmpresaNoViaja(t *testing.T) {
	t.Parallel()

	var cuerpo string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		cuerpo = string(raw)
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/members" {
			t.Errorf("el alta fue a %s %s, want POST /api/v1/members", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	if err := New(srv.URL, 5*time.Second).Members.Add(context.Background(), tokenDePrueba, userDePrueba); err != nil {
		t.Fatalf("Add devolvió %v", err)
	}
	if !strings.Contains(cuerpo, `"user_id":"`+userDePrueba+`"`) {
		t.Errorf("el cuerpo no lleva la persona: %s", cuerpo)
	}
	if strings.Contains(cuerpo, "tenant_id") {
		t.Errorf("el cuerpo del alta lleva tenant_id (INV-04): %s", cuerpo)
	}
}

// TestRoles_El409SigueSiendoGenerico: la traducción específica es del plano de miembros y NO debe
// contaminar al de roles, donde un 409 sí es un nombre repetido.
func TestRoles_El409SigueSiendoGenerico(t *testing.T) {
	t.Parallel()
	api, _ := servidorQueResponde(t, http.StatusConflict, "")

	_, err := api.Roles.Create(context.Background(), tokenDePrueba, "Cajera", "")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if errors.Is(err, ErrMemberOfAnotherTenant) {
		t.Error("un 409 al crear un rol se tradujo como «la persona pertenece a otra empresa»")
	}
}

// TestListas_UnaRespuestaVaciaEsUnArregloYNoNil: la API devuelve siempre arreglo, pero un `null` mal
// servido dejaría un slice nil que la plantilla pintaría como «no hay nadie». Se normaliza aquí.
func TestListas_UnaRespuestaVaciaEsUnArregloYNoNil(t *testing.T) {
	t.Parallel()
	api, _ := servidorQueResponde(t, http.StatusOK, "null")

	miembros, err := api.Members.List(context.Background(), tokenDePrueba)
	if err != nil || miembros == nil {
		t.Errorf("Members.List con null = (%v, %v), want arreglo vacío", miembros, err)
	}
	roles, err := api.Roles.List(context.Background(), tokenDePrueba)
	if err != nil || roles == nil {
		t.Errorf("Roles.List con null = (%v, %v), want arreglo vacío", roles, err)
	}
}

// TestRutas_LosIdentificadoresDelFormularioSeEscapan.
//
// El `user_id` y el `role_id` llegan de un formulario. Sin escapar, un valor con `/` reescribiría la
// ruta y la petición acabaría en OTRO endpoint de la API pública CON EL TOKEN DEL USUARIO.
//
// 🔴 El aserto va sobre `RequestURI`, que es lo que VIAJA POR EL CABLE, y no sobre `r.URL.Path`, que
// es la versión ya DECODIFICADA por el servidor: `Path` enseña `/../../v1/tenants/…` aunque el
// escapado haya funcionado perfectamente, porque decodificar es precisamente lo que hace. Un aserto
// sobre `Path` daría rojo con el código correcto — y, peor, daría verde si alguien "lo arreglara"
// quitando el escapado y limpiando la cadena a mano.
func TestRutas_LosIdentificadoresDelFormularioSeEscapan(t *testing.T) {
	t.Parallel()
	api, ultima := servidorQueResponde(t, http.StatusNoContent, "")

	malicioso := "../../v1/tenants/otra-empresa"
	_ = api.Members.Remove(context.Background(), tokenDePrueba, malicioso)

	const base = "/api/v1/members/"
	if !strings.HasPrefix(ultima.RequestURI, base) {
		t.Fatalf("la petición dejó de apuntar al recurso de miembros: %s", ultima.RequestURI)
	}
	// La prueba de que el valor sigue siendo UN solo segmento: no añadió ni una barra.
	if got, want := strings.Count(ultima.RequestURI, "/"), strings.Count(base, "/"); got != want {
		t.Errorf("el identificador del formulario añadió %d segmentos a la ruta: %s", got-want, ultima.RequestURI)
	}
	if strings.Contains(ultima.RequestURI, "/tenants/") {
		t.Errorf("la ruta alcanza otro recurso de la API: %s", ultima.RequestURI)
	}
}

// TestPeticiones_LlevanElTokenYNoLaEmpresa (INV-04 en la unidad): el tenant no viaja porque ningún
// método tiene dónde ponerlo, y el token va en la cabecera en TODAS.
func TestPeticiones_LlevanElTokenYNoLaEmpresa(t *testing.T) {
	t.Parallel()

	llamadas := map[string]func(*Client) error{
		"members.list":   func(c *Client) error { _, err := c.Members.List(context.Background(), tokenDePrueba); return err },
		"members.add":    func(c *Client) error { return c.Members.Add(context.Background(), tokenDePrueba, userDePrueba) },
		"members.remove": func(c *Client) error { return c.Members.Remove(context.Background(), tokenDePrueba, userDePrueba) },
		"roles.list":     func(c *Client) error { _, err := c.Roles.List(context.Background(), tokenDePrueba); return err },
		"roles.create": func(c *Client) error {
			_, err := c.Roles.Create(context.Background(), tokenDePrueba, "Cajera", "")
			return err
		},
		"roles.assign": func(c *Client) error {
			return c.Roles.Assign(context.Background(), tokenDePrueba, userDePrueba, roleDePrueba)
		},
		"roles.unassign": func(c *Client) error {
			return c.Roles.Unassign(context.Background(), tokenDePrueba, userDePrueba, roleDePrueba)
		},
		"entitlements.get": func(c *Client) error {
			_, err := c.Entitlements.Get(context.Background(), tokenDePrueba)
			return err
		},
	}

	for nombre, llamada := range llamadas {
		api, ultima := servidorQueResponde(t, http.StatusOK, "{}")
		_ = llamada(api)
		if got := ultima.Header.Get("Authorization"); got != "Bearer "+tokenDePrueba {
			t.Errorf("%s: Authorization = %q", nombre, got)
		}
		if ultima.URL.Query().Has("tenant_id") {
			t.Errorf("%s: manda tenant_id en la query", nombre)
		}
	}
}

// TestTransport_UnTimeoutInvalidoCaeAlDefault: un cliente sin plazo es un cuelgue esperando a ocurrir.
func TestTransport_UnTimeoutInvalidoCaeAlDefault(t *testing.T) {
	t.Parallel()

	for _, d := range []time.Duration{0, -time.Second} {
		tr := NewTransport("http://127.0.0.1:9", d)
		if tr.http.Timeout != defaultTimeout {
			t.Errorf("timeout %v dejó el cliente con %v, want %v", d, tr.http.Timeout, defaultTimeout)
		}
	}
	if tr := NewTransport("http://127.0.0.1:9", 3*time.Second); tr.http.Timeout != 3*time.Second {
		t.Errorf("un timeout válido no se respetó: %v", tr.http.Timeout)
	}
}
