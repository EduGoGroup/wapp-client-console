# Constitución de `wapp-client-console`

Las reglas de esta pieza. Lo que aquí se llama **invariante** es algo que no se puede violar sin
romper el producto o la seguridad; cada uno lleva **cómo se comprueba** y, si existe, **qué test lo
vigila**.

Foto verificada sobre el árbol de trabajo el **2026-08-30**, ramas `main` y `dev` en
`ac906e2c2cdf511d0754f20b632da908aad946ec` (idénticas), único tag `v0.1.0` (que apunta a `8c96797`,
**no** al HEAD).

---

## 0 · Qué pieza es esta dentro del ecosistema (repetido, porque el repo se clona solo)

wApp es un ecosistema de mensajería sobre WhatsApp cuyo **núcleo corre 24/7 en el equipo del
cliente** (el *Edge*) y está gobernado por una **plataforma cloud modular**. Esta consola es una de
las cuatro superficies de usuario, y es **la del cliente sobre su propio tenant**.

Las piezas que necesitas conocer para no equivocarte aquí:

| Pieza | Qué es | Cómo la ve esta consola |
|---|---|---|
| Plataforma cloud | Go modular; sirve `:8100` (admin, operadores) y `:8103` (**pública**, clientes) | 🔴 **solo `:8103`** |
| `identity` | emisor de Identity Tokens (`identity-core`), fuera de wApp | login, refresco y logout |
| Edge Agent | el daemon con la sesión de WhatsApp, en la máquina del cliente | **nunca directamente**: solo a través del cloud |
| `wapp-guardian-bff` (`:8104`) | la aduana del cliente (alta de cuenta, espera) y lo técnico del tenant | mismo perímetro, misma `system`, mismo tema visual |
| `wapp-platform-console` (`:8106`) | la consola de **operadores de plataforma** | 🔴 otro perímetro; **no se mezcla** |
| `wapp-shared` | monorepo multi-módulo propio de wApp, releases por módulo (tags `<modulo>/vX.Y.Z`) | de aquí salen CSRF, CSP, rate-limit, cookies, flash, IAM y las hojas de estilo |

### 0.1 Los invariantes del ecosistema que aplican aquí

- 🔒 **Zero-knowledge.** La nube **nunca** accede a credenciales ni a llaves privadas. Protege
  **llaves**, **no** el contenido de negocio: los pedidos, los catálogos y los textos de los
  clientes **sí** suben a la nube, a propósito. Esta consola muestra ese contenido de negocio; lo
  que **jamás** puede tocar, pedir ni pintar es material de llaves.
- 🔒 **Doble llave** (ADR-0007, en el repo de documentación del ecosistema). La **DEK** —la que
  descifra el almacén de `whatsmeow`— la custodia el **cliente** y **jamás cruza ningún contrato**:
  no existe en esta consola, ni como campo, ni como pantalla, ni como variable. El **Lease** —lo
  que autoriza a operar— lo emite y **revoca el servidor**, y es el kill-switch anti-clon. Nada de
  esto se opera desde aquí.
- 🔒 **Sin Redis ni broker en el Edge**: la concurrencia se resuelve con Go. Esta consola no tiene
  concurrencia propia más allá de una goroutine (`internal/bootstrap/server.go:33`) y el
  *single-flight* de refrescos (`internal/web/auth_handler.go:25`).
- 🔒 **Copia-adaptación, nunca dependencia.** Se copió código de otro producto (EduGo) y se adaptó
  al espacio de nombres de wApp. **Está prohibido importar un repo `edugo-*`.** Comprobación:
  `grep -rn 'edugo-' go.mod` debe dar cero como *require directo* (hoy hay un `indirect`,
  `github.com/EduGoGroup/identity-shared/auth`, que entra arrastrado por `wapp-shared/iam`; no se
  importa desde este código).
- 🔒 **El código compartido interno vive en `wapp-shared`**, y se compila **contra el tag
  publicado**, nunca contra el árbol de al lado. Ver §4.

---

## 1 · Los invariantes propios de esta pieza

### INV-C1 · El PRG se aplica **donde hubo mutación**, no en todo POST

**La regla, escrita en `internal/web/admin_handler.go:133-167` (D-047.16, 2026-08-29, ampliada el
2026-08-30 en T7.4):**

