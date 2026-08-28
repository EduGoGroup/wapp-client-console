// Package web monta el router HTTP de la consola de cliente.
//
// Lo que este paquete custodia desde el primer commit es la cadena de middleware endurecida de
// `wapp-shared/web` con los nombres de cookie propios de esta consola (ver session.go). Encima de
// ella vive el ciclo de sesión —login, logout y el AuthMiddleware (auth_handler.go)— y la pantalla
// autenticada mínima. Las pantallas de negocio llegan en la tanda siguiente.
package web

import (
	"bytes"
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/EduGoGroup/wapp-client-console/internal/config"
	"github.com/EduGoGroup/wapp-shared/iam"
	"github.com/EduGoGroup/wapp-shared/ui"
	sharedweb "github.com/EduGoGroup/wapp-shared/web"
	webgin "github.com/EduGoGroup/wapp-shared/web/gin"
	"github.com/gin-gonic/gin"
)

//go:embed templates
var templatesFS embed.FS

//go:embed static/css/app.css
var appCSS []byte

// systemWappBFF es la clave de ESTA aplicación en el catálogo de identity (`iam.systems`), la que
// viaja en el cuerpo del login y que el System Gate evalúa.
//
// 🔑 Es la MISMA que usa el BFF del cliente (wapp-guardian-bff), y es una decisión, no un descuido:
// esta consola y el BFF son el mismo perímetro —el del cliente sobre su propio tenant, contra la API
// pública :8103—, así que comparten aplicación en identity. La consola de plataforma se presenta con
// otra (`wapp.platform`) porque su perímetro sí es otro.
//
// Por qué NO se estrena una clave propia (`wapp.client-console` o similar), para que nadie lo
// "arregle": el canje de la plataforma solo acepta tres systems (`wapp.bff`, `wapp.edge`,
// `wapp.platform`), identity-core no expone endpoint para dar de alta uno nuevo, y `ReplaceUserSystems`
// es DECLARATIVO —revoca lo que no se le manda—, así que estrenar una clave obligaría a re-acreditar
// a CADA usuario de cliente que ya existe. Con `wapp.bff`, los que ya están entran el primer día.
const systemWappBFF = "wapp.bff"

// renderer pinta cada página sobre el layout maestro sembrando el nonce CSP, el token CSRF, la ruta
// actual y el estado de sesión. Que los ponga el renderizador y no cada handler es justo el punto:
// una pantalla nueva que se olvidara del nonce se serviría sin CSP utilizable.
var renderer = webgin.NewRenderer(webgin.DefaultLayout)

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

	router.SetHTMLTemplate(parseTemplates())

	// La hoja propia de esta consola (el marco: barra, rejilla, tarjetas de la pantalla de sesión).
	// Los componentes reutilizables NO están aquí: vienen de wapp-shared/ui, abajo.
	router.GET("/static/css/app.css", func(c *gin.Context) {
		c.Header("Cache-Control", "public, max-age=3600")
		c.Data(http.StatusOK, "text/css; charset=utf-8", appCSS)
	})

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
	// API pública.
	router.Use(webgin.CSRF(csrfOptions(cfg)))
	router.Use(webgin.RequestDeadline(cfg.UpstreamTimeout))

	// El `system` con el que esta consola se presenta ante identity es CAMPO del cliente, no una
	// constante del módulo: el System Gate autoriza aplicaciones, no ecosistemas. Unas opciones que
	// no pueden funcionar fallan aquí, en el arranque, y no dentro de un login.
	//
	// PlatformBaseURL es la API PÚBLICA (:8103), que es quien canjea el Identity Token por el
	// Context Token del tenant. No hay ninguna otra URL de plataforma en este repo, y es deliberado
	// (ver internal/config).
	authClient, err := iam.NewClient(iam.Options{
		System:          systemWappBFF,
		IdentityBaseURL: cfg.IdentityBaseURL,
		PlatformBaseURL: cfg.PublicAPIBaseURL,
		Timeout:         cfg.UpstreamTimeout,
	})
	if err != nil {
		slog.Error("configuración del cliente de identidad inválida", "error", err)
		panic(err)
	}

	authH := NewAuthHandler(cfg, authClient)

	// Rutas públicas: la entrada y la salida.
	router.GET("/login", authH.ShowLogin)
	router.POST("/login", authH.DoLogin)
	router.POST("/logout", authH.DoLogout)

	// Rutas protegidas. Hoy solo la pantalla de sesión; las de negocio cuelgan de aquí.
	protected := router.Group("/")
	protected.Use(authH.AuthMiddleware())
	protected.GET("/", showHome)

	var cleanup func()
	if rateLimiter != nil {
		cleanup = rateLimiter.Close
	}

	return router, cleanup
}

// parseTemplates compila el árbol de plantillas embebido.
//
// El helper `yield` es lo que permite que el layout maestro ejecute el fragmento de cada página
// (`{{ yield .ContentTemplate . }}`): html/template no tiene forma de ejecutar una plantilla cuyo
// nombre sea una variable. La clausura necesita el *Template ya compilado, de ahí el `var tmpl`
// declarado antes de ParseFS.
//
// Una plantilla que no compila ABORTA EL ARRANQUE en vez de servirse rota: el fallo aparece al
// desplegar, no en la cara del primer usuario que abra la pantalla afectada.
func parseTemplates() *template.Template {
	var tmpl *template.Template
	root := template.New("").Funcs(template.FuncMap{
		"yield": func(name string, data any) (template.HTML, error) {
			if name == "" {
				return "", nil
			}
			var buf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
				slog.Error("error al renderizar plantilla yield", "nombre", name, "error", err)
				return "", err
			}
			return template.HTML(buf.String()), nil // #nosec G203
		},
	})
	tmpl, err := root.ParseFS(templatesFS,
		"templates/layouts/*.html",
		"templates/pages/*.html",
		"templates/partials/*.html",
	)
	if err != nil {
		slog.Error("no se pudieron compilar las plantillas HTML", "error", err)
		panic(err)
	}
	return tmpl
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
