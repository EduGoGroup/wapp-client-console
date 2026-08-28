package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	sharedweb "github.com/EduGoGroup/wapp-shared/web"
)

// TestCookieNames_SonLasDeEstaConsolaYNoLasDeLosOtrosDosPerimetros custodia el punto más frágil de
// consumir el middleware compartido: en `wapp-shared/web` el nombre de cookie es un PARÁMETRO, no
// una constante de paquete. Somos el TERCER consumidor —tras el BFF del cliente y la consola de
// plataforma—, y si esta consola se olvidara de pasarlo, el módulo caería a sus valores por defecto
// (`wapp_csrf` / `wapp_session`, que son EXACTAMENTE los del BFF) y las superficies del ecosistema
// se pisarían la cookie en el mismo navegador. Compilaría igual: nadie avisa.
//
// El test afirma los LITERALES, no las constantes del paquete: comprobar `c.Name == csrfCookieName`
// pasaría igual con la constante cambiada, que es justo la regresión que hay que cazar.
//
// Todavía no hay login, así que se ejercita lo que existe:
//   - la SIEMBRA de la cookie CSRF, sobre la cadena global de middleware (ver más abajo);
//   - la cookie de SESIÓN, construida con las opciones de esta consola a través del propio módulo,
//     que es el camino exacto por el que se emitirá cuando el login aterrice.
func TestCookieNames_SonLasDeEstaConsolaYNoLasDeLosOtrosDosPerimetros(t *testing.T) {
	t.Parallel()

	// Los nombres de los otros dos perímetros y los defaults del módulo: ninguno puede salir de aquí.
	prohibidos := map[string]string{
		"wapp_csrf":             "es la cookie CSRF del BFF del cliente Y el default del módulo",
		"wapp_session":          "es la cookie de sesión del BFF del cliente Y el default del módulo",
		"wapp_guardian_csrf":    "es del BFF del cliente",
		"wapp_guardian_session": "es del BFF del cliente",
		"wapp_platform_csrf":    "es de la consola de plataforma (perímetro de operadores)",
		"wapp_platform_session": "es de la consola de plataforma (perímetro de operadores)",
	}

	// Guardia sobre el módulo: si `wapp-shared/web` cambiara sus defaults, este assert avisa el
	// primero, antes de que un consumidor descuidado herede un nombre nuevo sin enterarse.
	if sharedweb.DefaultCSRFCookieName != "wapp_csrf" || sharedweb.DefaultSessionCookieName != "wapp_session" {
		t.Fatalf("los defaults del módulo cambiaron (%q/%q): revisa que sigan sin colisionar con esta consola",
			sharedweb.DefaultCSRFCookieName, sharedweb.DefaultSessionCookieName)
	}

	cfg := testConfig()
	router := NewRouter(cfg)

	vistas := map[string]bool{}
	revisar := func(quien string, rec *httptest.ResponseRecorder) {
		t.Helper()
		for _, c := range rec.Result().Cookies() {
			vistas[c.Name] = true
			if motivo, prohibida := prohibidos[c.Name]; prohibida {
				t.Errorf("%s emitió la cookie %q, que %s: dos perímetros compartirían cookie",
					quien, c.Name, motivo)
			}
		}
	}

	// /healthz se registra ANTES del middleware CSRF a propósito (una sonda de salud no debe
	// recibir Set-Cookie), así que no siembra nada: lo que se comprueba aquí es justo eso.
	recHealth := httptest.NewRecorder()
	router.ServeHTTP(recHealth, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	revisar("GET /healthz", recHealth)
	if len(recHealth.Result().Cookies()) != 0 {
		t.Errorf("GET /healthz emitió cookies (%v); la sonda debe ir por delante del CSRF",
			recHealth.Result().Cookies())
	}

	// Una petición que sí atraviesa la cadena global completa: el CSRF está montado con `Use`, de
	// modo que cubre también el camino sin ruta. Es lo único que hay hasta que aterricen las
	// pantallas, y basta para probar que el nombre sembrado es el nuestro.
	recSeed := httptest.NewRecorder()
	router.ServeHTTP(recSeed, httptest.NewRequest(http.MethodGet, "/ruta-que-no-existe", nil))
	revisar("la cadena global", recSeed)

	if !vistas["wapp_client_csrf"] {
		t.Errorf("la cookie %q no se sembró; cookies vistas: %v", "wapp_client_csrf", vistas)
	}

	// La de sesión aún no la emite ningún handler; se verifica el camino por el que se emitirá:
	// opciones de esta consola → módulo compartido → cookie en el cable.
	sess := sharedweb.SessionCookie(sessionCookieOptions(cfg), "valor-de-prueba", 60)
	if _, prohibida := prohibidos[sess.Name]; prohibida {
		t.Errorf("la cookie de sesión saldría como %q, que es de otro perímetro", sess.Name)
	}
	if sess.Name != "wapp_client_session" {
		t.Errorf("la cookie de sesión saldría como %q, want %q", sess.Name, "wapp_client_session")
	}
	if !sess.HttpOnly {
		t.Error("la cookie de sesión debe ser HttpOnly")
	}
}
