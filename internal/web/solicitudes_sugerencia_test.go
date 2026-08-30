package web

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sharedweb "github.com/EduGoGroup/wapp-shared/web"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
	"github.com/EduGoGroup/wapp-client-console/internal/config"
)

// solicitudes_sugerencia_test.go vigila LA DÉCIMA RUTA DE LA BANDEJA (Plan 047 · T7.6): la respuesta
// redactada con la voz de la dueña.
//
// 🔒 LO QUE ESTE FICHERO AFIRMA, y por qué cada cosa necesita su aserto:
//
//  1. que el POST-Redirect-GET FUNCIONA, contado EN EL RECEPTOR. Un F5 sobre esta acción cuesta
//     20-40 s de modelo, y «no se repitió» solo se puede afirmar contando las llamadas que el doble
//     del cloud RECIBIÓ: el log del emisor dice lo que el emisor cree que mandó;
//  2. que el texto sobrevive UNA vez y ni una más;
//  3. que una cotización LARGA no se pierde. El mecanismo de un solo uso de esta casa nació para un
//     token de 43 caracteres y NO tiene guarda de tamaño, así que sin el tope el desenlace sería el
//     peor posible: la dueña espera 40 s, la página redirige y el texto NO ESTÁ;
//  4. que la cotización de A no se pinta en la pantalla de B. Son DOS cerraduras y hacen falta las
//     dos: el Path lo pone el navegador y el identificador del sobre lo comprueba el servidor;
//  5. que los DOS plazos de servidor dejan llegar la respuesta —el deadline por petición y el write
//     deadline—, éste último contra un http.Server DE VERDAD, que es el único sitio donde se puede
//     comprobar: sobre un ResponseRecorder no hay conexión que se pueda cortar;
//  6. que el ORIGEN se pinta, y con los trece motivos traducidos. Es la única señal que distingue «la
//     voz funciona» de «llevo semanas leyendo el texto sobrio»: esta puerta nunca da 502.
//
// 🔴 LO QUE ESTE FICHERO **NO** PUEDE AFIRMAR, y hay que decirlo con estas palabras: que la espera
// real quepa en los plazos. Aquí el otro lado del cable es un doble que contesta al instante o duerme
// lo que se le diga; lo medido en campo —24,8 / 28,4 / 29,7 / 35,5 s contra UAT— es de otra corrida y
// de otro repo. Que el navegador descarte una cookie de más de 4 KB tampoco se demuestra aquí:
// httptest no es un navegador. Lo que sí se demuestra es la DECISIÓN de esta consola ante un valor
// que se pasa del tope.

// --- El arnés ---

// Las rutas de esta casilla: la de la consola y la del cloud, con el identificador RESUELTO.
var (
	rutaSugerir   = rutaDetalle + sufijoSugerir
	puertaSugerir = "POST /api/v1/intakes/" + testIntakeID + "/quote-suggestion"
)

// sugerenciaBody es el 200 del generador.
func sugerenciaBody(texto, origen, respaldo string) string {
	return `{"rendered_text":` + jsonString(texto) + `,"source":"` + origen +
		`","fallback_reason":"` + respaldo + `"}`
}

// jsonString escapa lo justo para meter un texto en el literal de arriba: comillas y saltos de línea,
// que es lo único que traen las cotizaciones de estos tests.
func jsonString(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(v) + `"`
}

// laCotizacionDelModelo es lo que devuelve el doble en el camino bueno, y laMarcaDelModelo es el
// trozo por el que se la reconoce.
//
// 🔴 LA MARCA TIENE QUE SER UNA FRASE QUE LA PROPUESTA DE ESTA CONSOLA NO PUEDA TENER, y no es una
// precaución teórica: la primera versión de estos tests buscaba «portería», que está en el
// `customer_note` de la solicitud de campo y por tanto TAMBIÉN en la propuesta que arma
// propuestaDeRespuesta. Con esa marca, «la sugerencia se pintó» y «se pintó lo de siempre» daban el
// mismo verde, y el test de que el sobre se consume UNA vez salía rojo por la razón equivocada.
const (
	laCotizacionDelModelo = "¡Hola Ana! Te confirmo la torta de chocolate sin gluten para el " +
		"sábado. Queda en 21.000 con el envío incluido."
	laMarcaDelModelo = "con el envío incluido"
)

// sugerenciaDelRespaldo es el 200 del generador cuando NO lo redactó el modelo. Es el cuerpo con el
// que la familia de seguridad recorre la pantalla: pinta el párrafo del origen en su forma LARGA
// —procedencia más motivo—, que es la que más texto mete en el HTML y por tanto donde más fácil se
// colaría un `style=`.
var sugerenciaDelRespaldo = sugerenciaBody(laCotizacionDelModelo,
	apiclient.QuoteSourceDeterministic, apiclient.QuoteFallbackLLMFailed)

// sugerenciaRouter monta el router con el generador contestando que sí.
func sugerenciaRouter(t *testing.T, cambios map[string]stubResponse) (http.Handler, *stubAPI) {
	t.Helper()
	rutas := rutasSolicitudes()
	rutas["GET /api/v1/intakes/{id}"] = stubResponse{http.StatusOK, solicitudDeCampo()}
	rutas["POST /api/v1/intakes/{id}/quote-suggestion"] = stubResponse{http.StatusOK,
		sugerenciaBody(laCotizacionDelModelo, apiclient.QuoteSourceLLM, "")}
	for ruta, resp := range cambios {
		rutas[ruta] = resp
	}
	api := newStubAPI(t, rutas)
	return adminRouter(api), api
}

