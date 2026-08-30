# Contratos de `wapp-client-console`

Todo lo que otros consumen de esta pieza y todo lo que ella consume. Verificado sobre `ac906e2`
(2026-08-30).

---

## 0 · La regla de conteo, dicha antes de los números

🔴 **Se cuenta lo que devuelve `router.Routes()` de Gin en ejecución, no las líneas que registran
rutas.**

Aplicada a este repo:

- `grep -nE '^\s*(router|protected|solicitudes|catalogo)\.(GET|POST)\(' internal/web/server.go` da
  **42 líneas**.
- Una de esas líneas es un **bucle sobre tres hojas de estilo**: `internal/web/server.go:57-61`
  declara `sharedStylesheets` (tres nombres) y `internal/web/server.go:113-115` las registra en una
  sola línea.
- 41 registros directos + 3 del bucle = **44 rutas**.

⚠️ Tres informes previos escribieron «42 rutas» porque contaron líneas. **Son 44** (8 fuera de
sesión + 36 protegidas), y el número está confirmado ejecutando `router.Routes()` con un fichero de
test inyectado por *overlay*.

**Puerto por defecto: `127.0.0.1:8107`** (`internal/config/config.go:97`).
**No hay gRPC. No hay API JSON propia** salvo `/healthz`. **No hay PUT ni DELETE**
(`internal/web/server.go:169-173`): el navegador solo emite GET y POST, y el `apiclient` traduce al
verbo real hacia arriba.

---

## 1 · Rutas HTTP que sirve

Fuente: `internal/web/server.go:106-341`, el único sitio donde se registran rutas. Las constantes
salen de `internal/web/solicitudes_handler.go:41,46`,
`internal/web/solicitudes_detalle.go:47,64-70`, `internal/web/editor_handler.go:46-47`,
`internal/web/catalogo_handler.go:56,59`, `internal/web/invitations_handler.go:39` y
`internal/web/tenants_handler.go:32`.

### 1.1 Públicas — 8 (antes del `AuthMiddleware`)

| Método | Ruta | Nota |
|---|---|---|
| GET | `/healthz` | `internal/web/server.go:117` — JSON `{status, time}`. **Antes del CSRF**: no recibe cookies |
| GET | `/static/css/app.css` | hoja propia embebida (`:106`) |
| GET | `/static/css/wapp-tokens.css` | del módulo `wapp-shared/ui` (bucle, `:113-115`) |
| GET | `/static/css/wapp-components.css` | ídem |
| GET | `/static/css/theme-bff.css` | ídem — **el tema del perímetro del cliente**, el mismo que el BFF |
| GET | `/login` | `authH.ShowLogin` |
| POST | `/login` | `authH.DoLogin` |
| POST | `/logout` | `authH.DoLogout` |

### 1.2 Sesión y empresa — 3

| Método | Ruta | Handler |
|---|---|---|
| GET | `/` | `ShowHome` (portada + plan) |
| GET | `/mi-identificador` | `ShowMyIdentifier` — **la única que sirve a una sesión SIN empresa sin invocar el parcial `sin_empresa`** |
| POST | `/empresa` | `SelectTenant` — selector multi-empresa; **no exige empresa** |

### 1.3 Personas — 11

| Método | Ruta | Handler |
|---|---|---|
| GET | `/miembros` | `ShowMembers` |
| POST | `/miembros` | `AddMember` |
| POST | `/miembros/:user_id/baja` | `RemoveMember` (traduce a `DELETE` upstream) |
| GET | `/invitaciones` | `ShowInvitations` |
| POST | `/invitaciones` | `IssueInvitation` |
| POST | `/invitaciones/canjear` | `RedeemInvitation` — **no exige empresa** |
| POST | `/invitaciones/:id/revocar` | `RevokeInvitation` |
| GET | `/roles` | `ShowRoles` |
| POST | `/roles` | `CreateRole` |
| POST | `/roles/asignar` | `AssignRole` |
| POST | `/roles/retirar` | `UnassignRole` |

### 1.4 Flota — 3 · **sin gate de plan, a propósito**

`internal/web/server.go:222-232` lo declara: *«quien venga a gatearlo estaría cortando el acceso de
un tenant a su propia flota»*.