| desenlace | qué responde |
|---|---|
| validación que falla **antes** de llamar a la API | **400 repintando**, con el formulario intacto |
| la API responde error (409, 422, 502, 401…) | **303 + flash** |
| éxito | **303 + flash** |

🔴 **Esto cambia el contrato respecto al BFF.** El BFF repinta sobre el POST con 200/400/403 y
conserva lo tecleado; aquí un F5 tras guardar **no** reenvía el formulario, y lo tecleado se pierde
salvo en las excepciones. Quien migre una pantalla del BFF y compare «rutas y plantillas» sin
comparar el **andamiaje** se llevará el cambio de códigos por sorpresa.

**La única pregunta que decide un desenlace nuevo:** *¿pudo escribir algo al otro lado?* Si no,
repinta; si sí —o si **no se puede saber**—, 303.

Sitios donde hoy vive el repintado (16 `StatusBadRequest` de producción, en 8 ficheros):
`internal/web/editor_handler.go:270,341,348` (publicar flujo, crear disparador),
`internal/web/solicitudes_lineas.go:319,329,339,374`,
`internal/web/solicitudes_acciones.go:377,404,435`,
`internal/web/solicitudes_comparacion.go:431`, `internal/web/solicitudes_sugerencia.go:286`,
`internal/web/solicitudes_descarte.go:190,200`, `internal/web/catalogo_handler.go:415,480`,
más el 400 del login (`internal/web/auth_handler.go:67`).

**Cómo se comprueba:** `internal/web/solicitudes_prg_test.go` recorre los desenlaces de la bandeja.
⚠️ El comentario que dice «hoy la excepción vive en **DOS sitios**»
(`internal/web/admin_handler.go:158`) está **caducado**: son ocho ficheros. Ver `deuda.md`.

### INV-C2 · Esta consola **no puede saber si un WhatsApp salió**

**Es un invariante de producto, no un detalle de redacción.** Dos acciones de la bandeja mandan un
mensaje a una persona real: `POST /solicitudes/:id/aprobar` y `POST /solicitudes/:id/pedir-info`.

El envío es el **paso (4)** del cloud, **posterior** a la transición y a la revisión, y **no
devuelve error a propósito** —una aprobación ya escrita no se deshace porque el teléfono esté
apagado—. Con la sesión de WhatsApp caída, esta pantalla ve **el mismo 200**.

🔴 **Consecuencia obligatoria:** ningún texto de confirmación puede decir «enviado» ni «el cliente
lo recibió». El 200 significa «se aplicó y quedó registrado». La frase del mensaje de confirmación
es **lo único** que impide inventar ese dato.

**Dónde está escrito:** `internal/web/flash.go:505-512` (sobre `flashSolicitudAprobada` y
`flashSolicitudInfoPedida`) y `internal/apiclient/intakes_draft.go:388`.
**Cómo se comprueba:** leyendo los dos textos del catálogo de flash; hoy **ningún test estructural
prohíbe la palabra «enviado»** en ellos (candidato de `deuda.md`).

### INV-C3 · La **empresa activa la guarda el servidor** (INV-8), y el `tenant_id` no viaja (INV-04)

- **INV-04:** ningún método de `internal/apiclient` acepta un `tenantID`. La empresa sale del
  **Context Token** que la plataforma verifica en cada llamada, y por eso **no hay dónde ponerlo por
  error**: el parámetro no existe (`internal/apiclient/transport.go:1-17`).
- **La única excepción, declarada:** `TenantsClient.SetActive` →
  `POST /api/v1/auth/active-tenant` (`internal/apiclient/tenants.go:10-22`), que manda el
  `tenant_id` **en el cuerpo**. Es la elección de empresa de quien pertenece a varias, y ocurre
  **una vez, en una acción deliberada de una persona**.
- **Por qué importa:** los tres consumidores web **re-canjean solos cada ~13 min** sin nadie
  delante. Un `tenant_id` aceptado por el **canje** viajaría en cada refresco desatendido; con la
  elección en el servidor, lo que el canje lee después ya está guardado allí.

**Cómo se comprueba — hay tres tests, y uno es hermano con aserto positivo**
(`internal/web/inv04_test.go`): `TestINV04_LaConsolaNoMandaNuncaElTenant`,
`TestINV04_LaELECCIONdeEmpresaEsLaUNICAexcepcion`, `TestINV04_TodoLoQueSaleVaAlPlanoPublico`.
🔴 Si añades una segunda excepción, va **con test hermano**: meterla en la tabla del invariante lo
rompe, dejarla fuera en silencio lo envejece.

