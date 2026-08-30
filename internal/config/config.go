// Package config centraliza la configuración de la consola de cliente (wapp-client-console).
//
// La consola es una superficie web server-side endurecida para el ADMINISTRADOR DEL TENANT: la
// dueña del negocio y su equipo. Autentica contra identity (:8200) y opera exclusivamente contra la
// API pública de la plataforma (:8103 /api/v1), el plano que exige Context Token y filtra por
// tenant.
package config

import (
	"time"

	sharedconfig "github.com/EduGoGroup/wapp-shared/config"
)

// Config agrupa la configuración efectiva de la consola de cliente.
//
// 🔴 NO HAY —ni debe haber— un AdminAPIBaseURL. El listener admin de la plataforma (:8100) es el
// perímetro de OPERADORES (tenants, instalaciones, planes, kill-switch comercial) y lo consume
// wapp-platform-console. Esta consola es la del CLIENTE: su alcance termina en su propio tenant.
// El campo se omite a propósito y no por olvido: mientras no exista, ningún handler de aquí puede
// apuntar al plano admin "sin querer", porque no tiene de dónde sacar la URL. Quien vaya a
// añadirlo, que primero explique por qué la UI de un cliente necesita el plano de plataforma.
type Config struct {
	Environment      string
	HTTPAddr         string
	PublicAPIBaseURL string
	IdentityBaseURL  string

	CookieSecure   bool
	CookieSameSite string
	AllowedOrigins string
	TrustedProxies string
	HSTSEnabled    bool

	RateLimitEnabled bool
	RateLimitRPS     float64
	RateLimitBurst   float64
	// RateLimitTTL es la inactividad tras la cual se desaloja la entrada de una clave del limitador,
	// y RateLimitPurgeEvery cada cuánto se intenta ese barrido. Valor <= 0 → los valores por defecto
	// de web.NewKeyedRateLimiter (wapp-shared/web: 10 min / 1 min). Se exponen aquí para poder
	// bajarlos a milisegundos en los tests sin tocar constantes globales.
	RateLimitTTL        time.Duration
	RateLimitPurgeEvery time.Duration

	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	UpstreamTimeout   time.Duration

	// QuoteSuggestionTimeout es el plazo de LA ÚNICA RUTA DE ESTA CONSOLA QUE ESPERA A QUE UN MODELO
	// REDACTE: POST /solicitudes/{id}/sugerir-respuesta (Plan 047 · T7.6, sobre el endpoint del Plan
	// 044 · T5.1).
	//
	// 🔴 EXISTE PORQUE LOS TRES PLAZOS GENERALES LA CORTABAN, y no de uno en uno: medido contra UAT
	// el 2026-08-28, esa llamada tardó 24,8 / 28,4 / 29,7 / 35,5 s, y el cloud se da a sí mismo 48 s
	// para redactar (`pipeline.PlazoPorLlamadaSuelo`), así que el techo realista de la respuesta es
	// ~48-50 s. Los generales de esta consola son 15 s de http.Client, 20 s de UpstreamTimeout y 30 s
	// de WriteTimeout, y el último además cortaba de la peor manera: cerrando la conexión sin dejar
	// pintar pantalla.
	//
	// 🔴 SUBIR LOS GENERALES NO ERA LA SALIDA: le daría a las ~20 rutas de esta consola un plazo que
	// necesita UNA, y el WriteTimeout está puesto justo por lo contrario (defensa anti-slowloris:
	// aquí no hay streams de larga vida). De ahí que el plazo sea POR RUTA.
	//
	// De este número salen los otros dos (ver los métodos de abajo), y el suelo es el del cliente
	// HTTP —apiclient.DefaultInferenceTimeout—, que tiene que quedar por DENTRO. 0 == apagado: la
	// ruta se comporta como cualquier otra, que es lo que hacía antes de T7.6.
	QuoteSuggestionTimeout time.Duration
}

// DefaultQuoteSuggestionTimeout es el plazo por defecto de la ruta de la sugerencia.
//
// 🔑 TIENE QUE VALER LO MISMO QUE apiclient.DefaultInferenceTimeout, que es el plazo del cliente HTTP
// que hace esa llamada: éste es el segundo eslabón de la cadena y el orden —cliente < deadline de
// petición < write deadline— es lo único que hace que, cuando la espera se pase de larga, el que
// corte sea el cliente (que devuelve un error traducible a un aviso) y no el servidor (que cierra la
// conexión y deja al navegador sin nada que pintar). Hay un test que compara las dos constantes: no
// se pueden importar entre sí sin invertir la dependencia de config hacia apiclient.
const DefaultQuoteSuggestionTimeout = 55 * time.Second

