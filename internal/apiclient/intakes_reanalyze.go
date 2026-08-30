package apiclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// Razones por las que no hay literal del que re-analizar. Van EXPORTADAS porque la pantalla las
// necesita en dos sitios distintos y tienen que decir lo mismo en los dos: al leer el detalle —donde
// se deducen de `source_text` + `literal_pruned_at`— y al leer el 422 de `/reanalyze`. Dos literales
// sueltos acabarían discrepando.
const (
	SourcePurged      = "purged"
	SourceNeverStored = "never_stored"
)

// IntakeReanalysis es el 200 de POST /api/v1/intakes/{id}/reanalyze.
//
// 🔴 Los dos campos que más fácil se leen mal, y por eso se documentan aquí y no en la pantalla:
//
//   - `Status` vale SIEMPRE «processing» y NO es el estado del job (que nace `pending`). Es la
//     palabra con la que el endpoint dice «te acepté el encargo», nada más.
//   - `RevisionNo` es el número que la revisión TENDRÁ. Cuando esta respuesta llega, la revisión
//     todavía NO EXISTE: por eso esta ruta —a diferencia de aprobar o corregir— no devuelve el
//     detalle. Prometer en la UI que ya está lista sería prometer algo que no ha pasado.
type IntakeReanalysis struct {
	IntakeID   string `json:"intake_id"`
	RevisionNo int    `json:"revision_no"`
	JobID      string `json:"job_id"`
	Via        string `json:"via"`
	Status     string `json:"status"`
}

// reanalyzeIntakeRequest es el cuerpo de POST /api/v1/intakes/{id}/reanalyze.
//
// 🔴 NO TIENE CAMPO `via`, Y ESO ES LA DECISIÓN, no un olvido (D-044.51). El contrato acepta un `via`
// opcional pero solo para AFIRMAR la vía ya configurada de la empresa: mandar una distinta es un 400
// `invalid_via`, porque cambiar de vía es un acto de configuración (`PUT /api/v1/tenant-llm`, con su
// consentimiento) y no un parámetro de una llamada suelta que mandaría el texto de un cliente a un
// tercero de pago. Omitirlo es EQUIVALENTE a afirmar la configurada y, además, no puede
// desincronizarse el día que la empresa la cambie.
//
// `Text` es material EXTRA del dueño (una transcripción pegada): SUMA al literal del hilo, no lo
// sustituye. Vacío ⇒ no se manda, que es el caso corriente («regenera otra vez, según el origen»).
type reanalyzeIntakeRequest struct {
	Text string `json:"text,omitempty"`
}

// LLMCredentialsMissingError es el 422 `llm_credentials_missing`: la feature SÍ está, la credencial
// no.
//
// 🔴 Es un caso DISTINTO del 403 y el contrato los separa a propósito: el 403 es «tu plan no lo
// incluye» y lleva al paywall; este es «configura tus credenciales» y lleva a los ajustes. Tratarlos
// igual mandaría a comprar algo que la empresa ya tiene.
type LLMCredentialsMissingError struct {
	Via string
}

func (e *LLMCredentialsMissingError) Error() string {
	return fmt.Sprintf("apiclient: la vía %q no tiene credencial configurada", e.Via)
}

// Unwrap devuelve ErrInvalidInput, que es donde statusError mete el 422.
func (e *LLMCredentialsMissingError) Unwrap() error { return ErrInvalidInput }

// LLMCredentialsMissingOf extrae el rechazo por credencial (nil, false si no lo es).
func LLMCredentialsMissingOf(err error) (*LLMCredentialsMissingError, bool) {
	var missing *LLMCredentialsMissingError
	if errors.As(err, &missing) {
		return missing, true
	}
	return nil, false
}