### INV-C4 · El plano **admin `:8100` no existe** en este repo

No hay `AdminAPIBaseURL` en `internal/config/config.go:14-21`, no hay `WAPP_ADMIN_API_BASE` en
`.env.example:21-25`, y `internal/apiclient/transport.go:16-17` lo repite. **No basta con «no
usarla»: una variable declarada es una invitación a cablearla.**

**Cómo se comprueba:** `grep -rn 'ADMIN_API\|:8100' --include='*.go' internal` → cero.
En campo (UAT, 2026-08-30) se verificó que el `.env` de esta unidad **no** la lleva, mientras que el
de `wapp-platform-console` sí.

### INV-C5 · Fail-closed del gate por plan, **por construcción**

Dos grupos de rutas van detrás de una capacidad: `/solicitudes*` tras `cart_basic` y
`/importar-catalogo*` tras `catalog_import` (`internal/web/server.go:302`, `:338`).

- El corte es **un middleware sobre el grupo**, no un `if` por handler. El BFF copió ese `if` en
  cinco sitios y por eso su GET y sus POST acabaron respondiendo distinto ante la misma ausencia de
  capacidad sin que nadie lo decidiera (`internal/web/solicitudes_gate.go:22-27`).
- **Fail-closed sin condición que olvidar:** si el plan no se puede leer, `resolveEntitlements`
  devuelve la vista cero, cuyo mapa es `nil`, y `Has` sobre un mapa `nil` es `false`. 🔴 **No
  escribas un `if ent.Resolved`**: sería la única forma de que el gate se abriera por un fallo del
  upstream (`internal/web/entitlements.go:66-75`, `internal/web/solicitudes_gate.go:41-44`; en `:30-39` están las alternativas descartadas).
- **Responde 403 pintando la pantalla vacía con la razón, en GET y en POST.** No redirige: un 303 a
  una pantalla que ese tenant no puede ver es un bucle.
- ⚠️ **Excepción conocida:** sin empresa el middleware hace `c.Next()` sin comprobar la capacidad
  (`internal/web/solicitudes_gate.go:70-73`). Está razonado y hoy es inocuo, pero es un fail-open
  **condicionado** a que los handlers de dentro sigan sin llamar a la API sin tenant. Ver `deuda.md`.

**Cómo se comprueba:** `internal/web/solicitudes_gate_test.go` y `internal/web/entitlements_test.go`.

### INV-C6 · Los nombres de cookie son **de esta consola**, nunca los del módulo

`wapp-shared/web` expone el nombre como **parámetro**; sus defaults (`wapp_session` / `wapp_csrf`)
son los del BFF, y **quien no parametrice los hereda en silencio y compila igual**. Esta consola es
el **tercer** consumidor.

| Cookie | Vida | Papel |
|---|---|---|
| `wapp_client_session` | 24 h (`consoleWorkday`, `internal/web/session.go:42`) | sesión HttpOnly con Context + Refresh Token |
| `wapp_client_csrf` | 24 h | double-submit CSRF |
| `wapp_client_invitacion` | 60 s, `Path=/invitaciones` | código de invitación, un solo uso |
| `wapp_client_sugerencia` | 60 s, `Path=/solicitudes/{id}` | la cotización redactada, un solo uso |

🔴 **El puerto no aísla cookies.** Las cuatro superficies del ecosistema viven en `127.0.0.1` y
comparten dominio: lo único que las separa son los nombres.

**Cómo se comprueba:** `internal/web/cookies_test.go` →
`TestCookieNames_SonLasDeEstaConsolaYNoLasDeLosOtrosDosPerimetros`, que compara **literales** (no
las constantes del propio paquete: comparar contra la constante pasaría igual con la constante
cambiada) y además afirma que los defaults del módulo no cambiaron.

### INV-C7 · El flash de un solo uso **no es para textos**

El mecanismo de cookie efímera de `wapp-shared/web@v0.2.0` nació para un token de 43 caracteres:
**no tiene tope de tamaño ni identidad de objeto**, y el navegador **descarta en silencio** una
cookie que pase de ~4 KB. Las dos guardas las pone esta consola:

1. **Tope propio.** `maxCookieSugerencia = 3000` bytes del valor ya codificado
   (`internal/web/solicitudes_sugerencia.go:63`). Si no cabe, **se pinta sobre el POST**: no perder
   una cotización que costó 40 s de modelo manda sobre hacer el PRG.
