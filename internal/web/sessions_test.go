package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// sessionsOK son las rutas del doble para una pantalla de sesiones que funciona: una sesión activa en
// línea con el clasificador sano.
func sessionsOK() map[string]stubResponse {
	return map[string]stubResponse{
		"GET /api/v1/sessions": {http.StatusOK, sesionesBody(
			`{"session_id":"` + testSessionID + `","edge_id":"edge-alpha","state":"online",` +
				`"profile":"active","self_pn":"+593990000001",` +
				`"intent_circuit":"closed","worker_taskset":"disjunta"}`)},
		"POST /api/v1/messages": {http.StatusOK,
			`{"acked_command_id":"cmd-abc123","ok":true}`},
		"POST /api/v1/sessions/{id}/profile": {http.StatusOK,
			`{"session_id":"` + testSessionID + `","profile":"passive"}`},
		"GET /api/v1/entitlements": {http.StatusOK, entitlementsBody("commerce", "menu")},
	}
}

// TestSesiones_PintaLasSesionesQueDevuelveElCloud (T2.1, criterio 1).
//
// Lo que decide es que la fila salga con SUS datos: la etiqueta legible (el número propio cuando la
// plataforma lo sirve), el equipo, el estado y el perfil que la plataforma reportó — no un valor
// inventado por la plantilla.
func TestSesiones_PintaLasSesionesQueDevuelveElCloud(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, sessionsOK())
	rec := getWithSession(t, adminRouter(api), "/sesiones")

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /sesiones status = %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()

	if !strings.Contains(out, `id="table-sessions"`) {
		t.Fatal("la pantalla no pintó la tabla de sesiones")
	}
	// La etiqueta es el número propio; el identificador de sesión queda en el title, para copiarlo.
	//
	// Se busca sin el «+» a propósito: html/template escapa el signo más a `&#43;` (está en su tabla
	// de reemplazo), así que el HTML dice `&#43;593990000001` y el navegador pinta el número entero.
	// Buscar el literal con «+» daría un rojo sobre una pantalla correcta.
	if !strings.Contains(out, "593990000001") {
		t.Error("la fila no pinta el número propio de la sesión")
	}
	if !strings.Contains(out, `title="`+testSessionID+`"`) {
		t.Error("el identificador de sesión no está en el title de la celda")
	}
	if !strings.Contains(out, "edge-alpha") {
		t.Error("la fila no pinta el equipo de la sesión")
	}
	// Estado y perfil, cada uno con su forma.
	if !strings.Contains(out, ">online<") {
		t.Error("la fila no pinta el estado de conexión")
	}
	if !strings.Contains(out, `<option value="active" selected>activa</option>`) {
		t.Error("el desplegable no preselecciona el perfil que la plataforma reportó")
	}
	// El vocabulario de la dueña va en el texto y el del cable en el value: los dos, y distintos.
	if !strings.Contains(out, ">pasiva<") || strings.Contains(out, ">passive<") {
		t.Error("el <option> debe decir «pasiva» a la dueña y NUNCA enseñar el identificador del cable como texto")
	}
	if !strings.Contains(out, `value="passive"`) || !strings.Contains(out, `value="active"`) {
		t.Error("los identificadores active/passive deben seguir viajando en el value del <option>")
	}
	if !strings.Contains(out, `action="/sesiones/`+testSessionID+`/perfil"`) {
		t.Error("el formulario de perfil no apunta a la ruta de esa sesión")
	}
}

// TestSesiones_SinNumeroPropioLaEtiquetaEsElIdentificador es el gemelo del anterior por la rama que
// aquel no recorre: la plataforma sirve `self_pn` con `omitempty`, así que una sesión recién
// emparejada puede no traerlo. Sin esta rama la celda saldría vacía y la fila no se podría identificar.
func TestSesiones_SinNumeroPropioLaEtiquetaEsElIdentificador(t *testing.T) {
	t.Parallel()
	rutas := sessionsOK()
	rutas["GET /api/v1/sessions"] = stubResponse{http.StatusOK, sesionesBody(
		`{"session_id":"` + testOtherSession + `","edge_id":"edge-beta","state":"online","profile":"passive"}`)}
	api := newStubAPI(t, rutas)

	out := getWithSession(t, adminRouter(api), "/sesiones").Body.String()
	if !strings.Contains(out, testOtherSession) {
		t.Error("sin número propio, la celda tiene que enseñar el identificador de sesión")
	}
	if !strings.Contains(out, `<option value="passive" selected>pasiva</option>`) {
		t.Error("el desplegable no preselecciona el perfil pasiva que la plataforma reportó")
	}
}

