package web

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/EduGoGroup/wapp-client-console/internal/apiclient"
)

// catalogo_handler.go sirve la CARGA MASIVA DEL CATÁLOGO (Plan 047 · T8.2 · T8.3): la pantalla desde
// la que la dueña del negocio sube su lista de productos entera —una planilla o un JSON—, ve qué
// cambiaría y, en un segundo paso explícito, lo aplica. Es la casa nueva de `catalogimport_handler.go`
// + `catalog-import.html` de wapp-guardian-bff.
//
// 🔑 LA RUTA VA EN ESPAÑOL —`/importar-catalogo`—, como decidieron las olas 6 y 7 con `/flujos`,
// `/disparadores` y `/solicitudes`. Lo que NO se traduce es lo que viaja por el cable: el modo sigue
// siendo `validate`/`apply`, la ref sigue siendo `ref`, el formato de la plantilla sigue siendo
// `format` y el campo del fichero sigue llamándose `file`, porque eso es lo que la plataforma lee. La
// traducción es de SUPERFICIE.
//
// 🔴 LOS DOS PASOS COMPARTEN RUTA, PLANTILLA Y FORMULARIO, Y SOLO LOS SEPARA UN BOTÓN. No hay una
// ruta `/aplicar`: el POST atiende los dos y lo que decide cuál es el campo `mode` que manda el botón
// pulsado. La consecuencia para quien escriba un test es la misma que ya está escrita en
// solicitudes_lineas.go para `as_correction`: comprobar «el POST contestó» pasa con los dos
// confundidos, y confundirlos escribe el catálogo de una empresa sin que nadie lo haya aprobado. Lo
// que separa a los dos es el `mode` de la petición SALIENTE, y ahí es donde hay que mirar.
//
// Diferencias declaradas contra el origen, ninguna accidental:
//
//  1. 🔒 EL GATE DE `catalog_import` PASA A SER DE RUTA, y comparte middleware con la bandeja (ver
//     solicitudes_gate.go). En el BFF cada handler repetía su `if ent.Has(...)`, tres veces, y la
//     descarga de la plantilla respondía con la pantalla pintada donde el POST respondía 403.
//  2. 🔒 EL REPARTO DE CÓDIGOS LO MANDA D-047.16 y no el desenlace del upstream. El BFF NUNCA
//     redirigía —contestaba 200/400/403/413/502 repintando siempre—, así que un F5 después de
//     aplicar reenviaba la escritura del catálogo entero. Ver el reparto en ImportarCatalogo.
//  3. los textos salen del CATÁLOGO DE FLASH y nunca del upstream. El BFF llevaba a la pantalla la
//     prosa de la plataforma («La plataforma rechazó la petición: …»).
//  4. 🆕 EL TAMAÑO DEL FICHERO SE COMPRUEBA AQUÍ (ver archivoDelFormulario). El BFF no lo miraba y
//     dejaba que contestara el 413 del cloud, que por el camino tabular llega envuelto en
//     `validation_failed` con `{"field":"archivo"}` y SIN la cifra: un rechazo por tamaño que no
//     nombra ningún tamaño.
//
// Lo que se muda TAL CUAL, porque es contrato y no descuido: que la puerta se elija por el CONTENIDO
// y no por la extensión, que el paso 2 salga por la puerta JSON con el documento normalizado, que la
// ref viaje con él, y que la plantilla la SIRVA la plataforma en vez de vivir pegada en este repo.

const (
	// rutaCatalogo es la pantalla. El grupo la registra con "" y por eso el `path` que ve el
	// middleware del límite de cuerpo es exactamente esta cadena.
	rutaCatalogo = "/importar-catalogo"
	// rutaPlantillaSufijo es el verbo estático de la descarga DENTRO del grupo, y es lo que se
	// registra (ver server.go).
	rutaPlantillaSufijo = "/plantilla"
	// rutaPlantillaCatalogo es la ruta COMPLETA, y se DERIVA del sufijo a propósito: es la que arman
	// los tres enlaces de descarga de la plantilla, y una segunda copia escrita a mano sería un
	// literal que puede dejar de coincidir con lo que el router sirve sin que nada falle.
	rutaPlantillaCatalogo = rutaCatalogo + rutaPlantillaSufijo

	plantillaCatalogo = "catalogo.html"
	tituloCatalogo    = "Catálogo"
)