2. **Dos cerraduras de identidad.** El `Path` lleva el identificador de la solicitud —lo pone el
   navegador— y el identificador viaja **dentro del sobre** —lo comprueba el servidor—
   (`internal/web/session.go:104-133`, `tomaSugerenciaFlash`). Hacen falta las dos: una sola se cae
   con que alguien reescriba la ruta.

🔴 Sin la segunda cerradura, pedir la sugerencia de A y abrir B en otra pestaña pintaría **los
precios de A** delante de quien va a responderle a B.

**Cómo se comprueba:** `internal/web/solicitudes_sugerencia_test.go`.

### INV-C8 · Cero JavaScript, y la CSP lo respalda

Las plantillas se sirven **siempre** por el renderizador, que siembra el nonce CSP, el token CSRF,
la ruta actual y el estado de sesión (`internal/web/server.go:51`). No hay `<script>`, ni
`onclick`, ni estilos en línea.

**Cómo se comprueba — cuatro tests en `internal/web/security_test.go`:**
`TestTemplates_SinJavaScript`, `TestTemplates_SinEstilosInline`,
`TestRenderer_NoSeSirveHTMLSinPasarPorElRenderizador`,
`TestCSP_LaPantallaDeEntradaLlevaNonceYNoUnsafeInline`, más
`TestPaginas_TodasLasPantallasAutenticadasEstanCubiertas`, que compara contra las rutas GET que el
router registra de verdad (`internal/web/security_test.go:293-330`) para que una pantalla nueva no
quede sin vigilar.

**Corolario:** solo hay **GET y POST**. Un formulario HTML no sabe hacer otra cosa; el
`apiclient` traduce al verbo real (`DELETE` para la baja de un miembro, para retirar un rol, para
borrar un disparador y para anular una invitación). Pasar a `fetch()` significaría meter JS y, con
él, un nonce en cada página.

### INV-C9 · Cero persistencia, cero ficheros escritos

No hay base de datos, ni migraciones, ni DSN, ni versión de esquema. El único estado que esta pieza
custodia son **cookies en el navegador**; todo lo demás lo guarda la plataforma y se relee por la
API **sin caché, una vez por petición** (D-040.6, `internal/apiclient/entitlements.go:34`).

**Cómo se comprueba:** `grep -rn 'database/sql\|pgx\|lib/pq\|sqlite\|migrat' --include='*.go' .` →
cero fuera de tests; `grep -rn 'os.Create\|os.WriteFile\|os.OpenFile' --include='*.go' .` → cero.
El proceso solo escribe a `stdout` (`slog`).

### INV-C10 · El techo de cuerpo va **antes** del CSRF, y son **dos límites distintos**

|  | qué mide | quién lo aplica | valor |
|---|---|---|---|
| `maxCuerpoCatalogo` | el **sobre** de la petición entera | `limiteDeCuerpo` | **4 MiB** (`internal/web/catalogo_limite.go:41`) |
| `maxArchivoCatalogo` | el **fichero** ya extraído | `archivoDelFormulario` | **1 MiB** (`internal/web/catalogo_handler.go:112`) |

🔴 **El orden no es negociable:** el CSRF lee el formulario para comparar el token y con eso consume
el cuerpo entero —a memoria y a disco—, así que un tope montado después llegaría cuando el daño ya
está hecho (`internal/web/server.go:121-126`). Por eso la página de rechazo **no lleva barra de
navegación**: ahí todavía no ha corrido el `AuthMiddleware`.

🔴 **No fundas los dos límites en uno.** El paso 2 no manda un fichero: manda el documento
normalizado dentro de un campo `x-www-form-urlencoded`, varias veces mayor que el JSON que el cloud
mide. Bajar el sobre a 1 MiB rechazaría documentos que la plataforma **sí** acepta.

⚠️ Defecto vivo de superficie: las dos pantallas escriben «MB» donde las constantes son **MiB**, y
anuncian números distintos (1 y 4). Ver `deuda.md`.

### INV-C11 · Una plantilla que no compila **aborta el arranque**

`parseTemplates()` hace `panic` si el árbol embebido no compila (`internal/web/server.go:397`), y lo
mismo pasa con una lista de proxies inválida (`:81`) y con unas opciones de `iam` inválidas
(`:153`). Es deliberado: el fallo aparece al desplegar, no en la cara del primer usuario.
⚠️ Consecuencia: `NewRouter` **puede matar el proceso desde un test**.