| Método | Ruta | Handler |
|---|---|---|
| GET | `/sesiones` | `ShowSessions` |
| POST | `/sesiones/enviar` | `SendTestMessage` |
| POST | `/sesiones/:id/perfil` | `SetSessionProfile` (`active` \| `passive`) |

### 1.5 Editor — 6

| Método | Ruta | Handler |
|---|---|---|
| GET | `/flujos` | `ShowFlows` |
| GET | `/flujos/:id` | `ShowFlowDetail` — 🔴 `/flujos/nuevo` es **valor mágico**, no una ruta |
| POST | `/flujos` | `PublishFlow` (publica la versión N+1; los flujos son inmutables versionados) |
| GET | `/disparadores` | `ShowTriggers` |
| POST | `/disparadores` | `CreateTrigger` |
| POST | `/disparadores/:id/borrar` | `DeleteTrigger` (traduce a `DELETE`) |

### 1.6 Bandeja — 10 · grupo `/solicitudes`, **gate `cart_basic`**

`internal/web/server.go:301-313`.

| Método | Ruta | Handler | ¿Le habla al cliente por WhatsApp? |
|---|---|---|---|
| GET | `/solicitudes` | `ShowSolicitudes` | no |
| POST | `/solicitudes/descartar` | `DescartarSolicitudes` (dos pasos: `action=review` → `action=discard`) | no |
| GET | `/solicitudes/:id` | `ShowSolicitudDetalle` | no |
| POST | `/solicitudes/:id/estado` | `CambiarEstadoSolicitud` | no |
| POST | `/solicitudes/:id/lineas` | `GuardarLineasSolicitud` | no |
| POST | `/solicitudes/:id/corregir` | `CorregirInterpretacionSolicitud` | no |
| POST | `/solicitudes/:id/regenerar` | `RegenerarSolicitud` | no |
| POST | `/solicitudes/:id/aprobar` | `AprobarSolicitud` | 🔴 **sí** |
| POST | `/solicitudes/:id/pedir-info` | `PedirInfoSolicitud` | 🔴 **sí** |
| POST | `/solicitudes/:id/sugerir-respuesta` | `SugerirRespuestaSolicitud` + `plazoDeEscrituraSugerencia` | no (cuesta una inferencia) |

🔴 Para las dos que sí: un 200 **no** significa que el mensaje llegara. Ver `constitucion.md`,
INV-C2.

### 1.7 Catálogo — 3 · grupo `/importar-catalogo`, **gate `catalog_import`**

`internal/web/server.go:337-341`.

| Método | Ruta | Handler |
|---|---|---|
| GET | `/importar-catalogo` | `ShowCatalogo` |
| POST | `/importar-catalogo` | `ImportarCatalogo` — 🔴 **un solo POST para los dos pasos**, discriminado por el campo `mode` (`validate` \| `apply`, `internal/web/catalogo_handler.go:74-76`) |
| GET | `/importar-catalogo/plantilla` | `DescargarPlantillaCatalogo` (`?format=json\|csv\|xlsx`, lista blanca en `internal/apiclient/catalogimport.go:314-323`) |

**Total: 8 + 3 + 11 + 3 + 6 + 10 + 3 = 44.**

---

## 2 · Lo que consume (upstreams)

### 2.1 identity (`WAPP_IDENTITY_URL`) — vía `wapp-shared/iam@v0.1.0`

`POST /api/v1/auth/login` · `POST /api/v1/auth/refresh` · `POST /api/v1/auth/logout`.

El `system` que viaja es **`wapp.bff`**, constante en `internal/web/server.go:46` — **el mismo que
el BFF, a propósito**: son el mismo perímetro, el canje de la plataforma solo acepta tres systems,
e `identity-core` no expone endpoint para dar de alta uno nuevo.

### 2.2 API pública de la plataforma (`WAPP_PUBLIC_API_BASE`, `:8103 /api/v1`)