// Nombres de los campos del formulario. Van como constantes por lo mismo que los de las líneas: los
// lee el handler y los escribe la plantilla, y un desajuste entre los dos no lo detecta el
// compilador. En inglés porque son el vocabulario del cable (ver la cabecera).
const (
	// campoModoCatalogo es EL BOTÓN, y por tanto lo único que separa mirar de escribir. Lleva el
	// mismo nombre y los mismos valores que el parámetro `mode` de la API a propósito: así no hay una
	// traducción intermedia donde equivocarse de lado.
	campoModoCatalogo = "mode"
	// campoRefCatalogo es a qué catálogo del tenant se importa. Viaja también en el paso 2, oculto:
	// la ref NO está dentro del documento —el documento es portátil por contrato— y sin arrastrarla
	// se aplicaría sobre una ref distinta de aquella contra la que se calculó el diff.
	campoRefCatalogo = "ref"
	// campoDocumentoCatalogo es el JSON: el que se pega en el paso 1 y el que viaja OCULTO en el
	// paso 2, ya normalizado por la plataforma.
	campoDocumentoCatalogo = "document"
	// campoArchivoCatalogo es la parte del fichero. El nombre lo fija la plataforma (`file`).
	campoArchivoCatalogo = "file"
	// queryFormatoPlantilla es el formato de la plantilla descargable.
	queryFormatoPlantilla = "format"
)

// refDeArranque es la ref que el selector ofrece cuando la empresa NO tiene todavía ninguna: hay que
// poder estrenar catálogo, y un desplegable vacío no dejaría importar nunca.
//
// Es un ESPEJO declarado del `defaultCatalogRef` de la plataforma, no una segunda fuente de verdad:
// solo se usa cuando la lista viene vacía. La alternativa —mandar la ref vacía y dejar que elija la
// plataforma— es literalmente el defecto A3 que este selector cierra.
const refDeArranque = "catalogo"

// formatoPlantillaPorDefecto es el formato que sirve la descarga cuando no se pide otro.
const formatoPlantillaPorDefecto = apiclient.CatalogTemplateJSON

// maxArchivoCatalogo es el techo del FICHERO que se sube, y es la comprobación de NEGOCIO: el número
// que la plataforma honra de verdad sobre el archivo ya extraído del multipart.
//
// 🔴 NO ES EL MISMO NÚMERO QUE maxCuerpoCatalogo NI MIDE LO MISMO, y esa es toda la razón de que sean
// dos (ver catalogo_limite.go). Éste mide el FICHERO contra el techo del cloud; aquél mide el SOBRE
// de la petición y protege el proceso. Un solo número con el valor de éste rechazaría en el paso 2
// documentos que la plataforma SÍ acepta, porque allí el JSON viaja URL-encoded y ocupa varias veces
// más.
//
// Sale de la constante del apiclient y no de un literal para que no haya dos cifras que puedan
// separarse; el texto que la nombra es flashCatalogoArchivoGrande, y hay un test que ata los dos.
const maxArchivoCatalogo = apiclient.CatalogImportMaxBytesDefault

// enlacePlantilla es UNO de los formatos que la pantalla ofrece descargar.
type enlacePlantilla struct {
	Formato  string
	Etiqueta string
	Pista    string
}

// enlacesDePlantilla son las tres descargas. Las tres las sirve el MISMO endpoint de la plataforma
// cambiando `format`, y las tres vuelven a entrar por esta pantalla: la planilla es la que se llena
// sin saber qué es un JSON.
var enlacesDePlantilla = []enlacePlantilla{
	{apiclient.CatalogTemplateJSON, "Plantilla JSON", "el formato que se pega aquí"},
	{apiclient.CatalogTemplateCSV, "Planilla CSV", "para llenar en una hoja de cálculo"},
	{apiclient.CatalogTemplateXLSX, "Planilla Excel", "para llenar en Excel"},
}

// defectoCatalogo es UN problema del documento ya redactado: dónde está, qué campo es y qué le pasa.
//
// 🔑 `Donde` ES LO QUE HACE ÚTIL LA LISTA, y no es decoración: los defectos llegan localizados de dos
// maneras distintas según la puerta por la que entró el documento —por FILA si era una planilla, por
// índices de categoría y artículo si era un JSON— y quien corrige tiene delante una hoja de cálculo o
// un editor de texto, no la estructura del contrato. Perder esa correspondencia no rompe nada: se
// pintarían los mismos defectos, con el mismo texto, colgando del sitio equivocado, y solo se
// descubre corrigiendo la fila que no era. Es el mismo fallo que `defectosRemotos` evita en las
// líneas de una solicitud, y por eso se porta la FORMA de aquello: una función que traduce, y un
// test que comprueba el PAR y no que el texto aparezca.
//
// ⚠️ `Motivo` es prosa ESCRITA POR EL CLOUD y se conserva verbatim, por lo mismo que
// `defectoLinea.Mensaje`: es lo único que dice qué le pasa a ESE artículo. La doctrina de la casa
// sigue siendo que el texto de la pantalla sale del catálogo de flash; esto es un dato de una lista,
// no el desenlace.
type defectoCatalogo struct {
	Donde  string
	Campo  string
	Motivo string
}

// refCatalogoOption es UNA opción del selector de ref del paso 1.
type refCatalogoOption struct {
	Valor string
	// Elegida marca la que viene seleccionada. Es SIEMPRE exactamente una.
	Elegida bool
	// Existente distingue una ref que YA tiene contenido —se va a reemplazar— de la que se ofrece
	// para estrenar. La pantalla lo dice con palabras porque no es lo mismo pisar que crear.
	Existente bool
}