// veces cuenta cuántas veces recibió el doble esa ruta. Es EL contador del criterio 1: lo que importa
// no es lo que esta consola crea que mandó, sino lo que llegó al otro lado.
func veces(api *stubAPI, ruta string) int {
	n := 0
	for _, r := range api.Requests() {
		if r.Route() == ruta {
			n++
		}
	}
	return n
}

// cookieDeSugerencia devuelve la cookie efímera que puso una respuesta, o nil si no la puso.
func cookieDeSugerencia(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == sugerenciaCookieName {
			return ck
		}
	}
	return nil
}

// loQueLeQueda devuelve la cookie que el NAVEGADOR seguiría teniendo tras una respuesta: nil si esa
// respuesta la retiró (MaxAge negativo), y la misma si no la tocó.
//
// 🔑 EXISTE PARA NO REGALAR EL CRITERIO. Pedir la recarga «sin la cookie» a mano es asumir el borrado
// que estos tests vienen a comprobar: con el consumo roto, un navegador de verdad la seguiría
// mandando y la cotización sobreviviría recarga tras recarga hasta que venciera el MaxAge. Modelando
// al navegador, esa mutación sale roja; escribiéndolo a mano, sale verde.
func loQueLeQueda(puesta *http.Cookie, respuesta *httptest.ResponseRecorder) *http.Cookie {
	if retirada := cookieDeSugerencia(respuesta); retirada != nil && retirada.MaxAge < 0 {
		return nil
	}
	return puesta
}

// pedirSugerencia lanza el POST de la casilla sobre la ruta que se le diga.
func pedirSugerencia(t *testing.T, router http.Handler, ruta string) *httptest.ResponseRecorder {
	t.Helper()
	return postFormWithCSRF(router, ruta, url.Values{}, clientSessionCookie(t))
}

// --- Criterio 1: el PRG, contado en el RECEPTOR ---

// TestSugerencia_ElPOSTRedirigeYUnF5NoVuelveAPedirla.
//
// 🔑 EL ASERTO QUE CUENTA ES EL DEL RECEPTOR. «Redirigió» se puede comprobar mirando el 303, pero
// «un F5 no repite la inferencia» no: el F5 es un GET, y lo único que demuestra que el modelo no
// volvió a trabajar es que el doble del cloud recibió UNA sola llamada. Un test que mirara el log de
// esta consola estaría creyendo lo que el emisor dice que hizo.
//
// El recorrido es el de un navegador: POST → 303 → GET con la cookie → F5 (GET sin ella, porque el
// GET anterior la borró).
func TestSugerencia_ElPOSTRedirigeYUnF5NoVuelveAPedirla(t *testing.T) {
	t.Parallel()
	router, api := sugerenciaRouter(t, nil)

	rec := pedirSugerencia(t, router, rutaSugerir)
	destino := redirectTarget(t, rec)
	if !strings.Contains(destino, rutaDetalle) || !strings.Contains(destino, "?success="+flashSugerenciaLista) {
		t.Fatalf("el POST no salió por el PRG: fue a %q", destino)
	}

	ck := cookieDeSugerencia(rec)
	if ck == nil {
		t.Fatal("el 303 no puso la cookie efímera: la cotización se habría perdido en el redirect")
	}

	pintada := getConCookies(router, rutaDetalle, clientSessionCookie(t), ck)
	if pintada.Code != http.StatusOK {
		t.Fatalf("el GET tras el redirect respondió %d. Body: %s", pintada.Code, pintada.Body.String())
	}
	if !strings.Contains(pintada.Body.String(), laMarcaDelModelo) {
		t.Errorf("la cotización no llegó al GET: el PRG perdió justo lo que costó 40 s de modelo. "+
			"Campo: %s", bloque(t, pintada.Body.String(), `id="rendered_text"`, "</textarea>"))
	}

	// EL F5, hecho COMO LO HARÍA UN NAVEGADOR: con la cookie que le quede tras el primer GET. Ver
	// loQueLeQueda — pedir la página «sin la cookie» a mano sería asumir el borrado que este test
	// existe para comprobar.
	getConCookies(router, rutaDetalle, clientSessionCookie(t), loQueLeQueda(ck, pintada))

	if n := veces(api, puertaSugerir); n != 1 {
		t.Errorf("el generador recibió %d llamadas y debía recibir 1: cada una cuesta 20-40 s de "+
			"modelo. Recibidas: %v", n, api.Requests())
	}
}

// --- Criterio 2: una sola vez ---