| Plano | Endpoints | Cliente |
|---|---|---|
| Canje | `POST /auth/exchange` | `wapp-shared/iam` |
| Empresa | `GET /auth/tenants` · `POST /auth/active-tenant` | `internal/apiclient/tenants.go:75,105` |
| Plan | `GET /entitlements` | `internal/apiclient/entitlements.go:40` |
| Miembros | `GET\|POST /members` · `DELETE /members/{user_id}` | `internal/apiclient/members.go:105,123,149` |
| Roles | `GET\|POST /roles` · `POST /members/{user_id}/roles` · `DELETE /members/{user_id}/roles/{role_id}` | `internal/apiclient/roles.go:59,82,104,124` |
| Sesiones | `GET /sessions` · `POST /sessions/{id}/profile` · `POST /messages` | `internal/apiclient/sessions.go:139,169,190` |
| Invitaciones | `POST\|GET /invitations` · `DELETE /invitations/{id}` · `POST /invitations/accept` | `internal/apiclient/invitations.go:188,210,236,261` |
| Editor | `GET /flows` · `GET /flows/{id}` · `POST /flows` · `GET\|POST /triggers` · `DELETE /triggers/{id}` | `internal/apiclient/editor.go:160,189,214,233,255,277` |
| Bandeja | `GET /intakes` · `GET /intakes/{id}` · `POST /intakes/{id}/status` · `PUT /intakes/{id}/items` · `POST /intakes/discard` · `POST /intakes/{id}/approve` · `POST /intakes/{id}/request-info` · `POST /intakes/{id}/reanalyze` · `POST /intakes/{id}/quote-suggestion` | `internal/apiclient/intakes.go`, `intakes_draft.go`, `intakes_reanalyze.go`, `intakes_quote.go` |
| Catálogo | `POST /catalog/import` · `POST /catalog/import/tabular` · `GET /catalog/import/template?format=…` · `GET /catalog/import/prompt` · `GET /tenant-content` | `internal/apiclient/catalogimport.go:19,23,423,461,499,528` |

🔒 **INV-04 — el `tenant_id` NUNCA viaja** (ni en cuerpo, ni en query, ni en ruta), con **una sola
excepción declarada**: `POST /api/v1/auth/active-tenant`. Ver `constitucion.md`, INV-C3.

🔴 **No existe cliente del plano admin `:8100`**, y está escrito en tres sitios:
`internal/config/config.go:14-21`, `.env.example:20-25` y `internal/apiclient/transport.go:16-17`.

### 2.3 Sentinelas de error (el contrato interno entre capas)

`internal/apiclient/transport.go:35-58`: `ErrUnauthorized` (401), `ErrForbidden` (403),
`ErrNotFound` (404), `ErrConflict` (409), `ErrInvalidInput` (400); más los tipados con cuerpo
(`ErrMemberOfAnotherTenant`, `ErrPersonUnknown`, `*LinesWithoutPriceError`, `*NotApprovableError`,
`*InvalidTransitionError`, `ErrIntakeChanged`, `*FeatureNotEnabledError`).

🔴 **Un 404 no significa «no existe»**: la plataforma responde 404 —y no 403— cuando el UUID
pertenece a **otra empresa**, a propósito. La única operación donde un 404 sí significa «no existe»
es el alta de un miembro, que pregunta por el padrón de identity.

---

## 3 · Variables de entorno

**21 variables, todas con prefijo `WAPP_`**, compuesto por el loader
(`sharedconfig.New(sharedconfig.WithEnvPrefix("WAPP_"))`, `internal/config/config.go:90`). Los
nombres de abajo son los **efectivos en la máquina**: el literal `CONSOLE_ENV` del código es
`WAPP_CONSOLE_ENV` en el entorno.

Fuente: `internal/config/config.go:89-131` (`Load()`), documentadas en `.env.example`.