// promptCatalogoView es el prompt-plantilla tal como lo pinta la plantilla.
//
// NO hay copia local de reserva, y es a propósito: el texto está versionado junto al contrato en la
// plataforma, y un respaldo pegado aquí se quedaría viejo sin que nadie lo notara y le dictaría al
// asistente un formato que el validador ya rechaza. Si no se puede pedir, la pantalla lo dice; el
// resto —subir, comprobar, aplicar— sigue funcionando.
type promptCatalogoView struct {
	Texto   string
	Formato string
	Version int
	Cargado bool
}

// catalogoView es lo que pinta la plantilla.
type catalogoView struct {
	// Documento es el JSON que se pegó o subió en el paso 1, y en el paso 2 es el NORMALIZADO que
	// devolvió la plataforma. Se conserva para repintarlo cuando algo se rechaza —lo tecleado no se
	// tira— y para viajar oculto en la confirmación.
	Documento string
	// Ref es la del envío que se está repintando, para que un documento rechazado vuelva con SU ref
	// todavía elegida.
	Ref string
	// Refs son las opciones del selector del paso 1. NUNCA vacía cuando el paso 1 se pinta.
	Refs []refCatalogoOption
	// Defectos son los problemas del documento. No vacío ⇒ no hay diff que enseñar.
	Defectos []defectoCatalogo
	// Resultado es el diff de la COMPROBACIÓN, y solo el de la comprobación.
	//
	// 🔒 Aquí nunca llega un resultado aplicado, y no por casualidad: aplicar sale por 303 + flash
	// (D-047.16), así que el «catálogo aplicado» lo cuenta la pantalla recién cargada y no un
	// repintado del POST. Por eso esta vista no tiene tiempo verbal doble, que era la mitad del
	// aparato de la plantilla del BFF.
	Resultado *apiclient.CatalogImportResult
	// Prompt es el texto copiable para el asistente, pedido a la plataforma.
	Prompt promptCatalogoView
	// Plantillas son los tres formatos descargables.
	Plantillas []enlacePlantilla
}

// EsperandoConfirmacion dice si hay un diff comprobado esperando el «Aplicar». Es lo que separa los
// dos pasos de la pantalla: comprobar enseña, confirmar escribe.
//
// Comprueba ADEMÁS que no esté aplicado, aunque por el reparto de D-047.16 eso no pueda pasar: si
// algún día llegara aquí un resultado ya escrito, lo que NO debe hacer esta pantalla es ofrecer el
// botón de volver a escribirlo.
func (v catalogoView) EsperandoConfirmacion() bool {
	return v.Resultado != nil && !v.Resultado.Applied
}

// SinCambios dice si aplicar el documento dejaría el catálogo tal como está.
//
// Los avisos del catálogo VIGENTE no cuentan como cambio (mismo criterio que la plataforma):
// describen lo que ya pasaba antes de este import.
func (v catalogoView) SinCambios() bool {
	if v.Resultado == nil {
		return false
	}
	d := v.Resultado.Diff
	return len(d.PriceChanges) == 0 && len(d.Added) == 0 && len(d.Removed) == 0 && len(d.ChangedDetails) == 0
}

// RefElegida es la ref que el selector debe traer marcada. Manda la del RESULTADO cuando lo hay —es
// la que la plataforma usó de verdad, no la que se pidió— y si no, la del envío.
func (v catalogoView) RefElegida() string {
	if v.Resultado != nil && v.Resultado.Ref != "" {
		return v.Resultado.Ref
	}
	return v.Ref
}

// catalogoRender son las variables con las que se pinta la pantalla. Va como struct y no como
// argumentos sueltos por lo mismo que solicitudesRender: la mitad son opcionales.
type catalogoRender struct {
	// status es el código con el que se responde. La pantalla se pinta igual: el 400 del repintado
	// es D-047.16, no una pantalla distinta.
	status int
	// code es el flash del rechazo, que se pinta arriba. Pisa al `?error=` de la URL porque en un
	// repintado no hay URL de la que venir.
	code  string
	vista catalogoView
}

// ShowCatalogo pinta el paso 1: el formulario vacío con el selector de ref, los enlaces de plantilla
// y el prompt (T8.2).
func (h *AdminHandler) ShowCatalogo(c *gin.Context) {
	h.renderCatalogo(c, catalogoRender{status: http.StatusOK})
}