// TestSugerencia_ElTextoSobreviveUNASolaVez.
//
// 🔑 ES EL TEST DE LA MUTACIÓN M1 (no consumir el sobre). Leer la cookie sin borrarla dejaría la
// cotización viva hasta que venciera el MaxAge: cada recarga de esa pantalla —y las que se abrieran
// desde el historial— seguiría enseñando una propuesta vieja como si se acabara de pedir, con su
// línea de origen diciendo que la redactó el modelo hace un minuto.
func TestSugerencia_ElTextoSobreviveUNASolaVez(t *testing.T) {
	t.Parallel()
	router, _ := sugerenciaRouter(t, nil)

	ck := cookieDeSugerencia(pedirSugerencia(t, router, rutaSugerir))
	if ck == nil {
		t.Fatal("el 303 no puso la cookie efímera")
	}

	primera := getConCookies(router, rutaDetalle, clientSessionCookie(t), ck)
	if !strings.Contains(primera.Body.String(), laMarcaDelModelo) {
		t.Fatal("el primer GET no pintó la cotización: el resto del test no significaría nada")
	}
	// El propio GET emite el borrado, y eso es lo que hace que el navegador no la vuelva a mandar.
	if borrado := cookieDeSugerencia(primera); borrado == nil || borrado.MaxAge >= 0 {
		t.Errorf("el GET no borró la cookie en el mismo gesto: %v", borrado)
	}

	segunda := getConCookies(router, rutaDetalle, clientSessionCookie(t), loQueLeQueda(ck, primera))
	if strings.Contains(segunda.Body.String(), laMarcaDelModelo) {
		t.Errorf("la cotización sobrevivió a la recarga: el sobre no se consumió. Campo: %s",
			bloque(t, segunda.Body.String(), `id="rendered_text"`, "</textarea>"))
	}
	if strings.Contains(segunda.Body.String(), `id="solicitud-sugerencia-origen"`) {
		t.Error("la línea de origen sobrevivió a la recarga: diría que el modelo redactó la propuesta " +
			"que esta consola arma con las líneas")
	}
}

// --- Criterio 3: la cotización larga no se pierde ---

// TestSugerencia_UnaCotizacionLargaNoSePierdeYSeDegradaDeclarada.
//
// 🔑 ES EL TEST DE LA MUTACIÓN M3 (quitar el tope), y el aserto NO puede ser «el navegador descartó
// la cookie»: httptest no es un navegador y aquí una cookie de 8 KB viajaría tan feliz. Lo que se
// afirma es la DECISIÓN de esta consola —200 pintando sobre el POST en vez de 303—, que es
// exactamente lo que se pierde si alguien quita el tope: entonces contestaría 303, y en un navegador
// de verdad la dueña llegaría a una pantalla sin su cotización después de esperar 40 s.
//
// 🔴 Y el aviso que se lee es el de ÉXITO, no un error: no ha fallado nada. Lo que se degrada es el
// PRG, que es la mitad prescindible.
func TestSugerencia_UnaCotizacionLargaNoSePierdeYSeDegradaDeclarada(t *testing.T) {
	t.Parallel()

	larga := "Detalle del pedido, línea por línea. " + strings.Repeat(
		"Torta de chocolate sin gluten, 21.000 la unidad, entrega el sábado por la mañana. ", 60)
	router, api := sugerenciaRouter(t, map[string]stubResponse{
		"POST /api/v1/intakes/{id}/quote-suggestion": {http.StatusOK,
			sugerenciaBody(larga, apiclient.QuoteSourceLLM, "")},
	})

	rec := pedirSugerencia(t, router, rutaSugerir)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: por encima del tope se pinta sobre el POST, porque no perder "+
			"el texto manda sobre ahorrar la tecla", rec.Code)
	}
	if ck := cookieDeSugerencia(rec); ck != nil {
		t.Errorf("se puso la cookie con un valor de %d bytes pese al tope de %d: el navegador la "+
			"descartaría en silencio y la dueña se quedaría sin la cotización", len(ck.Value), maxCookieSugerencia)
	}
	out := rec.Body.String()
	if !strings.Contains(out, "entrega el sábado por la mañana") {
		t.Errorf("la cotización larga se perdió en el repintado. Campo: %s",
			bloque(t, out, `id="rendered_text"`, "</textarea>"))
	}
	if !strings.Contains(out, flashSuccess(flashSugerenciaLista)) {
		t.Error("el repintado no dice que la propuesta está lista: la degradación tiene que ser " +
			"declarada, no silenciosa")
	}
	// Y se pidió UNA vez: la degradación no reintenta.
	if n := veces(api, puertaSugerir); n != 1 {
		t.Errorf("el generador recibió %d llamadas, want 1", n)
	}
}

// TestSugerencia_LaQueCabeVaPorLaCookieYNoSePasaDelTope es el gemelo del de arriba: sin él, un tope
// puesto a 0 «pasaría» el test anterior degradando siempre, y el PRG habría dejado de existir sin que
// nada fallara.
func TestSugerencia_LaQueCabeVaPorLaCookieYNoSePasaDelTope(t *testing.T) {
	t.Parallel()
	router, _ := sugerenciaRouter(t, nil)

	rec := pedirSugerencia(t, router, rutaSugerir)
	redirectTarget(t, rec)

	ck := cookieDeSugerencia(rec)
	if ck == nil {
		t.Fatal("una cotización corta tiene que caber en la cookie: si no, el PRG no existe")
	}
	if len(ck.Value) > maxCookieSugerencia {
		t.Errorf("el valor puesto mide %d bytes y el tope es %d", len(ck.Value), maxCookieSugerencia)
	}
}

// --- Criterio 4: las DOS cerraduras ---

