// Package web monta el router HTTP de la consola de cliente.
//
// Andamiaje: aquí todavía NO hay login ni pantallas. Lo que sí está montado, y es lo que este
// paquete custodia desde el primer commit, es la cadena de middleware endurecida de
// `wapp-shared/web` con los nombres de cookie propios de esta consola (ver session.go).
package web

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/EduGoGroup/wapp-client-console/internal/config"
	"github.com/EduGoGroup/wapp-shared/ui"
	sharedweb "github.com/EduGoGroup/wapp-shared/web"
	webgin "github.com/EduGoGroup/wapp-shared/web/gin"
	"github.com/gin-gonic/gin"
)

// sharedStylesheets son las hojas que sirve el módulo `wapp-shared/ui`, no este repo: los tokens y
// los componentes comunes del ecosistema, más el tema del perímetro del CLIENTE (theme-bff.css),
// que es el mismo que viste al BFF. La consola de plataforma sirve theme-platform.css: son dos
// perímetros y se ven distinto a propósito.
var sharedStylesheets = []string{
	"wapp-tokens.css",
	"wapp-components.css",
	"theme-bff.css",
}

// NewRouter construye el motor Gin y descarta el cleanup del rate limiter, que aquí no hace falta:
// el limitador de `wapp-shared/web` no arranca ninguna goroutine y purga sus claves inactivas de
// forma perezosa dentro de Allow(), así que no hay barrido que filtrar ni mapa que crezca sin tope.
//
// Usa NewRouterWithLimiter solo si eres el dueño del ciclo de vida y quieres liberar las entradas al
// apagar (lo hace bootstrap).
func NewRouter(cfg *config.Config) *gin.Engine {
	router, _ := NewRouterWithLimiter(cfg)
	return router
}

// NewRouterWithLimiter construye el motor Gin y una función de limpieza para el rate limiter.
func NewRouterWithLimiter(cfg *config.Config) (*gin.Engine, func()) {
	webgin.SetReleaseMode()
	router := gin.New()

	if err := webgin.SetTrustedProxies(router, cfg.TrustedProxies); err != nil {
		slog.Error("lista de proxies de confianza inválida", "valor", cfg.TrustedProxies, "error", err)
		panic(err)
	}

	router.Use(gin.Recovery())
	router.Use(webgin.SlogLogger())
	router.Use(webgin.SecurityHeaders(sharedweb.SecurityOptions{HSTS: cfg.HSTSEnabled}))
	router.Use(webgin.CORS(sharedweb.CORSOptions{
		AllowedOrigins: sharedweb.ParseOrigins(cfg.AllowedOrigins),
	}))

	var rateLimiter *sharedweb.KeyedRateLimiter
	if cfg.RateLimitEnabled {
		rateLimiter = sharedweb.NewKeyedRateLimiter(sharedweb.RateLimiterOptions{
			RPS:        cfg.RateLimitRPS,
			Burst:      int(cfg.RateLimitBurst),
			TTL:        cfg.RateLimitTTL,
			PurgeEvery: cfg.RateLimitPurgeEvery,
		})
		router.Use(webgin.RateLimit(rateLimiter))
	}

	// Estilos compartidos y sonda de salud van ANTES del CSRF a propósito, igual que en la consola
	// de plataforma: ni una hoja de estilo ni una sonda deben recibir un Set-Cookie.
	for _, sheet := range sharedStylesheets {
		router.GET("/static/css/"+sheet, serveSharedCSS(sheet))
	}

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "healthy", "time": time.Now().UTC().Format(time.RFC3339)})
	})

	// A partir de aquí, todo lo que se registre queda bajo la defensa CSRF double-submit con la
	// cookie de ESTA consola, y con el plazo por petición que acota la cadena hacia identity y la
	// API pública. Las pantallas y el login de la tanda siguiente entran justo debajo.
	router.Use(webgin.CSRF(csrfOptions(cfg)))
	router.Use(webgin.RequestDeadline(cfg.UpstreamTimeout))

	var cleanup func()
	if rateLimiter != nil {
		cleanup = rateLimiter.Close
	}

	return router, cleanup
}

// serveSharedCSS devuelve el handler que sirve una hoja del módulo `wapp-shared/ui`.
func serveSharedCSS(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		data, err := ui.GetCSS(name)
		if err != nil {
			slog.Error("hoja de estilo compartida no encontrada", "hoja", name, "error", err)
			c.Status(http.StatusNotFound)
			return
		}
		c.Header("Cache-Control", "public, max-age=3600")
		c.Data(http.StatusOK, "text/css; charset=utf-8", data)
	}
}
