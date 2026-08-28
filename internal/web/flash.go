package web

import sharedweb "github.com/EduGoGroup/wapp-shared/web"

// flash.go es el VOCABULARIO de esta consola: los códigos estables que sus handlers ponen en
// `?error=`/`?success=` al redirigir, y el texto en español que ve el usuario.
//
// El MECANISMO —que el texto salga siempre de la tabla y nunca del query string ni del upstream, y
// que un código desconocido caiga al genérico— vive en `wapp-shared/web`.FlashCatalog. Los códigos
// no suben: dependen de las pantallas de cada consola.
//
// Hoy son dos, los del ciclo de sesión. Se declaran como CONSTANTES y no como literales sueltos
// porque el emisor (auth_handler.go) y la tabla tienen que decir lo mismo: un código que se escribe
// a mano en los dos sitios se desincroniza y el usuario recibe el mensaje genérico sin que nada
// falle. Hay un test que exige que todo código emitido tenga traducción.
const (
	// flashSessionExpired lo emite el AuthMiddleware cuando había cookie de sesión y ya no sirve.
	flashSessionExpired = "session_expired"
	// flashLoggedOut lo emite DoLogout tras cerrar la sesión.
	flashLoggedOut = "logged_out"
)

var (
	// El fallback vacío cae a web.DefaultFlashFallback ("Ocurrió un error inesperado.").
	flashErrors = sharedweb.NewFlashCatalog("", map[string]string{
		flashSessionExpired: "Tu sesión caducó. Vuelve a entrar.",
	})

	flashSuccesses = sharedweb.NewFlashCatalog("Acción completada.", map[string]string{
		flashLoggedOut: "Sesión cerrada.",
	})
)

// flashError traduce un código de error al mensaje que ve el usuario.
func flashError(code string) string { return flashErrors.Message(code) }

// flashSuccess traduce un código de éxito al mensaje que ve el usuario.
func flashSuccess(code string) string { return flashSuccesses.Message(code) }
