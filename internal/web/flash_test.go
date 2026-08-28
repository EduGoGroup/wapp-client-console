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
// ErrMemberOfAnotherTenant ENVUELVE a ErrConflict. Si flashCodeFor preguntara antes por el genérico,
// la guarda de membresía única (MD-055.2) se explicaría como «ya existe algo con ese nombre», que no
// dice nada de lo que de verdad pasó. El orden es contrato, no estilo.
func TestFlash_ElOrdenDeLasRamasProtegeAlMensajeEspecifico(t *testing.T) {
	t.Parallel()

	envuelto := fmt.Errorf("members.add: %w", apiclient.ErrMemberOfAnotherTenant)
	if got := flashCodeFor(envuelto); got != flashMemberElsewhere {
		t.Errorf("flashCodeFor(ErrMemberOfAnotherTenant) = %q, want %q", got, flashMemberElsewhere)
	}
	if got := flashCodeFor(fmt.Errorf("roles.create: %w", apiclient.ErrConflict)); got != flashConflict {
		t.Errorf("un conflicto genérico dio %q, want %q", got, flashConflict)
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

	for _, code := range []string{flashSessionExpired, flashSelfRemoval, flashMissingField} {
		if !flashErrors.Known(code) {
			t.Errorf("el código de error %q no tiene texto", code)
		}
	}
	for _, code := range []string{flashLoggedOut, flashMemberRemoved, flashRoleCreated, flashRoleAssigned, flashRoleRemoved} {
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