// TestSugerencia_ElPathDeLaCookieLlevaElIdentificador es la PRIMERA cerradura, la que pone el
// navegador: con la cookie acotada a `/solicitudes/{id}`, no se manda a la solicitud de al lado.
//
// El aserto mira el Path REAL de la cabecera Set-Cookie y no la constante: comprobar
// `ck.Path == solicitudURL(id)` pasaría igual con las dos cambiadas a la vez, que es justo la
// regresión que importa —el Path y el destino del 303 tienen que coincidir EXACTAMENTE—.
func TestSugerencia_ElPathDeLaCookieLlevaElIdentificador(t *testing.T) {
	t.Parallel()
	router, _ := sugerenciaRouter(t, nil)

	rec := pedirSugerencia(t, router, rutaSugerir)
	ck := cookieDeSugerencia(rec)
	if ck == nil {
		t.Fatal("el 303 no puso la cookie efímera")
	}
	if ck.Path != "/solicitudes/"+testIntakeID {
		t.Errorf("el Path de la cookie es %q: sin el identificador dentro, el navegador la mandaría a "+
			"la pantalla de cualquier otra solicitud", ck.Path)
	}
	// Y el destino del 303 es esa MISMA ruta. Si no lo fuera, el navegador no mandaría la cookie al
	// GET que la tiene que consumir y el texto se perdería sin que nada fallara.
	if destino := redirectTarget(t, rec); !strings.HasPrefix(destino, ck.Path) {
		t.Errorf("el 303 va a %q y la cookie está acotada a %q: el navegador no la mandaría",
			destino, ck.Path)
	}
	if !ck.HttpOnly {
		t.Error("la cookie de la cotización no es HttpOnly")
	}
}

// TestSugerencia_LaDeANoSePintaEnB es la SEGUNDA cerradura, la que comprueba el servidor.
//
// 🔑 ES EL TEST DE LA MUTACIÓN M2 (quitar el identificador del sobre), y hace falta APARTE del Path
// porque las dos cerraduras protegen de cosas distintas: el Path se cae con que alguien reescriba la
// ruta —o con un navegador que no lo respete—, y entonces lo único que queda entre la cotización de A
// y la pantalla de B es esta comparación. Pintar los precios de A delante de quien está a punto de
// responderle a B es texto que se le manda a un cliente.
//
// La cookie se entrega A MANO a la pantalla de B, que es exactamente lo que haría un navegador con el
// Path roto: httptest no filtra por Path, así que este test mide la cerradura del SERVIDOR y solo ésa.
func TestSugerencia_LaDeANoSePintaEnB(t *testing.T) {
	t.Parallel()

	otraSolicitud := strings.ReplaceAll(solicitudDeCampo(), testIntakeID, testOtroIntake)
	router, _ := sugerenciaRouter(t, map[string]stubResponse{
		// Más específica que el patrón `{id}`, así que gana: la pantalla de B tiene que decir que es B.
		"GET /api/v1/intakes/" + testOtroIntake: {http.StatusOK, otraSolicitud},
	})

	ck := cookieDeSugerencia(pedirSugerencia(t, router, rutaSugerir))
	if ck == nil {
		t.Fatal("el 303 no puso la cookie efímera")
	}

	enB := getConCookies(router, rutaSolicitudes+"/"+testOtroIntake, clientSessionCookie(t), ck)
	if enB.Code != http.StatusOK {
		t.Fatalf("la pantalla de la otra solicitud respondió %d. Body: %s", enB.Code, enB.Body.String())
	}
	out := enB.Body.String()
	if strings.Contains(out, laMarcaDelModelo) {
		t.Errorf("la cotización de %s se pintó en la pantalla de %s: son los precios de otro pedido "+
			"delante de quien va a responder éste. Campo: %s",
			testIntakeID, testOtroIntake, bloque(t, out, `id="rendered_text"`, "</textarea>"))
	}
	if strings.Contains(out, `id="solicitud-sugerencia-origen"`) {
		t.Error("la pantalla de la otra solicitud dice que su propuesta la acaba de redactar el modelo")
	}
}

// --- Criterio 6: el origen, y los trece motivos ---

// TestSugerencia_ElOrigenSePintaYDiceQuienRedacto recorre los dos orígenes que el cloud publica.
//
// 🔴 ES LA ÚNICA SEÑAL QUE EXISTE, y por eso no basta con que el texto llegue al campo: esta puerta
// NUNCA da 502 —con el modelo caído la plataforma contesta 200 con el respaldo sobrio—, así que sin
// este párrafo «la voz de la dueña funciona» y «lleva semanas apagada» se ven exactamente igual.
func TestSugerencia_ElOrigenSePintaYDiceQuienRedacto(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre   string
		origen   string
		respaldo string
		dice     []string
		noDice   string
	}{
		{
			nombre: "lo redactó el modelo",
			origen: apiclient.QuoteSourceLLM,
			dice:   []string{"Origen: LLM", "quien responde sigues siendo tú"},
			noDice: "determinista",
		},
		{
			nombre:   "lo compuso el respaldo sobrio, y por qué",
			origen:   apiclient.QuoteSourceDeterministic,
			respaldo: apiclient.QuoteFallbackNoExamples,
			dice: []string{"NO lo redactó el modelo",
				"todavía no hay cotizaciones aprobadas de las que aprender tu estilo"},
			noDice: "Origen: LLM",
		},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			router, _ := sugerenciaRouter(t, map[string]stubResponse{
				"POST /api/v1/intakes/{id}/quote-suggestion": {http.StatusOK,
					sugerenciaBody(laCotizacionDelModelo, caso.origen, caso.respaldo)},
			})

			ck := cookieDeSugerencia(pedirSugerencia(t, router, rutaSugerir))
			if ck == nil {
				t.Fatal("el 303 no puso la cookie efímera")
			}
			out := getConCookies(router, rutaDetalle, clientSessionCookie(t), ck).Body.String()
			parrafo := bloque(t, out, `id="solicitud-sugerencia-origen"`, "</p>")
			for _, quiere := range caso.dice {
				if !strings.Contains(parrafo, quiere) {
					t.Errorf("el párrafo del origen no dice %q: %s", quiere, parrafo)
				}
			}
			if strings.Contains(parrafo, caso.noDice) {
				t.Errorf("el párrafo del origen dice %q, que es del otro caso: %s", caso.noDice, parrafo)
			}
		})
	}
}

