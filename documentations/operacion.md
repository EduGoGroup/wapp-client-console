# Operación de `wapp-client-console`

Cómo se arranca, se prueba, se publica y se depura. Todo verificado el **2026-08-30**.

---

## 1 · Requisitos

| Herramienta | Versión | Dónde está fijada |
|---|---|---|
| Go | **1.26.5** | `go.mod:3` y `Makefile` (`GO_VERSION := 1.26.5`) |
| golangci-lint | **v2.12.2** | `Makefile:9` (`LINT_VERSION`) |
| Docker | solo para `make ci-docker` | — |

**Nada más.** No hace falta Postgres, ni Redis, ni red, ni contenedor para correr los tests: esta
pieza **no tiene base de datos** y sus upstreams se levantan con `httptest` (`newFakeIdentity` /
`newFakePlatform`, `internal/web/server_test.go`). Es la diferencia más grande con el resto del
ecosistema, donde el gate de integración pide contenedor.

---

## 2 · Arranque en local

```sh
make run     # = GOWORK=off go run ./cmd/client-console/main.go
curl -s http://127.0.0.1:8107/healthz
# {"status":"healthy","time":"..."}
```

Con los defaults (`WAPP_CONSOLE_ENV=local`) la consola escucha en `127.0.0.1:8107`, apunta a
`http://127.0.0.1:8103` (API pública) y a `http://127.0.0.1:8200` (identity), con `Secure=false` y
HSTS apagado.

### 🔴 Trampa: `.env` **no se lee**

`config.Load()` usa `sharedconfig.New(WithEnvPrefix("WAPP_"))`
(`internal/config/config.go:90`), y ese lector **solo consulta `os.LookupEnv`**. No hay `godotenv`
en el repo (`grep -ri dotenv go.mod go.sum Makefile` → vacío) y `make run` es `go run` a secas.

**Copiar `.env.example` a `.env` no configura nada.** Si necesitas valores distintos, expórtalos:

```sh
export WAPP_CONSOLE_ENV=local
export WAPP_PUBLIC_API_BASE=http://127.0.0.1:8103
export WAPP_IDENTITY_URL=http://127.0.0.1:8200
make run
# o, para una sesión suelta:
set -a; . ./.env; set +a; make run
```

⚠️ El `README.md` de la raíz del repo dice `cp .env.example .env && make run`. Es incorrecto; en UAT
funciona porque quien lee el `.env` es **`systemd`** (`EnvironmentFile=`), no el proceso.

### Qué hace falta arriba para ver algo

La consola **no tiene datos propios**. Sin la API pública (`:8103`) arriba y sin identity, `/healthz`
responde 200 igualmente (no comprueba upstreams) pero `/login` no puede autenticar. Los modos
degradados están diseñados: sin plan legible el gate **cierra**, sin listado de empresas no se pinta
selector, sin sesiones se pinta la pantalla con su aviso.

---

## 3 · Cómo se prueba

### 3.1 Los targets reales del `Makefile`

| Target | Qué corre | Qué valida |
|---|---|---|
| `make fmt-check` | `gofmt -l .` | que no quede ni un fichero sin formatear (falla si la lista no está vacía) |
| `make vet` | `GOWORK=off go vet ./...` | los análisis estáticos del toolchain, **incluidos los tests** |
| `make lint` | `golangci-lint run --timeout=5m` | `errcheck` (con `check-type-assertions: true`), `govet`, `ineffassign`, `staticcheck`, `unused`, `errorlint`, `errname`, `nilerr`, más `gofmt`/`goimports` (`.golangci.yml`) |
| `make test` | `GOWORK=off go test -race ./...` | los 354 `Test*` con detector de carreras |
| `make build` | `GOWORK=off go build ./...` | que compila **el código de producción** |
| `make ci-local` | fmt + vet + lint + test + build | **el gate real antes de publicar** |
| `make ci-docker` | lo mismo dentro de `golang:1.26.5-bookworm` | reproduce el toolchain fijado; monta la raíz del ecosistema como `/workspace` |

**No hay `make test-integration`.** Aquí no existe.

### 3.2 🔴 En wApp **un PR no valida nada**

`.github/workflows/ci.yml` es **`on: workflow_dispatch`**: no se dispara ni con `push` ni con
`pull_request`. La validación continua vive en la máquina de desarrollo, y **el gate son
`make ci-local` y `make ci-docker`**. El único workflow que se dispara solo es
`sync-main-to-dev.yml` (`on: push: branches: [main]`), que no valida nada: realinea `dev` tras cada
publicación.

