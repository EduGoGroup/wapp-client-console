package web

import (
	"log/slog"
	"net/http"
	"strconv"

	sharedweb "github.com/EduGoGroup/wapp-shared/web"
	webgin "github.com/EduGoGroup/wapp-shared/web/gin"
	"github.com/gin-gonic/gin"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
)

// invitations_handler.go sirve las CUATRO pantallas de la invitación (Plan 047 · Ola A, T-A7 y T-A8),
// que en rutas son tres del lado de quien administra y una del lado de quien entra:
//
//	GET  /invitaciones            emitir + el código UNA vez + el listado
//	POST /invitaciones            emitir  → 303 a la de arriba
//	POST /invitaciones/:id/revocar         → 303 a la de arriba
//	POST /invitaciones/canjear    el invitado pega su código (el formulario vive en sin_empresa)
//
// 🔴 POST-REDIRECT-GET, Y AQUÍ NO ES ESTILO. El código en claro se enseña UNA sola vez. Si la
// emisión renderizara sobre el POST, un F5 reenviaría el formulario y crearía otra invitación válida
// y viva durante horas QUE YA NADIE PUEDE VER —su código se mostró en una respuesta que el navegador
// acaba de descartar—, y limpiarlas sería trabajo manual con el listado delante. Es la deuda M-10 de
// la consola de plataforma; esta pantalla nace sin ella.
//
// 🔴 EL CÓDIGO NO VIAJA EN LA URL DEL REDIRECT. Viaja en una cookie efímera de un solo uso
// (web.OneTimeCookie) que el GET lee y borra en el mismo gesto. Una URL acaba en el log de acceso de
// cualquier proxy, en la cabecera `Referer` de lo que se cargue después y en el historial del
// navegador: es exactamente la misma fuga que hizo mover el token al CUERPO en la API.
//
// 🔴 Y NO SE REGISTRA NUNCA, en ninguna rama. Ni el que se emite ni el que el invitado pega: en el
// log de esta consola no entra material que autorice a entrar en una empresa.

// rutaInvitaciones es la pantalla y, a la vez, el Path EXACTO de la cookie efímera del código y el
// destino de los tres redirects. Es UNA sola constante a propósito: ver invitacionCookieOptions.
const rutaInvitaciones = "/invitaciones"

// ttlOpcion es una de las caducidades que el formulario ofrece.
//
// La lista es UNA y la usan las dos puntas: la plantilla la recorre para pintar el desplegable y el
// handler la recorre para validar lo que llega. Con dos listas —una en el HTML y otra en Go— el día
// que alguien añadiera una opción al desplegable, elegirla daría «caducidad inválida» sin que nada
// fallara al compilar.
type ttlOpcion struct {
	Segundos   int
	Etiqueta   string
	PorDefecto bool
}

// ttlOfrecidos son las tres caducidades de la pantalla. Las tres caen DENTRO del recorte del servidor
// ([60 s, 30 días]), así que esta consola no vuelve a validar el rango: el recorte vive en un solo
// sitio y aquí solo se ofrece lo que cabe en él.
//
// El default de 24 h es el del servidor y se marca como preseleccionado para que la pantalla y el
// cable digan lo mismo. Que las invitaciones duren poco es la idea: un código que se reparte por
// WhatsApp y vive un mes es un código que se queda en un chat.
var ttlOfrecidos = []ttlOpcion{
	{Segundos: 3600, Etiqueta: "1 hora"},
	{Segundos: 86400, Etiqueta: "24 horas", PorDefecto: true},
	{Segundos: 7 * 86400, Etiqueta: "7 días"},
}

// ttlElegido traduce el valor del formulario a segundos.
//
// Vacío ⇒ 0 ⇒ el campo NO viaja y manda el default del servidor. Es la única entrada que no se
// rechaza, y no es laxitud: «no digo nada de la caducidad» es una petición legítima —la que hace un
// cuerpo `{}`— y el default que resolvería esta consola sería un segundo sitio donde escribir las
// 24 h.
//
// Cualquier otro valor que no sea uno de los ofrecidos se rechaza SIN salir a la red, igual que el
// perfil de una sesión: el servidor lo recortaría a su rango y devolvería una invitación con una
// caducidad que nadie pidió, y quien la emitió creería haber elegido otra cosa.
func ttlElegido(valor string) (int, bool) {
	if valor == "" {
		return 0, true
	}
	for _, o := range ttlOfrecidos {
		if valor == strconv.Itoa(o.Segundos) {
			return o.Segundos, true
		}
	}
	return 0, false
}