// TestSugerencia_ElOrigenNoSePintaSiNoSeAcabaDePedir.
//
// Fuera del PRG el campo trae la propuesta que arma ESTA CONSOLA con las líneas, y decir «lo redactó
// el modelo» sobre ella sería mentir sobre un texto que el modelo no ha visto. Es el gemelo negativo
// del test de arriba: sin él, pintar el párrafo SIEMPRE pasaría los dos.
func TestSugerencia_ElOrigenNoSePintaSiNoSeAcabaDePedir(t *testing.T) {
	t.Parallel()
	router, _ := sugerenciaRouter(t, nil)

	out := getWithSession(t, router, rutaDetalle).Body.String()
	if strings.Contains(out, `id="solicitud-sugerencia-origen"`) {
		t.Error("el detalle pinta la línea de origen sin que se haya pedido ninguna sugerencia: " +
			"estaría atribuyéndole al modelo la propuesta que esta consola arma con las líneas")
	}
}

// TestSugerencia_LosTreceMotivosDelRespaldoEstanTraducidos.
//
// 🔴 SON TRECE, NO SEIS: cuatro los emite el generador y NUEVE el verificador de precios del cloud, y
// los nueve viajan por el MISMO campo. Un motivo sin traducir no rompe nada visible —se cuela como
// clave cruda en una pantalla que lee una persona que no programa—, así que la lista va escrita a
// mano aquí y el aserto es que NINGUNO cae en el genérico.
func TestSugerencia_LosTreceMotivosDelRespaldoEstanTraducidos(t *testing.T) {
	t.Parallel()

	motivos := []string{
		// Los CUATRO del generador.
		apiclient.QuoteFallbackNoExamples,
		apiclient.QuoteFallbackProviderDown,
		apiclient.QuoteFallbackLLMFailed,
		apiclient.QuoteFallbackBadOutput,
		// Los NUEVE del verificador de precios.
		apiclient.QuoteFallbackDraftWithoutAmounts,
		apiclient.QuoteFallbackUnreadableText,
		apiclient.QuoteFallbackUnreadableNumber,
		apiclient.QuoteFallbackTextWithoutAmounts,
		apiclient.QuoteFallbackMissingUnitPrice,
		apiclient.QuoteFallbackMissingTotal,
		apiclient.QuoteFallbackForeignAmount,
		apiclient.QuoteFallbackForeignNumber,
		apiclient.QuoteFallbackAmountsOutOfPlace,
	}
	if len(motivos) != 13 {
		t.Fatalf("la lista tiene %d motivos y el contrato del cloud publica 13", len(motivos))
	}

	vistos := map[string]bool{}
	for _, motivo := range motivos {
		texto := respaldoText(motivo)
		if strings.Contains(texto, "sin traducir en esta consola") {
			t.Errorf("el motivo %q cae en el genérico: se pintaría como clave cruda", motivo)
		}
		if !strings.HasPrefix(texto, "Motivo:") {
			t.Errorf("el motivo %q no se redacta como motivo: %q", motivo, texto)
		}
		vistos[motivo] = true
	}
	if len(vistos) != 13 {
		t.Errorf("la lista repite motivos: %d claves distintas de 13", len(vistos))
	}

	// Y los dos desenlaces que NO son motivos del catálogo, que tienen que seguir diciendo lo suyo.
	if texto := respaldoText(""); !strings.Contains(texto, "no dijo por qué") {
		t.Errorf("un motivo vacío no se dice como tal: %q", texto)
	}
	if texto := respaldoText("motivo_nuevo_del_cloud"); !strings.Contains(texto, "sin traducir") {
		t.Errorf("un motivo desconocido se está escondiendo tras una frase amable: %q", texto)
	}
}

// TestSugerencia_UnOrigenDesconocidoNoSeInventa: misma doctrina que el estado y la vía. Antes una
// clave cruda que una procedencia inventada sobre un texto que se le va a mandar a un cliente.
func TestSugerencia_UnOrigenDesconocidoNoSeInventa(t *testing.T) {
	t.Parallel()

	if texto := origenText(&apiclient.IntakeQuoteSuggestion{Source: "oraculo"}); !strings.Contains(texto, "oraculo") {
		t.Errorf("un origen desconocido no se nombra tal cual: %q", texto)
	}
	if texto := origenText(&apiclient.IntakeQuoteSuggestion{}); !strings.Contains(texto, "no dijo quién") {
		t.Errorf("un origen vacío no se declara: %q", texto)
	}
}

// --- El reparto de desenlaces ---

// TestSugerencia_SinLaCapacidadNoSeLlamaAlCloud es la defensa en profundidad: el botón ya sale
// deshabilitado, pero un `disabled` es del navegador y un POST a mano no lo tiene.
//
// El aserto que decide es el del RECEPTOR: «respondió 403» pasaría igual con una consola que llama al
// cloud, recibe su 403 y lo traduce — gastando el viaje que este corte existe para ahorrar.
func TestSugerencia_SinLaCapacidadNoSeLlamaAlCloud(t *testing.T) {
	t.Parallel()
	router, api := sugerenciaRouter(t, map[string]stubResponse{
		"GET /api/v1/entitlements": {http.StatusOK, entitlementsBody("basic", featureCartBasic)},
	})

	rec := pedirSugerencia(t, router, rutaSugerir)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403. Body: %s", rec.Code, rec.Body.String())
	}
	if n := veces(api, puertaSugerir); n != 0 {
		t.Errorf("se llamó al generador %d veces sin la capacidad: el corte local no está cortando", n)
	}
	if !strings.Contains(rec.Body.String(), flashError(flashSugerenciaSinPlan)) {
		t.Error("el repintado no dice por qué no se pidió nada")
	}
}

