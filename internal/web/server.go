// Package web monta el router HTTP de la consola de cliente.
//
// Lo que este paquete custodia desde el primer commit es la cadena de middleware endurecida de
// `wapp-shared/web` con los nombres de cookie propios de esta consola (ver session.go). Encima de
// ella vive el ciclo de sesión —login, logout y el AuthMiddleware (auth_handler.go)— y las pantallas
// de administración del tenant: la portada, los miembros y los roles (admin_handler.go y compañía),
// que hablan con la API pública :8103 a través de internal/apiclient.
package web

import (
	"bytes"
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
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

	// El cliente de la API PÚBLICA (:8103), el único upstream de negocio de esta consola. Comparte el
	// plazo por petición con el resto de la cadena: un upstream sin plazo cuelga la pantalla entera.
	adminH := NewAdminHandler(authH, apiclient.New(cfg.PublicAPIBaseURL, cfg.UpstreamTimeout))

	// Rutas públicas: la entrada y la salida.
	router.GET("/login", authH.ShowLogin)
	router.POST("/login", authH.DoLogin)
	router.POST("/logout", authH.DoLogout)

	// Rutas protegidas: todo lo que hay detrás de la sesión.
	//
	// 🔴 Los verbos son HTML puro (GET y POST) y no PUT/DELETE: esta consola es server-side rendering
	// sin una línea de JavaScript, y un formulario HTML solo sabe hacer esas dos cosas. La consola
	// hace POST y el cliente de la API traduce al verbo que la plataforma espera —DELETE para la baja
	// y para retirar un rol—. Cambiar esto por fetch() significaría meter JS y, con él, un nonce en
	// cada página; ver security_test.go.
	protected := router.Group("/")
	protected.Use(authH.AuthMiddleware())
	protected.GET("/", adminH.ShowHome)

	// «Mi identificador» va la PRIMERA de las protegidas y fuera de todo lo demás a propósito: es la
	// única pantalla que sirve a una sesión SIN empresa, y es la que hace utilizable el resto (quien
	// administra no puede buscar a nadie por su correo, así que la persona tiene que aportar su
	// identificador). No llama a la API pública.
	protected.GET("/mi-identificador", adminH.ShowMyIdentifier)

	// Miembros (T1.4a) y su ALTA (T1.2).
	//
	// El alta es POST /miembros, simétrica con POST /roles: el formulario vive DENTRO de la pantalla
	// de miembros —igual que crear y asignar viven dentro de la de roles— y no en una pantalla
	// aparte, porque tras incorporar a alguien lo que se quiere ver es la tabla con esa persona ya
	// dentro. De ahí que las dos redirijan a /miembros (POST-redirect-GET).
	protected.GET("/miembros", adminH.ShowMembers)
	protected.POST("/miembros", adminH.AddMember)
	protected.POST("/miembros/:user_id/baja", adminH.RemoveMember)

	// Roles (T1.3). Asignar y retirar cuelgan de /roles y no de /miembros porque lo que mueven es un
	// PERMISO: consumen `roles.write`, igual que crear un rol.
	protected.GET("/roles", adminH.ShowRoles)
	protected.POST("/roles", adminH.CreateRole)
	protected.POST("/roles/asignar", adminH.AssignRole)
	protected.POST("/roles/retirar", adminH.UnassignRole)

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