// invitacionEmitida es lo que viaja dentro de la cookie efímera. Las claves son cortas porque el
// valor va en una cabecera, no porque se pretenda ofuscar nada.
type invitacionEmitida struct {
	Token  string `json:"t"`
	Expira string `json:"e,omitempty"`
}

// invitationView es una fila de la tabla de invitaciones.
//
// No lleva el código ni tiene dónde ponerlo, que es la mitad del criterio «el listado no muestra el
// token»: la otra mitad es que apiclient.Invitation tampoco lo trae. Para que un código apareciera
// aquí habría que añadirle un campo a los DOS tipos.
type invitationView struct {
	ID        string
	Short     string
	Estado    string
	ChipClass string
	Expira    string
	Rol       string
	Revocable bool
}

// estadoInvitacion traduce el estado que sirve la API a lo que se lee en la tabla, y decide si esa
// fila ofrece anular.
//
// Solo `pending` es revocable, y el desconocido tampoco: si el servidor estrenara un estado, ofrecer
// una acción sobre algo que esta consola no sabe interpretar es peor que no ofrecerla — la operación
// sigue existiendo en la API y el hueco se ve en la pantalla, que es donde se nota.
//
// El estado desconocido NO se pinta tal cual: el texto del upstream no llega a la pantalla en ningún
// sitio de esta consola, y una cadena que viene de fuera pintada como etiqueta es justo por donde se
// empieza a hacerlo.
func estadoInvitacion(status string) (etiqueta, chip string, revocable bool) {
	switch status {
	case "pending":
		return "pendiente", "wapp-chip--info", true
	case "redeemed":
		return "canjeada", "wapp-chip--success", false
	case "revoked":
		return "anulada", "wapp-chip--danger", false
	case "expired":
		return "caducada", "wapp-chip--neutral", false
	default:
		return "sin dato", "wapp-chip--neutral", false
	}
}

// invitationsView proyecta la respuesta de la API a filas de la tabla, resolviendo el nombre del rol
// contra el catálogo que la misma pantalla ya trae para el desplegable.
//
// Si el catálogo no se pudo leer —o el rol ya no está en él— se cae al identificador abreviado en vez
// de dejar la celda vacía: la invitación SÍ concede ese rol, y no decirlo sería peor que decirlo con
// un identificador feo.
func invitationsView(invitaciones []apiclient.Invitation, roles []apiclient.Role) []invitationView {
	nombres := make(map[string]string, len(roles))
	for _, r := range roles {
		nombres[r.RoleID] = r.Name
	}

	out := make([]invitationView, 0, len(invitaciones))
	for _, inv := range invitaciones {
		etiqueta, chip, revocable := estadoInvitacion(inv.Status)
		v := invitationView{
			ID:        inv.ID,
			Short:     shortID(inv.ID),
			Estado:    etiqueta,
			ChipClass: chip,
			Expira:    inv.ExpiresAt,
			Revocable: revocable,
		}
		if inv.RoleID != "" {
			if nombre, ok := nombres[inv.RoleID]; ok {
				v.Rol = nombre
			} else {
				v.Rol = shortID(inv.RoleID)
			}
		}
		out = append(out, v)
	}
	return out
}