// TestSesiones_UnPerfilDesconocidoNoSePintaComoActiva (T2.1, criterio 2) — EL INVARIANTE.
//
// `EffectiveProfile` devuelve "" para un perfil ausente, desconocido o basura, y su test vive en
// internal/apiclient. Este es el HERMANO de pantalla, y hace falta porque la mitad peligrosa ocurre en
// el HTML: un <select> sin ninguna opción `selected` NO se pinta vacío — el navegador enseña la
// PRIMERA, que aquí sería «activa». La dueña leería «esta sesión conversa sola» sobre una sesión de la
// que no sabemos nada, y un clic en «Aplicar» la activaría de verdad.
//
// Las tres formas de no saber van en la misma tabla porque el fallo sería el mismo en las tres y una
// sola no probaría la regla: solo el caso `bot` distingue «normaliza» de «pasa el campo tal cual».
func TestSesiones_UnPerfilDesconocidoNoSePintaComoActiva(t *testing.T) {
	t.Parallel()

	casos := map[string]string{
		"sin el campo":            `{"session_id":"s-x","edge_id":"e1","state":"online"}`,
		"vocabulario viejo (bot)": `{"session_id":"s-x","edge_id":"e1","state":"online","profile":"bot"}`,
		"castellano":              `{"session_id":"s-x","edge_id":"e1","state":"online","profile":"activa"}`,
	}
	for nombre, fila := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			rutas := sessionsOK()
			rutas["GET /api/v1/sessions"] = stubResponse{http.StatusOK, sesionesBody(fila)}
			api := newStubAPI(t, rutas)

			rec := getWithSession(t, adminRouter(api), "/sesiones")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200. Body: %s", rec.Code, rec.Body.String())
			}
			out := rec.Body.String()

			if strings.Contains(out, `<option value="active" selected>`) {
				t.Error("un perfil desconocido NO puede preseleccionar «activa»: activaría una sesión que nadie activó")
			}
			if strings.Contains(out, `<option value="passive" selected>`) {
				t.Error("un perfil desconocido tampoco puede afirmar «pasiva»: la plataforma no lo dijo")
			}
			if !strings.Contains(out, `<option value="" selected disabled>— sin dato —</option>`) {
				t.Error("sin dato, el desplegable tiene que decirlo en vez de dejar que el navegador elija la primera opción")
			}
		})
	}
}

// TestSesiones_PintaLaSaludDelClasificador cubre la columna «Clasificador» (Plan 051 · Ola 4 · T4.3):
// se tiene que poder responder «¿está clasificando?» y «¿se estorban el cajero y Ollama?» sin entrar
// en la máquina.
//
// Los tres casos van juntos a propósito, porque el que importa solo se ve por CONTRASTE: la sesión sin
// los campos tiene que pintar «desconocido». Un test que solo mirase la fila poblada seguiría verde
// con la plantilla pintando "closed" por defecto.
func TestSesiones_PintaLaSaludDelClasificador(t *testing.T) {
	t.Parallel()
	rutas := sessionsOK()
	rutas["GET /api/v1/sessions"] = stubResponse{http.StatusOK, sesionesBody(
		`{"session_id":"s-ok","edge_id":"e1","state":"online","profile":"active",`+
			`"intent_circuit":"closed","worker_taskset":"disjunta"}`,
		`{"session_id":"s-roto","edge_id":"e2","state":"offline","profile":"active",`+
			`"intent_circuit":"open","worker_taskset":"solapada"}`,
		`{"session_id":"s-mudo","edge_id":"e3","state":"online","profile":"active"}`)}
	api := newStubAPI(t, rutas)

	out := getWithSession(t, adminRouter(api), "/sesiones").Body.String()

	for _, want := range []string{">closed<", ">CPU disjunta<", ">open<", ">CPU solapada<", ">offline<"} {
		if !strings.Contains(out, want) {
			t.Errorf("la tabla debía contener %q", want)
		}
	}

	// 🔴 EL CASO QUE DECIDE. `s-mudo` no manda ninguno de los dos campos, y eso NO significa «sano»:
	// el Edge manda su cero a propósito cuando el parte del worker-cajero lleva más de 90 s sin
	// refrescarse, o sea cuando el cajero puede estar MUERTO.
	if !strings.Contains(out, ">desconocido<") || !strings.Contains(out, ">CPU desconocida<") {
		t.Error("una sesión sin intent_circuit/worker_taskset debe pintarse «desconocido», no un valor sano")
	}
	if !strings.Contains(out, "NO significa que esté sano") {
		t.Error("el chip desconocido tiene que explicar que la ausencia de dato no es salud")
	}
}

