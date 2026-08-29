package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// membersOK son las rutas del doble para una pantalla de miembros que funciona.
func membersOK() map[string]stubResponse {
	return map[string]stubResponse{
		"GET /api/v1/members":                  {http.StatusOK, membersBody(testUserID, testOtherUserID)},
		"DELETE /api/v1/members/{user_id}":     {http.StatusNoContent, ""},
		"GET /api/v1/roles":                    {http.StatusOK, rolesBody},
		"GET /api/v1/entitlements":             {http.StatusOK, entitlementsBody("commerce", "menu", "catalog_import")},
		"POST /api/v1/members/{user_id}/roles": {http.StatusNoContent, ""},
		// UNA empresa: el mundo de siempre. Se sirve DE VERDAD —y no se deja que la llamada falle—
		// para que «aquí no hay selector» sea una decisión observable y no el efecto de un listado
		// que no se pudo leer, que es la forma en que ese negativo se volvería vacuo.
		rutaListadoDeEmpresas: {http.StatusOK, unaEmpresa()},
	}
}

// membersConDosEmpresas es membersOK con la persona en DOS empresas: el mundo de la Ola 5.
func membersConDosEmpresas() map[string]stubResponse {
	rutas := membersOK()
	rutas[rutaListadoDeEmpresas] = stubResponse{http.StatusOK, dosEmpresas()}
	return rutas
}

// TestMiembros_PintaLaIdentidadQueHayYDiceDeDondeSaleElNombre (T1.4a).
//
// GET /api/v1/members devuelve SOLO UUIDs —no hay `name` ni `email`, y es contrato explícito porque
// wApp no guarda PII de personas—, así que la pantalla no puede pintar un nombre. Lo que sí debe
// hacer es que ese identificador se pueda leer: abreviado en la celda, COMPLETO en el `title` para
// verlo y copiarlo, y una nota que diga dónde vive la identidad de verdad.
func TestMiembros_PintaLaIdentidadQueHayYDiceDeDondeSaleElNombre(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, membersOK())
	rec := getWithSession(t, adminRouter(api), "/miembros")

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /miembros status = %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()

	if !strings.Contains(out, `id="table-members"`) {
		t.Fatal("la pantalla no pintó la tabla de miembros")
	}
	// El completo, en el title: sin él el identificador abreviado no sirve para nada.
	if !strings.Contains(out, `title="`+testOtherUserID+`"`) {
		t.Errorf("el identificador completo no está en el title de la celda")
	}
	// El abreviado, en la celda.
	if !strings.Contains(out, shortID(testOtherUserID)) {
		t.Errorf("el identificador abreviado (%q) no aparece en la tabla", shortID(testOtherUserID))
	}
	if !strings.Contains(out, `id="nota-identidad"`) {
		t.Error("falta la nota que dice que el nombre y el correo viven en el proveedor de identidad")
	}
}

// TestMiembros_MarcaAlUsuarioDeLaSesionYNoLeOfreceDarseDeBaja (T1.4a).
//
// Marcar «tú» es lo ÚNICO que la consola sabe de la identidad de alguien, porque ese alguien es quien
// tiene la sesión abierta: sale del `whoami` del token, no de la API de miembros.
//
// El assert negativo —la fila propia no trae botón de baja— NO es vacuo: su gemelo positivo está en
// el mismo render, la fila del OTRO miembro, que sí lo trae. Si el botón dejara de pintarse por
// cualquier motivo, el positivo cae y el negativo no podría pasar solo.
func TestMiembros_MarcaAlUsuarioDeLaSesionYNoLeOfreceDarseDeBaja(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, membersOK())
	out := getWithSession(t, adminRouter(api), "/miembros").Body.String()

	if !strings.Contains(out, `<span class="wapp-chip wapp-chip--info">tú</span>`) {
		t.Error("el usuario de la sesión no está marcado como «tú»")
	}
	// Gemelo POSITIVO: la otra persona sí tiene su formulario de baja.
	if !strings.Contains(out, `action="/miembros/`+testOtherUserID+`/baja"`) {
		t.Fatal("la fila de la otra persona no ofrece darla de baja: el negativo de abajo sería vacuo")
	}
	// NEGATIVO: la fila propia, no.
	if strings.Contains(out, `action="/miembros/`+testUserID+`/baja"`) {
		t.Error("la pantalla ofrece al usuario darse de baja a sí mismo")
	}
}

