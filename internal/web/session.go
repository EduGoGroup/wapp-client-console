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
	// invitacionCookieName es la cookie EFÍMERA que lleva el código de una invitación recién emitida
	// del POST al GET que lo enseña. Vive segundos y solo en la pantalla de invitaciones; el nombre es
	// propio de esta consola por lo mismo que los otros dos.
	invitacionCookieName = "wapp_client_invitacion"
	// sugerenciaCookieName es la cookie EFÍMERA que lleva la cotización recién redactada del POST que
	// la pide al GET que la pinta (Plan 047 · T7.6). Vive segundos, solo en la pantalla de ESA
	// solicitud, y la borra el propio GET que la consume.
	sugerenciaCookieName = "wapp_client_sugerencia"
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

// invitacionCookieMaxAge es el TOPE de la cookie del código, no el mecanismo que la retira: quien la
// borra de verdad es el GET que la consume (webgin.TakeOneTimeCookie). Es lo que tarda el navegador
// en seguir un 303, con holgura para una red lenta; si el GET no llega en ese plazo, el código se
// pierde y la invitación —que sigue viva— se anula desde el listado y se emite otra.
const invitacionCookieMaxAge = 60 * time.Second

// invitacionCookieOptions es la política de la cookie efímera del código de invitación.
//
// El Path se acota a la PANTALLA destino y no a la raíz: fuera de /invitaciones el navegador no la
// manda, así que el código no viaja en peticiones que no tienen nada que ver con él. Es la MISMA
// constante con la que se registra la ruta y con la que se redirige tras emitir, y eso es
// deliberado: si el Path y el destino del redirect se escribieran por separado, bastaría tocar uno
// para que el navegador dejara de mandar la cookie (o de borrarla) sin que nada fallara al compilar
// — la pantalla saldría sin el código y solo se vería en producción.
//
// El HttpOnly lo fija el módulo SIEMPRE; Secure y SameSite siguen la misma config que la cookie de
// sesión, porque son política de despliegue de la consola y no de cada pantalla.
//
// El valor NO se cifra ni se firma, y está razonado en el doc de web.OneTimeCookieOptions: el
// destinatario del código es justo quien tiene la cookie, y se le va a pintar en la pantalla dos
// milisegundos después. Lo único que compra la cookie es que el código no pase por la URL.
func invitacionCookieOptions(cfg *config.Config) sharedweb.OneTimeCookieOptions {
	return sharedweb.OneTimeCookieOptions{
		Name:     invitacionCookieName,
		Path:     rutaInvitaciones,
		MaxAge:   invitacionCookieMaxAge,
		Secure:   cfg.CookieSecure,
		SameSite: cfg.CookieSameSite,
	}
}

// sugerenciaCookieMaxAge es el TOPE de vida de la cookie de la cotización, NO el mecanismo que la
// retira: quien la borra de verdad es el GET que la consume (webgin.TakeOneTimeCookie). Es lo que
// tarda el navegador en seguir el 303 que la puso, con holgura para una red lenta.
const sugerenciaCookieMaxAge = 60 * time.Second

// sugerenciaCookieOptions es la política de la cookie efímera de la cotización, acotada a la pantalla
// de UNA solicitud.
//
// 🔴 EL PATH LLEVA EL IDENTIFICADOR A PROPÓSITO, y es la PRIMERA DE DOS CERRADURAS. Con la cookie
// acotada a `/solicitudes/{id}`, el navegador NO la manda a la solicitud de al lado: sin eso, pedir la
// sugerencia de A y abrir B en otra pestaña dentro del minuto siguiente pintaría el texto de A —con
// los precios de A— en la pantalla de B, y ese texto se le manda a un cliente. La SEGUNDA cerradura
// es el identificador que viaja DENTRO del sobre y que el lector compara (ver tomaSugerenciaFlash):
// el Path lo pone el navegador y el identificador lo comprueba el servidor, y hacen falta las dos
// porque una sola se cae con que alguien reescriba la ruta.
//
// 🔑 El Path sale de `solicitudURL`, que es LA MISMA función que compone el destino del 303: el
// navegador identifica una cookie por la terna (dominio, ruta, nombre), así que si el Path y el
// destino del redirect se escribieran por separado bastaría tocar uno para que la cookie dejara de
// llegar —o de borrarse— sin que nada fallara al compilar. Un Path vacío, además, NO se rellena a "/"
// (es deliberado en el módulo), así que equivocarse aquí no ensancha: rompe.
//
// El valor no se cifra ni se firma, por lo razonado en el doc de web.OneTimeCookieOptions: lo que
// viaja es exactamente lo que se le va a pintar en la cara a quien lo pidió, dos milisegundos después.
func sugerenciaCookieOptions(cfg *config.Config, solicitudID string) sharedweb.OneTimeCookieOptions {
	return sharedweb.OneTimeCookieOptions{
		Name:     sugerenciaCookieName,
		Path:     solicitudURL(solicitudID),
		MaxAge:   sugerenciaCookieMaxAge,
		Secure:   cfg.CookieSecure,
		SameSite: cfg.CookieSameSite,
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
