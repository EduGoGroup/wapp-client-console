package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// members_alta_test.go cubre la incorporación de una persona a la empresa (T1.2): el formulario que
// vive dentro de /miembros, el rol opcional del mismo paso, y los desenlaces del upstream.
//
// El alta es la operación con MÁS códigos distintos de toda la consola —es la única que hace que la
// plataforma salga a identity con su credencial M2M—, así que la tabla de desenlaces está aquí y no
// repartida por casos sueltos.

// altaOK son las rutas del doble para una pantalla de miembros con el alta funcionando.
func altaOK() map[string]stubResponse {
	rutas := membersOK()
	rutas["POST /api/v1/members"] = stubResponse{http.StatusNoContent, ""}
	return rutas
}

// TestAlta_ElFormularioPideElIdentificadorYExplicaDeDondeSale.
//
// La pantalla no puede buscar por correo (ver MembersClient), así que lo único que la hace usable es
// decir de dónde saca la persona el identificador que hay que pegar — y ofrecer el enlace a la
// pantalla donde ella lo tiene a la vista. Un campo sin esa explicación es un callejón.
func TestAlta_ElFormularioPideElIdentificadorYExplicaDeDondeSale(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, altaOK())
	out := getWithSession(t, adminRouter(api), "/miembros").Body.String()

	if !strings.Contains(out, `id="form-add-member"`) || !strings.Contains(out, `action="/miembros"`) {
		t.Fatal("la pantalla de miembros no ofrece el formulario de alta")
	}
	if !strings.Contains(out, `name="user_id"`) {
		t.Error("el formulario no pide el identificador de la persona")
	}
	if !strings.Contains(out, `href="/mi-identificador"`) {
		t.Error("el texto de ayuda no enlaza a «Mi identificador», que es de donde sale el dato que se pega")
	}
	// El formulario va bajo la defensa CSRF como todos los demás.
	if !strings.Contains(out, `name="csrf_token"`) {
		t.Error("el formulario de alta no incrusta el token CSRF")
	}
	// Y el texto viejo —«todavía no se hace desde aquí»— no puede sobrevivir a la pantalla que sí lo
	// hace: es documentación que miente en la cara del usuario.
	if strings.Contains(out, "todavía no se hace desde aquí") {
		t.Error("la pantalla sigue diciendo que no se puede incorporar a nadie desde aquí")
	}
}

// TestAlta_SinRolEsUnaSolaLlamada: el rol es OPCIONAL. Sin él no debe salir ninguna petición al plano
// de roles — una asignación con el rol vacío sería un 400 del que el usuario no tiene la culpa.
func TestAlta_SinRolEsUnaSolaLlamada(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, altaOK())
	router := adminRouter(api)

	rec := postFormWithCSRF(router, "/miembros", url.Values{"user_id": {testOtherUserID}}, clientSessionCookie(t))

	if got, want := redirectTarget(t, rec), "/miembros?success="+flashMemberAdded; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	req := api.Last(t, "POST /api/v1/members")
	if !strings.Contains(req.Body, `"user_id":"`+testOtherUserID+`"`) {
		t.Errorf("el alta no mandó la persona en el cuerpo: %q", req.Body)
	}
	if api.Called("POST /api/v1/members/" + testOtherUserID + "/roles") {
		t.Error("sin rol en el formulario se llamó igual al plano de roles")
	}
}

// TestAlta_ConRolEncadenaLasDosLlamadasEnOrden.
//
// Las dos operaciones y en ese orden: asignar un rol a quien todavía no es miembro responde 404. El
// orden no es una preferencia, es la única secuencia que puede funcionar.
func TestAlta_ConRolEncadenaLasDosLlamadasEnOrden(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, altaOK())
	router := adminRouter(api)

	rec := postFormWithCSRF(router, "/miembros",
		url.Values{"user_id": {testOtherUserID}, "role_id": {testTenantRoleID}}, clientSessionCookie(t))

	if got, want := redirectTarget(t, rec), "/miembros?success="+flashMemberAdded; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}

	rutaAlta := "POST /api/v1/members"
	rutaRol := "POST /api/v1/members/" + testOtherUserID + "/roles"
	var posAlta, posRol = -1, -1
	for i, r := range api.Requests() {
		switch r.Route() {
		case rutaAlta:
			posAlta = i
		case rutaRol:
			if posRol == -1 {
				posRol = i
			}
		}
	}
	if posAlta == -1 || posRol == -1 {
		t.Fatalf("faltó alguna de las dos llamadas; llegaron: %v", routesOf(api.Requests()))
	}
	if posAlta > posRol {
		t.Error("se asignó el rol ANTES de incorporar: a quien no es miembro, esa llamada le responde 404")
	}
	if req := api.Last(t, rutaRol); !strings.Contains(req.Body, `"role_id":"`+testTenantRoleID+`"`) {
		t.Errorf("la asignación no mandó el rol en el cuerpo: %q", req.Body)
	}
}