// TestMiembros_ElSelectorDeEmpresaAparecePorCARDINALIDAD (T5.3, criterio de la ola).
//
// 🆕 ESTE TEST CAMBIÓ DE SENTIDO Y NO SE BORRÓ. Era `TestMiembros_LaEmpresaEsUnDatoYNoHaySelector` y
// afirmaba que aquí NO hay selector de empresa, porque el canje fallaba con ErrMultipleTenants si la
// persona pertenecía a más de una (MD-055.2). La Ola 5 abrió ese cerrojo, así que la mitad negativa
// deja de valer para todo el mundo y pasa a valer para UN caso: quien tiene UNA sola empresa. La
// mitad positiva —el selector CON DOS— es nueva. Borrarlo habría dejado el ecosistema sin vigilar
// ninguno de los dos.
//
// 🔴 EL GATE ES POR CARDINALIDAD, NO POR FEATURE. Se pinta cuando hay más de una empresa entre las
// que elegir, y no detrás de `Entitlements.Has "multi_empresa"`: los entitlements solo se resuelven
// en la portada —aquí el gate cerraría siempre— y, sobre todo, quien ya está en dos empresas ya está
// en dos; esconderle el control no lo deshace, solo lo deja atrapado en una.
//
// 🔴 Y EL NEGATIVO YA NO ES UN CONTEO DE `<select>`. Antes era `strings.Count(html, "<select") != 1`,
// que ya se había tenido que estrechar UNA vez —cuando el alta trajo el desplegable del rol— y que
// habría vuelto a romperse con el tercer desplegable que llegara, sin que nadie hubiera hecho nada
// mal. Lo que el criterio dice de verdad no es «cuántos desplegables hay»: es «no hay ningún control
// capaz de cambiar de empresa». Y eso tiene una firma exacta e independiente del número de
// desplegables: para cambiar de empresa hay que hacer POST a `rutaEmpresa` con un campo `tenant_id`.
// Un selector «llamado de otra forma» seguiría teniendo que hacer las dos cosas para funcionar, así
// que el aserto las persigue a ellas y no al recuento. Un cuarto `<select>` de cualquier otra cosa
// ya no puede romper este test.
//
// Los dos positivos anti-vacuidad de la versión anterior se conservan intactos en su intención:
//  1. la empresa SÍ se pinta como dato —ahora por su NOMBRE, con el identificador completo en el
//     `title`, que es lo que impide que el aserto del UUID se quede sin nada que probar—;
//  2. esta pantalla SÍ sabe pintar un `<select>` (el del rol), así que «aquí no hay uno de empresa»
//     es una decisión observable y no la ausencia de una capacidad que no existe.
func TestMiembros_ElSelectorDeEmpresaAparecePorCARDINALIDAD(t *testing.T) {
	t.Parallel()

	t.Run("con UNA empresa no hay forma de cambiar de empresa", func(t *testing.T) {
		t.Parallel()
		api := newStubAPI(t, membersOK())
		miembros := getWithSession(t, adminRouter(api), "/miembros").Body.String()

		// Positivo 1: la empresa está en la pantalla, como dato, y con las DOS mitades — el nombre
		// legible, que es lo que se lee, y el identificador COMPLETO en el `title`, que es lo que se
		// copia. Sustituir uno por el otro habría vaciado este aserto.
		if !strings.Contains(miembros, `id="tenant-actual"`) {
			t.Fatal("la pantalla no pinta la empresa de la sesión: el negativo de abajo sería vacuo")
		}
		if !strings.Contains(miembros, `title="`+testTenantID+`"`) {
			t.Fatalf("el identificador completo de la empresa (%s) ya no está en el HTML: se perdió el dato que se copia",
				testTenantID)
		}
		if !strings.Contains(miembros, testTenantName) {
			t.Errorf("la empresa se sigue pintando sin su nombre (%q): el UUID crudo no le dice nada a quien lo lee",
				testTenantName)
		}
		// Positivo 2: la pantalla sabe pintar un desplegable, y lo hace.
		if !strings.Contains(miembros, `name="role_id"`) {
			t.Fatal("la pantalla no pinta el <select> de rol: el negativo de abajo sería vacuo")
		}

		// NEGATIVO: no hay NINGÚN control capaz de cambiar de empresa. Los tres rastros que
		// cualquiera de ellos dejaría, se llame como se llame.
		for _, rastro := range []string{`name="tenant_id"`, `id="tenant-switcher"`, `action="` + rutaEmpresa + `"`} {
			if strings.Contains(miembros, rastro) {
				t.Errorf("con UNA sola empresa la pantalla ofrece cambiar de empresa (%s): no hay entre qué elegir", rastro)
			}
		}
	})

	t.Run("con DOS empresas el selector está", func(t *testing.T) {
		t.Parallel()
		api := newStubAPI(t, membersConDosEmpresas())
		miembros := getWithSession(t, adminRouter(api), "/miembros").Body.String()

		// Los MISMOS tres rastros, ahora exigidos. Es el mismo fixture con UNA variable cambiada, que
		// es lo que hace que el negativo de arriba signifique algo.
		for _, rastro := range []string{`name="tenant_id"`, `id="tenant-switcher"`, `action="` + rutaEmpresa + `"`} {
			if !strings.Contains(miembros, rastro) {
				t.Errorf("con DOS empresas falta el selector (%s)", rastro)
			}
		}
		// Y ofrece las dos, con la ACTIVA marcada. `selected` tiene que caer en la del token, no en
		// la primera de la lista: aquí la activa se sirve la SEGUNDA justo para que «marca siempre la
		// primera» no pueda pasar.
		if !strings.Contains(miembros, testOtherTenantName) {
			t.Errorf("el selector no ofrece la otra empresa (%q)", testOtherTenantName)
		}
		if !strings.Contains(miembros, `<option value="`+testTenantID+`" selected>`) {
			t.Errorf("la empresa ACTIVA (%s) no está marcada como seleccionada", testTenantID)
		}
		if strings.Contains(miembros, `<option value="`+testOtherTenantID+`" selected>`) {
			t.Errorf("está marcada como activa una empresa que el servidor NO marcó (%s)", testOtherTenantID)
		}
		// 🔴 Y sigue siendo un FORMULARIO, no un `<select onchange>`: la CSP de esta consola no
		// admite JavaScript en línea (ver security_test.go).
		if strings.Contains(strings.ToLower(miembros), "onchange") {
			t.Error("el selector usa un manejador en línea; la CSP lo descarta y el cambio no ocurriría")
		}
	})
}

