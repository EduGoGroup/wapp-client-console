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

	for _, code := range []string{flashSessionExpired, flashSelfRemoval, flashMissingField, flashAddedWithoutRole} {
		if !flashErrors.Known(code) {
			t.Errorf("el código de error %q no tiene texto", code)
		}
	}
	for _, code := range []string{flashLoggedOut, flashMemberAdded, flashMemberRemoved, flashRoleCreated,
		flashRoleAssigned, flashRoleRemoved} {
		if !flashSuccesses.Known(code) {
			t.Errorf("el código de éxito %q no tiene texto", code)
		}
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