// ShowInvitations pinta la pantalla de invitaciones: el formulario para emitir, el código recién
// emitido —si se acaba de emitir uno— y el listado con su botón de anular.
//
// Hace DOS llamadas —invitaciones y roles— porque el formulario ofrece conceder un rol al canjear, y
// cada una degrada por su lado, igual que en la pantalla de miembros. Que el catálogo de roles no se
// pueda leer NO puede tumbar esta pantalla: lo que se pierde es el desplegable opcional (que se
// omite) y el nombre bonito del rol en la tabla, no la emisión ni el listado.
//
// 🔴 EL CÓDIGO SE LEE ANTES QUE NADA, y no por orden estético: la caja se tiene que pintar aunque el
// listado falle. Perder el listado es recargar; perder el código es haber emitido una invitación que
// ya nadie puede repartir. Es la misma lección que M-10 dejó en la consola hermana, donde una
// relectura fallida abortaba la plantilla DESPUÉS del 200 y se llevaba el código por delante.
func (h *AdminHandler) ShowInvitations(c *gin.Context) {
	data := h.pageData(c, "Invitaciones")

	// El aviso que gana se decide en UNA variable y se vuelca al final. Escribir data["Error"] a
	// medida que se descubren los fallos no valdría para preguntar «¿ya hay aviso?»: pageData SIEMPRE
	// deja la clave puesta —con el texto del query string, o vacía—, así que comprobar su PRESENCIA
	// daría siempre que sí y el segundo aviso no se pintaría nunca.
	aviso := ""

	// La cookie se lee y se BORRA en el mismo gesto, así que un F5 sobre esta pantalla ya no
	// encuentra nada: se vuelve a pintar el listado y el código no reaparece. Se hace también sin
	// empresa —cuesta nada— para que un código huérfano no se quede esperando en el navegador.
	if raw := webgin.TakeOneTimeCookie(c, invitacionCookieOptions(h.cfg)); raw != "" {
		var emitida invitacionEmitida
		if err := sharedweb.DecodeCookiePayload(raw, &emitida); err != nil || emitida.Token == "" {
			// Sin el contenido de la cookie en el log: es (o era) el código.
			slog.Warn("la cookie del código de invitación llegó ilegible", "error", err)
			aviso = flashError(flashInvitationLost)
		} else {
			data["NuevoToken"] = emitida.Token
			data["NuevoTokenExpira"] = emitida.Expira
		}
	}

	// Sin empresa no hay a quién invitar, y la API respondería 403 —«no tienes permiso»—, que es un
	// diagnóstico falso: no le falta un permiso, le falta una empresa. Se explica y no se llama.
	if sinEmpresa(c) {
		if aviso != "" {
			data["Error"] = aviso
		}
		renderer.HTML(c, http.StatusOK, "invitaciones.html", data)
		return
	}

	var invitaciones []apiclient.Invitation
	var roles []apiclient.Role
	var invitacionesErr, rolesErr error

	invitacionesCode := flashCodeForInvitaciones(h.auth.withAuthRetry(c, func(accessToken string) error {
		var err error
		invitaciones, err = h.api.Invitations.List(c.Request.Context(), accessToken)
		invitacionesErr = err
		return err
	}))
	rolesCode := flashCodeFor(h.auth.withAuthRetry(c, func(accessToken string) error {
		var err error
		roles, err = h.api.Roles.List(c.Request.Context(), accessToken)
		rolesErr = err
		return err
	}))
	if sessionIsDead(invitacionesErr) || sessionIsDead(rolesErr) {
		// Si aquí había un código recién emitido, se pierde: la cookie ya se consumió arriba. Es el
		// mal menor —la sesión no vale y hay que volver a entrar—, y la invitación sigue en el
		// listado para anularla cuando se vuelva.
		h.auth.expireSession(c)
		return
	}

	if invitacionesCode != "" {
		slog.Warn("no se pudo listar las invitaciones de la empresa", "codigo", invitacionesCode, "error", invitacionesErr)
		// El aviso del código perdido manda sobre el del listado: es el único que no tiene arreglo
		// recargando.
		if aviso == "" {
			aviso = flashError(invitacionesCode)
		}
	}
	if rolesCode != "" {
		slog.Warn("no se pudo listar el catálogo de roles para la invitación", "codigo", rolesCode, "error", rolesErr)
		// El aviso del listado principal manda sobre el del catálogo: es el que explica por qué la
		// tabla está vacía.
		if aviso == "" {
			aviso = flashError(rolesCode)
		}
	}
	if aviso != "" {
		data["Error"] = aviso
	}

	data["Invitaciones"] = invitationsView(invitaciones, roles)
	data["Roles"] = rolesView(roles)
	data["TTLOpciones"] = ttlOfrecidos
	renderer.HTML(c, http.StatusOK, "invitaciones.html", data)
}