// TestSesiones_AvisaQueElRepartoDeCPUEsDelArranque (portado de T4.6 del Plan 051, por su salida (b)).
//
// El veredicto del `taskset` NO se recalcula: el Edge lo mide una vez, al arrancar el cajero
// (`veredictoTaskset` se escribe en `Run`, antes del bucle), y lo republica igual en cada parte. Un
// cambio de afinidad en caliente deja esta pantalla enseñando un reparto que ya no existe, y la regla
// de rancidez no puede cazarlo porque el parte SÍ se refresca: es obsoleto, no rancio.
//
// Cuenta OCURRENCIAS a propósito: comprobar «aparece la frase» seguiría verde con dos de los tres
// chips desprovistos del aviso, y quien mira «CPU solapada» para decidir si toca un taskset es justo
// quien más necesita saber que tendrá que reiniciar el cajero para ver el resultado.
func TestSesiones_AvisaQueElRepartoDeCPUEsDelArranque(t *testing.T) {
	t.Parallel()
	const aviso = "al arrancar el cajero, y NO se recalcula"

	rutas := sessionsOK()
	rutas["GET /api/v1/sessions"] = stubResponse{http.StatusOK, sesionesBody(
		`{"session_id":"s-a","edge_id":"e1","state":"online","profile":"active",`+
			`"intent_circuit":"closed","worker_taskset":"disjunta"}`,
		`{"session_id":"s-b","edge_id":"e2","state":"online","profile":"active",`+
			`"intent_circuit":"closed","worker_taskset":"solapada"}`,
		`{"session_id":"s-c","edge_id":"e3","state":"online","profile":"active",`+
			`"intent_circuit":"closed","worker_taskset":"cajero_sin_confinar"}`)}
	api := newStubAPI(t, rutas)

	out := getWithSession(t, adminRouter(api), "/sesiones").Body.String()

	if got := strings.Count(out, aviso); got != 3 {
		t.Errorf("los TRES chips de reparto de CPU deben avisar de que el veredicto es del arranque; "+
			"encontrado %d veces, esperado 3", got)
	}

	// El breaker SÍ es continuo: si el aviso se colara en su tooltip diría una mentira distinta.
	i := strings.Index(out, ">closed<")
	if i < 0 {
		t.Fatal("la fila debía pintar el chip del breaker cerrado")
	}
	if inicio := strings.LastIndex(out[:i], "<span"); inicio >= 0 && strings.Contains(out[inicio:i], aviso) {
		t.Error("el aviso del arranque es del reparto de CPU, no del breaker: el breaker sí se refresca")
	}
}