// renderCatalogo pinta la pantalla de importación.
//
// DEGRADA en vez de tumbar, igual que el resto de esta casa: si el prompt o las refs no se pueden
// leer, la pantalla sigue en pie —con la ref de arranque y sin el texto para el asistente—, porque lo
// que hace falta para importar es el formulario, no la ayuda.
func (h *AdminHandler) renderCatalogo(c *gin.Context, r catalogoRender) {
	// Sin empresa no hay catálogo que importar, y la API respondería 403 —«no tienes permiso»—, que
	// es un diagnóstico falso: no le falta un permiso, le falta una empresa. Ver sinEmpresa().
	if sinEmpresa(c) {
		renderer.HTML(c, http.StatusOK, plantillaCatalogo, h.pageData(c, tituloCatalogo))
		return
	}

	vista := r.vista
	vista.Plantillas = enlacesDePlantilla
	// El prompt y el selector solo se piden cuando se va a pintar el PASO 1. Con un diff esperando
	// confirmación la ref ya está decidida y viaja en su oculto: volver a listarla sería un viaje de
	// más y, peor, una segunda oportunidad de cambiarla entre el diff y el apply.
	if !vista.EsperandoConfirmacion() {
		vista.Prompt = h.resolvePromptCatalogo(c)
		vista.Refs = h.resolveRefsCatalogo(c, vista.RefElegida())
	}

	data := h.pageData(c, tituloCatalogo)
	// 🔑 La vista del plan la SEMBRÓ el gate (solicitudes_gate.go) y aquí se REUTILIZA: sin esto se
	// pagarían DOS llamadas a /entitlements por petición, y esta consola resuelve el plan SIN CACHÉ.
	data[entitlementsDataKey] = entitlementsFromContext(c)
	data["Catalogo"] = vista
	if r.code != "" {
		data["Error"] = flashError(r.code)
	}
	renderer.HTML(c, r.status, plantillaCatalogo, data)
}

// ImportarCatalogo atiende LOS DOS PASOS con el mismo endpoint: comprobar (el que no escribe) y
// aplicar (el que reemplaza el catálogo entero de la empresa). Cuál de los dos lo dice el BOTÓN.
//
// 🔒 EL REPARTO DE DESENLACES (D-047.16, aplicada a esta pantalla):
//
//	COMPROBAR · éxito ......... 200 REPINTANDO con el diff. No mutó nada, y el paso 2 es esa misma
//	                            página con el documento ya congelado en un oculto.
//	COMPROBAR · el documento no vale (400 con la lista de defectos)
//	                    ...... 400 REPINTANDO, con lo subido y los defectos ANCLADOS A SU FILA.
//	COMPROBAR · rechazo LOCAL (sin documento, no es JSON, fichero ilegible, demasiado grande, modo
//	            desconocido) .. 400 REPINTANDO, con el formulario intacto.
//	COMPROBAR · resto de la API (403, 413, 502…)
//	                    ...... 400 REPINTANDO con el aviso.
//	APLICAR · lo que sea ...... 303 + flash.
//
// 🔑 POR QUÉ COMPROBAR NO REDIRIGE NUNCA, ni siquiera cuando el que falla es el upstream: la
// pregunta que D-047.16 manda hacerse es «¿pudo escribir algo al otro lado?», y para `mode=validate`
// la respuesta es NO por contrato de la plataforma —ese modo no llega a escribir y su respuesta trae
// `applied:false` siempre—. Repintar no crea el problema que el PRG resuelve, y sí evita tirar un
// documento de cientos de líneas que alguien acaba de pegar. Es la misma extensión que ya se aplicó
// al `invalid_items` de las líneas.
//
// 🔑 Y POR QUÉ APLICAR REDIRIGE SIEMPRE, éxito o fracaso: ahí sí se pudo escribir. Un 502 al aplicar
// no dice si el catálogo quedó reemplazado, y un repintado invita a un F5 que lo reenviaría. Se paga
// el precio conocido: del rechazo de un apply se pierde la lista de defectos, y quien la necesite la
// recupera volviendo a comprobar el mismo documento —que es lo que el aviso le dice que haga—.
func (h *AdminHandler) ImportarCatalogo(c *gin.Context) {
	// Sin empresa no hay nada que importar y el repintado releería una pantalla que tampoco existe.
	if sinEmpresa(c) {
		c.Redirect(http.StatusSeeOther, rutaCatalogo)
		return
	}

	envio, rechazo := envioDelFormulario(c)
	if rechazo != "" {
		h.repintaCatalogo(c, envio, rechazo, nil)
		return
	}

	var resultado *apiclient.CatalogImportResult
	var importErr error
	code := flashCodeForCatalogo(h.auth.withAuthRetry(c, func(accessToken string) error {
		var err error
		// 🔑 AQUÍ SE DECIDE POR QUÉ PUERTA SALE, y NO es lo mismo que decidir si escribe: el modo va
		// en `envio.Modo` y viaja igual por las dos. La planilla entra por `/tabular` porque es lo
		// único que sabe traducirla; el JSON —pegado, subido, o el normalizado del paso 2— entra por
		// la puerta de siempre.
		if envio.esPlanilla() {
			resultado, err = h.api.Catalog.ImportCatalogTabular(c.Request.Context(), accessToken,
				envio.Archivo, envio.Modo, envio.Ref)
		} else {
			resultado, err = h.api.Catalog.ImportCatalog(c.Request.Context(), accessToken,
				[]byte(envio.Documento), envio.Modo, envio.Ref)
		}
		importErr = err
		return err
	}), envio.aplica())
	if sessionIsDead(importErr) {
		h.auth.expireSession(c)
		return
	}
	if code != "" {
		slog.Warn("no se pudo importar el catálogo",
			"codigo", code, "modo", string(envio.Modo), "planilla", envio.esPlanilla(), "error", importErr)
	}

	// APLICAR: 303 SIEMPRE. Pudo escribir el catálogo entero de la empresa, así que la única
	// respuesta que no invita a reenviarlo con un F5 es la redirección.
	if envio.aplica() {
		redirectWith(c, rutaCatalogo, code, flashCatalogoAplicado)
		return
	}

	// COMPROBAR: no se redirige nunca.
	if invalido, ok := apiclient.CatalogImportInvalidOf(importErr); ok {
		h.repintaCatalogo(c, envio, code, defectosDelCatalogo(invalido.Errors))
		return
	}
	if code != "" {
		h.repintaCatalogo(c, envio, code, nil)
		return
	}
	h.renderCatalogo(c, catalogoRender{status: http.StatusOK, vista: catalogoView{
		Documento: documentoParaConfirmar(envio, resultado),
		Ref:       envio.Ref,
		Resultado: resultado,
	}})
}