// TestSugerencia_LineasSinPrecioRepintaCon400YLasLineas: el desenlace MÁS probable en campo, y el
// único de la API de esta puerta que no sale por el PRG.
//
// El cloud lo decide ANTES de llamar al modelo, así que no mutó nada, y trae la lista con la que se
// corrige — que es justo lo que un 303 no puede llevarse. Es el mismo trato que le da la aprobación.
func TestSugerencia_LineasSinPrecioRepintaCon400YLasLineas(t *testing.T) {
	t.Parallel()
	router, _ := sugerenciaRouter(t, map[string]stubResponse{
		"POST /api/v1/intakes/{id}/quote-suggestion": {http.StatusBadRequest,
			`{"error":"lines_without_price","lines":[{"index":1,"label":"Tequeños"}]}`},
	})

	rec := pedirSugerencia(t, router, rutaSugerir)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400. Body: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	if !strings.Contains(out, flashError(flashSugerenciaSinPrecio)) {
		t.Error("el rechazo no se explica con las palabras de esta puerta")
	}
	// 🔴 Y NO con las de aprobar: aquí no se iba a enviar nada, así que «NO se le envió nada al
	// cliente» hablaría de un envío que nunca iba a ocurrir.
	if strings.Contains(out, flashError(flashSolicitudSinPrecio)) {
		t.Error("el rechazo se explica con el texto de la aprobación, que habla de un envío")
	}
	lista := bloque(t, out, `id="solicitud-aprobar-lineas-sin-precio"`, "</ul>")
	if !strings.Contains(lista, "Tequeños") {
		t.Errorf("el rechazo no dice qué línea falta. Lista: %s", lista)
	}
}

// TestSugerencia_LosDemasRechazosSalenPorEl303 fija la diferencia declarada con el origen: en el BFF
// TODOS los desenlaces malos repintaban —aquella casa repintaba entera—, y aquí la regla es la de
// D-047.16: se repinta cuando hay algo que devolver que un 303 no puede llevarse.
func TestSugerencia_LosDemasRechazosSalenPorEl303(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre string
		fallo  stubResponse
		code   string
	}{
		{
			nombre: "400 sin clave: no hay líneas que cotizar",
			fallo:  stubResponse{http.StatusBadRequest, `{"error":"no_lines_to_quote"}`},
			code:   flashSugerenciaSinLineas,
		},
		{
			// El 403 de la plataforma por la capacidad de la IA. Llega aquí solo si el plan cambió
			// entre el gate del grupo y esta llamada, o si la plataforma es más estricta.
			nombre: "403 del plan por `llm_intake`",
			fallo:  stubResponse{http.StatusForbidden, `{"error":"feature_not_enabled","feature":"llm_intake"}`},
			code:   flashSugerenciaSinPlan,
		},
		{
			// 🔑 Y el 403 por la capacidad de la BANDEJA se dice DISTINTO: lo que falta entonces no es
			// la redacción automática sino la pantalla entera, y mandar a contratar la IA sería mandar
			// a comprar lo que no falta.
			nombre: "403 del plan por `cart_basic`",
			fallo:  stubResponse{http.StatusForbidden, `{"error":"feature_not_enabled","feature":"cart_basic"}`},
			code:   flashSolicitudesSinPlan,
		},
		{
			nombre: "404: no es de esta empresa",
			fallo:  stubResponse{http.StatusNotFound, `{"error":"not_found"}`},
			code:   flashNotInYourTenant,
		},
		{
			nombre: "502 del upstream",
			fallo:  stubResponse{http.StatusBadGateway, `{"error":"upstream"}`},
			code:   flashUpstreamUnavailable,
		},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			router, _ := sugerenciaRouter(t, map[string]stubResponse{
				"POST /api/v1/intakes/{id}/quote-suggestion": caso.fallo,
			})

			rec := pedirSugerencia(t, router, rutaSugerir)
			destino := redirectTarget(t, rec)
			if destino != rutaDetalle+"?error="+caso.code {
				t.Errorf("el 303 fue a %q, want %q", destino, rutaDetalle+"?error="+caso.code)
			}
			if ck := cookieDeSugerencia(rec); ck != nil && ck.MaxAge >= 0 {
				t.Error("un rechazo puso la cookie de la cotización")
			}
		})
	}
}

// --- Criterio 5: los plazos ---