// TestMiembros_LaBajaLlamaAlDeleteDeLaAPI (T1.4a): el formulario POST de la consola se traduce en el
// DELETE que la plataforma espera, con la persona en la RUTA.
func TestMiembros_LaBajaLlamaAlDeleteDeLaAPI(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, membersOK())
	router := adminRouter(api)

	rec := postFormWithCSRF(router, "/miembros/"+testOtherUserID+"/baja", url.Values{}, clientSessionCookie(t))

	if got, want := redirectTarget(t, rec), "/miembros?success="+flashMemberRemoved; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	req := api.Last(t, "DELETE /api/v1/members/"+testOtherUserID)
	if req.Body != "" {
		t.Errorf("el DELETE viajó con cuerpo (%q); la persona va en la ruta", req.Body)
	}
	if !strings.HasPrefix(req.Auth, "Bearer ") {
		t.Errorf("el DELETE no llevó el Context Token: Authorization = %q", req.Auth)
	}
}

// TestMiembros_LaBajaPropiaNiSiquieraSaleALaRed (T1.4a).
//
// La API aceptaría la baja propia —es una baja legítima— y el usuario perdería el acceso en el mismo
// clic. La plantilla no pinta el botón, pero un botón que no se pinta no impide un POST: la guarda
// tiene que estar en el handler, y por eso el criterio se afirma sobre la petición SALIENTE (que no
// existe) y no sobre el HTML.
func TestMiembros_LaBajaPropiaNiSiquieraSaleALaRed(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, membersOK())
	router := adminRouter(api)

	rec := postFormWithCSRF(router, "/miembros/"+testUserID+"/baja", url.Values{}, clientSessionCookie(t))

	if got, want := redirectTarget(t, rec), "/miembros?error="+flashSelfRemoval; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if api.Called("DELETE /api/v1/members/" + testUserID) {
		t.Error("la baja propia llegó al upstream: la guarda del handler no la cortó")
	}
}

