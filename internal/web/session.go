package web

import (
	"time"

	"github.com/EduGoGroup/wapp-client-console/internal/config"
	sharedjwt "github.com/EduGoGroup/wapp-shared/auth/jwt"
	sharedweb "github.com/EduGoGroup/wapp-shared/web"
	"github.com/golang-jwt/jwt/v5"
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

// consoleWorkday es la jornada de trabajo de la consola y la vida de sus DOS cookies. Ninguna se
// deja al default del módulo (1 h la de sesión, 12 h la de CSRF): 24 h iguala lo que sirve la
// consola de plataforma.
//
// Se declara UNA vez y las dos constantes de abajo se derivan de ella porque los dos APIs piden
// unidades distintas —`web.CSRFOptions.MaxAge` es un time.Duration y `webgin.SetSessionCookie`
// quiere segundos—, y dos números escritos a mano en dos unidades es exactamente lo que se
// desincroniza en cuanto alguien cambia uno.
const consoleWorkday = 24 * time.Hour

const (
	csrfCookieMaxAge    = consoleWorkday
	sessionCookieMaxAge = int(consoleWorkday / time.Second)
)

// sessionCookieOptions es la política de la cookie de sesión de la consola: la que emite el login
// (ver auth_handler.go) y la que el AuthMiddleware lee en cada petición. Es el punto exacto donde se
// heredaría el default del módulo; el test la ejercita contra sharedweb.SessionCookie.
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

// unverifiedParser lee el Context Token SIN verificar la firma: quien la valida de verdad es la
// plataforma en cada llamada a :8103. Aquí solo se necesita el `exp` (para decidir si la sesión
// sigue viva o toca refrescarla) y el usuario/empresa que las pantallas muestran.
var unverifiedParser = jwt.NewParser()

// parseAccessClaims decodifica los claims del access token. Se queda en la consola a propósito:
// `wapp-shared/web` no importa ninguna librería de JWT —recibe el `exp` ya extraído— y esa frontera
// es lo que lo mantiene en el nivel 0.
func parseAccessClaims(accessToken string) (*sharedjwt.Claims, error) {
	var claims sharedjwt.Claims
	if _, _, err := unverifiedParser.ParseUnverified(accessToken, &claims); err != nil {
		return nil, err
	}
	return &claims, nil
}

// accessExpiry traduce el `exp` de los claims a lo que esperan web.SessionValid y web.RefreshDue.
// Un token sin `exp` devuelve nil, que el módulo trata como sesión inválida y refresco debido.
func accessExpiry(claims *sharedjwt.Claims) *time.Time {
	if claims == nil || claims.ExpiresAt == nil {
		return nil
	}
	exp := claims.ExpiresAt.Time
	return &exp
}