// TestSesiones_NoPrometeMasPrivacidadDeLaQueSeEntrega.
//
// 🔴 ESTE TEST NO ES EL DEL BFF, y la diferencia es el hallazgo de esta tarea. El del BFF
// (TestDashboardNoPrometeLaPrivacidadQueAunNoEntrega) exige que la pantalla diga que el descarte de
// entrantes «todavía no está disponible» y que lo que le escriban a una pasiva «sigue llegando a la
// nube». Eso era verdad hasta la Ola 2 del Plan 046; HOY ES FALSO — el Edge corta el entrante de una
// sesión pasiva en la puerta del listener, el plan está cerrado y desplegado, y el propio comentario
// del BFF decía «QUÍTALA CUANDO LA OLA 2 DEL PLAN 046 ESTÉ EN CAMPO».
//
// Lo que se conserva es el CRITERIO —no prometer una privacidad que no se entrega—, aplicado a lo que
// hoy se cumple. De ahí las dos mitades:
//   - positiva: la pantalla dice qué pasa de verdad con los entrantes de una pasiva;
//   - negativa: la pantalla NO arrastra el texto viejo. Es el aserto que se cae el día en que alguien
//     copie y pegue el dashboard del BFF al retirarlo, que es exactamente el riesgo de la tanda que
//     viene.
func TestSesiones_NoPrometeMasPrivacidadDeLaQueSeEntrega(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, sessionsOK())
	out := getWithSession(t, adminRouter(api), "/sesiones").Body.String()

	// Positiva: lo que hoy SÍ se entrega, dicho con sus tres verbos.
	for _, frase := range []string{"se descarta en tu propio equipo", "no sube a la nube"} {
		if !strings.Contains(out, frase) {
			t.Errorf("la pantalla no dice qué pasa con los entrantes de una sesión pasiva: falta %q", frase)
		}
	}
	// Y el matiz que sigue siendo verdad: el perfil viaja como configuración, no es instantáneo.
	if !strings.Contains(out, "hasta que llega, la sesión sigue comportándose como estaba") {
		t.Error("falta el matiz de que el cambio de perfil viaja al equipo y no es instantáneo")
	}

	// Negativa: el texto VIEJO del BFF, que hoy sería mentira en sentido contrario.
	for _, obsoleto := range []string{"todavía no está disponible", "sigue llegando a la nube"} {
		if strings.Contains(out, obsoleto) {
			t.Errorf("la pantalla arrastra el aviso obsoleto del BFF (%q): el descarte en el equipo YA existe "+
				"desde la Ola 2 del Plan 046", obsoleto)
		}
	}
}

// TestSesiones_EnviarLlamaAMessagesConLosCamposYMuestraElAcuse (T2.1, criterio 3).
//
// Dos mitades que se necesitan: lo que SALE por el cable (la ruta, el método y los tres campos con su
// nombre json) y lo que VUELVE a la pantalla (el aviso y el identificador del comando). La primera no
// se puede ver en el HTML y la segunda no se puede ver en la petición.
func TestSesiones_EnviarLlamaAMessagesConLosCamposYMuestraElAcuse(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, sessionsOK())
	router := adminRouter(api)

	rec := postFormWithCSRF(router, "/sesiones/enviar", url.Values{
		"session_id": {testSessionID},
		"to":         {"+593990000002"},
		"text":       {"hola"},
	}, clientSessionCookie(t))

	destino := redirectTarget(t, rec)
	if !strings.HasPrefix(destino, "/sesiones?success="+flashMessageSent) {
		t.Fatalf("Location = %q, want /sesiones con el aviso de mensaje aceptado", destino)
	}

	// Lo que salió por el cable.
	req := api.Last(t, "POST /api/v1/messages")
	for _, campo := range []string{
		`"session_id":"` + testSessionID + `"`,
		`"to":"+593990000002"`,
		`"text":"hola"`,
	} {
		if !strings.Contains(req.Body, campo) {
			t.Errorf("el cuerpo del envío no lleva %s. Body: %s", campo, req.Body)
		}
	}

	// Lo que vuelve a la pantalla: el aviso y el acuse.
	out := getWithSession(t, router, destino).Body.String()
	if !strings.Contains(out, "aceptó el mensaje") {
		t.Errorf("la pantalla no confirma que el equipo aceptó el mensaje. Body: %s", out)
	}
	if !strings.Contains(out, "cmd-abc123") {
		t.Error("la pantalla no pinta el identificador del comando acusado por el equipo")
	}
}

// TestSesiones_UnEnvioIncompletoNiSiquieraSaleALaRed: los tres campos son obligatorios y se comprueban
// antes de la red. La plataforma respondería 400 igual, pero el usuario habría esperado el viaje
// entero para leer lo mismo.
func TestSesiones_UnEnvioIncompletoNiSiquieraSaleALaRed(t *testing.T) {
	t.Parallel()

	casos := map[string]url.Values{
		"sin sesión":  {"to": {"+1"}, "text": {"hola"}},
		"sin destino": {"session_id": {testSessionID}, "text": {"hola"}},
		"sin texto":   {"session_id": {testSessionID}, "to": {"+1"}},
		"solo blancos": {"session_id": {testSessionID}, "to": {"+1"},
			"text": {"   "}},
	}
	for nombre, form := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			api := newStubAPI(t, sessionsOK())
			rec := postFormWithCSRF(adminRouter(api), "/sesiones/enviar", form, clientSessionCookie(t))

			if destino := redirectTarget(t, rec); destino != "/sesiones?error="+flashMissingField {
				t.Fatalf("Location = %q, want el aviso de campos incompletos", destino)
			}
			if api.Called("POST /api/v1/messages") {
				t.Error("un formulario incompleto salió a la red")
			}
		})
	}
}