// repintaCatalogo devuelve el paso 1 con lo que se mandó y el motivo del rechazo. Es el 400 de
// D-047.16 en un solo sitio, para que los cuatro caminos que lo usan no puedan responder códigos
// distintos ante rechazos que son el mismo.
//
// 🔴 DE UNA PLANILLA NO VUELVE DOCUMENTO, y no es un olvido: un `<input type=file>` no se puede
// rellenar desde el servidor —el navegador no lo permite, y con razón—, así que lo único que se
// conserva de ese envío es la ref elegida. El fichero se corrige en la hoja de cálculo, que es donde
// están las filas que los defectos señalan.
func (h *AdminHandler) repintaCatalogo(c *gin.Context, envio envioCatalogo, code string, defectos []defectoCatalogo) {
	h.renderCatalogo(c, catalogoRender{
		status: http.StatusBadRequest,
		code:   code,
		vista: catalogoView{
			Documento: envio.Documento,
			Ref:       envio.Ref,
			Defectos:  defectos,
		},
	})
}

// DescargarPlantillaCatalogo sirve la plantilla de ejemplo al navegador (T8.3).
//
// 🔴 ES LA ÚNICA RESPUESTA DE ESTA CONSOLA QUE NO PASA POR EL RENDERIZADOR, y por tanto la única sin
// layout, sin nonce y sin el par de avisos: son BYTES para guardar en disco, no una página. Envolver
// un XLSX en el marco de la consola no lo haría más seguro, lo haría inservible.
//
// Va por aquí y no con un enlace directo a la plataforma porque el token vive server-side en una
// cookie HttpOnly: el navegador no lo tiene y un enlace a :8103 se llevaría un 401.
//
// 🔑 EL `Content-Type` Y EL NOMBRE DEL FICHERO SALEN DE LA LISTA BLANCA DEL CLIENTE, nunca de una
// cabecera ajena, y el nombre además pasa por una comprobación (ver `catalogAttachmentName` y
// `nombreDeDescargaSeguro` en apiclient): un nombre con comillas o con un salto de línea dentro
// convertiría esta cabecera en dos.
//
// El fallo NO se descarga: se vuelve a la pantalla con el aviso, que es donde se puede hacer algo.
func (h *AdminHandler) DescargarPlantillaCatalogo(c *gin.Context) {
	if sinEmpresa(c) {
		c.Redirect(http.StatusSeeOther, rutaCatalogo)
		return
	}

	formato := strings.TrimSpace(c.Query(queryFormatoPlantilla))
	if formato == "" {
		formato = formatoPlantillaPorDefecto
	}

	var plantilla *apiclient.CatalogTemplate
	var descargaErr error
	code := flashCodeForPlantillaCatalogo(h.auth.withAuthRetry(c, func(accessToken string) error {
		var err error
		plantilla, err = h.api.Catalog.GetCatalogTemplate(c.Request.Context(), accessToken, formato)
		descargaErr = err
		return err
	}))
	if sessionIsDead(descargaErr) {
		h.auth.expireSession(c)
		return
	}
	if code != "" {
		slog.Warn("no se pudo descargar la plantilla de catálogo",
			"codigo", code, "formato", formato, "error", descargaErr)
		h.renderCatalogo(c, catalogoRender{status: statusDeLaDescarga(code), code: code})
		return
	}

	c.Header("Content-Disposition", `attachment; filename="`+plantilla.Filename+`"`)
	c.Data(http.StatusOK, plantilla.ContentType, plantilla.Content)
}

// statusDeLaDescarga reparte los dos desenlaces malos de la descarga, y son dos porque significan
// cosas distintas: un formato que no existe es un rechazo de la PETICIÓN —no salió a la red— y va con
// 400, igual que cualquier otra validación local de esta consola; que la plataforma no conteste es
// una DEGRADACIÓN, y ésas se sirven con 200 y el aviso arriba, como ShowFlows y ShowSessions.
func statusDeLaDescarga(code string) int {
	if code == flashCatalogoFormatoDesconocido {
		return http.StatusBadRequest
	}
	return http.StatusOK
}

