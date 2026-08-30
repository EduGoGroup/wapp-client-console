package web

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// multipart_test.go vigila la mitad de FUERA del criterio de T8.1: «el multipart se construye en el
// Transport y NO en el handler — el handler no ve un multipart.Writer».
//
// La mitad de dentro —que el único sitio que arma sobres es transport.go— vive en
// internal/apiclient/catalogimport_test.go, porque es un aserto sobre aquel paquete.

// paqueteDelApiclient es donde SÍ tiene que estar el multipart. Se usa como control positivo: sin él,
// este test pasaría igual de verde el día que la detección de imports dejara de funcionar.
const paqueteDelApiclient = "../apiclient"

// TestWeb_ElHandlerNoVeUnMULTIPARTWriter.
//
// 🔴 Por qué es un candado estructural y no un test de conducta: la regla no la vigila ningún tipo.
// Un handler que armara su propio `multipart.Writer` compila, pasa los tests de la pantalla y sube el
// fichero igual de bien — hasta que hay una segunda pantalla que sube algo y hay dos formas distintas
// de mandar un fichero por el cable, cada una con su nombre de campo y su idea de qué hacer con el
// Content-Type. Y un test de conducta por pantalla envejece en cuanto se añade la siguiente.
//
// Se mira el AST y no el texto a propósito: un `grep` de «mime/multipart» lo dispara también un
// comentario que lo nombre —este fichero mismo, sin ir más lejos—, y un candado que salta por hablar
// de lo que vigila acaba desactivado.
func TestWeb_ElHandlerNoVeUnMULTIPARTWriter(t *testing.T) {
	t.Parallel()

	ficheros := ficherosDeProduccion(t, ".")
	// ANTI-VACUIDAD: si el listado se quedara en nada —un glob mal escrito, un `chdir` de otro test—,
	// el bucle de abajo no daría ni una vuelta y esto saldría verde sin haber mirado nada.
	if len(ficheros) < 20 {
		t.Fatalf("solo se listaron %d ficheros de producción: este test no está mirando el paquete", len(ficheros))
	}

	var culpables []string
	for _, f := range ficheros {
		if importa(t, f, "mime/multipart") {
			culpables = append(culpables, filepath.Base(f))
		}
	}
	if len(culpables) > 0 {
		t.Errorf("internal/web importa mime/multipart en %v: el sobre lo arma el Transport "+
			"(apiclient.newMultipartRequest), no la pantalla", culpables)
	}

	// CONTROL POSITIVO: la detección tiene que ser capaz de VER un import de mime/multipart donde sí
	// está. Si esto falla, o el detector está roto —y el aserto de arriba no vale nada— o el armado
	// del sobre se ha ido del Transport, que es la otra mitad del mismo criterio.
	if !importa(t, filepath.Join(paqueteDelApiclient, "transport.go"), "mime/multipart") {
		t.Error("apiclient/transport.go ya no importa mime/multipart: o el detector no ve los imports, " +
			"o el sobre multipart dejó de armarse en el Transport")
	}
}

// ficherosDeProduccion lista los .go de un directorio sin los tests: lo que se vigila es el código
// que corre, y en un test importar el paquete para LEER lo que salió es legítimo.
func ficherosDeProduccion(t *testing.T, dir string) []string {
	t.Helper()
	entradas, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("no se pudo listar %s: %v", dir, err)
	}
	var out []string
	for _, e := range entradas {
		nombre := e.Name()
		if e.IsDir() || !strings.HasSuffix(nombre, ".go") || strings.HasSuffix(nombre, "_test.go") {
			continue
		}
		out = append(out, filepath.Join(dir, nombre))
	}
	return out
}

// importa responde si un fichero Go importa un paquete. Se parsea con `parser.ImportsOnly`, que lee
// justo hasta el bloque de imports: es lo más barato que da la respuesta EXACTA, sin confundir un
// import con la misma cadena escrita en un comentario o dentro de otro literal.
func importa(t *testing.T, fichero, paquete string) bool {
	t.Helper()
	archivo, err := parser.ParseFile(token.NewFileSet(), fichero, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("no se pudo parsear %s: %v", fichero, err)
	}
	for _, imp := range archivo.Imports {
		ruta, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if ruta == paquete {
			return true
		}
	}
	return false
}