// TestAlta_SiElRolFallaLaPersonaQuedaDentroYSeDice — el desenlace MIXTO, y el más importante.
//
// 🔴 Las dos llamadas NO son atómicas y no hay forma de que lo sean. Cuando la segunda falla, el
// estado real es «incorporada y sin rol»: pintarlo como éxito dejaría a alguien creyendo que tiene
// permisos que no tiene, y pintarlo como error a secas le haría repetir un alta que ya funcionó.
//
// Y NO se compensa dando de baja: la baja no retira roles ni grants, así que el rollback dejaría un
// estado TERCERO, y además borraría una incorporación que sí se quería.
func TestAlta_SiElRolFallaLaPersonaQuedaDentroYSeDice(t *testing.T) {
	t.Parallel()
	rutas := altaOK()
	rutas["POST /api/v1/members/{user_id}/roles"] = stubResponse{http.StatusNotFound, ""}
	api := newStubAPI(t, rutas)
	router := adminRouter(api)

	rec := postFormWithCSRF(router, "/miembros",
		url.Values{"user_id": {testOtherUserID}, "role_id": {testTenantRoleID}}, clientSessionCookie(t))

	destino := redirectTarget(t, rec)
	if destino != "/miembros?error="+flashAddedWithoutRole {
		t.Fatalf("Location = %q, want %q", destino, "/miembros?error="+flashAddedWithoutRole)
	}
	// El alta SÍ ocurrió: sin esto, el criterio de «quedó dentro» no estaría probado.
	if !api.Called("POST /api/v1/members") {
		t.Fatal("la incorporación no llegó a salir: el desenlace mixto no es el que se está probando")
	}
	// Y no se compensó: ninguna baja detrás.
	if api.Called("DELETE /api/v1/members/" + testOtherUserID) {
		t.Error("se dio de baja a la persona para compensar el rol fallido: eso deja un estado TERCERO")
	}

	out := getWithSession(t, router, destino).Body.String()
	if !strings.Contains(out, "quedó incorporada") {
		t.Errorf("el aviso no dice que la persona SÍ entró. Body: %s", out)
	}
	if !strings.Contains(out, "Roles") {
		t.Error("el aviso no dice dónde arreglarlo")
	}
}

// TestAlta_CadaDesenlaceDelUpstreamTieneSuAviso.
//
// El alta es la única operación de la consola que puede recibir 502 y 503, porque es la única que
// hace que la plataforma salga a identity con su credencial M2M. Ninguno de los dos es culpa de lo
// que la dueña escribió, así que los dos caen al aviso genérico de «inténtalo de nuevo» — que es
// exactamente lo que toca hacer— y NO al de «revisa lo que escribiste».
func TestAlta_CadaDesenlaceDelUpstreamTieneSuAviso(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre string
		status int
		codigo string
		dice   string
	}{
		{"400 datos rechazados", http.StatusBadRequest, flashInvalidInput, "Revisa lo que escribiste"},
		{"403 sin scope", http.StatusForbidden, flashForbidden, "no tiene permiso"},
		{"404 esa persona no está en el padrón", http.StatusNotFound, flashPersonUnknown, "no existe en wApp"},
		{"409 ya es de otra empresa", http.StatusConflict, flashMemberElsewhere, "ya pertenece a otra empresa"},
		{"502 identity no la acredita", http.StatusBadGateway, flashUpstreamUnavailable, "Inténtalo de nuevo"},
		{"503 sin cliente M2M o identity caído", http.StatusServiceUnavailable, flashUpstreamUnavailable, "Inténtalo de nuevo"},
		{"500 infra", http.StatusInternalServerError, flashUpstreamUnavailable, "Inténtalo de nuevo"},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			rutas := altaOK()
			rutas["POST /api/v1/members"] = stubResponse{caso.status, `{"error":"identity_no_configurado"}`}
			api := newStubAPI(t, rutas)
			router := adminRouter(api)

			rec := postFormWithCSRF(router, "/miembros",
				url.Values{"user_id": {testOtherUserID}}, clientSessionCookie(t))

			destino := redirectTarget(t, rec)
			if destino != "/miembros?error="+caso.codigo {
				t.Fatalf("Location = %q, want %q", destino, "/miembros?error="+caso.codigo)
			}
			out := getWithSession(t, router, destino).Body.String()
			if !strings.Contains(out, caso.dice) {
				t.Errorf("el aviso no contiene %q. Body: %s", caso.dice, out)
			}
			// El detalle del upstream no se pinta nunca: el texto sale del catálogo.
			if strings.Contains(out, "identity_no_configurado") {
				t.Error("el cuerpo del upstream acabó en pantalla")
			}
			// Y un alta fallida no encadena la asignación: el rol se pidió sobre alguien que no entró.
			if api.Called("POST /api/v1/members/" + testOtherUserID + "/roles") {
				t.Error("tras fallar el alta se intentó asignar el rol igualmente")
			}
		})
	}
}