// TestSesiones_ElAcuseOkFalseNoSePintaComoExito.
//
// 🔴 El caso que este test existe para fijar: la plataforma responde 200 y el envío FALLÓ. El 200 es
// el acuse del equipo, y el acuse puede traer `ok:false` —el equipo recibió el comando y su ejecución
// falló—. Un handler que mire solo el código HTTP le diría a la dueña que el mensaje salió cuando no
// salió, y no hay ningún rojo que lo delate.
func TestSesiones_ElAcuseOkFalseNoSePintaComoExito(t *testing.T) {
	t.Parallel()
	rutas := sessionsOK()
	rutas["POST /api/v1/messages"] = stubResponse{http.StatusOK,
		`{"acked_command_id":"cmd-fallido","ok":false,"error":"no such recipient"}`}
	api := newStubAPI(t, rutas)
	router := adminRouter(api)

	rec := postFormWithCSRF(router, "/sesiones/enviar", url.Values{
		"session_id": {testSessionID}, "to": {"+1"}, "text": {"hola"},
	}, clientSessionCookie(t))

	destino := redirectTarget(t, rec)
	if destino != "/sesiones?error="+flashSendNotDelivered {
		t.Fatalf("Location = %q, want el aviso de «recibido pero no entregado»", destino)
	}
	out := getWithSession(t, router, destino).Body.String()
	if !strings.Contains(out, "no pudo entregarlo") {
		t.Error("la pantalla no dice que el equipo recibió el mensaje y no pudo entregarlo")
	}
	// El detalle del upstream se queda en el log, como en el resto de la consola.
	if strings.Contains(out, "no such recipient") {
		t.Error("el detalle del upstream acabó en pantalla")
	}
}

// TestSesiones_CadaDesenlaceDelEnvioTieneSuAviso.
//
// Los dos que decide este test son el 502 y el 504, porque son los que el traductor GENÉRICO se
// comería: los dos llegan como *APIError sin sentinela y caerían al «no se pudo completar la
// operación», que es justo el texto que no ayuda. El 502 es accionable («está desconectado, espera») y
// el 504 es peligroso (el comando ya salió: repetirlo puede duplicar el mensaje a un cliente real).
func TestSesiones_CadaDesenlaceDelEnvioTieneSuAviso(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre string
		status int
		code   string
		frase  string
	}{
		{"datos rechazados", http.StatusBadRequest, flashInvalidInput, "rechazó los datos"},
		{"sesión de otra empresa", http.StatusNotFound, flashSessionNotYours, "no es de tu empresa"},
		{"teléfono desconectado", http.StatusBadGateway, flashSessionOffline, "está desconectado"},
		{"acuse a destiempo", http.StatusGatewayTimeout, flashSendTimeout, "ANTES de repetirlo"},
		{"fallo interno", http.StatusInternalServerError, flashUpstreamUnavailable, "No se pudo completar"},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			rutas := sessionsOK()
			rutas["POST /api/v1/messages"] = stubResponse{caso.status, `{"error":"detalle interno"}`}
			api := newStubAPI(t, rutas)
			router := adminRouter(api)

			rec := postFormWithCSRF(router, "/sesiones/enviar", url.Values{
				"session_id": {testSessionID}, "to": {"+1"}, "text": {"hola"},
			}, clientSessionCookie(t))

			destino := redirectTarget(t, rec)
			if destino != "/sesiones?error="+caso.code {
				t.Fatalf("Location = %q, want /sesiones?error=%s", destino, caso.code)
			}
			out := getWithSession(t, router, destino).Body.String()
			if !strings.Contains(out, caso.frase) {
				t.Errorf("el aviso no dice %q. Body: %s", caso.frase, out)
			}
			if strings.Contains(out, "detalle interno") {
				t.Error("el cuerpo del upstream acabó en pantalla")
			}
		})
	}
}

