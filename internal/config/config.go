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
}

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
	}
}