// TestPlazos_LosTresVanEnOrdenYElQueCortaEsElClienteHTTP.
//
// 🔑 EL ORDEN ES EL DISEÑO, no una comprobación de aritmética: cliente < deadline de petición < write
// deadline es lo único que hace que, cuando la espera se pase de larga, corte el CLIENTE —que
// devuelve un error que el handler traduce a un aviso en pantalla— y no el servidor, que cierra la
// conexión y deja al navegador sin nada que pintar.
//
// El primer aserto es el que no puede faltar: el plazo del cliente HTTP vive en `apiclient` y el de
// la config vive en `config`, que no se pueden importar entre sí sin invertir la dependencia. Si
// alguien mueve uno de los dos, esto es lo único que lo dice.
func TestPlazos_LosTresVanEnOrdenYElQueCortaEsElClienteHTTP(t *testing.T) {
	t.Parallel()

	if config.DefaultQuoteSuggestionTimeout != apiclient.DefaultInferenceTimeout {
		t.Fatalf("el plazo por defecto de la ruta (%s) y el del cliente HTTP que hace la llamada (%s) "+
			"dejaron de coincidir: el cliente tiene que ser el eslabón más corto",
			config.DefaultQuoteSuggestionTimeout, apiclient.DefaultInferenceTimeout)
	}

	cfg := &config.Config{
		UpstreamTimeout:        20 * time.Second,
		QuoteSuggestionTimeout: config.DefaultQuoteSuggestionTimeout,
	}
	cliente := apiclient.DefaultInferenceTimeout
	peticion := cfg.QuoteSuggestionRequestDeadline()
	escritura := cfg.QuoteSuggestionWriteDeadline()

	if cliente >= peticion || peticion >= escritura {
		t.Errorf("los tres plazos no van en orden: cliente %s, petición %s, escritura %s",
			cliente, peticion, escritura)
	}
	// Los números medidos contra UAT, escritos para que un cambio de márgenes se vea aquí.
	if cliente != 55*time.Second || peticion != 58*time.Second || escritura != 60*time.Second {
		t.Errorf("los plazos son %s / %s / %s y la tabla dice 55 s / 58 s / 60 s",
			cliente, peticion, escritura)
	}
	// Y los tres por encima de los generales, que es de donde vienen: si no, esta ruta no habría
	// necesitado ninguno.
	if peticion <= cfg.UpstreamTimeout {
		t.Errorf("el deadline de la sugerencia (%s) no supera al general (%s)", peticion, cfg.UpstreamTimeout)
	}
	// La espera que anuncia la pantalla es la del cliente, que es la que se cumple de verdad.
	if cfg.QuoteSuggestionEffectiveWait() != cliente {
		t.Errorf("la pantalla anunciaría %s y la ruta espera %s", cfg.QuoteSuggestionEffectiveWait(), cliente)
	}
}

// TestPlazos_ConElPlazoApagadoLaRutaCaeAlCortoYNoASinPlazo.
//
// 🔴 Un cero significa «sin plazo PROPIO», nunca «sin plazo». Es la diferencia entre una ruta
// configurada a la baja y una puerta por la que se cuelga la consola entera, y no se ve en ningún
// sitio salvo aquí: los dos casos compilan igual.
func TestPlazos_ConElPlazoApagadoLaRutaCaeAlCortoYNoASinPlazo(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{UpstreamTimeout: 20 * time.Second, QuoteSuggestionTimeout: 0}
	if d := cfg.QuoteSuggestionRequestDeadline(); d != 0 {
		t.Errorf("con el plazo apagado el deadline propio es %s, want 0 (== usa el del grupo)", d)
	}
	if d := cfg.QuoteSuggestionWriteDeadline(); d != 0 {
		t.Errorf("con el plazo apagado el write deadline es %s, want 0 (== manda el del servidor)", d)
	}
	if w := cfg.QuoteSuggestionEffectiveWait(); w != cfg.UpstreamTimeout {
		t.Errorf("la pantalla anunciaría %s y la ruta espera el plazo del grupo, %s", w, cfg.UpstreamTimeout)
	}
}

// TestPlazos_ElDeadlineLargoEsSOLODeLaRutaDeLaSugerencia.
//
// Es el criterio 5 por el lado del deadline de petición, y va con su CONTROL: sin él, un despachador
// que le diera el plazo largo a TODAS las rutas pasaría la mitad buena del test. Lo que se mide es el
// desenlace observable —la sugerencia contesta, la otra acción muere— con un upstream que tarda más
// que el plazo general y menos que el propio.
func TestPlazos_ElDeadlineLargoEsSOLODeLaRutaDeLaSugerencia(t *testing.T) {
	t.Parallel()

	api := apiQueTarda(t, 250*time.Millisecond)
	cfg := testConfig(api.URL, "http://127.0.0.1:8200")
	// El general MUERE antes que el upstream; el de la sugerencia le sobra.
	cfg.UpstreamTimeout = 60 * time.Millisecond
	cfg.QuoteSuggestionTimeout = 5 * time.Second
	router := NewRouter(cfg)

	sugerida := pedirSugerencia(t, router, rutaSugerir)
	if destino := redirectTarget(t, sugerida); !strings.Contains(destino, "?success="+flashSugerenciaLista) {
		t.Errorf("la sugerencia murió en el plazo del grupo: fue a %q. Con 60 ms de deadline general "+
			"y un upstream de 250 ms, el plazo por ruta es lo único que la deja llegar", destino)
	}

	// EL CONTROL: la misma consola, el mismo upstream lento, otra ruta. Tiene que morir.
	aprobada := postFormWithCSRF(router, rutaAprobar,
		url.Values{campoRespuesta: {"Te confirmo el pedido."}}, clientSessionCookie(t))
	destino := redirectTarget(t, aprobada)
	if !strings.Contains(destino, "?error=") {
		t.Errorf("una ruta que NO es la sugerencia sobrevivió a un upstream de 250 ms con un deadline "+
			"general de 60 ms: el plazo largo se está aplicando de más. Fue a %q", destino)
	}
}