// TestSesiones_ElPerfilInvalidoYElVacioNoSalenALaRed (T2.1, criterio 4).
//
// El caso que manda es el VACÍO: es el `value` del <option> placeholder de «sin dato», así que es
// exactamente lo que llega si alguien fuerza ese envío. Si la consola lo dejara pasar, un perfil
// DESCONOCIDO se convertiría en una llamada a la plataforma que nadie pidió — y el placeholder, que
// existe para que ante la duda no se active nada, dejaría de ser seguro.
func TestSesiones_ElPerfilInvalidoYElVacioNoSalenALaRed(t *testing.T) {
	t.Parallel()

	// ⚠️ « active » CON ESPACIOS no está en la tabla, y su ausencia es la afirmación: formValue recorta
	// antes de validar —como en toda esta consola—, así que eso es un `active` legítimo y se acepta. Un
	// caso así aquí daría rojo sobre un comportamiento correcto.
	casos := map[string]string{
		"vacío (el placeholder «sin dato»)": "",
		"vocabulario viejo":                 "bot",
		"castellano":                        "activa",
		"mayúsculas":                        "ACTIVE",
		"con basura pegada":                 "active;passive",
	}
	for nombre, perfil := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			api := newStubAPI(t, sessionsOK())
			router := adminRouter(api)

			rec := postFormWithCSRF(router, "/sesiones/"+testSessionID+"/perfil",
				url.Values{"profile": {perfil}}, clientSessionCookie(t))

			destino := redirectTarget(t, rec)
			if destino != "/sesiones?error="+flashInvalidProfile {
				t.Fatalf("Location = %q, want el aviso de perfil inválido", destino)
			}
			// No se compara contra una ruta concreta: se afirma que NO SALIÓ NADA. Un aserto sobre
			// «no se llamó a POST /api/v1/sessions/{id}/profile» sería vacuo si el patrón se escribe
			// distinto de la ruta real, y aquí lo que importa es que la red no se tocó.
			if n := len(api.Requests()); n != 0 {
				t.Errorf("un perfil inválido produjo %d peticiones: %v", n, routesOf(api.Requests()))
			}
			out := getWithSession(t, router, destino).Body.String()
			if !strings.Contains(out, "activa o pasiva") {
				t.Error("el aviso no dice cuáles son los perfiles válidos")
			}
		})
	}
}

// TestSesiones_CambiarElPerfilViajaConElIdentificadorDelCable.
//
// El gemelo POSITIVO del anterior, y sin él aquel sería vacuo: si el cambio de perfil no funcionara
// nunca, «el inválido no sale a la red» pasaría solo. Afirma las tres cosas que solo se ven en la
// petición saliente —ruta con la sesión, cuerpo con el identificador EN INGLÉS— y la que solo se ve en
// la pantalla: el aviso nombra el perfil que quedó puesto.
func TestSesiones_CambiarElPerfilViajaConElIdentificadorDelCable(t *testing.T) {
	t.Parallel()

	casos := []struct {
		perfil string
		code   string
		frase  string
	}{
		{"passive", flashProfilePassive, "PASIVA"},
		{"active", flashProfileActive, "ACTIVA"},
	}
	for _, caso := range casos {
		t.Run(caso.perfil, func(t *testing.T) {
			t.Parallel()
			api := newStubAPI(t, sessionsOK())
			router := adminRouter(api)

			rec := postFormWithCSRF(router, "/sesiones/"+testSessionID+"/perfil",
				url.Values{"profile": {caso.perfil}}, clientSessionCookie(t))

			destino := redirectTarget(t, rec)
			if destino != "/sesiones?success="+caso.code {
				t.Fatalf("Location = %q, want /sesiones?success=%s", destino, caso.code)
			}

			// La ruta se nombra CONCRETA, no con el patrón del doble: capturedRequest.Route()
			// devuelve el path que llegó de verdad, y buscar el patrón nunca casaría.
			req := api.Last(t, "POST /api/v1/sessions/"+testSessionID+"/profile")
			if !strings.Contains(req.Body, `"profile":"`+caso.perfil+`"`) {
				t.Errorf("el cuerpo no lleva el identificador del cable: %s", req.Body)
			}

			out := getWithSession(t, router, destino).Body.String()
			if !strings.Contains(out, caso.frase) {
				t.Errorf("el aviso no dice a qué perfil quedó cambiada la sesión (%q)", caso.frase)
			}
		})
	}
}

