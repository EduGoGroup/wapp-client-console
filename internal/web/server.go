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

	// 🔴 EL TECHO DE CUERPO VA AQUÍ, POR DELANTE DEL CSRF, Y EL ORDEN ES EL DISEÑO: el CSRF lee el
	// formulario para comparar el token, y con eso consume el cuerpo entero —a memoria y a disco—, así
	// que un tope montado después llegaría cuando el daño ya está hecho. Cubre UNA ruta, la única de
	// esta consola que recibe un fichero. El porqué del número —y por qué NO es el mismo que el techo
	// del fichero— está entero en catalogo_limite.go.
	router.Use(limiteDeCuerpo(maxCuerpoCatalogo, rutaCatalogo))

	// A partir de aquí, todo lo que se registre queda bajo la defensa CSRF double-submit con la
	// cookie de ESTA consola, y con el plazo por petición que acota la cadena hacia identity y la
	// API pública.
	router.Use(webgin.CSRF(csrfOptions(cfg)))
	// 🔴 EL DEADLINE POR PETICIÓN YA NO ES UNO PARA TODOS (T7.6): sigue siendo UpstreamTimeout en las
	// ~20 rutas de esta consola y es más largo en UNA, la sugerencia de la respuesta, que es la única
	// que espera a que un modelo redacte. El porqué —y por qué no vale un middleware propio DESPUÉS
	// de éste— está entero en solicitudes_plazos.go.
	router.Use(plazoPorRuta(cfg))

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
	adminH := NewAdminHandler(authH, apiclient.New(cfg.PublicAPIBaseURL, cfg.UpstreamTimeout), cfg)

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

	// El SELECTOR DE EMPRESAS (T5.3). Es un POST y no tiene GET propio: no hay «pantalla de elegir
	// empresa» que listar, porque el control vive donde se necesita —en la barra, junto a «Cerrar
	// sesión», para quien ya tiene una activa, y dentro del parcial `sin_empresa` para quien
	// pertenece a varias y todavía no ha elegido—.
	//
	// Va en el grupo protegido y NO exige empresa, igual que el canje de una invitación y por la
	// misma razón: quien llega aquí tiene varias membresías y ninguna elegida, así que su Context
	// Token viene SIN tenant. Exigirle empresa sería exigirle justo lo que viene a conseguir.
	protected.POST(rutaEmpresa, adminH.SelectTenant)

	// Miembros (T1.4a) y su ALTA (T1.2).
	//
	// El alta es POST /miembros, simétrica con POST /roles: el formulario vive DENTRO de la pantalla
	// de miembros —igual que crear y asignar viven dentro de la de roles— y no en una pantalla
	// aparte, porque tras incorporar a alguien lo que se quiere ver es la tabla con esa persona ya
	// dentro. De ahí que las dos redirijan a /miembros (POST-redirect-GET).
	protected.GET("/miembros", adminH.ShowMembers)
	protected.POST("/miembros", adminH.AddMember)
	protected.POST("/miembros/:user_id/baja", adminH.RemoveMember)

	// Invitaciones (T-A7) y su revocación (T-A8). Es la vía BUENA para incorporar a alguien: quien
	// administra emite un código, lo reparte por WhatsApp y la persona entra sola. El alta por
	// identificador de /miembros es el apaño anterior y sigue ahí para quien no llegue con invitación.
	//
	// 🔴 La emisión es POST-redirect-GET y el código viaja al GET en una cookie efímera, NUNCA en la
	// URL: sin eso, un F5 crearía otra invitación válida que ya nadie puede ver. Ver
	// invitations_handler.go, que es donde está el razonamiento entero.
	//
	// El verbo estático `canjear` va ANTES del parámetro por el mismo criterio que /sesiones/enviar, y
	// aquí ni siquiera hay vecindad que resolver: `:id` cuelga de /invitaciones/:id/revocar, que es
	// otro segmento.
	protected.GET(rutaInvitaciones, adminH.ShowInvitations)
	protected.POST(rutaInvitaciones, adminH.IssueInvitation)
	protected.POST(rutaInvitaciones+"/canjear", adminH.RedeemInvitation)
	protected.POST(rutaInvitaciones+"/:id/revocar", adminH.RevokeInvitation)

	// Sesiones (T2.1): los teléfonos vinculados de la empresa, su perfil y el envío de prueba.
	//
	// 🔴 SIN RequireFeature, y no por olvido: en el BFF esto es capacidad base —una empresa con
	// cualquier plan tiene que poder ver sus teléfonos y cambiarles el perfil— y aquí también. Quien
	// venga a gatearlo estaría cortando el acceso de un tenant a su propia flota; el gate por plan de
	// esta consola cuelga de `catalog_import` (ver internal/web/entitlements.go · D-047.10).
	//
	// El envío va a /sesiones/enviar, con el verbo estático ANTES del parámetro: Gin resuelve bien un
	// hermano estático y uno con `:id` (comprobado), y lo único que esa vecindad se come es una sesión
	// que se llamara literalmente «enviar», que no existe (los identificadores son UUID).
	protected.GET("/sesiones", adminH.ShowSessions)
	protected.POST("/sesiones/enviar", adminH.SendTestMessage)
	protected.POST("/sesiones/:id/perfil", adminH.SetSessionProfile)

	// Roles (T1.3). Asignar y retirar cuelgan de /roles y no de /miembros porque lo que mueven es un
	// PERMISO: consumen `roles.write`, igual que crear un rol.
	protected.GET("/roles", adminH.ShowRoles)
	protected.POST("/roles", adminH.CreateRole)
	protected.POST("/roles/asignar", adminH.AssignRole)
	protected.POST("/roles/retirar", adminH.UnassignRole)

	// El EDITOR (T6.3 · T6.4): los flujos que la conversación recorre y los disparadores que deciden
	// cuándo se entra en ellos. Mudado de wapp-guardian-bff, que se queda sin las dos pantallas en
	// este mismo ciclo (REQ-08).
	//
	// 🔴 Los DOS POST que llevan formulario —publicar y crear— NO redirigen siempre, y es la única
	// excepción al POST-redirect-GET universal de esta consola: D-047.16. Un rechazo de la validación
	// LOCAL repinta con 400 y devuelve lo tecleado; el error de la API y el éxito van por 303 + flash.
	// El razonamiento entero está en editor_handler.go y junto a redirectWith.
	//
	// El borrado va por POST y el cliente de la API lo traduce a DELETE, igual que la baja de un
	// miembro: el navegador no emite DELETE y esta casa no tiene JavaScript.
	//
	// `/flujos/nuevo` cae en la ruta con parámetro y NO tiene ruta propia: `nuevo` es un VALOR MÁGICO
	// que el handler reconoce y que pinta el formulario de alta sin llamar a la API (ver flujoNuevo).
	// Registrarlo como hermano estático de `:id` no serviría de nada y sí escondería que el valor
	// mágico existe.
	protected.GET(rutaFlujos, adminH.ShowFlows)
	protected.GET(rutaFlujos+"/:id", adminH.ShowFlowDetail)
	protected.POST(rutaFlujos, adminH.PublishFlow)

	protected.GET(rutaDisparadores, adminH.ShowTriggers)
	protected.POST(rutaDisparadores, adminH.CreateTrigger)
	protected.POST(rutaDisparadores+"/:id/borrar", adminH.DeleteTrigger)

	// LA BANDEJA DE SOLICITUDES (T7.2): lo que los clientes pidieron por WhatsApp. Mudada de
	// wapp-guardian-bff, que se queda sin ella en este mismo ciclo (T7.7).
	//
	// 🔒 ES EL PRIMER GRUPO DE ESTA CONSOLA CON GATE POR FEATURE, y va como MIDDLEWARE SOBRE EL
	// GRUPO y no como un `if` dentro de cada handler: el BFF copió ese `if` en cinco sitios y por eso
	// su GET y sus POST acabaron respondiendo códigos distintos ante la misma ausencia de feature sin
	// que nadie lo decidiera. Aquí, quien añada una ruta de solicitudes la registra dentro del grupo
	// y hereda el gate sin acordarse de él. El porqué del 403 y del fail-closed está en
	// solicitudes_gate.go.
	//
	// El verbo estático `descartar` va ANTES del `:id` del detalle, que es la regla de esta casa
	// (mismo criterio que /sesiones/enviar). Lo único que esa vecindad se comería es una solicitud que
	// se llamara literalmente «descartar», y los identificadores son `in-…`.
	//
	// 📌 LAS SIETE ACCIONES DEL DETALLE ESTÁN COMPLETAS. Las cuatro que NO le hablan al cliente
	// llegaron en T7.4 —mover el estado, guardar las líneas facturables, corregir la interpretación y
	// regenerarla, y ninguna manda un mensaje por WhatsApp: regenerar reinterpreta un texto ya recibido
	// y el cliente no se entera—, LAS DOS QUE SÍ LE HABLAN en T7.5 —aprobar y pedir más información— y
	// la que cuesta una inferencia en T7.6: sugerir la respuesta.
	//
	// 🔴 ESAS DOS DE T7.5 MANDAN UN WHATSAPP A UNA PERSONA. No se registran distinto —el gate del grupo
	// y el CSRF de la casa son los mismos— pero su reparto de desenlaces sí es propio, porque un
	// repintado sobre un envío ya hecho invita a un F5 que lo enviaría dos veces: está escrito entero
	// en la cabecera de solicitudes_acciones.go.
	//
	// 🔴 Y LA DE T7.6 ES LA ÚNICA RUTA DE ESTA CONSOLA CON PLAZOS PROPIOS, los tres: el cliente HTTP
	// (apiclient), el deadline por petición (plazoPorRuta, arriba) y el write deadline, que se instala
	// AQUÍ como middleware de la ruta y no en el grupo —relevar al WriteTimeout del servidor es
	// exactamente lo que el resto de esta consola no debe hacer—. Es POST aunque no escriba nada, y no
	// es por el formulario: consume una inferencia, no es cacheable, no es gratis, y un GET lo
	// dispararía un prefetch del navegador.
	//
	// 🔑 Las rutas se componen con las MISMAS constantes de sufijo con las que solicitudes_detalle.go
	// arma el `action` de cada formulario. Escribirlas aquí como literal daría dos cadenas que nadie
	// compila y que pueden dejar de coincidir sin que nada falle: un formulario apuntando a una ruta
	// que el router escribe distinto es un 404 que ningún gate ve venir.
	solicitudes := protected.Group(rutaSolicitudes)
	solicitudes.Use(adminH.requiereFeature(featureCartBasic, plantillaSolicitudes, tituloSolicitudes))
	solicitudes.GET("", adminH.ShowSolicitudes)
	solicitudes.POST(rutaDescartarSufijo, adminH.DescartarSolicitudes)
	solicitudes.GET(rutaSolicitudDetalle, adminH.ShowSolicitudDetalle)
	solicitudes.POST(rutaSolicitudDetalle+sufijoEstado, adminH.CambiarEstadoSolicitud)
	solicitudes.POST(rutaSolicitudDetalle+sufijoLineas, adminH.GuardarLineasSolicitud)
	solicitudes.POST(rutaSolicitudDetalle+sufijoCorregir, adminH.CorregirInterpretacionSolicitud)
	solicitudes.POST(rutaSolicitudDetalle+sufijoRegenerar, adminH.RegenerarSolicitud)
	solicitudes.POST(rutaSolicitudDetalle+sufijoAprobar, adminH.AprobarSolicitud)
	solicitudes.POST(rutaSolicitudDetalle+sufijoPedirInfo, adminH.PedirInfoSolicitud)
	solicitudes.POST(rutaSolicitudDetalle+sufijoSugerir,
		plazoDeEscrituraSugerencia(cfg), adminH.SugerirRespuestaSolicitud)

	// LA IMPORTACIÓN DE CATÁLOGO (T8.2 · T8.3): la carga masiva de la lista de productos. Mudada de
	// wapp-guardian-bff, que se queda sin ella en este mismo ciclo (T8.5).
	//
	// 🔒 SEGUNDO GRUPO CON GATE POR FEATURE, y REUTILIZA el middleware que nació con la bandeja
	// (solicitudes_gate.go) en vez de estrenar uno: es exactamente el mismo corte sobre otra
	// capacidad, y dos implementaciones del mismo fail-closed son dos sitios donde una se abre. El
	// gate cubre las TRES rutas, descarga incluida — en el BFF cada handler repetía su propio `if` y
	// la descarga acababa contestando distinto que el POST ante la misma ausencia de plan.
	//
	// 🔴 EL POST ES UNO Y ATIENDE LOS DOS PASOS —comprobar y aplicar—, y lo que los separa es el
	// campo `mode` que manda el botón pulsado. No hay una ruta `/aplicar` y no debe inventarse: la
	// API tampoco tiene dos endpoints, tiene uno parametrizado por `mode`. La consecuencia para quien
	// escriba un test está en la cabecera de catalogo_handler.go — comprobar «el POST contestó» pasa
	// con los dos confundidos, y confundirlos reemplaza el catálogo de una empresa sin aprobación.
	//
	// El verbo estático `plantilla` cuelga de la ruta y NO compite con ningún `:id`: esta pantalla no
	// tiene detalle por identificador, así que la vecindad que sí hubo que resolver en /solicitudes
	// aquí no existe.
	//
	// 🔑 Las rutas se componen con las MISMAS constantes con las que la plantilla arma los enlaces de
	// descarga. Escribirlas aquí como literal daría dos cadenas que nadie compila y que pueden dejar
	// de coincidir sin que nada falle.
	catalogo := protected.Group(rutaCatalogo)
	catalogo.Use(adminH.requiereFeature(featureCatalogImport, plantillaCatalogo, tituloCatalogo))
	catalogo.GET("", adminH.ShowCatalogo)
	catalogo.POST("", adminH.ImportarCatalogo)
	catalogo.GET(rutaPlantillaSufijo, adminH.DescargarPlantillaCatalogo)

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
		// `tabla` arma el descriptor del partial `data_table` desde la propia plantilla: html/template
		// no sabe construir un valor compuesto, y describir la tabla en el handler la alejaría de la
		// pantalla que la pinta. Ver table_view.go.
		"tabla": tabla,
		// `statusLabel` traduce la clave del ciclo de vida de una solicitud al nombre con el que la
		// dueña del negocio la llama. Es helper de plantilla y no un campo de la vista porque lo usan
		// las DOS pantallas de la bandeja —la lista y el detalle— sobre listas que vienen crudas de la
		// API. Ver solicitudes_estado.go.
		"statusLabel": statusLabel,
		// `fecha` escribe un instante de la API para que lo lea una persona, y ESCRIBE ELLA el huso
		// (T7.3). Es helper de plantilla por lo mismo que statusLabel: los instantes llegan dentro de
		// tipos del apiclient que pintan las dos pantallas de la bandeja, y copiarlos a una vista solo
		// para formatearlos habría dado dos redacciones del mismo dato. Ver formato.go, que es donde
		// está escrito por qué el huso es UTC y no el de quien mira.
		"fecha": fecha,
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