// TestPlazos_ElWriteDeadlineDejaSALIRLaRespuestaPorLaConexion.
//
// 🔴 ES EL PLAZO QUE FALLA SIN DEJAR RASTRO, y el único que NO se puede comprobar con un
// httptest.ResponseRecorder: ahí no hay conexión que cortar, así que el middleware ni siquiera puede
// instalar nada. Por eso este test levanta un http.Server DE VERDAD con un WriteTimeout corto —lo que
// hace el `bootstrap` en producción— y mide lo que ve el CLIENTE.
//
// Con el WriteTimeout del servidor por debajo de lo que tarda el upstream, la conexión se cierra a
// mitad y el navegador no recibe ni la página degradada: sin aviso y sin explicación. El control es
// otra ruta de la misma consola, que es justo lo que le pasa.
func TestPlazos_ElWriteDeadlineDejaSALIRLaRespuestaPorLaConexion(t *testing.T) {
	t.Parallel()

	api := apiQueTarda(t, 400*time.Millisecond)
	cfg := testConfig(api.URL, "http://127.0.0.1:8200")
	// Los deadlines de petición NO son el cuello aquí: lo que corta es el WriteTimeout del servidor.
	cfg.UpstreamTimeout = 5 * time.Second
	cfg.QuoteSuggestionTimeout = config.DefaultQuoteSuggestionTimeout
	router := NewRouter(cfg)
	base := servidorConWriteTimeout(t, router, 150*time.Millisecond)

	resp, err := postReal(t, router, base+rutaSugerir)
	if err != nil {
		t.Fatalf("la respuesta de la sugerencia NO salió por la conexión: %v. El WriteTimeout del "+
			"servidor la cortó a mitad, que es el fallo que no deja rastro en pantalla", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", resp.StatusCode)
	}

	// EL CONTROL: sin el middleware, el mismo servidor corta. Si esto NO fallara, el test de arriba
	// estaría pasando por el WriteTimeout y no por el middleware.
	control, err := postReal(t, router, base+rutaAprobar, campoRespuesta, "Te confirmo el pedido.")
	if err == nil {
		_ = control.Body.Close()
		t.Errorf("una ruta SIN write deadline propio sobrevivió a un WriteTimeout de 150 ms con un "+
			"upstream de 400 ms (status %d): entonces el test de arriba no demuestra nada", control.StatusCode)
	}
}

// --- Arnés de los plazos ---

// apiQueTarda levanta un doble de la API pública que DUERME antes de contestar las dos puertas que
// escriben, y contesta al instante las de lectura (el plan y la ficha), que no son lo que se mide.
func apiQueTarda(t *testing.T, retraso time.Duration) *httptest.Server {
	t.Helper()

	var llamadas atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/quote-suggestion"):
			llamadas.Add(1)
			time.Sleep(retraso)
			_, _ = io.WriteString(w, sugerenciaBody(laCotizacionDelModelo, apiclient.QuoteSourceLLM, ""))
		case strings.HasSuffix(r.URL.Path, "/approve"):
			time.Sleep(retraso)
			_, _ = io.WriteString(w, solicitudDeCampo())
		case r.URL.Path == "/api/v1/entitlements":
			_, _ = io.WriteString(w, entitlementsBody("pro", featureCartBasic, featureLLMIntake))
		case r.URL.Path == "/api/v1/auth/tenants":
			_, _ = io.WriteString(w, unaEmpresa())
		default:
			_, _ = io.WriteString(w, solicitudDeCampo())
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// servidorConWriteTimeout sirve el router desde un http.Server DE VERDAD, con el WriteTimeout que se
// le diga. Es el único montaje donde el write deadline de la ruta significa algo: sobre un
// ResponseRecorder no hay conexión sobre la que ponerlo.
func servidorConWriteTimeout(t *testing.T, router http.Handler, writeTimeout time.Duration) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("no se pudo abrir el listener: %v", err)
	}
	srv := &http.Server{
		Handler:           router,
		WriteTimeout:      writeTimeout,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return "http://" + ln.Addr().String()
}

// postReal manda el POST por la RED, con el double-submit CSRF y la cookie de sesión, y sin seguir el
// redirect: lo que se quiere ver es la respuesta que salió por esa conexión.
func postReal(t *testing.T, router http.Handler, destino string, campos ...string) (*http.Response, error) {
	t.Helper()

	csrf := mintCSRF(router)
	form := url.Values{sharedweb.CSRFFieldName: {csrf.Value}}
	for i := 0; i+1 < len(campos); i += 2 {
		form.Set(campos[i], campos[i+1])
	}
	req, err := http.NewRequest(http.MethodPost, destino, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("armar la petición: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(csrf)
	req.AddCookie(clientSessionCookie(t))

	cliente := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return cliente.Do(req)
}

// cookieDeSugerenciaRecienPedida pide una sugerencia y devuelve la cookie efímera con la que el GET
// pinta el párrafo del origen. Falla el test si el POST no la puso: sin ella, la rama de la pantalla
// que se quiere examinar no existe y quien la use estaría mirando otra cosa.
//
// Vive aquí y no en security_test.go por lo mismo que `cookieDeInvitacion` vive con las invitaciones:
// el que sabe cómo se emite esta cookie es el fichero de la casilla que la emite.
func cookieDeSugerenciaRecienPedida(t *testing.T, router http.Handler) *http.Cookie {
	t.Helper()
	rec := pedirSugerencia(t, router, rutaSolicitudes+"/"+testIntakeID+sufijoSugerir)
	if ck := cookieDeSugerencia(rec); ck != nil {
		return ck
	}
	t.Fatalf("el POST de la sugerencia no puso la cookie efímera (status %d, cookies %v)",
		rec.Code, rec.Result().Cookies())
	return nil
}