// Load resuelve la configuración desde variables de entorno con prefijo WAPP_.
//
// Dos familias de nombres, igual que en la consola de plataforma: CLIENT_CONSOLE_* es lo que
// identifica a ESTA aplicación (dónde escucha, tras qué proxies) y CONSOLE_* lo que es política de
// consola y se lee igual en ambas (ambiente, cookies, CORS, rate-limit, plazos). A diferencia de la
// consola de plataforma, aquí no hay alias legados: el repo nace con un solo nombre por variable.
func Load() Config {
	l := sharedconfig.New(sharedconfig.WithEnvPrefix("WAPP_"))

	env := l.GetString("CONSOLE_ENV", "local")
	secureDefault := env != "local"

	return Config{
		Environment:      env,
		HTTPAddr:         l.GetString("CLIENT_CONSOLE_HTTP_ADDR", "127.0.0.1:8107"),
		PublicAPIBaseURL: l.GetString("PUBLIC_API_BASE", "http://127.0.0.1:8103"),
		IdentityBaseURL:  l.GetString("IDENTITY_URL", "http://127.0.0.1:8200"),

		CookieSecure:   l.GetBool("CONSOLE_COOKIE_SECURE", secureDefault),
		CookieSameSite: l.GetString("CONSOLE_COOKIE_SAMESITE", "lax"),
		AllowedOrigins: l.GetString("CONSOLE_ALLOWED_ORIGINS", ""),
		TrustedProxies: l.GetString("CLIENT_CONSOLE_TRUSTED_PROXIES", ""),
		HSTSEnabled:    l.GetBool("CONSOLE_HSTS_ENABLED", secureDefault),

		RateLimitEnabled: l.GetBool("CONSOLE_RATE_ENABLED", true),
		RateLimitRPS:     float64(l.GetInt("CONSOLE_RATE_RPS", 5)),
		RateLimitBurst:   float64(l.GetInt("CONSOLE_RATE_BURST", 10)),

		RateLimitTTL:        time.Duration(l.GetInt("CONSOLE_RATE_TTL_SECS", 300)) * time.Second,
		RateLimitPurgeEvery: time.Duration(l.GetInt("CONSOLE_RATE_PURGE_SECS", 60)) * time.Second,

		ReadHeaderTimeout: time.Duration(l.GetInt("CONSOLE_READ_HEADER_TIMEOUT_SECS", 5)) * time.Second,
		ReadTimeout:       time.Duration(l.GetInt("CONSOLE_READ_TIMEOUT_SECS", 15)) * time.Second,
		WriteTimeout:      time.Duration(l.GetInt("CONSOLE_WRITE_TIMEOUT_SECS", 30)) * time.Second,
		IdleTimeout:       time.Duration(l.GetInt("CONSOLE_IDLE_TIMEOUT_SECS", 60)) * time.Second,

		ShutdownTimeout: time.Duration(l.GetInt("CONSOLE_SHUTDOWN_TIMEOUT_SECS", 10)) * time.Second,
		UpstreamTimeout: time.Duration(l.GetInt("CONSOLE_UPSTREAM_TIMEOUT_SECS", 20)) * time.Second,

		QuoteSuggestionTimeout: time.Duration(l.GetInt("CONSOLE_QUOTE_SUGGESTION_TIMEOUT_SECS",
			int(DefaultQuoteSuggestionTimeout/time.Second))) * time.Second,
	}
}

// Los DOS MÁRGENES de los que salen los otros dos plazos de la ruta de la sugerencia, sumados sobre
// QuoteSuggestionTimeout.
//
// Son márgenes y no números sueltos a propósito: los tres plazos tienen que quedar en ORDEN —cliente
// < deadline de petición < write deadline— y ese orden es lo único que hace que el corte, cuando
// llegue, lo dé el cliente HTTP (que devuelve un error traducible a pantalla) y no el servidor (que
// cierra la conexión sin nada que pintar). Escribir 55/58/60 a mano en tres sitios deja tres números
// que se desincronizan; escribir uno y dos sumas, no.
//
// 3 s cubre el viaje de vuelta y el render tras el corte del cliente; 5 s deja además el margen del
// cierre de la conexión. Con el default de 55 s salen los 58 s y los 60 s que el BFF midió en campo.
const (
	quoteSuggestionRequestMargin = 3 * time.Second
	quoteSuggestionWriteMargin   = 5 * time.Second
)

// QuoteSuggestionRequestDeadline es el deadline POR PETICIÓN de la ruta de la sugerencia — el que
// sustituye a UpstreamTimeout solo ahí. Va por encima del plazo del cliente HTTP para que el que
// corte primero sea el cliente: un contexto vencido aborta la llamada sin cuerpo que leer, y el aviso
// en pantalla sale peor.
//
// 0 == apagado, y entonces la ruta usa UpstreamTimeout como cualquier otra. Nunca «sin plazo»: ver
// plazoPorRuta.
func (c *Config) QuoteSuggestionRequestDeadline() time.Duration {
	if c.QuoteSuggestionTimeout <= 0 {
		return 0
	}
	return c.QuoteSuggestionTimeout + quoteSuggestionRequestMargin
}

// QuoteSuggestionWriteDeadline es el write deadline propio de la ruta de la sugerencia, el que se
// instala con http.NewResponseController sobre la conexión y RELEVA al WriteTimeout del http.Server
// solo en esa petición.
//
// 🔴 SIN ÉL LOS OTROS DOS PLAZOS NO SIRVEN DE NADA: el WriteTimeout de 30 s cierra la conexión a
// mitad de la espera y el navegador no recibe ni la página degradada. Es el único de los tres que
// falla sin dejar rastro en pantalla. 0 == apagado (manda el WriteTimeout del servidor).
func (c *Config) QuoteSuggestionWriteDeadline() time.Duration {
	if c.QuoteSuggestionTimeout <= 0 {
		return 0
	}
	return c.QuoteSuggestionTimeout + quoteSuggestionWriteMargin
}

// QuoteSuggestionEffectiveWait es lo que la ruta de la sugerencia espera DE VERDAD antes de rendirse:
// su plazo propio si lo tiene, y si no el del grupo, que es el que la corta entonces.
//
// Existe para que la PANTALLA pueda decir la magnitud sin inventársela. En el BFF el aviso que lee la
// dueña se escribió una vez con la espera de aquel momento y se quedó mintiendo en cuanto los plazos
// cambiaron —decía «unos segundos» cuando lo medido eran 25-35 s—, y un número a mano en una
// plantilla no tiene forma de enterarse. Colgándolo de aquí, mover el plazo mueve el texto.
func (c *Config) QuoteSuggestionEffectiveWait() time.Duration {
	if c.QuoteSuggestionTimeout > 0 {
		return c.QuoteSuggestionTimeout
	}
	return c.UpstreamTimeout
}
