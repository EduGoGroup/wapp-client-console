package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
)

// TestFlash_TodoDesenlaceDelApiclientTieneTexto.
//
// El catálogo traduce CÓDIGOS a mensajes y un código desconocido cae al genérico SIN fallar: por eso
// un desenlace que se quedara sin entrada no rompería nada — el usuario leería «ocurrió un error
// inesperado» ante un problema de permisos y nadie se enteraría. Esto lo caza.
func TestFlash_TodoDesenlaceDelApiclientTieneTexto(t *testing.T) {
	t.Parallel()

	desenlaces := []error{
		apiclient.ErrUnauthorized,
		apiclient.ErrForbidden,
		apiclient.ErrNotFound,
		apiclient.ErrConflict,
		apiclient.ErrInvalidInput,
		apiclient.ErrMemberOfAnotherTenant,
		apiclient.ErrPersonUnknown,
		&apiclient.APIError{Op: "prueba", StatusCode: http.StatusInternalServerError},
		errors.New("fallo de red"),
	}
	for _, err := range desenlaces {
		code := flashCodeFor(err)
		if code == "" {
			t.Errorf("%v no se tradujo a ningún código", err)
			continue
		}
		if !flashErrors.Known(code) {
			t.Errorf("%v da el código %q, que no está en el catálogo: el usuario vería el genérico", err, code)
		}
	}
	if flashCodeFor(nil) != "" {
		t.Error("el éxito (err == nil) no debe producir código de error")
	}
}

// TestFlash_ElOrdenDeLasRamasProtegeAlMensajeEspecifico.
//
// Los DOS pares de sentinelas envueltos, en el mismo test porque comparten la regla: el específico va
// antes que el genérico o el genérico se lo come, sin que nada falle y con el usuario leyendo otra
// cosa. El orden es contrato, no estilo.
//
// Cada par se afirma con su GEMELO —el error genérico de verdad— al lado: sin él, un flashCodeFor
// que devolviera siempre el código específico pasaría el positivo.
func TestFlash_ElOrdenDeLasRamasProtegeAlMensajeEspecifico(t *testing.T) {
	t.Parallel()

	// Par 1: ErrMemberOfAnotherTenant envuelve a ErrConflict (MD-055.2).
	envuelto := fmt.Errorf("members.add: %w", apiclient.ErrMemberOfAnotherTenant)
	if got := flashCodeFor(envuelto); got != flashMemberElsewhere {
		t.Errorf("flashCodeFor(ErrMemberOfAnotherTenant) = %q, want %q", got, flashMemberElsewhere)
	}
	if got := flashCodeFor(fmt.Errorf("roles.create: %w", apiclient.ErrConflict)); got != flashConflict {
		t.Errorf("un conflicto genérico dio %q, want %q", got, flashConflict)
	}

	// Par 2: ErrPersonUnknown envuelve a ErrNotFound. Al revés, el 404 del alta —un UUID que no
	// existe en NINGUNA empresa— se explicaría como «no pertenece a tu empresa».
	if got := flashCodeFor(fmt.Errorf("members.add: %w", apiclient.ErrPersonUnknown)); got != flashPersonUnknown {
		t.Errorf("flashCodeFor(ErrPersonUnknown) = %q, want %q", got, flashPersonUnknown)
	}
	if got := flashCodeFor(fmt.Errorf("members.remove: %w", apiclient.ErrNotFound)); got != flashNotInYourTenant {
		t.Errorf("un 404 genérico dio %q, want %q", got, flashNotInYourTenant)
	}
}

// TestFlash_ElTextoDelAltaDiceQueNoExisteYNuncaQueEsDeOtraEmpresa.
//
// Es el simétrico exacto de TestFlash_ElTextoDel404NoDiceQueNoExiste, y los dos tienen que convivir:
// el MISMO código HTTP se traduce a dos textos contrarios según la operación, porque significa dos
// cosas contrarias. El negativo es el que importa —«no pertenece a tu empresa» es justo el texto que
// saldría con la rama mal ordenada— y además el mensaje tiene que decir qué hacer, que aquí sí se
// puede: pedirle a la persona su identificador.
func TestFlash_ElTextoDelAltaDiceQueNoExisteYNuncaQueEsDeOtraEmpresa(t *testing.T) {
	t.Parallel()

	msg := flashError(flashPersonUnknown)
	if !strings.Contains(msg, "no existe") {
		t.Errorf("el texto del 404 del alta no dice que no existe: %q", msg)
	}
	if strings.Contains(msg, "no pertenece a tu empresa") {
		t.Errorf("el texto del 404 del alta habla de frontera de empresa, y ahí no hay ninguna: %q", msg)
	}
	if !strings.Contains(msg, "Mi identificador") {
		t.Errorf("el texto no dice de dónde saca la persona su identificador: %q", msg)
	}
}

