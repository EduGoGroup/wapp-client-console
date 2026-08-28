package web

import (
	"time"

	"github.com/EduGoGroup/wapp-client-console/internal/config"
	sharedweb "github.com/EduGoGroup/wapp-shared/web"
)

// Los nombres de cookie son de ESTA consola, no del módulo: `wapp-shared/web` los expone como
// PARÁMETRO (web.CSRFOptions.CookieName, web.SessionCookieOptions.Name) justo porque las
// superficies web del ecosistema conviven en el mismo navegador y una constante compartida las
// haría pisarse la cookie entre ellas.
//
// 🔴 Somos el TERCER consumidor del módulo. Los defaults (`wapp_session` / `wapp_csrf`) son los del
// BFF del cliente: quien no parametrice los HEREDA EN SILENCIO y compila igual, así que estas dos
// constantes se pasan explícitamente en sessionCookieOptions y csrfOptions, y hay un test
// (cookies_test.go) que verifica por el cable que ninguno de los nombres ajenos sale de aquí.
const (
	sessionCookieName = "wapp_client_session"
	csrfCookieName    = "wapp_client_csrf"
)

// csrfCookieMaxAge es la vida de la cookie CSRF. Se declara y no se deja al default del módulo
// (12 h) para igualar la jornada de trabajo de la consola de plataforma: 24 h.
const csrfCookieMaxAge = 24 * time.Hour

// sessionCookieOptions es la política de la cookie de sesión de la consola.
//
// Todavía no hay login que la emita —eso llega en la tanda siguiente—, pero la política se fija YA:
// es el punto exacto donde se hereda el default del módulo, y el andamiaje existe para que ese
// heredado no pueda ocurrir. El test la ejercita contra sharedweb.SessionCookie.
func sessionCookieOptions(cfg *config.Config) sharedweb.SessionCookieOptions {
	return sharedweb.SessionCookieOptions{
		Name:     sessionCookieName,
		Secure:   cfg.CookieSecure,
		SameSite: cfg.CookieSameSite,
	}
}

// csrfOptions es la política de la cookie CSRF de la consola. El SameSite=Lax y el HttpOnly no
// aparecen aquí porque el módulo los fija SIEMPRE: el fail-safe CSRF no se degrada aunque la cookie
// de sesión se configure de otra forma.
func csrfOptions(cfg *config.Config) sharedweb.CSRFOptions {
	return sharedweb.CSRFOptions{
		CookieName: csrfCookieName,
		MaxAge:     csrfCookieMaxAge,
		Secure:     cfg.CookieSecure,
	}
}
