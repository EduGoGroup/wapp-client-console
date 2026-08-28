package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/EduGoGroup/wapp-client-console/internal/config"
	"github.com/EduGoGroup/wapp-shared/iam"
	sharedweb "github.com/EduGoGroup/wapp-shared/web"
	webgin "github.com/EduGoGroup/wapp-shared/web/gin"
	"github.com/gin-gonic/gin"
)

// AuthHandler gestiona el login/logout y el AuthMiddleware de la consola de cliente.
//
// El AuthMiddleware NO sube al módulo compartido: depende del upstream y del perímetro de cada
// superficie web, y el del cliente (su tenant, la API pública :8103) y el de plataforma (el listener
// admin :8100) son dos perímetros de autorización distintos que no se tocan.
type AuthHandler struct {
	cfg     *config.Config
	auth    *iam.Client
	refresh *sharedweb.RefreshGroup[*iam.AuthResult]
}

// NewAuthHandler construye el handler de autenticación.
func NewAuthHandler(cfg *config.Config, auth *iam.Client) *AuthHandler {
	return &AuthHandler{
		cfg:     cfg,
		auth:    auth,
		refresh: sharedweb.NewRefreshGroup[*iam.AuthResult](),
	}
}

// ShowLogin muestra el formulario de entrada.
//
// El texto del aviso sale SIEMPRE del catálogo (flash.go) y nunca del query string: `?error=` y
// `?success=` transportan un CÓDIGO, no un mensaje, y un código desconocido cae al genérico.
func (h *AuthHandler) ShowLogin(c *gin.Context) {
	if h.hasValidSession(c) {
		c.Redirect(http.StatusSeeOther, "/")
		return
	}
	h.renderLogin(c, http.StatusOK, flashError(c.Query("error")), flashSuccess(c.Query("success")), "")
}

// DoLogin procesa las credenciales del administrador del tenant.
//
// El 401 de credenciales y el 403 del System Gate llegan como sentinelas distintos —`iam` no los
// colapsa— y aquí se muestran con el mismo texto a propósito: al que está en la pantalla de login no
// se le dice si el correo existe.
//
// 🔑 Esa distinción, que en la pantalla se oculta a propósito, en el LOG es lo único que hay: quien
// diagnostica un «no puedo entrar» necesita saber si buscar la contraseña o la fila de
// `iam.user_systems`. Por eso las DOS ramas escriben, y con texto distinto, y hay un test que lo
// vigila (auth_test.go). En la consola de plataforma esto estuvo prometido en un comentario y
// cumplido solo para una de las dos ramas: el 2026-08-28 costó una tarde de diagnóstico a ciegas,
// porque la causa había que deducirla por la AUSENCIA de la línea del System Gate.
//
// Lo que se registra es la CAUSA, nunca el correo: en el log de esta consola no entra PII.
func (h *AuthHandler) DoLogin(c *gin.Context) {
	email := strings.TrimSpace(c.PostForm("email"))
	password := c.PostForm("password")
	if email == "" || password == "" {
		h.renderLogin(c, http.StatusBadRequest, "Introduce tu correo y contraseña.", "", email)
		return
	}

	res, err := h.auth.Login(c.Request.Context(), email, password)
	if err != nil {
		switch {
		case errors.Is(err, iam.ErrForbidden):
			slog.Warn("login de cliente rechazado por el System Gate: falta la fila en iam.user_systems para "+systemWappBFF,
				"error", err)
		case errors.Is(err, iam.ErrUnauthorized):
			slog.Warn("login de cliente rechazado por identity: credenciales inválidas", "error", err)
		default:
			slog.Warn("login de cliente rechazado", "error", err)
		}
		if errors.Is(err, iam.ErrUnauthorized) || errors.Is(err, iam.ErrForbidden) {
			h.renderLogin(c, http.StatusUnauthorized, "Credenciales inválidas o sin acceso a esta consola.", "", email)
			return
		}
		h.renderLogin(c, http.StatusUnauthorized, "No se pudo iniciar sesión. Verifica tus credenciales.", "", email)
		return
	}

	if err := h.startSession(c, res); err != nil {
		slog.Error("falló iniciar sesión", "error", err)
		h.renderLogin(c, http.StatusInternalServerError, "Error interno al iniciar sesión.", "", email)
		return
	}

	c.Redirect(http.StatusSeeOther, "/")
}

// DoLogout finaliza la sesión.
//
// La cookie local se borra SIEMPRE, aunque falle la revocación remota: nadie debe quedarse con una
// sesión que él cree cerrada. Pero el fallo no se traga en silencio: si identity responde con error,
// el refresh token sigue vivo allí, y eso tiene que quedar en el log para que se note.
//
// Cierra UNA sesión, la de esta aplicación en identity: la del teléfono del Edge sobrevive.
func (h *AuthHandler) DoLogout(c *gin.Context) {
	if raw := webgin.SessionCookieValue(c, sessionCookieOptions(h.cfg)); raw != "" {
		if sess, derr := sharedweb.DecodeSession(raw); derr == nil && sess.RefreshToken != "" {
			if lerr := h.auth.Logout(c.Request.Context(), sess.RefreshToken); lerr != nil {
				slog.Warn("logout en identity falló; la sesión se cierra localmente igualmente", "error", lerr)
			}
		}
	}
	h.clearSession(c)
	c.Redirect(http.StatusSeeOther, "/login?success="+flashLoggedOut)
}