// IssueInvitation emite una invitación de un solo uso y REDIRIGE a la pantalla que enseña su código
// (POST-Redirect-GET). Ver la cabecera del fichero para el porqué de las dos mitades: el 303 y la
// cookie.
func (h *AdminHandler) IssueInvitation(c *gin.Context) {
	ttl, ok := ttlElegido(formValue(c, "ttl"))
	if !ok {
		redirectWith(c, rutaInvitaciones, flashInvalidTTL, "")
		return
	}
	roleID := formValue(c, "role_id")

	var emitida *apiclient.IssuedInvitation
	if code := flashCodeForInvitaciones(h.auth.withAuthRetry(c, func(accessToken string) error {
		var err error
		emitida, err = h.api.Invitations.Issue(c.Request.Context(), accessToken, roleID, ttl)
		return err
	})); code != "" {
		slog.Warn("no se pudo emitir la invitación", "codigo", code)
		redirectWith(c, rutaInvitaciones, code, "")
		return
	}

	valor, err := sharedweb.EncodeCookiePayload(invitacionEmitida{Token: emitida.Token, Expira: emitida.ExpiresAt})
	if err != nil {
		// La invitación YA se emitió y su código es de un solo uso: aquí ya no se puede recuperar. Se
		// dice exactamente eso —y sin el código en el log—, con el sitio donde arreglarlo: anularla.
		slog.Error("no se pudo empaquetar el código de la invitación emitida", "error", err)
		redirectWith(c, rutaInvitaciones, flashInvitationLost, "")
		return
	}

	webgin.SetOneTimeCookie(c, invitacionCookieOptions(h.cfg), valor)
	// Sin `?success=`: el acuse es el código en pantalla. Ver la nota del catálogo de flash.
	c.Redirect(http.StatusSeeOther, rutaInvitaciones)
}

// RevokeInvitation anula una invitación viva (T-A8), de modo que quien tenga ese código ya no pueda
// usarlo.
//
// El 409 —«ya se canjeó»— NO se cuenta como hecho: ver flashInvitationRedeemed. Que la fila siga en
// el listado después de anularla es lo que se quiere: quien administra necesita reconocer cuál de los
// códigos que repartió es el que ya no vale.
func (h *AdminHandler) RevokeInvitation(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		redirectWith(c, rutaInvitaciones, flashMissingField, "")
		return
	}

	code := flashCodeForInvitaciones(h.auth.withAuthRetry(c, func(accessToken string) error {
		return h.api.Invitations.Revoke(c.Request.Context(), accessToken, id)
	}))
	if code != "" {
		slog.Warn("no se pudo anular la invitación", "codigo", code)
	}
	redirectWith(c, rutaInvitaciones, code, flashInvitationRevoked)
}

// RedeemInvitation canjea el código que el invitado pegó en el formulario de sin_empresa.
//
// Va en el grupo protegido pero NO exige empresa, y es la única operación de esta consola así: quien
// canjea acaba de registrarse y su Context Token viene SIN tenant y sin un solo grant. Exigirle
// empresa sería exigirle justo lo que viene a conseguir.
//
// 🔴 TRAS EL 204 HAY QUE REFRESCAR LA SESIÓN, y no es cosmética: la persona ya es miembro en la base,
// pero el token que tiene en la mano se emitió ANTES de existir esa membresía y sigue sin empresa.
// Sin el refresco, el redirect la devolvería a la misma pantalla de «todavía no perteneces a ninguna
// empresa» que acaba de usar, justo después de leer que ya está dentro — que es la forma más rápida
// de convencer a alguien de que el canje no funcionó. El refresco vuelve a canjear el Identity Token
// y ESE canje sí encuentra la membresía.
//
// Si el refresco falla, el desenlace es a medias y se cuenta como tal (flashJoinedRelogin): la
// membresía está escrita —eso no se deshace— y lo único que falta es una sesión nueva.
//
// 🔴 El código pegado NO se registra en ninguna rama, ni siquiera al fallar: es material que autoriza
// a entrar en una empresa, y el log de esta consola lo guardaría en claro.
func (h *AdminHandler) RedeemInvitation(c *gin.Context) {
	token := formValue(c, "token")
	if token == "" {
		redirectWith(c, "/", flashMissingField, "")
		return
	}

	if code := flashCodeForCanje(h.auth.withAuthRetry(c, func(accessToken string) error {
		return h.api.Invitations.Redeem(c.Request.Context(), accessToken, token)
	})); code != "" {
		slog.Warn("no se pudo canjear la invitación", "codigo", code)
		redirectWith(c, "/", code, "")
		return
	}

	refreshToken := webgin.RefreshTokenFromContext(c)
	if refreshToken == "" {
		slog.Warn("invitación canjeada, pero la sesión no tiene refresh token con el que releer la empresa")
		redirectWith(c, "/", flashJoinedRelogin, "")
		return
	}
	if _, err := h.auth.refreshSession(c, refreshToken); err != nil {
		slog.Warn("invitación canjeada, pero el refresco de la sesión falló: la empresa no se verá hasta volver a entrar",
			"error", err)
		redirectWith(c, "/", flashJoinedRelogin, "")
		return
	}
	redirectWith(c, "/", "", flashInvitationAccepted)
}
