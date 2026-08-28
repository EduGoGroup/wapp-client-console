package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// rolesOK son las rutas del doble para un plano de roles que funciona.
func rolesOK() map[string]stubResponse {
	return map[string]stubResponse{
		"GET /api/v1/roles":        {http.StatusOK, rolesBody},
		"GET /api/v1/members":      {http.StatusOK, membersBody(testUserID, testOtherUserID)},
		"GET /api/v1/entitlements": {http.StatusOK, entitlementsBody("commerce", "menu", "catalog_import")},
		"POST /api/v1/roles": {http.StatusCreated,
			`{"role_id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","name":"Cajera","tenant_id":"` + testTenantID + `","global":false}`},
		"POST /api/v1/members/{user_id}/roles":             {http.StatusNoContent, ""},
		"DELETE /api/v1/members/{user_id}/roles/{role_id}": {http.StatusNoContent, ""},
	}
}

// TestRoles_ElCatalogoDistinguePlantillaGlobalDeRolPropio (T1.3).
//
// `global` es DERIVADA en el servidor y viaja explícita justo para esto: una plantilla del ecosistema
// se puede asignar pero no editar desde una empresa (la API responde 422). Si la pantalla las pintara
// iguales, el usuario intentaría editarlas.
func TestRoles_ElCatalogoDistinguePlantillaGlobalDeRolPropio(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, rolesOK())
	rec := getWithSession(t, adminRouter(api), "/roles")

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /roles status = %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()

	if !strings.Contains(out, `id="table-roles"`) {
		t.Fatal("no se pintó el catálogo de roles")
	}
	if !strings.Contains(out, "Plantilla del ecosistema") {
		t.Error("el rol global no se marca como plantilla del ecosistema")
	}
	if !strings.Contains(out, "De tu empresa") {
		t.Error("el rol propio del tenant no se marca como tal")
	}
	// El padre llega como UUID y la pantalla lo resuelve a NOMBRE contra el propio catálogo.
	if !strings.Contains(out, "Encargada de pedidos") || !strings.Contains(out, "tenant_admin") {
		t.Error("faltan los nombres de los roles del catálogo")
	}
	if strings.Contains(out, `<td>`+testGlobalRoleID+`</td>`) {
		t.Error("la columna «hereda de» pinta el UUID crudo en vez del nombre del rol padre")
	}
}

// TestRoles_CrearRolMandaNombreYPadreYNoLaEmpresa (T1.3): el cuerpo lleva EXACTAMENTE los dos campos
// del contrato. La empresa no viaja (INV-04): sale del token.
func TestRoles_CrearRolMandaNombreYPadreYNoLaEmpresa(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, rolesOK())

	rec := postFormWithCSRF(adminRouter(api), "/roles", url.Values{
		"name":           {"Cajera"},
		"parent_role_id": {testGlobalRoleID},
	}, clientSessionCookie(t))

	if got, want := redirectTarget(t, rec), "/roles?success="+flashRoleCreated; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	req := api.Last(t, "POST /api/v1/roles")
	if !strings.Contains(req.Body, `"name":"Cajera"`) {
		t.Errorf("el cuerpo no lleva el nombre del rol: %s", req.Body)
	}
	if !strings.Contains(req.Body, `"parent_role_id":"`+testGlobalRoleID+`"`) {
		t.Errorf("el cuerpo no lleva el rol padre: %s", req.Body)
	}
	if strings.Contains(req.Body, "tenant_id") {
		t.Errorf("el cuerpo lleva tenant_id: la empresa sale del TOKEN (INV-04). Body: %s", req.Body)
	}
}

// TestRoles_CrearRolSinNombreNoSaleALaRed: la validación de campo vacío ocurre antes del viaje.
func TestRoles_CrearRolSinNombreNoSaleALaRed(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, rolesOK())

	rec := postFormWithCSRF(adminRouter(api), "/roles", url.Values{"name": {"   "}}, clientSessionCookie(t))

	if got, want := redirectTarget(t, rec), "/roles?error="+flashMissingField; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if api.Called("POST /api/v1/roles") {
		t.Error("un formulario incompleto llegó al upstream")
	}
}