// AuthMiddleware valida la cookie de sesión y renueva el token proactivamente.
//
// Distingue los dos motivos por los que se acaba en /login: no había cookie (primera visita, nadie
// ha perdido nada) o la había y ya no sirve (caducada o ilegible). Solo el segundo pinta un aviso;
// darle «tu sesión caducó» a quien nunca entró sería mentirle.
func (h *AuthHandler) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := webgin.SessionCookieValue(c, sessionCookieOptions(h.cfg))
		if raw == "" {
			h.redirectToLogin(c, "")
			return
		}

		sess, err := sharedweb.DecodeSession(raw)
		if err != nil || sess.AccessToken == "" {
			h.expireSession(c)
			return
		}

		claims, err := parseAccessClaims(sess.AccessToken)
		if err != nil {
			h.expireSession(c)
			return
		}
		exp := accessExpiry(claims)

		accessToken := sess.AccessToken
		refreshToken := sess.RefreshToken

		if sharedweb.RefreshDue(exp, 0) && refreshToken != "" {
			res, rerr := h.refreshSession(c, refreshToken)
			if rerr == nil && res != nil {
				accessToken = res.AccessToken
				refreshToken = res.RefreshToken
			} else if !sharedweb.SessionValid(exp) {
				h.expireSession(c)
				return
			}
		} else if !sharedweb.SessionValid(exp) {
			h.expireSession(c)
			return
		}

		c.Set(webgin.ContextAccessToken, accessToken)
		c.Set(webgin.ContextRefreshToken, refreshToken)
		c.Set(webgin.ContextUserID, claims.UserID)
		c.Set(webgin.ContextTenantID, claims.TenantID)
		c.Next()
	}
}

func (h *AuthHandler) hasValidSession(c *gin.Context) bool {
	raw := webgin.SessionCookieValue(c, sessionCookieOptions(h.cfg))
	if raw == "" {
		return false
	}
	sess, err := sharedweb.DecodeSession(raw)
	if err != nil || sess.AccessToken == "" {
		return false
	}
	claims, err := parseAccessClaims(sess.AccessToken)
	if err != nil {
		return false
	}
	return sharedweb.SessionValid(accessExpiry(claims))
}

// startSession guarda la sesión en la cookie HttpOnly.
//
// 🔴 Lo que entra aquí es el AuthResult de `iam`, cuyo AccessToken es SIEMPRE el Context Token del
// canje: el Identity Token muere dentro del módulo, no vuelve al llamante y por tanto no puede
// llegar al navegador. Un test lo afirma por el cable (auth_test.go).
func (h *AuthHandler) startSession(c *gin.Context, res *iam.AuthResult) error {
	raw, err := sharedweb.EncodeSession(sharedweb.SessionData{
		AccessToken:  res.AccessToken,
		RefreshToken: res.RefreshToken,
		ExpiresAt:    res.ExpiresAt,
	})
	if err != nil {
		return err
	}
	webgin.SetSessionCookie(c, sessionCookieOptions(h.cfg), raw, sessionCookieMaxAge)
	return nil
}

func (h *AuthHandler) clearSession(c *gin.Context) {
	webgin.ClearSessionCookie(c, sessionCookieOptions(h.cfg))
}

// expireSession borra la cookie que ya no sirve y manda a /login con el aviso de sesión caducada.
func (h *AuthHandler) expireSession(c *gin.Context) {
	h.clearSession(c)
	h.redirectToLogin(c, flashSessionExpired)
}

// redirectToLogin corta la petición y manda a la pantalla de entrada. `code` vacío = sin aviso.
func (h *AuthHandler) redirectToLogin(c *gin.Context, code string) {
	destino := "/login"
	if code != "" {
		destino += "?error=" + code
	}
	c.Redirect(http.StatusSeeOther, destino)
	c.Abort()
}

// refreshSession serializa por refresh token los refrescos concurrentes: N peticiones del mismo
// usuario que llegan a la vez hacen UN solo viaje a identity, no N.
func (h *AuthHandler) refreshSession(c *gin.Context, refreshToken string) (*iam.AuthResult, error) {
	res, err := h.refresh.Do(refreshToken, func() (*iam.AuthResult, error) {
		return h.auth.Refresh(c.Request.Context(), refreshToken)
	})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, errors.New("refresh falló")
	}
	_ = h.startSession(c, res)
	return res, nil
}

// renderLogin pinta la pantalla de entrada.
//
// `email` es el correo que se acaba de teclear, y se devuelve al formulario para no tener que
// reescribirlo en cada intento. La contraseña NO se repuebla: sería mandarla de vuelta al navegador
// dentro del HTML. Va vacío en el GET inicial.
func (h *AuthHandler) renderLogin(c *gin.Context, status int, errMsg, successMsg, email string) {
	renderer.HTML(c, status, "login.html", gin.H{
		"Title":    "Iniciar sesión",
		"Subtitle": "Consola del cliente",
		"Error":    errMsg,
		"Success":  successMsg,
		"Email":    email,
	})
}