// envioCatalogo es lo que se mandó, ya resuelto: el modo, la ref y O BIEN un documento JSON —pegado,
// subido, o el que viaja oculto en la confirmación— O BIEN una planilla que hay que subir tal cual.
type envioCatalogo struct {
	Modo      apiclient.CatalogImportMode
	Ref       string
	Documento string
	Archivo   apiclient.CatalogUpload
}

// esPlanilla dice si esto va por la puerta del fichero.
func (e envioCatalogo) esPlanilla() bool { return len(e.Archivo.Content) > 0 }

// aplica dice si este envío ESCRIBE. Es una pregunta y no un campo `bool` para que el sitio de la
// llamada diga lo que hace en vez de pasar un `true` que hay que ir a buscar.
func (e envioCatalogo) aplica() bool { return e.Modo == apiclient.CatalogModeApply }

// envioDelFormulario resuelve qué se mandó y por qué puerta tiene que salir. Devuelve el CÓDIGO de
// flash del rechazo (vacío = todo bien) en vez de escribir la respuesta.
//
// LA PUERTA SE ELIGE POR EL CONTENIDO Y NO POR LA EXTENSIÓN, igual que hace la plataforma para elegir
// el parser: si lo subido empieza por «{» es el documento del contrato, y si no, es una planilla y se
// sube tal cual. Fiarse del `.csv` del nombre sería fiarse de algo que se cambia sin cambiar el
// fichero — y renombrar un XLSX a `.json` no debe romper nada.
//
// Lo ÚNICO que se comprueba del contenido es que se pueda enviar: que haya algo, que lo PEGADO sea
// JSON (una planilla no se pega, se sube) y que el fichero quepa. Qué tiene de malo el documento lo
// dice el validador de la plataforma, que acumula TODOS los defectos y los redacta para una persona;
// repetir aquí ese criterio garantizaría que los dos discrepen.
func envioDelFormulario(c *gin.Context) (envioCatalogo, string) {
	envio := envioCatalogo{Ref: formValue(c, campoRefCatalogo)}

	modo, rechazo := modoDelFormulario(c)
	if rechazo != "" {
		return envio, rechazo
	}
	envio.Modo = modo

	archivo, rechazo := archivoDelFormulario(c)
	if rechazo != "" {
		return envio, rechazo
	}
	if len(archivo.Content) > 0 {
		if pareceDocumentoJSON(archivo.Content) {
			envio.Documento = string(bytes.TrimSpace(archivo.Content))
			return envio, ""
		}
		// Los bytes van SIN TOCAR: ni se recorta el espacio de los bordes ni se quita el BOM, porque
		// un XLSX es binario y un CSV lo lee la plataforma, que ya sabe de BOM y de separadores.
		envio.Archivo = archivo
		return envio, ""
	}

	envio.Documento = formValue(c, campoDocumentoCatalogo)
	switch {
	case envio.Documento == "":
		return envio, flashCatalogoSinDocumento
	case !strings.HasPrefix(envio.Documento, "{"):
		return envio, flashCatalogoNoEsJSON
	}
	return envio, ""
}

// modoDelFormulario lee el botón que se pulsó.
//
// 🔴 EL VACÍO CAE EN COMPROBAR, Y ES LA DECISIÓN IMPORTANTE DE ESTA FUNCIÓN. Un formulario enviado
// con Enter viaja SIN el `name`/`value` del botón —eso solo lo manda el botón que se pulsa—, así que
// el modo ausente es un caso real y no un envío manipulado. Cae en el paso que no escribe, que es el
// único lado por el que se puede fallar aquí: convertir un Enter en un reemplazo del catálogo entero
// no lo arregla ningún aviso posterior.
//
// Un modo con VALOR pero desconocido sí se rechaza, y por lo contrario: eso no lo produce ningún
// botón de esta pantalla, así que degradarlo a comprobar en silencio escondería que alguien está
// mandando algo que esta consola no ofrece.
func modoDelFormulario(c *gin.Context) (apiclient.CatalogImportMode, string) {
	switch crudo := formValue(c, campoModoCatalogo); crudo {
	case "", string(apiclient.CatalogModeValidate):
		return apiclient.CatalogModeValidate, ""
	case string(apiclient.CatalogModeApply):
		return apiclient.CatalogModeApply, ""
	default:
		return "", flashCatalogoModoDesconocido
	}
}

