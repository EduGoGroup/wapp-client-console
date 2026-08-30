package web

import (
	"net/http"
	"strings"
	"testing"
)

// gatedBlockMarker es el ANCLA del bloque gateado por `catalog_import` en home.html.
//
// Es un `id` HTML estable y único, y no un trozo de texto: el copy de una sección cambia cuando
// alguien la reescribe, y entonces el assert negativo pasaría a salir verde SIEMPRE sin que nadie se
// entere. Un `id` solo cambia si se cambia el bloque a propósito.
const gatedBlockMarker = `id="section-catalog-import"`

// homeRoutes son las rutas del doble para la portada, con el plan que se le indique.
func homeRoutes(entitlements stubResponse) map[string]stubResponse {
	return map[string]stubResponse{
		"GET /api/v1/entitlements": entitlements,
	}
}

// enlaceRealDelBloque es el ancla del ENLACE a la pantalla dentro del bloque gateado.
//
// 🆕 Hasta T8.2 este bloque no llevaba enlace: decía «la pantalla de carga llega en la tanda
// siguiente», que era un texto de espera. Ahora la pantalla existe y el bloque la ofrece, así que el
// par ON/OFF de abajo mide el ENLACE y no solo la tarjeta — la tarjeta sin enlace volvería a ser una
// promesa, y eso pasaría el aserto viejo.
//
// 🔴 Es un `id` y NO el `href`, y la diferencia es la que hace que este test siga midiendo algo: el
// enlace de la BARRA apunta a la misma ruta y NO está gateado (decisión de T8.2), así que sale en
// TODAS las páginas, incluida ésta y también sin la feature. Un aserto de ausencia sobre
// `href="/importar-catalogo"` sería falso siempre, y el negativo de abajo saldría rojo por el motivo
// equivocado — o, peor, alguien lo «arreglaría» borrándolo.
const enlaceRealDelBloque = `id="link-importar-catalogo"`

// TestPlanGate_ConLaFeatureElBloqueSeEmite (T1.5, mitad ON).
func TestPlanGate_ConLaFeatureElBloqueSeEmite(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, homeRoutes(stubResponse{http.StatusOK,
		entitlementsBody("commerce", "cart_basic", featureCatalogImport, "menu")}))

	rec := getWithSession(t, adminRouter(api), "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	if !strings.Contains(out, gatedBlockMarker) {
		t.Error("con catalog_import efectiva, el bloque gateado debía emitirse")
	}
	// Y el bloque ofrece la pantalla DE VERDAD, con el enlace a la ruta que el router sirve.
	if !strings.Contains(out, enlaceRealDelBloque) {
		t.Error("el bloque gateado no lleva el enlace a la pantalla de importación")
	}
	if !strings.Contains(out, `href="`+rutaCatalogo+`"`) {
		t.Errorf("el enlace del bloque no apunta a %q, que es la ruta que registra el router", rutaCatalogo)
	}
}