// TestFlash_ElAvisoDeAltaSinRolNoSeDaComoExito.
//
// Es un estado a medias —la persona entró, el rol no se le puso— y vive entre los ERRORES a
// propósito: como éxito, alguien se quedaría dentro de la empresa creyendo que tiene permisos que no
// tiene. El aviso tiene que decir las dos mitades y adónde ir a arreglarlo.
func TestFlash_ElAvisoDeAltaSinRolNoSeDaComoExito(t *testing.T) {
	t.Parallel()

	if !flashErrors.Known(flashAddedWithoutRole) {
		t.Fatal("el aviso de «incorporada sin rol» no está entre los errores")
	}
	if flashSuccesses.Known(flashAddedWithoutRole) {
		t.Error("el aviso de «incorporada sin rol» está entre los ÉXITOS: no lo es, la mitad falló")
	}
	msg := flashError(flashAddedWithoutRole)
	if !strings.Contains(msg, "quedó incorporada") {
		t.Errorf("el aviso no dice que la persona SÍ entró: %q", msg)
	}
	if !strings.Contains(msg, "Roles") {
		t.Errorf("el aviso no dice dónde asignar el rol que faltó: %q", msg)
	}
}

// TestFlash_ElTextoDel404NoDiceQueNoExiste.
//
// Es el criterio de cross-tenant en el catálogo, donde vive el texto: la plataforma responde 404 —y
// no 403— ante un identificador de otra empresa para no confirmar que existe, así que «no
// encontrado» sería una traducción equivocada del mismo código.
func TestFlash_ElTextoDel404NoDiceQueNoExiste(t *testing.T) {
	t.Parallel()

	msg := flashError(flashNotInYourTenant)
	if !strings.Contains(msg, "no pertenece a tu empresa") {
		t.Errorf("el texto del 404 no habla de la frontera de empresa: %q", msg)
	}
	for _, prohibido := range []string{"no encontrado", "no existe"} {
		if strings.Contains(strings.ToLower(msg), prohibido) {
			t.Errorf("el texto del 404 dice %q: %q", prohibido, msg)
		}
	}
}

// TestFlash_TodosLosCodigosDeLasPantallasTienenTexto cubre los que NO nacen de un error del
// apiclient: los que la propia consola emite antes de salir a la red, y los de éxito.
func TestFlash_TodosLosCodigosDeLasPantallasTienenTexto(t *testing.T) {
	t.Parallel()

	for _, code := range []string{flashSessionExpired, flashSelfRemoval, flashMissingField, flashAddedWithoutRole,
		flashSessionNotYours, flashInvalidProfile, flashSessionOffline, flashSendTimeout, flashSendNotDelivered,
		flashInvalidTTL, flashInvitationLost, flashInvitationRedeemed, flashInvitationUnknown,
		flashInvitationExpired, flashInvitationUnusable, flashJoinedRelogin} {
		if !flashErrors.Known(code) {
			t.Errorf("el código de error %q no tiene texto", code)
		}
	}
	for _, code := range []string{flashLoggedOut, flashMemberAdded, flashMemberRemoved, flashRoleCreated,
		flashRoleAssigned, flashRoleRemoved, flashMessageSent, flashProfileActive, flashProfilePassive,
		flashInvitationRevoked, flashInvitationAccepted} {
		if !flashSuccesses.Known(code) {
			t.Errorf("el código de éxito %q no tiene texto", code)
		}
	}
}