// SourceUnavailableError es el 422 `source_unavailable`: no hay literal del que re-analizar. `Reason`
// distingue las dos historias —`purged` (existió y venció por retención) y `never_stored` (el plan de
// la empresa no guardaba el texto cuando ocurrió)—, y son dos mensajes distintos porque una es una
// pérdida y la otra nunca fue una promesa.
type SourceUnavailableError struct {
	Reason string
}

func (e *SourceUnavailableError) Error() string {
	return fmt.Sprintf("apiclient: no hay literal del que re-analizar (%s)", e.Reason)
}

// Purged responde si el literal EXISTIÓ y se podó.
func (e *SourceUnavailableError) Purged() bool { return e.Reason == SourcePurged }

// Unwrap devuelve ErrInvalidInput, que es donde statusError mete el 422.
func (e *SourceUnavailableError) Unwrap() error { return ErrInvalidInput }

// SourceUnavailableOf extrae el rechazo por fuente (nil, false si no lo es).
func SourceUnavailableOf(err error) (*SourceUnavailableError, bool) {
	var missing *SourceUnavailableError
	if errors.As(err, &missing) {
		return missing, true
	}
	return nil, false
}

// ReanalysisInProgressError es el 422 `reanalysis_in_progress`: ya hay un trabajo no terminal para
// esta solicitud. Trae el job para poder nombrarlo, que es lo único con lo que el dueño distingue «no
// se hizo» de «se está haciendo».
//
// ⚠️ Envuelve a ErrInvalidInput y NO a ErrConflict, aunque «ya hay uno en curso» suene a conflicto:
// el cloud lo emite con 422 y esta traducción no reescribe el código que da la plataforma. Quien
// quiera este significado pregunta por el tipo.
type ReanalysisInProgressError struct {
	JobID string
}

func (e *ReanalysisInProgressError) Error() string {
	return fmt.Sprintf("apiclient: ya hay un re-análisis en curso (job %q)", e.JobID)
}

// Unwrap devuelve ErrInvalidInput, que es donde statusError mete el 422.
func (e *ReanalysisInProgressError) Unwrap() error { return ErrInvalidInput }

// ReanalysisInProgressOf extrae el rechazo por concurrencia (nil, false si no lo es).
func ReanalysisInProgressOf(err error) (*ReanalysisInProgressError, bool) {
	var running *ReanalysisInProgressError
	if errors.As(err, &running) {
		return running, true
	}
	return nil, false
}

// InvalidViaError es el 400 `invalid_via`.
//
// ⚠️ Este cliente NUNCA manda `via`, así que en teoría no puede provocarlo. Se traduce igual —y no se
// deja caer en el error genérico— porque si algún día llega significa que alguien reintrodujo el
// campo, y un aviso nombrado es lo que lo delata en vez de un «no se pudo, inténtalo de nuevo».
type InvalidViaError struct {
	Via string
}

func (e *InvalidViaError) Error() string {
	return fmt.Sprintf("apiclient: la plataforma rechazó la vía %q", e.Via)
}

// Unwrap devuelve ErrInvalidInput: es un 400.
func (e *InvalidViaError) Unwrap() error { return ErrInvalidInput }

// InvalidViaOf extrae el rechazo por vía (nil, false si no lo es).
func InvalidViaOf(err error) (*InvalidViaError, bool) {
	var invalid *InvalidViaError
	if errors.As(err, &invalid) {
		return invalid, true
	}
	return nil, false
}

// TextTooLongError es el 400 `text_too_long`: el material extra que pegó el dueño no cabe. Trae
// CUÁNTAS runas mandó y cuántas caben, que es lo único con lo que puede recortar sin adivinar.
//
// 🆕 🔴 ESTE NO VIENE DEL BFF: el BFF no lo traducía y lo dejaba caer en su error genérico, así que
// la dueña que pegara una transcripción larga leía «no se pudo, inténtalo de nuevo» y volvía a pegar
// lo mismo. El cloud lo emite (`publicapi/reanalyze.go`, junto a `invalid_via`), y el sexto motivo se
// traduce aquí porque el consejo que da —«recorta hasta N»— no lo da ningún otro.
type TextTooLongError struct {
	Runes int
	Max   int
}