---

## 2 · Tecnología y versiones reales

De `go.mod` (verificado):

- **Go `1.26.5`** (`go.mod:3`), la misma versión que fija el `Makefile` (`GO_VERSION := 1.26.5`).
- **golangci-lint `v2.12.2`**, fijado en `Makefile:9`.

| Módulo | Versión | Para qué |
|---|---|---|
| `github.com/gin-gonic/gin` | `v1.10.0` | router y render HTML |
| `github.com/golang-jwt/jwt/v5` | `v5.3.1` | leer los claims **sin verificar firma** |
| `github.com/EduGoGroup/wapp-shared/web` | `v0.2.0` | CSRF, CSP, rate-limit, cookies, flash, body-limit, `web/gin` |
| `github.com/EduGoGroup/wapp-shared/iam` | `v0.1.0` | login/refresh/logout contra identity + canje contra la plataforma |
| `github.com/EduGoGroup/wapp-shared/auth` | `v0.5.0` | el tipo `Claims` del JWT |
| `github.com/EduGoGroup/wapp-shared/ui` | `v0.4.1` | hojas comunes (`wapp-tokens.css`, `wapp-components.css`, `theme-bff.css`) |
| `github.com/EduGoGroup/wapp-shared/config` | `v0.3.0` | lector de entorno con prefijo |

**Tamaño medido:** 76 ficheros `.go` · 28.247 líneas (13.171 de producción, 15.076 de test) · 20
ficheros de plantilla/CSS con 3.078 líneas · **354 funciones `Test*`**.

**Nada de:** base de datos, streaming, websockets, JavaScript, broker, caché.

---

## 3 · Convenciones de código de esta casa

1. **Las rutas se componen con constantes, nunca con literales repetidos.** Las mismas constantes
   con las que se registra la ruta arman el `action` del formulario y el patrón que compara el
   despachador de plazos (`internal/web/solicitudes_detalle.go:47,64-70`,
   `internal/web/solicitudes_plazos.go:55`). Un formulario apuntando a una ruta que el router
   escribe distinto es un 404 que ningún gate ve venir.
2. **Los textos de pantalla salen SIEMPRE del catálogo local**, nunca del upstream: `flash.go`
   tiene **104 códigos** (`grep -cE '^\s*flash[A-Za-z]+\s*=\s*"' internal/web/flash.go`). El cuerpo
   de error de la API se registra en el log, no se pinta.
3. **La API se traduce a la voz de la dueña.** Los estados del ciclo de vida se pintan traducidos
   (`internal/web/solicitudes_estado.go:44-56`); las etiquetas de la barra dicen «Disparadores», no
   «Triggers». 🔑 **Lo que viaja por el cable no se traduce**: los valores siguen siendo `keyword`,
   `fallback`, `escape`, `event_start`, `event_stop`, y los campos del cuerpo siguen en inglés
   (`rendered_text`, `question`, `mode`).
4. **La deuda no se marca: se argumenta en prosa.** No hay ni un `TODO`, `FIXME`, `HACK` ni `XXX` en
   todo el repo, ni un `nolint` (el único supresor es un `// #nosec G203` justificado en
   `internal/web/server.go:372`). Se lee muy bien y **no se puede grepear**: nada avisa cuando un
   párrafo caduca, y hay varios caducados (ver `deuda.md`).
5. **Los errores de dominio son sentinelas**, no códigos sueltos: `ErrUnauthorized`, `ErrForbidden`,
   `ErrNotFound`, `ErrConflict`, `ErrInvalidInput` (`internal/apiclient/transport.go:35-58`), más
   los tipados con cuerpo. 🔴 Un **404 no significa «no existe»**: la plataforma responde 404 —y no
   403— cuando el UUID pertenece a otra empresa, a propósito.
6. **Cero PII de personas.** `GET /api/v1/members` devuelve **solo UUIDs**: `tenant_members` es
   `user_id, tenant_id, created_at`. La pantalla enseña el identificador abreviado y deja el
   completo en el `title` (`internal/web/admin_handler.go:176-196`). **No inventes una fuente de
   nombres.**
7. **Nada de correos ni de contenido de negocio en el log.** El login registra la causa (401 de
   credenciales vs. 403 del System Gate) **sin el correo**; la cotización y el texto del cliente no
   se registran nunca (`internal/web/solicitudes_sugerencia.go:92-94`).