// TestRoles_AsignarVaPorLaRutaDeRolesYNoPorLaDeMiembros (T1.3, criterio de la ola).
//
// 🔴 El scope lo decide la OPERACIÓN, no el prefijo. La ruta de la plataforma empieza por
// `/api/v1/members/{user_id}` pero el guardián que la protege es `roles.write`, NO `members.write`:
// lo que se mueve es un permiso, y quien puede asignar roles puede asignarse `tenant_admin`.
//
// Un test no puede ver el scope —lo evalúa el servidor—, así que afirma lo único que la consola
// controla y que determina qué guardián se evalúa: el par (verbo, ruta). Y afirma también lo que NO
// se tocó: las dos rutas de `members.write` (POST y DELETE sobre /api/v1/members) quedan sin llamar.
func TestRoles_AsignarVaPorLaRutaDeRolesYNoPorLaDeMiembros(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, rolesOK())

	rec := postFormWithCSRF(adminRouter(api), "/roles/asignar", url.Values{
		"user_id": {testOtherUserID},
		"role_id": {testTenantRoleID},
	}, clientSessionCookie(t))

	if got, want := redirectTarget(t, rec), "/roles?success="+flashRoleAssigned; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}

	// La ruta de roles.write: la persona en la RUTA, el rol en el CUERPO.
	req := api.Last(t, "POST /api/v1/members/"+testOtherUserID+"/roles")
	if !strings.Contains(req.Body, `"role_id":"`+testTenantRoleID+`"`) {
		t.Errorf("el cuerpo no lleva el rol: %s", req.Body)
	}

	// Las dos rutas de members.write, intactas.
	if api.Called("POST /api/v1/members") {
		t.Error("asignar un rol llamó al ALTA de miembro (members.write): es la operación equivocada")
	}
	if api.Called("DELETE /api/v1/members/" + testOtherUserID) {
		t.Error("asignar un rol llamó a la BAJA de miembro (members.write)")
	}
}

// TestRoles_RetirarUsaDeleteConElRolEnLaRuta (T1.3): mismo scope que asignar, y el rol viaja en la
// ruta porque es la IDENTIDAD de lo que se borra.
func TestRoles_RetirarUsaDeleteConElRolEnLaRuta(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, rolesOK())

	rec := postFormWithCSRF(adminRouter(api), "/roles/retirar", url.Values{
		"user_id": {testOtherUserID},
		"role_id": {testTenantRoleID},
	}, clientSessionCookie(t))

	if got, want := redirectTarget(t, rec), "/roles?success="+flashRoleRemoved; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	req := api.Last(t, "DELETE /api/v1/members/"+testOtherUserID+"/roles/"+testTenantRoleID)
	if req.Body != "" {
		t.Errorf("el DELETE viajó con cuerpo (%q); el par persona/rol va en la ruta", req.Body)
	}
}

// TestRoles_LaPantallaDiceQueElEstadoActualNoEsConsultable (T1.3).
//
// La API pública NO expone `GET /api/v1/members/{user_id}/roles`: en el plano de roles hay POST y
// DELETE, y ningún GET. La pantalla asigna y retira, y lo DICE en vez de fingir una lista vacía que
// se leería como «esta persona no tiene ningún rol».
//
// El negativo —no se llamó a esa ruta— no es vacuo: los dos tests de arriba prueban que el prefijo
// `/api/v1/members/{id}/roles` SÍ se recorre con POST y con DELETE.
func TestRoles_LaPantallaDiceQueElEstadoActualNoEsConsultable(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, rolesOK())
	out := getWithSession(t, adminRouter(api), "/roles").Body.String()

	if !strings.Contains(out, `id="nota-sin-lectura-de-roles"`) {
		t.Error("la pantalla no advierte de que los roles actuales de una persona no se pueden consultar")
	}
	for _, r := range api.Requests() {
		if r.Method == http.MethodGet && strings.HasSuffix(r.Path, "/roles") && strings.Contains(r.Path, "/members/") {
			t.Errorf("se intentó leer los roles de un miembro (%s): esa ruta no existe en la API", r.Route())
		}
	}
}