// TestPlanGate_SinLaFeatureElBloqueNoSeEmiteYElRestoSigueIntacto (T1.5, mitad OFF).
//
// El gate es SERVER-SIDE: sin la feature el bloque NO LLEGA al HTML. No basta con ocultarlo —lo que
// no está contratado no debe estar ahí para que nadie lo destape con el inspector—, y además la CSP
// endurecida de esta consola no admitiría el JS que haría falta para esconderlo.
//
// La segunda mitad del test es la que evita el falso verde por el otro lado: si el gate se llevara
// por delante media portada, el negativo también saldría verde.
func TestPlanGate_SinLaFeatureElBloqueNoSeEmiteYElRestoSigueIntacto(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, homeRoutes(stubResponse{http.StatusOK,
		entitlementsBody("basic", "cart_basic", "intakes_export", "menu")}))

	rec := getWithSession(t, adminRouter(api), "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", rec.Code)
	}
	out := rec.Body.String()

	if strings.Contains(out, gatedBlockMarker) {
		t.Error("sin catalog_import, el bloque gateado NO debía emitirse")
	}
	if strings.Contains(out, enlaceRealDelBloque) {
		t.Error("sin la feature, la portada sigue ofreciendo el enlace a la pantalla de importación")
	}
	if strings.Contains(out, "Importar catálogo") || strings.Contains(out, "catalog_import") {
		t.Error("sin la feature no debe quedar rastro del bloque en el HTML")
	}
	// 🔴 LO QUE ESTE TEST NO PUEDE AFIRMAR, dicho aquí para que nadie lo «arregle»: que la ruta
	// `/importar-catalogo` no aparezca en el HTML. El enlace de la BARRA apunta a ella y NO está
	// gateado a propósito (T8.2), así que sale también sin la feature. Lo que cierra esa puerta no es
	// esta plantilla sino el gate POR RUTA, y su prueba está en catalogo_test.go.
	if !strings.Contains(out, `>Catálogo</a>`) {
		t.Error("la barra dejó de ofrecer «Catálogo» sin la feature: ese enlace NO va gateado, y si " +
			"alguien lo gateó hay que decidirlo a propósito y actualizar el aviso de base.html")
	}

	// Lo que NO depende del plan sigue ahí. El plano de roles y miembros es CAPACIDAD BASE
	// (D-047.10): no va detrás de ninguna feature y el gate no puede haberlo tocado.
	for _, want := range []string{
		`id="section-administracion"`,
		`href="/miembros"`,
		`href="/roles"`,
		`id="section-plan"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("el gate se llevó por delante %q, que no depende del plan", want)
		}
	}
	// Y las features que sí tiene se siguen pintando: el plan se resolvió, no falló.
	for _, want := range []string{"cart_basic", "intakes_export", "menu"} {
		if !strings.Contains(out, `<li class="wapp-chip wapp-chip--success">`+want+`</li>`) {
			t.Errorf("falta el chip de la feature efectiva %q", want)
		}
	}
}

// TestPlanGate_ElPlanoDeRolesYMiembrosNoEstaGateado (D-047.10).
//
// Administrar a tu propia gente es capacidad BASE: una empresa sin ninguna feature contratada sigue
// teniendo que poder entrar a sus miembros y a sus roles. El caso límite —plan resuelto y CERO
// features— es el que lo demuestra, porque es el único donde un gate de más se vería.
func TestPlanGate_ElPlanoDeRolesYMiembrosNoEstaGateado(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, map[string]stubResponse{
		"GET /api/v1/entitlements": {http.StatusOK, entitlementsBody("basic")},
		"GET /api/v1/members":      {http.StatusOK, membersBody(testUserID)},
		"GET /api/v1/roles":        {http.StatusOK, rolesBody},
		"GET /api/v1/sessions":     {http.StatusOK, sesionesBody()},
	})
	router := adminRouter(api)

	out := getWithSession(t, router, "/").Body.String()
	if !strings.Contains(out, `href="/miembros"`) || !strings.Contains(out, `href="/roles"`) {
		t.Error("sin ninguna feature, la portada dejó de ofrecer la administración de personas y permisos")
	}
	// La pantalla de SESIONES es capacidad base por el mismo motivo, y además es la que enseña la
	// flota de la empresa: gatearla dejaría a un tenant sin ver sus propios teléfonos.
	if !strings.Contains(out, `href="/sesiones"`) {
		t.Error("sin ninguna feature, la portada dejó de ofrecer las sesiones vinculadas")
	}
	// Y las pantallas responden, no solo el enlace.
	for _, ruta := range []string{"/sesiones", "/miembros", "/roles"} {
		if rec := getWithSession(t, router, ruta); rec.Code != http.StatusOK {
			t.Errorf("GET %s con un plan sin features status = %d, want 200", ruta, rec.Code)
		}
	}
}

// TestPlanGate_FailClosedCuandoNoSePuedeConsultarElPlan (T1.5).
//
// Si el endpoint falla, la vista cero no tiene ninguna feature y el gate CIERRA. Preferimos una
// consola que enseña de menos a una que promete lo que el tenant no ha contratado. La portada sigue
// sirviendo 200 y avisa: sin el aviso, el usuario vería una portada mutilada sin saber por qué.
func TestPlanGate_FailClosedCuandoNoSePuedeConsultarElPlan(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, homeRoutes(stubResponse{http.StatusInternalServerError,
		`{"error":"detalle interno que no debe verse"}`}))

	rec := getWithSession(t, adminRouter(api), "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (degradado)", rec.Code)
	}
	out := rec.Body.String()

	if strings.Contains(out, gatedBlockMarker) {
		t.Error("con el plan no consultado el gate debía CERRAR")
	}
	if !strings.Contains(out, "No se pudo consultar el plan de la empresa") {
		t.Error("falta el aviso del modo degradado")
	}
	if strings.Contains(out, "detalle interno") {
		t.Error("el cuerpo del upstream acabó en pantalla")
	}
	if !strings.Contains(out, `href="/miembros"`) {
		t.Error("el modo degradado se llevó por delante la administración, que es capacidad base")
	}
}

// TestPlanGate_El403DaUnAvisoDistintoDelFallo: «no puedo preguntarlo» y «no tienes permiso para
// preguntarlo» mandan a sitios distintos —uno a reintentar, otro a pedir permisos—, así que no
// pueden compartir texto.
func TestPlanGate_El403DaUnAvisoDistintoDelFallo(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, homeRoutes(stubResponse{http.StatusForbidden, ""}))

	out := getWithSession(t, adminRouter(api), "/").Body.String()
	if !strings.Contains(out, "no tiene permiso para consultar el plan") {
		t.Errorf("el 403 no da su aviso propio. Body: %s", out)
	}
	if strings.Contains(out, gatedBlockMarker) {
		t.Error("con un 403 el gate debía CERRAR")
	}
}

// TestPlanGate_LaVistaCeroNoHabilitaNada afirma el fail-closed en la unidad, no solo por el cable: el
// mapa nil de la vista cero devuelve false para CUALQUIER feature, incluidas las cuatro que solo
// existen como literal en el seed y no tienen constante en Go.
func TestPlanGate_LaVistaCeroNoHabilitaNada(t *testing.T) {
	t.Parallel()
	var cero entitlementsView

	for _, f := range []string{
		"menu", "survey", "media", "cart_basic", "intakes_export", "catalog_import", "crm_bridge",
		"llm_intent", "llm_intake", "api_llm", "stt_audio",
		"owner_app", "passive_profiles", "multi_empresa",
	} {
		if cero.Has(f) {
			t.Errorf("la vista cero habilitó %q: el gate no es fail-closed", f)
		}
	}
	if cero.Resolved {
		t.Error("la vista cero se declara resuelta: no se podría distinguir «sin features» de «no se pudo preguntar»")
	}
}

// TestPlanGate_LaConstanteYLaPlantillaHablanDeLaMismaFeature.
//
// 🔴 El gate vive en una CADENA DE PLANTILLA —`{{ if $.Entitlements.Has "catalog_import" }}`— y
// html/template no la compila contra nada: cambiar la constante de Go y olvidarse del `.html` deja
// los dos gates apuntando a features distintas, con `vet`, el linter y todos los tests en verde. Lo
// único que se vería es una sección que aparece cuando no debe o que no aparece nunca.
//
// Esto lo ata: la constante y el literal de la plantilla tienen que decir lo mismo, y además tiene
// que haber EXACTAMENTE un gate en la portada (dos condiciones distintas sobre el mismo bloque son
// la otra forma de que esto se desincronice).
func TestPlanGate_LaConstanteYLaPlantillaHablanDeLaMismaFeature(t *testing.T) {
	t.Parallel()

	raw, err := templatesFS.ReadFile("templates/pages/home.html")
	if err != nil {
		t.Fatalf("leer la plantilla de la portada: %v", err)
	}
	home := string(raw)

	gate := `{{ if $.Entitlements.Has "` + featureCatalogImport + `" }}`
	if !strings.Contains(home, gate) {
		t.Fatalf("la portada no gatea por %q; el gate que tiene no coincide con la constante de Go", featureCatalogImport)
	}
	if n := strings.Count(home, `$.Entitlements.Has `); n != 1 {
		t.Errorf("la portada tiene %d gates por feature, want 1: cada uno más es un sitio donde desincronizarse", n)
	}
	// Y el ancla del bloque está donde los tests la buscan.
	if !strings.Contains(home, gatedBlockMarker) {
		t.Errorf("la portada ya no lleva %s: el par ON/OFF de arriba estaría midiendo otra cosa", gatedBlockMarker)
	}
}