| Variable | Default | Qué hace |
|---|---|---|
| `WAPP_CONSOLE_ENV` | `local` | ambiente lógico; `local` ⇒ log Text+Debug y `Secure`/HSTS a `false` por defecto |
| `WAPP_CLIENT_CONSOLE_HTTP_ADDR` | `127.0.0.1:8107` | dirección de escucha |
| `WAPP_PUBLIC_API_BASE` | `http://127.0.0.1:8103` | API pública de la plataforma |
| `WAPP_IDENTITY_URL` | `http://127.0.0.1:8200` | emisor de identidad |
| `WAPP_CONSOLE_COOKIE_SECURE` | `true` salvo `ENV=local` | atributo `Secure` de las cuatro cookies |
| `WAPP_CONSOLE_COOKIE_SAMESITE` | `lax` | `lax` \| `strict` \| `none` (`none` obliga `Secure`) |
| `WAPP_CONSOLE_ALLOWED_ORIGINS` | *(vacío)* | allowlist CSV de CORS; vacío == same-origin. **Nunca `*`** |
| `WAPP_CLIENT_CONSOLE_TRUSTED_PROXIES` | *(vacío)* | CSV de IP/CIDR; inválido ⇒ **panic en el arranque** |
| `WAPP_CONSOLE_HSTS_ENABLED` | sigue a `COOKIE_SECURE` | emite `Strict-Transport-Security` |
| `WAPP_CONSOLE_RATE_ENABLED` | `true` | enciende el rate-limit en memoria |
| `WAPP_CONSOLE_RATE_RPS` | `5` | tasa |
| `WAPP_CONSOLE_RATE_BURST` | `10` | ráfaga |
| `WAPP_CONSOLE_RATE_TTL_SECS` | `300` | inactividad tras la que se desaloja una clave |
| `WAPP_CONSOLE_RATE_PURGE_SECS` | `60` | cada cuánto se intenta el barrido |
| `WAPP_CONSOLE_READ_HEADER_TIMEOUT_SECS` | `5` | anti-slowloris |
| `WAPP_CONSOLE_READ_TIMEOUT_SECS` | `15` | ídem |
| `WAPP_CONSOLE_WRITE_TIMEOUT_SECS` | `30` | ídem; **lo releva la sugerencia** |
| `WAPP_CONSOLE_IDLE_TIMEOUT_SECS` | `60` | ídem |
| `WAPP_CONSOLE_SHUTDOWN_TIMEOUT_SECS` | `10` | plazo del apagado ordenado |
| `WAPP_CONSOLE_UPSTREAM_TIMEOUT_SECS` | `20` | deadline por petición **y** `http.Client.Timeout` del cliente general |
| `WAPP_CONSOLE_QUOTE_SUGGESTION_TIMEOUT_SECS` | `55` | 🔴 plazo de **una sola ruta**; de él salen los otros dos (58 s y 60 s) sumando márgenes. `0` == apagado (la ruta cae al plazo del grupo, **nunca a «sin plazo»**) |

### 3.1 🔴 16 nombres compartidos con `wapp-platform-console`, **y las dos corren en la misma máquina**

La intersección de nombres efectivos entre esta consola y `wapp-platform-console` son **18**, de los
cuales dos (`WAPP_IDENTITY_URL`, `WAPP_PUBLIC_API_BASE`) son upstreams legítimamente comunes. Los
**16 restantes son la familia `WAPP_CONSOLE_*` entera**:

```
WAPP_CONSOLE_ALLOWED_ORIGINS       WAPP_CONSOLE_COOKIE_SAMESITE    WAPP_CONSOLE_COOKIE_SECURE
WAPP_CONSOLE_ENV                   WAPP_CONSOLE_HSTS_ENABLED       WAPP_CONSOLE_IDLE_TIMEOUT_SECS
WAPP_CONSOLE_RATE_BURST            WAPP_CONSOLE_RATE_ENABLED       WAPP_CONSOLE_RATE_PURGE_SECS
WAPP_CONSOLE_RATE_RPS              WAPP_CONSOLE_RATE_TTL_SECS      WAPP_CONSOLE_READ_HEADER_TIMEOUT_SECS
WAPP_CONSOLE_READ_TIMEOUT_SECS     WAPP_CONSOLE_SHUTDOWN_TIMEOUT_SECS
WAPP_CONSOLE_UPSTREAM_TIMEOUT_SECS WAPP_CONSOLE_WRITE_TIMEOUT_SECS
```

En UAT las dos corren en el **mismo host** (`127.0.0.1:8107` y `127.0.0.1:8106`) y hoy se salvan
solo porque cada unidad `systemd` tiene su propio `EnvironmentFile`. **Cualquier variable de esa
lista exportada en el entorno del host se aplica a las dos a la vez**, y no hay forma de darle un
valor distinto a cada una sin ficheros separados. Lo que **sí** está desambiguado es la dirección de
escucha (`WAPP_CLIENT_CONSOLE_HTTP_ADDR` vs `WAPP_PLATFORM_CONSOLE_HTTP_ADDR`) y los proxies de
confianza. El BFF se salva porque su familia es `WAPP_GUARDIAN_*`. Ver `deuda.md`.