// TestSesiones_SinCookieLasTresRutasRedirigenALogin (T2.1, criterio 6): las tres cuelgan del grupo
// protegido, así que ninguna llega a rozar la API pública sin sesión.
func TestSesiones_SinCookieLasTresRutasRedirigenALogin(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, sessionsOK())
	router := adminRouter(api)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sesiones", nil))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Errorf("GET /sesiones sin cookie: %d %q, want 303 a /login", rec.Code, rec.Header().Get("Location"))
	}

	for _, caso := range []struct {
		ruta string
		form url.Values
	}{
		{"/sesiones/enviar", url.Values{"session_id": {testSessionID}, "to": {"+1"}, "text": {"hola"}}},
		{"/sesiones/" + testSessionID + "/perfil", url.Values{"profile": {"passive"}}},
	} {
		rec := postFormWithCSRF(router, caso.ruta, caso.form, nil)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
			t.Errorf("POST %s sin cookie: %d %q, want 303 a /login",
				caso.ruta, rec.Code, rec.Header().Get("Location"))
		}
	}

	if len(api.Requests()) != 0 {
		t.Errorf("sin sesión salieron %d peticiones a la API: %v",
			len(api.Requests()), routesOf(api.Requests()))
	}
}

// TestSesiones_DegradaSiElListadoFalla: la pantalla sigue sirviendo 200 con el aviso arriba y el
// formulario pidiendo la sesión A MANO. No es un adorno: enviar es justo lo que alguien necesita
// cuando algo va mal, y un desplegable vacío parecería un fallo del formulario.
func TestSesiones_DegradaSiElListadoFalla(t *testing.T) {
	t.Parallel()
	rutas := sessionsOK()
	rutas["GET /api/v1/sessions"] = stubResponse{http.StatusInternalServerError, `{"error":"detalle interno"}`}
	api := newStubAPI(t, rutas)

	rec := getWithSession(t, adminRouter(api), "/sesiones")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (degradado)", rec.Code)
	}
	out := rec.Body.String()

	if !strings.Contains(out, `id="sessions-degradado"`) {
		t.Error("sin listado debía explicarse por qué la tabla no está")
	}
	if !strings.Contains(out, "No se pudo completar la operación") {
		t.Error("falta el aviso del fallo")
	}
	if strings.Contains(out, "detalle interno") {
		t.Error("el cuerpo del upstream acabó en pantalla")
	}
	// El formulario sigue en pie, y con el campo de texto en vez del desplegable.
	if !strings.Contains(out, `id="form-enviar"`) {
		t.Fatal("el formulario de envío desapareció con el listado")
	}
	if !strings.Contains(out, `type="text" id="session_id"`) {
		t.Error("sin listado, la sesión de salida tiene que poder escribirse a mano")
	}
}

// TestSesiones_SinNingunaSesionSeDiceDondeSeEmpareja: el vacío no es un fallo, y la pantalla no puede
// dejarlo mudo — no hay botón de emparejar aquí, porque eso vive en el equipo del cliente.
func TestSesiones_SinNingunaSesionSeDiceDondeSeEmpareja(t *testing.T) {
	t.Parallel()
	rutas := sessionsOK()
	rutas["GET /api/v1/sessions"] = stubResponse{http.StatusOK, `[]`}
	api := newStubAPI(t, rutas)

	out := getWithSession(t, adminRouter(api), "/sesiones").Body.String()
	if !strings.Contains(out, `id="sessions-vacio"`) {
		t.Fatal("sin sesiones debía pintarse el estado vacío")
	}
	if !strings.Contains(out, "en tu propio equipo") {
		t.Error("el estado vacío no dice dónde se empareja un teléfono")
	}
}

