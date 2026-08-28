package apiclient

import "testing"

// TestEffectiveProfileNuncaInventaActive fija el contrato de EffectiveProfile por su CONSECUENCIA, no
// por su implementación: el valor que devuelve decide qué perfil ve la dueña preseleccionado en el
// desplegable, y "active" es el que hace que la sesión conteste sola.
//
// Por eso el caso que manda es el ÚLTIMO: sin un `profile` utilizable NO se cae a "active". Caerse
// ahí sería enseñarle a la dueña que una sesión de la que no sabemos nada «conversa sola», y un clic
// en «Aplicar» la activaría de verdad. Ante la duda, DESCONOCIDO ("") — y sesiones.html lo pinta como
// «— sin dato —», porque un <select> sin `selected` enseña la primera opción, no ninguna.
//
// 📌 Portado del BFF (wapp-guardian-bff/internal/apiclient/dashboard_test.go) con su tabla intacta,
// incluido `bot`: es el vocabulario que la plataforma emitía ANTES de la migración 0064, y si alguien
// lo reintrodujera por error NO puede colarse como «activa».
func TestEffectiveProfileNuncaInventaActive(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre  string
		sesion  Session
		esperar string
	}{
		// El camino normal.
		{"profile active", Session{Profile: ProfileActive}, ProfileActive},
		{"profile passive", Session{Profile: ProfilePassive}, ProfilePassive},

		// Basura en el único eje que hay NO se propaga a la vista.
		{"profile desconocido (el vocabulario viejo)", Session{Profile: "bot"}, ""},
		{"profile desconocido (castellano)", Session{Profile: "activa"}, ""},
		{"profile desconocido (mayúsculas)", Session{Profile: "ACTIVE"}, ""},

		// 🔴 El caso que protege a la dueña: no hay dato ⇒ no hay perfil, y NO es "active".
		{"vacío", Session{}, ""},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			t.Parallel()
			if got := c.sesion.EffectiveProfile(); got != c.esperar {
				t.Errorf("EffectiveProfile() = %q, esperaba %q", got, c.esperar)
			}
		})
	}
}

// TestValidProfileSoloAceptaLosDosDelCable es el gemelo del de arriba por el otro lado: lo que la
// consola deja SALIR hacia la plataforma.
//
// El caso que decide es la cadena vacía. Es el `value` del <option> placeholder de «sin dato», así
// que es exactamente lo que llega si alguien fuerza el envío del formulario con el perfil
// desconocido; si esto lo diera por bueno, el placeholder dejaría de ser seguro y el rebote que hoy
// hace la consola se lo comería la plataforma con un 400 —o, peor, algún día con un default—.
func TestValidProfileSoloAceptaLosDosDelCable(t *testing.T) {
	t.Parallel()

	for _, bueno := range []string{ProfileActive, ProfilePassive} {
		if !ValidProfile(bueno) {
			t.Errorf("ValidProfile(%q) = false, y es uno de los dos del contrato", bueno)
		}
	}
	for _, malo := range []string{"", " ", "bot", "activa", "pasiva", "ACTIVE", "active "} {
		if ValidProfile(malo) {
			t.Errorf("ValidProfile(%q) = true: la plataforma solo acepta active|passive", malo)
		}
	}
}