// archivoDelFormulario lee el fichero elegido, si lo hay, y lo mide contra el techo que la plataforma
// honra de verdad.
//
// Devuelve el cero cuando no hay fichero utilizable, y eso incluye el caso que manda de verdad el
// navegador: un `<input type=file>` sin elegir nada SÍ viaja, como una parte vacía. Tratarla como
// «hay fichero» le pondría un «pega el documento» en la cara a quien lo pegó.
//
// 🔴 LA MEDIDA VA DOS VECES Y NO ES REDUNDANCIA BARATA: `fh.Size` es lo que dice la parte ya
// parseada y permite rechazar ANTES de leer un solo byte a memoria; el `LimitReader` con el +1 es lo
// que hace que un tamaño que no cuadre con lo que hay dentro no se cuele. Y el +1 es lo que permite
// NOTAR el exceso en vez de entregar justo el techo creyendo que era el fichero entero.
func archivoDelFormulario(c *gin.Context) (apiclient.CatalogUpload, string) {
	fh, err := c.FormFile(campoArchivoCatalogo)
	if err != nil || fh == nil || fh.Size == 0 {
		return apiclient.CatalogUpload{}, ""
	}
	if fh.Size > maxArchivoCatalogo {
		return apiclient.CatalogUpload{}, flashCatalogoArchivoGrande
	}

	f, err := fh.Open()
	if err != nil {
		slog.Warn("no se pudo abrir el fichero de catálogo subido", "error", err)
		return apiclient.CatalogUpload{}, flashCatalogoArchivoIlegible
	}
	defer func() { _ = f.Close() }()

	raw, err := io.ReadAll(io.LimitReader(f, maxArchivoCatalogo+1))
	if err != nil {
		slog.Warn("no se pudo leer el fichero de catálogo subido", "error", err)
		return apiclient.CatalogUpload{}, flashCatalogoArchivoIlegible
	}
	if int64(len(raw)) > maxArchivoCatalogo {
		return apiclient.CatalogUpload{}, flashCatalogoArchivoGrande
	}
	return apiclient.CatalogUpload{
		Filename: fh.Filename,
		// El tipo es lo que DECLARÓ el navegador, no lo que el fichero es, y no decide nada: la
		// plataforma reconoce CSV y XLSX por el contenido. Viaja porque es la verdad de lo que se
		// subió y porque el único sitio donde queda escrito es la parte del multipart.
		ContentType: fh.Header.Get("Content-Type"),
		Content:     raw,
	}, ""
}

// pareceDocumentoJSON decide si lo subido es el documento del contrato. Se mira el primer carácter
// con contenido, saltando el BOM que escriben algunos editores de Windows: sin eso, un JSON
// impecable guardado desde el Bloc de notas se iría por la puerta de las planillas y se rechazaría
// con un motivo que no le diría nada a nadie.
func pareceDocumentoJSON(raw []byte) bool {
	return bytes.HasPrefix(bytes.TrimSpace(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))), []byte("{"))
}

// documentoParaConfirmar decide qué texto viaja al paso 2.
//
// 🔑 DE LA PLANILLA SALE EL DOCUMENTO NORMALIZADO QUE DEVOLVIÓ LA PLATAFORMA, y ahí está el diseño
// entero del segundo paso: un `.xlsx` es binario y no cabe en un campo oculto, y volver a pedir el
// fichero dejaría subir uno DISTINTO del que se confirmó. Del JSON sale lo que ya se tenía. En los
// dos casos se reenvía tal cual llegó, sin re-serializar: esta consola no interpreta el contrato del
// catálogo, y cualquier campo nuevo se perdería en la traducción sin que nadie lo notara.
func documentoParaConfirmar(envio envioCatalogo, resultado *apiclient.CatalogImportResult) string {
	if resultado != nil && len(resultado.Document) > 0 {
		return string(resultado.Document)
	}
	return envio.Documento
}

// defectosDelCatalogo traduce los defectos que manda la plataforma a filas de la lista que se pinta.
// Solo se calcula la UBICACIÓN legible: el campo y el motivo se copian tal cual.
//
// 🔴 EL CAMPO NO SE TRADUCE, a diferencia de lo que hace `etiquetasDeCampo` con las líneas de una
// solicitud, y la razón es que aquí no hay una lista cerrada que traducir: por el camino TABULAR el
// nombre ya viene en español porque es la COLUMNA de la planilla («precio», «categoria»), y por el
// JSON es una ruta del contrato que puede llevar índices dentro (`variants[1].price`). Una tabla
// código→texto cubriría la mitad de los casos y dejaría la otra mitad sin traducir o, peor, con un
// nombre inventado que no está en el fichero que hay que corregir.
func defectosDelCatalogo(errs []apiclient.CatalogImportFieldError) []defectoCatalogo {
	out := make([]defectoCatalogo, 0, len(errs))
	for _, e := range errs {
		out = append(out, defectoCatalogo{
			Donde:  ubicacionDelDefecto(e),
			Campo:  e.Field,
			Motivo: e.Reason,
		})
	}
	return out
}