// TestFlash_ElPlanoDeInvitacionesConservaElSignificadoDelConflicto.
//
// flashCodeForInvitaciones existe por UN desenlace: el 409 de revocar, que aquí significa «esa
// invitación ya se canjeó». El genérico lo traduciría como «ya existe algo con ese nombre en tu
// empresa», que no describe nada de lo que pasó y, peor, deja a quien administra sin saber que la
// persona ya está DENTRO y que retirarla es otra operación.
//
// El gemelo —lo que el genérico habría dicho— va al lado: si alguien «simplificara» el traductor,
// este test se cae en vez de dejar un texto equivocado en pantalla.
func TestFlash_ElPlanoDeInvitacionesConservaElSignificadoDelConflicto(t *testing.T) {
	t.Parallel()

	yaCanjeada := fmt.Errorf("invitations.revoke: %w", apiclient.ErrInvitationRedeemed)
	if got := flashCodeForInvitaciones(yaCanjeada); got != flashInvitationRedeemed {
		t.Errorf("el 409 de revocar dio %q, want %q", got, flashInvitationRedeemed)
	}
	if got := flashCodeFor(yaCanjeada); got == flashInvitationRedeemed {
		t.Error("el traductor genérico ya distingue este 409: este test dejó de probar nada")
	}

	// Y el resto delega: un traductor propio que copiara la tabla sería una tabla paralela.
	for _, caso := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("x: %w", apiclient.ErrForbidden), flashForbidden},
		{fmt.Errorf("x: %w", apiclient.ErrNotFound), flashNotInYourTenant},
		{errors.New("fallo de red"), flashUpstreamUnavailable},
		{nil, ""},
	} {
		if got := flashCodeForInvitaciones(caso.err); got != caso.want {
			t.Errorf("flashCodeForInvitaciones(%v) = %q, want %q", caso.err, got, caso.want)
		}
	}
}

// TestFlash_ElCanjeConservaSusTresSignificadosYNingunoCaeAlGenerico.
//
// Los tres desenlaces malos del canje llevan a acciones DISTINTAS —volver a copiar el código, pedir
// otro, o hablar con quien lo mandó— y quien los lee no administra nada: acaba de registrarse. Con el
// traductor genérico, dos de los tres dirían «no pertenece a tu empresa» (a alguien que todavía no
// tiene ninguna) y el tercero «inténtalo de nuevo en un momento» ante un código caducado, que es
// mandar a repetir algo que fallará igual para siempre.
//
// Cada caso va con su gemelo genérico para que el test se caiga si alguien funde los traductores.
func TestFlash_ElCanjeConservaSusTresSignificadosYNingunoCaeAlGenerico(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre     string
		err        error
		want       string
		genericoDa string
	}{
		{"no existe", apiclient.ErrInvitationUnknown, flashInvitationUnknown, flashNotInYourTenant},
		{"caducada", apiclient.ErrInvitationExpired, flashInvitationExpired, flashUpstreamUnavailable},
		{"ya usada", apiclient.ErrInvitationUnusable, flashInvitationUnusable, flashConflict},
	}
	for _, caso := range casos {
		envuelto := fmt.Errorf("invitations.redeem: %w", caso.err)
		if got := flashCodeForCanje(envuelto); got != caso.want {
			t.Errorf("%s dio %q, want %q", caso.nombre, got, caso.want)
		}
		// El gemelo: esto es lo que el genérico habría dicho, y por eso hace falta el traductor.
		if got := flashCodeFor(envuelto); got != caso.genericoDa {
			t.Errorf("%s: el genérico dio %q, want %q — si ya distingue el desenlace, este test dejó de probar nada",
				caso.nombre, got, caso.genericoDa)
		}
	}

	// Los tres textos son distintos entre sí: dos desenlaces con el mismo consejo dejarían a quien
	// canjea intentando lo que no toca.
	vistos := make(map[string]string, len(casos))
	for _, caso := range casos {
		texto := flashError(caso.want)
		if otro, repetido := vistos[texto]; repetido {
			t.Errorf("%q y %q dan el MISMO texto: %q", caso.nombre, otro, texto)
		}
		vistos[texto] = caso.nombre
	}

	// Y lo que no es de este plano sigue cayendo por el genérico.
	if got := flashCodeForCanje(fmt.Errorf("x: %w", apiclient.ErrUnauthorized)); got != flashSessionExpired {
		t.Errorf("flashCodeForCanje(401) = %q, want %q", got, flashSessionExpired)
	}
	if got := flashCodeForCanje(nil); got != "" {
		t.Errorf("flashCodeForCanje(nil) = %q, want \"\"", got)
	}
}

// TestFlash_ElAvisoDeCaducadoNoMandaAReintentar: el 410 del canje NO se arregla insistiendo. El texto
// se fija por su EFECTO —a dónde manda a quien lo lee—, no por su redacción.
func TestFlash_ElAvisoDeCaducadoNoMandaAReintentar(t *testing.T) {
	t.Parallel()

	texto := flashError(flashInvitationExpired)
	if !strings.Contains(texto, "Pídele una nueva") {
		t.Errorf("el aviso de caducado no manda a pedir otra invitación: %q", texto)
	}
	if strings.Contains(strings.ToLower(texto), "inténtalo de nuevo") {
		t.Errorf("el aviso de caducado manda a reintentar algo que fallará igual siempre: %q", texto)
	}
}