### 3.2 Estado en UAT (verificado el 2026-08-30)

El `EnvironmentFile` de la unidad define **18** de las 21. Las **tres que corren con su valor por
defecto** son `WAPP_CONSOLE_QUOTE_SUGGESTION_TIMEOUT_SECS`, `WAPP_CONSOLE_RATE_PURGE_SECS` y
`WAPP_CONSOLE_RATE_TTL_SECS`. Las 18 puestas tienen lector, comprobado a mano.

⚠️ El auditor de variables del ecosistema (`scripts/auditar-env-vs-codigo.sh`, en el repo de
documentación) **no audita este servicio**: su mapa no tiene la fila, y **sale verde sin decir que
hay un `.env` que no ha visto**.

---

## 4 · Cookies que emite (contrato con el navegador)

`internal/web/session.go:20-31`. Ninguna se deja al default del módulo.

| Cookie | Vida | `Path` | Papel |
|---|---|---|---|
| `wapp_client_session` | 24 h | `/` | sesión HttpOnly: Context Token + Refresh Token |
| `wapp_client_csrf` | 24 h | `/` | double-submit CSRF (HttpOnly y `SameSite` los fija el módulo, siempre) |
| `wapp_client_invitacion` | 60 s | `/invitaciones` | código de invitación, un solo uso; lo borra el GET que lo consume |
| `wapp_client_sugerencia` | 60 s | `/solicitudes/{id}` | cotización redactada, un solo uso, **con el id dentro del sobre como segunda cerradura** |

Las dos efímeras **no se cifran ni se firman**: lo que viaja es exactamente lo que se le va a pintar
en la cara a quien lo pidió, dos milisegundos después. Lo que compran es que el dato **no pase por
la URL**.

---

## 5 · Ficheros, CLI, esquemas

- **Ficheros que escribe: ninguno.**
  `grep -rn 'os.Create\|os.WriteFile\|os.OpenFile\|ioutil.WriteFile' --include='*.go' .` → cero. El
  proceso solo escribe a `stdout`, en **JSON** salvo con `WAPP_CONSOLE_ENV=local`. En UAT esa salida
  se redirige a `client-console.log` desde la unidad `systemd`.
- **Ficheros que lee en ejecución: ninguno.** Las plantillas y `app.css` van **embebidos** en el
  binario (`//go:embed`); las otras tres hojas salen del módulo `wapp-shared/ui`. El `.env` lo lee
  `systemd`, **no el proceso**.
- **Ficheros que recibe por HTTP: uno.** El documento de catálogo en `POST /importar-catalogo`
  (multipart), con techo de **4 MiB** en el sobre y **1 MiB** en el fichero extraído.
- **CLI: no hay.** Ni subcomandos ni banderas.
- **gRPC: no hay.**
- **Tablas / esquemas: ninguno.** Esta pieza **no tiene base de datos**. Los nombres de tabla que
  aparecen en comentarios (`tenant_members`, `intake_jobs`) son de la plataforma y se citan para
  explicar por qué la API devuelve lo que devuelve.

---

## 6 · Qué registra en ejecución

- **Una línea por petición**, emitida por `webgin.SlogLogger`: `msg="petición web completada"` con
  `status`, `method`, `path` y `latency`. Nivel `INFO`, formato JSON fuera de `local`.
- **Arranque y apagado**: `«arrancando consola de cliente»` (con `http_addr`, `public_api`,
  `identity`, `env`), `«consola de cliente escuchando»`, `«señal de apagado recibida…»`,
  `«consola de cliente apagada limpiamente»`.
- **Cada fallo de negocio** deja un `WARN` con el error del upstream: unos 40 mensajes distintos, del
  tipo `«no se pudo aprobar la solicitud»` o `«no se pudieron leer las features del tenant (modo
  degradado; el gate CIERRA)»`.
- **El login registra la causa** distinguiendo el 401 de credenciales del 403 del System Gate —en
  pantalla los dos dan el mismo texto para no revelar si un correo existe— **y sin el correo**
  (`internal/web/auth_handler.go:73-80`).
- 🔴 **Lo que nunca se registra:** contraseñas, tokens, correos, el texto del cliente y la cotización
  redactada. La regla está escrita donde se aplica
  (`internal/web/solicitudes_sugerencia.go:92-94`).