// ubicacionDelDefecto redacta DÓNDE está el problema, contando desde 1 —como cuenta una persona y
// como ya cuenta la prosa del motivo—.
//
// 🔑 LA FILA MANDA CUANDO VIENE, y es lo que hace útil el rechazo de una planilla: quien la llenó
// tiene delante una hoja de cálculo con su margen numerado, no un árbol de categorías, y ese número
// llega YA en su sistema (cabecera = 1), así que aquí no se le suma nada. Por el camino JSON no hay
// filas y mandan los índices, que sí van en base 0 y sí hay que traducir.
//
// 🔴 Y LOS DOS AUSENTES NO SIGNIFICAN LO MISMO, que es el motivo de que los índices sean punteros en
// el contrato: sin categoría el defecto es del documento entero —la cabecera, los límites—, y con
// categoría pero sin artículo es de la categoría y no de un artículo suyo. Colapsarlos mandaría a
// buscar una categoría que no existe.
func ubicacionDelDefecto(e apiclient.CatalogImportFieldError) string {
	switch {
	case e.Row > 0:
		return "Fila " + strconv.Itoa(e.Row)
	case e.CategoryIndex == nil:
		return "Todo el documento"
	case e.ItemIndex == nil:
		return "Categoría " + strconv.Itoa(*e.CategoryIndex+1)
	default:
		return "Categoría " + strconv.Itoa(*e.CategoryIndex+1) +
			" · artículo " + strconv.Itoa(*e.ItemIndex+1)
	}
}

// resolveRefsCatalogo pide las refs de contenido de la empresa y arma el selector del paso 1.
//
// Como resolvePromptCatalogo, no falla nunca hacia arriba. Lo que NO hace —y es la diferencia con el
// prompt— es degradar a «sin opciones»: el selector tiene que ofrecer siempre algo que mandar, porque
// una ref vacía es el defecto que esto viene a cerrar.
func (h *AdminHandler) resolveRefsCatalogo(c *gin.Context, elegida string) []refCatalogoOption {
	var refs []apiclient.TenantContentRef
	err := h.auth.withAuthRetry(c, func(accessToken string) error {
		var gerr error
		refs, gerr = h.api.Catalog.ListTenantContentRefs(c.Request.Context(), accessToken)
		return gerr
	})
	if err != nil {
		slog.Warn("no se pudieron listar las refs de contenido (se ofrece solo la de arranque)", "error", err)
		refs = nil
	}
	return opcionesDeRef(refs, elegida)
}

// opcionesDeRef convierte las refs de la empresa en las opciones del desplegable.
//
// Tres reglas, y las tres salen del defecto A3 del Plan 041:
//
//   - La lista NUNCA sale vacía. Sin refs —empresa nueva, o el listado no se pudo leer— se ofrece
//     refDeArranque para estrenar.
//   - Hay SIEMPRE exactamente una marcada. Si `elegida` viene con valor —el paso 2, que tiene que
//     aplicar sobre la MISMA ref contra la que se calculó el diff— manda ésa, y si no está en la
//     lista se añade igual: perder la ref elegida entre los dos pasos es justo el fallo silencioso
//     del que nace todo esto.
//   - El orden es el que devuelve la plataforma, sin reordenar por «parece un catálogo». Filtrar por
//     prefijo inventaría una convención que el contrato no tiene.
func opcionesDeRef(refs []apiclient.TenantContentRef, elegida string) []refCatalogoOption {
	opciones := make([]refCatalogoOption, 0, len(refs)+1)
	encontrada := false
	for _, r := range refs {
		if r.Ref == "" {
			continue
		}
		opciones = append(opciones, refCatalogoOption{Valor: r.Ref, Existente: true})
		if r.Ref == elegida {
			encontrada = true
		}
	}
	if elegida != "" && !encontrada {
		opciones = append(opciones, refCatalogoOption{Valor: elegida})
	}
	if len(opciones) == 0 {
		opciones = append(opciones, refCatalogoOption{Valor: refDeArranque})
	}

	// La marca va en una SEGUNDA pasada para que haya exactamente una: con `elegida` vacía manda la
	// primera, que es el caso del paso 1 recién abierto.
	quiere := elegida
	if quiere == "" {
		quiere = opciones[0].Valor
	}
	for i := range opciones {
		opciones[i].Elegida = opciones[i].Valor == quiere
	}
	return opciones
}

// resolvePromptCatalogo pide el prompt-plantilla y devuelve SIEMPRE una vista usable: el fallo se
// traduce en la vista cero, nunca en un error que tumbe la página. Es información de AYUDA, no la
// operación: el import tiene que poder hacerse aunque el texto para el asistente no cargue (mismo
// criterio que resolveEntitlements con las features).
func (h *AdminHandler) resolvePromptCatalogo(c *gin.Context) promptCatalogoView {
	var prompt *apiclient.CatalogPrompt
	err := h.auth.withAuthRetry(c, func(accessToken string) error {
		var gerr error
		prompt, gerr = h.api.Catalog.GetCatalogPrompt(c.Request.Context(), accessToken)
		return gerr
	})
	if err != nil || prompt == nil {
		slog.Warn("no se pudo leer el prompt del import (la pantalla sigue sirviendo)", "error", err)
		return promptCatalogoView{}
	}
	return promptCatalogoView{
		Texto:   prompt.Prompt,
		Formato: prompt.Format,
		Version: prompt.Version,
		Cargado: true,
	}
}