func (e *TextTooLongError) Error() string {
	return fmt.Sprintf("apiclient: el texto tiene %d runas y el máximo es %d", e.Runes, e.Max)
}

// Unwrap devuelve ErrInvalidInput: es un 400.
func (e *TextTooLongError) Unwrap() error { return ErrInvalidInput }

// TextTooLongOf extrae el rechazo por longitud (nil, false si no lo es).
func TextTooLongOf(err error) (*TextTooLongError, bool) {
	var tooLong *TextTooLongError
	if errors.As(err, &tooLong) {
		return tooLong, true
	}
	return nil, false
}

// ReanalyzeIntake pide re-interpretar la solicitud desde el literal original del cliente, vía
// POST /api/v1/intakes/{id}/reanalyze (Plan 044 · T4.7 sobre el endpoint de T4.6).
//
// `text` es material EXTRA opcional del dueño (una transcripción pegada) y SUMA al origen. La vía NO
// viaja: sale de la configuración de la empresa (ver reanalyzeIntakeRequest).
//
// 🔴 El 200 NO significa que la nueva interpretación esté lista: abre un trabajo que corre por
// detrás, y la revisión que anuncia todavía no existe. Quien pinte esta respuesta tiene que decirlo.
//
// 🔴 Es la ÚNICA ruta de la bandeja SIN el middleware de entitlements, y eso es deliberado en el
// cloud: el 400 de forma (`invalid_via`) tiene que ganarle al gate de feature, y el segundo gate
// (`api_llm`) depende de la vía efectiva, que solo se conoce tras leer `tenant_llm`. El 403 lo emite
// el propio handler con el mismo cuerpo, así que aquí se traduce igual. Por eso también es la única
// donde `FeatureNotEnabledError.Feature` puede valer `llm_intake` O `api_llm`.
//
// Errores: *FeatureNotEnabledError (403), *LLMCredentialsMissingError, *SourceUnavailableError,
// *ReanalysisInProgressError (422 nombrados), *InvalidViaError y *TextTooLongError (400 nombrados) y
// los sentinelas generales para 404/5xx. NO emite 409.
func (c *IntakesClient) ReanalyzeIntake(ctx context.Context, accessToken, id, text string) (*IntakeReanalysis, error) {
	const op = "intakes.reanalyze"
	req, err := c.t.newAuthedRequest(ctx, http.MethodPost,
		"/api/v1/intakes/"+pathSegment(id)+"/reanalyze",
		reanalyzeIntakeRequest{Text: text}, accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := c.t.doTyped(req, op, reanalyzeError)
	if err != nil {
		return nil, err
	}
	var out IntakeReanalysis
	if err := decodeJSON(resp, op, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// reanalyzeError traduce un no-2xx del re-análisis. Decide por la CLAVE `error` del cuerpo porque el
// código HTTP NO basta: el 400 puede ser dos cosas y el 422 tres historias distintas, y solo las
// separa la clave.
func reanalyzeError(op string, resp *http.Response) error {
	body := readIntakeError(resp)
	if err := intakeCommonError(op, resp.StatusCode, body); err != nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusBadRequest:
		switch body.Error {
		case errInvalidVia:
			return &InvalidViaError{Via: body.Via}
		case errTextTooLong:
			return &TextTooLongError{Runes: body.Runes, Max: body.Max}
		}
	case http.StatusUnprocessableEntity:
		switch body.Error {
		case errLLMCredentialsMissing:
			return &LLMCredentialsMissingError{Via: body.Via}
		case errSourceUnavailable:
			return &SourceUnavailableError{Reason: body.Reason}
		case errReanalysisInProgress:
			return &ReanalysisInProgressError{JobID: body.JobID}
		}
	}
	return statusError(op, resp.StatusCode)
}