// TestRoles_ElFormularioDeAsignacionOfreceMiembrosYRoles: el par de <select> se llena con las dos
// listas, y la persona de la sesión aparece marcada como «tú» también aquí.
func TestRoles_ElFormularioDeAsignacionOfreceMiembrosYRoles(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, rolesOK())
	out := getWithSession(t, adminRouter(api), "/roles").Body.String()

	if !strings.Contains(out, `id="form-role-assignment"`) {
		t.Fatal("no se pintó el formulario de asignación")
	}
	for _, want := range []string{
		`<option value="` + testOtherUserID + `">`,
		`<option value="` + testTenantRoleID + `">`,
		`formaction="/roles/asignar"`,
		`formaction="/roles/retirar"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("falta %q en el formulario de asignación", want)
		}
	}
	if !strings.Contains(out, "(tú)") {
		t.Error("el usuario de la sesión no se distingue en el selector de personas")
	}
}

// TestRoles_SinListaDeMiembrosNoSePintaElFormularioPeroSiElCatalogo: las dos llamadas degradan por
// separado. Que no se pueda listar a la gente no debe borrar el catálogo de roles de la pantalla.
func TestRoles_SinListaDeMiembrosNoSePintaElFormularioPeroSiElCatalogo(t *testing.T) {
	t.Parallel()
	rutas := rolesOK()
	rutas["GET /api/v1/members"] = stubResponse{http.StatusInternalServerError, ""}
	api := newStubAPI(t, rutas)

	rec := getWithSession(t, adminRouter(api), "/roles")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (degradado)", rec.Code)
	}
	out := rec.Body.String()
	if !strings.Contains(out, `id="table-roles"`) {
		t.Error("el fallo del listado de miembros se llevó por delante el catálogo de roles")
	}
	if !strings.Contains(out, `id="asignacion-no-disponible"`) {
		t.Error("sin miembros debía pintarse el estado vacío del formulario de asignación")
	}
	if strings.Contains(out, `id="form-role-assignment"`) {
		t.Error("se pintó un formulario de asignación sin personas a las que asignar")
	}
}

// TestRoles_El409AlCrearEsNombreRepetidoYNoLaGuardaDeMembresia.
//
// Fija el ORDEN de las ramas de flashCodeFor: ErrMemberOfAnotherTenant envuelve a ErrConflict. Si el
// traductor preguntara primero por el genérico, un rol repetido y una persona de otra empresa darían
// el mismo texto; si preguntara al revés sin distinguir el plano, un nombre repetido diría «esa
// persona ya pertenece a otra empresa», que no tiene ningún sentido al crear un rol.
func TestRoles_El409AlCrearEsNombreRepetidoYNoLaGuardaDeMembresia(t *testing.T) {
	t.Parallel()
	rutas := rolesOK()
	rutas["POST /api/v1/roles"] = stubResponse{http.StatusConflict, ""}
	api := newStubAPI(t, rutas)
	router := adminRouter(api)

	rec := postFormWithCSRF(router, "/roles", url.Values{"name": {"Cajera"}}, clientSessionCookie(t))
	destino := redirectTarget(t, rec)
	if destino != "/roles?error="+flashConflict {
		t.Fatalf("Location = %q, want el código de conflicto genérico", destino)
	}
	out := getWithSession(t, router, destino).Body.String()
	if !strings.Contains(out, "Ya existe algo con ese nombre") {
		t.Error("el 409 al crear un rol no se explica como nombre repetido")
	}
	if strings.Contains(out, "ya pertenece a otra empresa") {
		t.Error("un rol repetido se explicó con el mensaje de la guarda de membresía")
	}
}