⚠️ El comentario de `.github/workflows/ci.yml:18-19` dice que las únicas dependencias de la
organización son `wapp-shared/{config,ui,web}`. **Son cinco**: `go.mod:6-10` declara además `auth` e
`iam`. No rompe nada, pero el inventario está desfasado.

### 3.3 🔴 Cómo se leen los resultados sin engañarse

1. **Lee el `rc` sin pipe.** `make ci-local | tail -30` imprime «code 0» aunque el make falle,
   porque el código de salida es el del `tail`. Hazlo así:
   ```sh
   make ci-local > /tmp/cc.log 2>&1; echo "RC = $?"
   ```
2. **Un `rc=0` cuenta igual un `--- SKIP` que un `--- PASS`.** Un test que se salta solo es un test
   que no midió nada, y en el ecosistema hay suites que se saltan enteras sin la variable de base de
   datos puesta. **Cuenta los SKIP siempre**:
   ```sh
   GOWORK=off go test -v ./... 2>&1 | grep -c -- "--- SKIP"
   GOWORK=off go test -v ./... 2>&1 | grep -c -- "--- PASS"
   ```
   Medido hoy en este repo: **549 PASS · 0 SKIP** (con caché limpia, `internal/apiclient` ~1,0 s e
   `internal/web` ~1,3 s). Que hoy sean cero no exime de contarlos mañana.
3. **Mira también el «no test files».** Tres paquetes —`cmd/client-console`, `internal/bootstrap` e
   `internal/config`— **no tienen ni un test**, y `go test ./...` los reporta en verde con
   `[no test files]`. Es otro cero silencioso.
4. **`go build ./...` no compila los tests**: sale verde con un test roto. Quien valida los tests son
   `vet` y `test`.

### 3.4 Los tests que son candados, no pruebas de conducta

Merecen conocerse porque **fallan por diseño cuando alguien cambia algo que no debía**:

- `internal/web/security_test.go` — `TestTemplates_SinJavaScript`,
  `TestTemplates_SinEstilosInline`, `TestRenderer_NoSeSirveHTMLSinPasarPorElRenderizador`,
  `TestSonda_NoRecibeCookiesNiCSRF`, `TestEstaticos_NoRecibenCookies` y
  `TestPaginas_TodasLasPantallasAutenticadasEstanCubiertas`, que compara contra las **rutas GET que
  el router registra de verdad**, no contra una lista a mano.
- `internal/web/cookies_test.go` — `TestCookieNames_SonLasDeEstaConsolaYNoLasDeLosOtrosDosPerimetros`,
  que compara **literales** contra los seis nombres ajenos.
- `internal/web/inv04_test.go` — los tres de INV-04, uno de ellos **hermano con aserto positivo**
  sobre la única excepción.
- `internal/web/barra_test.go` — tres candados de CSS sobre la barra superior.
- `internal/web/solicitudes_prg_test.go` — el reparto de desenlaces de D-047.16.

---

## 4 · Cómo se publica una versión

**Cadencia del ecosistema:** toda ola aterriza en `dev`; a `main` se pasa **al final del plan**.

1. `make ci-local` en verde (leyendo el `rc` sin pipe) y, si tocas dependencias o el toolchain,
   `make ci-docker`.
2. Merge de `dev` → `main` y `push`.
3. El workflow `sync-main-to-dev.yml` realinea `dev` solo. **No lo repitas a mano.**
4. Tag si procede: `vX.Y.Z` sobre `main`.

**Estado hoy:** `main` y `dev` están en el mismo SHA (`ac906e2`). El único tag es **`v0.1.0`**, que
apunta a `8c96797` («login, miembros, roles y sesiones»), **no al HEAD**: todo lo mudado del BFF
—editor, bandeja, catálogo— está **sin versionar**.

### Reglas de dependencias al publicar

- 🔴 **Se compila contra el tag publicado de `wapp-shared`**, nunca contra el árbol de al lado. Por
  eso todos los targets del `Makefile` llevan `GOWORK=off`: `GOWORK=off go build ./...` es lo que
  dice si el repo está de verdad en verde.
- 🔴 **Prohibido `replace`** a un repo de wApp: la ruta base del checkout lleva un espacio y falla
  con *«malformed module path»*.
- El módulo está dado de alta en el `go.work` del ecosistema, que vive en **otro repo**: un `grep`
  desde aquí no lo encuentra. Sin esa línea, `go build` con el workspace activo muere con
  *«directory prefix . does not contain modules listed in go.work»*. **El alta no sustituye al
  release.**

---

## 5 · Cómo corre en UAT (verificado el 2026-08-30)