// TestSesiones_UnA401QueSobreviveAlRefrescoExpulsaALogin: withAuthRetry refresca UNA vez; si el
// segundo intento también es 401, la sesión ya no vale y la pantalla no sirve de nada.
func TestSesiones_UnA401QueSobreviveAlRefrescoExpulsaALogin(t *testing.T) {
	t.Parallel()
	rutas := sessionsOK()
	rutas["GET /api/v1/sessions"] = stubResponse{http.StatusUnauthorized, ""}
	api := newStubAPI(t, rutas)

	rec := getWithSession(t, adminRouter(api), "/sesiones")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 a /login", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/login?error="+flashSessionExpired {
		t.Errorf("Location = %q, want /login con el aviso de sesión caducada", got)
	}
}

// TestSesiones_SinEmpresaExplicaEnVezDeLlamar: se puede llegar por la URL aunque la barra no ofrezca
// el enlace. Sin empresa el token sale sin un solo grant y la API respondería 403 —«no tienes
// permiso»—, que MIENTE sobre la causa: no le falta un permiso, le falta una empresa. La respuesta ya
// se sabe, así que ni se pregunta.
func TestSesiones_SinEmpresaExplicaEnVezDeLlamar(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, sessionsOK())

	rec := getConCookie(adminRouter(api), "/sesiones", sessionCookieFor(t, testUserID, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /sesiones sin empresa status = %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	if !strings.Contains(out, `id="section-sin-empresa"`) {
		t.Error("la pantalla no explica que todavía no hay empresa")
	}
	if strings.Contains(out, "no tiene permiso") || strings.Contains(out, "no tienes permiso") {
		t.Error("se acusa de falta de permiso a quien lo que no tiene es empresa")
	}
	if n := len(api.Requests()); n != 0 {
		t.Errorf("sin empresa se hicieron %d llamadas (%v); la respuesta ya se sabe", n, routesOf(api.Requests()))
	}
}

// TestSesiones_ElAcuseDeLaURLSoloSePintaSiEsUnIdentificador.
//
// El acuse es lo ÚNICO de esta consola que llega por el query string y acaba a la vista, así que es la
// única puerta por la que alguien podría meter texto propio en la pantalla con un enlace enviado por
// correo. El escapado de la plantilla ya impide inyectar HTML; esto impide inyectar FRASES.
//
// El positivo va en el mismo test para que el negativo no sea vacuo: si el chip no se pintara nunca,
// «no pinta lo que le pongan» pasaría solo.
func TestSesiones_ElAcuseDeLaURLSoloSePintaSiEsUnIdentificador(t *testing.T) {
	t.Parallel()
	api := newStubAPI(t, sessionsOK())
	router := adminRouter(api)

	// Positivo: un identificador de comando con la forma que emite la plataforma.
	out := getWithSession(t, router, "/sesiones?success="+flashMessageSent+"&ack=cmd-abc_123").Body.String()
	if !strings.Contains(out, `id="chip-ack"`) || !strings.Contains(out, "cmd-abc_123") {
		t.Fatal("un acuse con forma de identificador no se pintó: el negativo de abajo sería vacuo")
	}

	// Negativo: cualquier otra cosa se descarta entera, sin pintar nada.
	for _, basura := range []string{
		"tu+cuenta+ha+sido+bloqueada",
		"cmd/../otro",
		"cmd abc",
		strings.Repeat("c", 65),
	} {
		out := getWithSession(t, router,
			"/sesiones?success="+flashMessageSent+"&ack="+url.QueryEscape(basura)).Body.String()
		if strings.Contains(out, `id="chip-ack"`) {
			t.Errorf("el acuse %q se pintó: solo se admite un identificador de comando", basura)
		}
	}
}

// TestAckSeguro fija el filtro del acuse en su propia tabla, que es donde se ve el borde exacto: 64
// caracteres pasan y 65 no. Por la pantalla ese límite solo se puede tantear.
func TestAckSeguro(t *testing.T) {
	t.Parallel()

	for _, bueno := range []string{"cmd-1", "CMD_ABC-123", "0", strings.Repeat("c", 64)} {
		if got := ackSeguro(bueno); got != bueno {
			t.Errorf("ackSeguro(%q) = %q, esperaba que pasara entero", bueno, got)
		}
	}
	for _, malo := range []string{"", "cmd 1", "cmd/1", "cmd.1", "cmd<b>", "cmd\r\nSet-Cookie: x",
		strings.Repeat("c", 65)} {
		if got := ackSeguro(malo); got != "" {
			t.Errorf("ackSeguro(%q) = %q, esperaba cadena vacía", malo, got)
		}
	}
}