// TestAlta_UnIdentificadorVacioNiSiquieraSaleALaRed: la validación de un campo obligatorio se hace
// antes de gastar una llamada, y el aviso dice lo que pasa —falta un dato— en vez del 400 que la
// plataforma devolvería.
func TestAlta_UnIdentificadorVacioNiSiquieraSaleALaRed(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, altaOK())
	router := adminRouter(api)

	// Con espacios: `formValue` recorta, y pegar un UUID arrastra espacios de sobra.
	rec := postFormWithCSRF(router, "/miembros", url.Values{"user_id": {"   "}}, clientSessionCookie(t))

	if got, want := redirectTarget(t, rec), "/miembros?error="+flashMissingField; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if api.Called("POST /api/v1/members") {
		t.Error("un formulario vacío llegó al upstream")
	}
}

// TestAlta_ElIdentificadorSeRecortaAntesDeViajar: pegar un UUID arrastra espacios y saltos de línea,
// y la plataforma respondería 404 sobre una cadena que a la vista es correcta — el peor diagnóstico
// posible, porque el usuario ve bien lo que escribió.
func TestAlta_ElIdentificadorSeRecortaAntesDeViajar(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, altaOK())
	router := adminRouter(api)

	postFormWithCSRF(router, "/miembros",
		url.Values{"user_id": {"  " + testOtherUserID + "\n"}}, clientSessionCookie(t))

	if req := api.Last(t, "POST /api/v1/members"); !strings.Contains(req.Body, `"user_id":"`+testOtherUserID+`"`) {
		t.Errorf("el identificador viajó con espacios: %q", req.Body)
	}
}

// TestAlta_SiElCatalogoDeRolesFallaLaPantallaSigueEnPie.
//
// 🔴 El desplegable de rol es un ACCESORIO del formulario: que no se pueda leer el catálogo no puede
// llevarse por delante ni la tabla de miembros ni el alta, que es para lo que se entra aquí. Se omite
// el <select> —pintarlo vacío parecería un fallo del formulario— y todo lo demás sigue.
func TestAlta_SiElCatalogoDeRolesFallaLaPantallaSigueEnPie(t *testing.T) {
	t.Parallel()
	rutas := altaOK()
	rutas["GET /api/v1/roles"] = stubResponse{http.StatusInternalServerError, `{"error":"detalle interno"}`}
	api := newStubAPI(t, rutas)

	rec := getWithSession(t, adminRouter(api), "/miembros")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (degradado)", rec.Code)
	}
	out := rec.Body.String()

	if !strings.Contains(out, `id="table-members"`) {
		t.Error("un fallo del catálogo de roles se llevó por delante la tabla de miembros")
	}
	if !strings.Contains(out, `id="form-add-member"`) {
		t.Error("un fallo del catálogo de roles se llevó por delante el formulario de alta")
	}
	if strings.Contains(out, `name="role_id"`) {
		t.Error("se pintó el desplegable de rol sin catálogo: un <select> vacío parece un formulario roto")
	}
	if !strings.Contains(out, "No se pudo completar la operación") {
		t.Error("falta el aviso del modo degradado")
	}
	if strings.Contains(out, "detalle interno") {
		t.Error("el cuerpo del upstream acabó en pantalla")
	}
}

// TestAlta_ElFalloDelCatalogoNoTapaElFalloDelListado: si fallan los dos, el aviso que se pinta es el
// del listado de miembros. Es el que explica por qué la tabla —el contenido principal— no está.
func TestAlta_ElFalloDelCatalogoNoTapaElFalloDelListado(t *testing.T) {
	t.Parallel()
	rutas := altaOK()
	rutas["GET /api/v1/members"] = stubResponse{http.StatusForbidden, ""}
	rutas["GET /api/v1/roles"] = stubResponse{http.StatusInternalServerError, ""}
	api := newStubAPI(t, rutas)

	out := getWithSession(t, adminRouter(api), "/miembros").Body.String()
	if !strings.Contains(out, flashError(flashForbidden)) {
		t.Errorf("el aviso del listado (403) no se pintó. Body: %s", out)
	}
}