---

## 4 · Trampas conocidas (lo que se hace mal aquí si nadie lo dice)

1. 🔴 **Contar rutas leyendo líneas.** `grep` sobre los registros de `internal/web/server.go` da
   **42 líneas**, y tres informes escribieron «42 rutas». Son **44**: una de esas líneas es un bucle
   sobre tres hojas de estilo (`internal/web/server.go:57-61` define `sharedStylesheets`,
   `:113-115` las registra). **La regla es contar lo que devuelve `router.Routes()` en ejecución.**
2. 🔴 **Creer que este repo compila contra el árbol de `wapp-shared` de al lado.** Compila contra el
   **tag publicado**. `make` usa `GOWORK=off` en todos los targets a propósito; un puerto nuevo de
   `shared` no está disponible hasta que hay release.
3. 🔴 **`replace` a un repo de wApp.** Prohibido: la ruta base del checkout lleva un espacio y falla
   con *«malformed module path»*.
4. 🔴 **Copiar el `.env.example` y esperar que se lea.** `config.Load()` usa
   `sharedconfig.New(WithEnvPrefix("WAPP_"))`, que **solo consulta `os.LookupEnv`**. No hay
   `godotenv`: `make run` es `go run` a secas. **Copiar `.env` no configura nada.**
5. 🔴 **Escribir la variable con el nombre del código.** El prefijo lo compone el loader: el literal
   `CONSOLE_ENV` de `internal/config/config.go:92` es **`WAPP_CONSOLE_ENV`** en la máquina. Todos
   los nombres efectivos están en `contratos.md`.
6. 🔴 **Registrar una ruta de solicitudes o de catálogo fuera de su grupo.** El gate por capacidad
   es del grupo: fuera de él la ruta queda **abierta** y nada lo dice.
7. 🔴 **Añadir un `if ent.Resolved`** para «arreglar» el modo degradado del plan. Es exactamente lo
   que abriría el gate ante un fallo del upstream (INV-C5).
8. 🔴 **Suponer que `/flujos/nuevo` es una ruta.** No lo es: `nuevo` es un **valor mágico** que el
   handler reconoce y que pinta el formulario de alta sin llamar a la API
   (`internal/web/server.go:253-256`, `internal/web/editor_handler.go:50-58`).
9. 🔴 **Suponer que `/importar-catalogo` tiene dos POST.** Tiene **uno**, y lo que separa comprobar
   de aplicar es el campo `mode` (`validate` / `apply`, `internal/web/catalogo_handler.go:74-76`).
   Un test que compruebe «el POST contestó» pasa con los dos confundidos, y confundirlos
   **reemplaza el catálogo de una empresa sin aprobación**.
10. 🔴 **Poner un plazo más largo *después* del general.** Un `context.WithTimeout` más largo colgado
    de un padre que vence antes **no alarga nada**. El plazo largo tiene que **sustituir** al corto,
    que es por lo que `plazoPorRuta` es un despachador y no un grupo hermano
    (`internal/web/solicitudes_plazos.go:66-92`). Los tres plazos de la sugerencia tienen que quedar
    en orden **cliente (55 s) < deadline de petición (58 s) < write deadline (60 s)**; subir uno sin
    mover los otros invierte el diseño.
11. 🔴 **Retirar una ruta y buscar solo en el fichero que se borra.** Un test que **se queda** puede
    estar usando esa ruta de testigo y seguirá verde midiendo un 404. Al retirar hay **dos**
    búsquedas.
12. 🔴 **Un aserto de ausencia sin guarda anti-cero.** «Ninguna de estas rutas existe» lo cumple el
    conjunto vacío. Este repo **no tiene** el candado de inventario de rutas que sí tiene
    `wapp-guardian-bff` (`internal/web/rutas_declaradas_test.go` allí, con su `t.Fatal` cuando el
    conjunto de sujetos queda vacío). Ver `deuda.md`.
13. ⚠️ **Fiarse del `README.md` de la raíz del repo.** Describe el estado del primer día
    (2026-08-28) y afirma que las pantallas de negocio «todavía no están». **Es falso**: están las
    seis, y hay nueve clientes de dominio sobre ~38 endpoints.
14. ⚠️ **Fiarse del `rc` de un pipe.** `make ci-local | tail -30` imprime «code 0» aunque el make
    falle. Ver `operacion.md`.