// TestFlash_ElPlanoDeSesionesConservaSusTresSignificados.
//
// flashCodeForSessions existe porque el traductor GENÉRICO se comería tres desenlaces que en esta
// pantalla sí significan algo, y los tres fallos serían silenciosos: todo seguiría verde y el usuario
// leería un texto que no le sirve. Cada caso va con su GEMELO —lo que el genérico habría dicho— para
// que el test se caiga si alguien «simplifica» el traductor.
func TestFlash_ElPlanoDeSesionesConservaSusTresSignificados(t *testing.T) {
	t.Parallel()

	// 502: el teléfono está desconectado. El genérico lo llamaría «no se pudo completar».
	offline := &apiclient.APIError{Op: "messages.send", StatusCode: http.StatusBadGateway}
	if got := flashCodeForSessions(offline); got != flashSessionOffline {
		t.Errorf("el 502 dio %q, want %q", got, flashSessionOffline)
	}
	if got := flashCodeFor(offline); got == flashSessionOffline {
		t.Error("el traductor genérico ya distingue el 502: este test dejó de probar nada")
	}

	// 504: el acuse no llegó a tiempo, que NO es «no se envió».
	timeout := &apiclient.APIError{Op: "messages.send", StatusCode: http.StatusGatewayTimeout}
	if got := flashCodeForSessions(timeout); got != flashSendTimeout {
		t.Errorf("el 504 dio %q, want %q", got, flashSendTimeout)
	}
	if got := flashCodeFor(timeout); got == flashSendTimeout {
		t.Error("el traductor genérico ya distingue el 504: este test dejó de probar nada")
	}

	// 404: frontera de empresa, con el sustantivo de ESTA pantalla. El genérico habla de roles.
	notFound := fmt.Errorf("sessions.list: %w", apiclient.ErrNotFound)
	if got := flashCodeForSessions(notFound); got != flashSessionNotYours {
		t.Errorf("el 404 de sesiones dio %q, want %q", got, flashSessionNotYours)
	}
	if got := flashCodeFor(notFound); got != flashNotInYourTenant {
		t.Errorf("el genérico dejó de traducir el 404 a %q: dio %q", flashNotInYourTenant, got)
	}

	// Y el resto sigue cayendo por el genérico: un traductor propio que no delegara sería una tabla
	// paralela que se desincroniza.
	for _, caso := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("x: %w", apiclient.ErrForbidden), flashForbidden},
		{fmt.Errorf("x: %w", apiclient.ErrInvalidInput), flashInvalidInput},
		{fmt.Errorf("x: %w", apiclient.ErrUnauthorized), flashSessionExpired},
		{errors.New("fallo de red"), flashUpstreamUnavailable},
		{nil, ""},
	} {
		if got := flashCodeForSessions(caso.err); got != caso.want {
			t.Errorf("flashCodeForSessions(%v) = %q, want %q", caso.err, got, caso.want)
		}
	}
}

// TestFlash_ElAvisoDel504NoMandaAReintentarSinComprobar.
//
// Un 504 del envío NO significa «no se envió»: la nube ya empujó el comando al equipo y lo que expiró
// es la espera del acuse. El texto del BFF decía «Inténtalo de nuevo», y repetir un envío que quizá
// salió le manda el mensaje DOS VECES a un cliente real. Este test fija el texto por su efecto.
func TestFlash_ElAvisoDel504NoMandaAReintentarSinComprobar(t *testing.T) {
	t.Parallel()

	texto := flashError(flashSendTimeout)
	if !strings.Contains(texto, "no se sabe si el mensaje salió") {
		t.Error("el aviso del 504 tiene que decir que el desenlace es DESCONOCIDO, no que falló")
	}
	if !strings.Contains(texto, "ANTES de repetirlo") {
		t.Error("el aviso del 504 tiene que mandar a comprobar antes de reintentar")
	}
}

// TestFlash_UnCodigoInventadoNoVuelveAlaPantalla: el `?error=` transporta un CÓDIGO, nunca un
// mensaje. Lo que llegue por la URL no se pinta jamás, ni escapado.
func TestFlash_UnCodigoInventadoNoVuelveAlaPantalla(t *testing.T) {
	t.Parallel()

	inyectado := "<script>alert(1)</script>"
	if got := flashError(inyectado); got == inyectado || strings.Contains(got, "script") {
		t.Errorf("el código del query string acabó en el mensaje: %q", got)
	}
	if got := flashSuccess(inyectado); strings.Contains(got, "script") {
		t.Errorf("el código del query string acabó en el mensaje de éxito: %q", got)
	}
}