| Dato | Valor |
|---|---|
| Unidad | `wapp-client-console.service` (`/etc/systemd/system/`), `active/running`, `NRestarts=0` |
| Binario | `/usr/local/bin/wapp-client-console` |
| Checkout | `/root/source/wApp/cloud/wapp-client-console`, rama `main`, limpio (⚠️ **bajo `cloud/`**, aunque la pieza sea de `guardian/`) |
| Escucha | `127.0.0.1:8107` (loopback, correcto) |
| `EnvironmentFile` | el `.env` del checkout, `-rw-------`, **18 variables** |
| Log | `client-console.log`, **formato JSON**, sin rotación |
| Memoria | ~7 MB |

**Cómo saber qué commit corre de verdad:** ningún binario responde a `-version`. La vía fiable es el
buildinfo empotrado, y **hay que preguntar por el proceso vivo, no por el fichero instalado**
(instalar y reiniciar son dos pasos):

```sh
go version -m /proc/$(systemctl show -p MainPID --value wapp-client-console)/exe
# busca vcs.revision, vcs.time y vcs.modified
```

Hoy: `vcs.revision = ac906e2c2cdf511d0754f20b632da908aad946ec`, `vcs.modified=false`, y el md5 del
proceso coincide con el del fichero en disco.

---

## 6 · Cómo se depura

### 6.1 Lo primero

```sh
curl -s http://127.0.0.1:8107/healthz          # 200 {"status":"healthy","time":...}
```

`/healthz` **no comprueba upstreams**: un 200 dice que el proceso está vivo y el router montado, no
que identity o la API pública contesten.

### 6.2 El log

Una línea por petición, `msg="petición web completada"` con `status`, `method`, `path` y `latency`
(en nanosegundos). En `local` es texto legible; fuera de `local` es **JSON**.

```sh
# las peticiones que no fueron 2xx/3xx
grep '"msg":"petición web completada"' client-console.log | grep -v '"status":2' | grep -v '"status":3'
# los fallos de negocio
grep '"level":"WARN"' client-console.log
```

🔴 **Lo que el log no tiene, a propósito:** correos, contraseñas, tokens, el texto del cliente y la
cotización redactada. Si estás depurando «qué escribió la dueña», **no está ahí y no debe estarlo**.

### 6.3 Síntomas y su causa

| Síntoma | Causa habitual | Dónde mirar |
|---|---|---|
| El proceso muere al arrancar sin servir nada | `panic` deliberado: proxies inválidos, opciones de `iam` inválidas o **una plantilla que no compila** | `internal/web/server.go:81`, `:153`, `:397` |
| Login siempre «Credenciales inválidas o sin acceso a esta consola» | 401 de identity **o** 403 del System Gate: en pantalla dan el **mismo texto** a propósito | el `WARN` del log distingue las dos: `internal/web/auth_handler.go:73-80` |
| Vuelve a `/login?error=session_expired` | había cookie y ya no sirve; el `AuthMiddleware` la borra | `internal/web/auth_handler.go:123-165` |
| Una sección responde 403 con la pantalla vacía | falta la capacidad del plan (`cart_basic` o `catalog_import`), **o el plan no se pudo leer** (fail-closed) | `internal/web/solicitudes_gate.go`; busca el `WARN` «no se pudieron leer las features del tenant» |
| Todas las pantallas pintan «sin empresa» | el Context Token viene sin tenant: o cero empresas, o **varias sin elegir** | `internal/web/tenants_handler.go:13-24`; comprueba `GET /api/v1/auth/tenants` |
| La sugerencia muere sin pintar nada | los tres plazos desordenados: han de ser **55 s cliente < 58 s petición < 60 s escritura** | `internal/web/solicitudes_plazos.go`; si `WAPP_CONSOLE_QUOTE_SUGGESTION_TIMEOUT_SECS=0` la ruta cae al plazo del grupo (20 s) y **vuelve a morir** |
| Se pidió la sugerencia, se esperó, y el campo llegó vacío | la cotización **no cupo en la cookie** (tope 3.000 B) y se pintó sobre el POST, o la cookie era de otra solicitud | `INFO` «cotización demasiado larga para la cookie», `WARN` «la cookie de la cotización no es de esta solicitud» |
| Un fichero de catálogo se rechaza con un número que no cuadra | son **dos límites**: 4 MiB el sobre, 1 MiB el fichero; y las pantallas escriben «MB» donde son MiB | `internal/web/catalogo_limite.go`, y `deuda.md` |
| Cerrar sesión en una consola cierra la otra | los nombres de cookie se pisaron: el puerto **no** aísla cookies | `internal/web/session.go:20-31` y `internal/web/cookies_test.go` |
| «La pantalla dice que el teléfono lleva un día sin verse» | el campo `last_seen_at` de la plataforma **solo se escribe en cambios de estado**, no en el latido | defecto del cloud, no de esta consola; ver `deuda.md` |