// TestMiembros_El404SeExplicaComoFronteraDeEmpresaYNoComoInexistencia (T1.4a).
//
// 🔴 Aquí un 404 NO significa «no existe»: la plataforma responde 404 —y no 403— cuando el UUID
// pertenece a OTRA empresa, precisamente para no confirmar que existe. Un texto de «no encontrado»
// mandaría al usuario a buscar una errata donde lo que hay es una frontera de tenant.
func TestMiembros_El404SeExplicaComoFronteraDeEmpresaYNoComoInexistencia(t *testing.T) {
	t.Parallel()
	rutas := membersOK()
	rutas["DELETE /api/v1/members/{user_id}"] = stubResponse{http.StatusNotFound, `{"error":"recurso no encontrado"}`}
	api := newStubAPI(t, rutas)
	router := adminRouter(api)

	rec := postFormWithCSRF(router, "/miembros/"+testOtherUserID+"/baja", url.Values{}, clientSessionCookie(t))
	destino := redirectTarget(t, rec)
	if destino != "/miembros?error="+flashNotInYourTenant {
		t.Fatalf("Location = %q, want el código de frontera de empresa", destino)
	}

	out := getWithSession(t, router, destino).Body.String()
	if !strings.Contains(out, "no pertenece a tu empresa") {
		t.Errorf("el aviso no explica que el identificador es de otra empresa. Body: %s", out)
	}
	if strings.Contains(out, "no encontrado") || strings.Contains(out, "no existe") {
		t.Error("el aviso dice «no encontrado»/«no existe»: eso es justo lo que el 404 NO significa aquí")
	}
	// El detalle del upstream no se pinta: el texto sale del catálogo, nunca del cuerpo de la API.
	if strings.Contains(out, "recurso no encontrado") {
		t.Error("el mensaje del upstream acabó en pantalla")
	}
}

// TestMiembros_El409EsPersonaDeOtraEmpresaYNoUnDuplicado: MD-055.2. El 409 del plano de miembros no
// es «ya existe»; su traductor propio le da el mensaje que explica por qué está prohibido.
func TestMiembros_El409EsPersonaDeOtraEmpresaYNoUnDuplicado(t *testing.T) {
	t.Parallel()
	rutas := membersOK()
	rutas["DELETE /api/v1/members/{user_id}"] = stubResponse{http.StatusConflict, ""}
	api := newStubAPI(t, rutas)
	router := adminRouter(api)

	rec := postFormWithCSRF(router, "/miembros/"+testOtherUserID+"/baja", url.Values{}, clientSessionCookie(t))
	destino := redirectTarget(t, rec)
	if destino != "/miembros?error="+flashMemberElsewhere {
		t.Fatalf("Location = %q, want el código de «pertenece a otra empresa»", destino)
	}
	if out := getWithSession(t, router, destino).Body.String(); !strings.Contains(out, "ya pertenece a otra empresa") {
		t.Error("el aviso no explica la guarda de membresía única")
	}
}

// TestMiembros_DegradaSiElListadoFalla: la pantalla sigue sirviendo 200 con el aviso arriba. Un 5xx
// del upstream no debe dejar al usuario sin marco ni sin navegación.
func TestMiembros_DegradaSiElListadoFalla(t *testing.T) {
	t.Parallel()
	rutas := membersOK()
	rutas["GET /api/v1/members"] = stubResponse{http.StatusInternalServerError, `{"error":"detalle interno"}`}
	api := newStubAPI(t, rutas)

	rec := getWithSession(t, adminRouter(api), "/miembros")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (degradado)", rec.Code)
	}
	out := rec.Body.String()
	if !strings.Contains(out, `id="members-vacio"`) {
		t.Error("sin listado debía pintarse el estado vacío")
	}
	if !strings.Contains(out, "No se pudo completar la operación") {
		t.Error("falta el aviso del fallo")
	}
	if strings.Contains(out, "detalle interno") {
		t.Error("el cuerpo del upstream acabó en pantalla")
	}
}

// TestMiembros_UnA401QueSobreviveAlRefrescoExpulsaALogin: withAuthRetry refresca UNA vez; si el
// segundo intento también es 401, la sesión ya no vale y la pantalla no sirve de nada. Identity está
// apagado en el arnés, así que el refresco falla y se conserva el 401 original.
func TestMiembros_UnA401QueSobreviveAlRefrescoExpulsaALogin(t *testing.T) {
	t.Parallel()
	rutas := membersOK()
	rutas["GET /api/v1/members"] = stubResponse{http.StatusUnauthorized, ""}
	api := newStubAPI(t, rutas)

	rec := getWithSession(t, adminRouter(api), "/miembros")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 a /login", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/login?error="+flashSessionExpired {
		t.Errorf("Location = %q, want /login con el aviso de sesión caducada", got)
	}
}
