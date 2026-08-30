# Deuda viva de `wapp-client-console`

Lo que está mal, lo que está a medias y lo que caducó. Verificado sobre `ac906e2` el **2026-08-30**.

🔴 **Este repo no marca su deuda: la argumenta en prosa.**
`grep -rnE '(//|#).*(TODO|FIXME|HACK|XXX)'` sobre `*.go`, `*.html` y `*.yml` devuelve **cero**
(solo falsos positivos de la palabra española «TODO/TODOS» en mayúsculas dentro de prosa). Tampoco
hay ni un `nolint`; el único supresor es un `// #nosec G203` justificado en
`internal/web/server.go:372`. Se lee muy bien y **no se puede grepear**: nada avisa cuando un
párrafo caduca, y por eso este fichero existe.

---

## 1 · Defectos de código

### D-1 🔴 Error tragado en el refresco de sesión

`internal/web/auth_handler.go:235`

```go
_ = h.startSession(c, res)
```

**Consecuencia:** si `EncodeSession` falla, el navegador **conserva la cookie vieja** —con el refresh
token ya consumido— y **no queda rastro**: ni `slog`, ni error devuelto. La sesión se cae más tarde,
en otra petición, con un síntoma que no apunta aquí.

**Es el único `_ =` del repo que descarta un error con consecuencia funcional.** Los otros cinco son
cierres de `Body` y decodificaciones de cuerpos de error deliberadamente *best-effort*
(`internal/apiclient/transport.go:400-401`, `internal/apiclient/catalogimport.go:688`,
`internal/apiclient/intakes.go:380`, `internal/web/catalogo_handler.go:594`).

**Cómo se cierra:** registrar un `slog.Warn` con el fallo y decidir explícitamente qué se hace —lo
más probable, borrar la cookie y expulsar a `/login`, que es el estado real—. Un test que fuerce el
fallo de codificación y compruebe que la cookie **no** sobrevive.

### D-2 🔴 El refresco reactivo no actualiza el contexto de la petición

`internal/web/auth_handler.go:223-236` (`refreshSession`) + `:248-262` (`withAuthRetry`)

`refreshSession` renueva la **cookie**, pero **no reescribe** `webgin.ContextAccessToken` ni
`ContextRefreshToken`. Y **una misma petición hace dos o más llamadas**: `pageData` →
`resolveTenants` es una (hay **24** llamadas a `pageData` en el paquete), y el handler hace la suya;
el gate por capacidad también corre sobre GET y POST.

**Consecuencia:** la segunda llamada parte del **token viejo** del contexto y, si vuelve a haber que
refrescar, lo intenta con el **refresh token ya usado**.

⚠️ **NO VERIFICADO** si esto rompe: depende de si identity **rota** el refresh token, y ese repo no
se leyó. Lo verificado es (a) que el contexto no se actualiza y (b) que hay ≥2 llamadas por
petición.

**Cómo se cierra:** que `refreshSession` siembre los dos tokens nuevos en el contexto de gin, con un
test de dos llamadas consecutivas donde la primera fuerza 401.

### D-3 🔴 El gate por capacidad se abre para las sesiones sin empresa

`internal/web/solicitudes_gate.go:70-73`

```go
if sinEmpresa(c) {
    c.Next()
    return
}
```

Está razonado —sin tenant, `GET /api/v1/entitlements` responde 401, y el 403 que saldría sería un
**diagnóstico falso**— y **hoy es inocuo**: el handler no llama a la API sin tenant y el POST del
descarte se va por 303 antes de tocar nada.

**Pero es un fail-open condicionado** a que los handlers de dentro sigan portándose bien, y **nada lo
vigila estructuralmente**. Es exactamente el tipo de invariante que depende de que alguien se
acuerde.

**Cómo se cierra:** un test que, con sesión sin empresa, recorra **las diez rutas del grupo** y
afirme que ninguna llega a tocar la API pública (contando peticiones sobre el `httptest` falso), con
guarda anti-cero sobre el número de rutas recorridas.

---

## 2 · Candados que faltan

### D-4 🔴 No existe el inventario de rutas anti-fantasma que **sí** tiene el BFF

`wapp-guardian-bff` tiene `internal/web/rutas_declaradas_test.go`: recorre el **AST** del paquete,
recolecta toda constante cuyo valor tenga forma de patrón de ruta y comprueba que **está registrada
en el router**, con dos guardas explícitas —`t.Fatal` si el conjunto de constantes queda vacío y
`t.Fatal` si `router.Routes()` viene vacío—.

Nació de una mutación que ningún test mataba: al retirar la bandeja, un despachador de plazos siguió
comparando contra una constante de ruta **que ya no existía**. Compilaba, `vet` en cero, suite verde.

**Este repo tiene el mismo material inflamable y no tiene el candado:** 16 constantes de ruta y
sufijo (`internal/web/solicitudes_detalle.go:47,64-70`, `internal/web/editor_handler.go:46-47`,
`internal/web/catalogo_handler.go:56,59`, `internal/web/solicitudes_handler.go:41,46`,
`internal/web/invitations_handler.go:39`, `internal/web/tenants_handler.go:32`) y **un despachador
que compara `c.FullPath()` contra una cadena compuesta** (`rutaSugerenciaCompleta`,
`internal/web/solicitudes_plazos.go:55`). Si esa ruta cambiara de forma, el despachador dejaría de
reconocerla **en silencio** y la sugerencia volvería a morir a los 20 s.

Los dos tests que hoy tocan `router.Routes()` —`internal/web/security_test.go:293-330` y
`internal/web/solicitudes_detalle_test.go:708-723`— **no cubren esto**: el primero mira solo GET de
páginas y el segundo solo los siete sufijos del detalle, escritos a mano.

**Cómo se cierra:** portar el test del BFF. Es el mismo AST, el mismo `router.Routes()` y las mismas
dos guardas anti-cero.

### D-5 🔴 Nada impide que un texto de confirmación prometa una entrega

INV-C2 —«esta consola no puede saber si un WhatsApp salió»— hoy **descansa solo en la redacción** de
dos entradas del catálogo (`flashSolicitudAprobada` y `flashSolicitudInfoPedida`,
`internal/web/flash.go:500-512`). Su razón está escrita justo encima, pero **ningún test falla** si
alguien reescribe el texto y pone «enviado al cliente».

**Cómo se cierra:** un test de tabla sobre el catálogo de flash que prohíba, en esos dos códigos, los
literales «enviado», «recibió» y «entregado». Con guarda anti-cero: si los dos códigos dejan de
existir, el test tiene que gritar, no quedarse verde midiendo el conjunto vacío.

---

## 3 · Defectos de superficie

### D-6 🔴 El techo que la pantalla anuncia **no es** el que rechaza, y las unidades están mal

Son **dos límites que miden cosas distintas** (y eso está bien, ver `constitucion.md` INV-C10). El
defecto es lo que se le dice a quien mira:

| Dónde | Qué dice | Qué compara de verdad |
|---|---|---|
| `internal/web/templates/pages/catalogo.html:221` | «No puede pasar de **1 MB**» | `maxArchivoCatalogo` = **1 MiB** |
| `internal/web/templates/pages/cuerpo-demasiado-grande.html:21` | «pasa de **{{ .LimiteMiB }} MB**» | `maxCuerpoCatalogo` = **4 MiB** |

**Consecuencia:** existe un rango entre 1 MiB y 4 MiB donde el fichero **valida el sobre y se
rechaza por negocio**, con un mensaje distinto del que la pantalla anunciaba. Y las dos escriben
«MB» donde las constantes son **MiB**, que no es lo mismo (1 MiB = 1,048576 MB).

Hay candado que ata constante y texto (`internal/web/catalogo_test.go:990-1007`), así que **el
defecto es de unidad y de coherencia entre pantallas, no de sincronía**.

**Cómo se cierra:** escribir «MiB» en las dos plantillas, y que la pantalla de importación anuncie
los **dos** techos diciendo cuál mide qué.

---

## 4 · Comentarios caducados (la deuda que no se puede grepear)

| Dónde | Qué afirma | La verdad medida |
|---|---|---|
| `internal/web/admin_handler.go:158` | «hoy la excepción [al PRG] vive en **DOS sitios**» | **16 `StatusBadRequest` de producción en 8 ficheros** |
| `internal/web/admin_handler.go:45-48` | «`pageData` … solo lo llaman los **SEIS GET** que pintan … ni ningún POST» | **24 llamadas**, y **sí lo llama un POST**: `PublishFlow` → `repintarFlowDetail` (`internal/web/editor_handler.go:237`, `:264-270`), además del gate por capacidad (`internal/web/solicitudes_gate.go:77`), que corre sobre GET **y** POST. El `GET /api/v1/auth/tenants` que el comentario dice que ningún POST paga, **sí se paga** |
| `internal/config/config.go:63` · `internal/web/server.go:133` · `internal/web/solicitudes_plazos.go:39` · `.env.example:80` | «las **~20 rutas** de esta consola» | **44** (36 protegidas). El argumento sigue siendo válido; la cifra no |
| `internal/web/templates/partials/sin_empresa.html:4-16` | «**DIEZ** pantallas» | **once**: falta `catalogo.html`, que entró después. Es la **cuarta** vez que este recuento se da mal, y el propio comentario avisa: *«cuéntalo, no lo copies»* |
| `.github/workflows/ci.yml:18-19` | «sus únicas dependencias … son `wapp-shared/{config,ui,web}`» | **cinco**: `go.mod:6-10` añade `auth v0.5.0` e `iam v0.1.0` |
| `README.md` (raíz del repo), sección «Estado» | «las pantallas de negocio —miembros, roles, bandeja— **todavía no están** … este repo aún no tiene ningún cliente HTTP de la API pública más allá del canje» | **las dos mitades son falsas**: están registradas y sirven, y `internal/apiclient` tiene 14 ficheros de producción y **nueve clientes de dominio** sobre ~38 endpoints. Describe el estado del primer día (2026-08-28) y no se tocó tras las olas 5–9 |
| `README.md` (raíz) e `internal/web/cookies_test.go:32` | «sus defaults (`wapp_session` / `wapp_csrf`) **son los del BFF del cliente**» | solo la de **CSRF** coincide: la de sesión del BFF es **`wapp_guardian_session`**, parametrizada. Sin consecuencia funcional —la lista de prohibidos cubre los cuatro nombres— pero la justificación escrita es incorrecta, **y está copiada dentro del test** |
| `README.md` (raíz), «Cómo se corre» | `cp .env.example .env` | **el proceso no lee `.env`**: no hay `godotenv`. Ver `operacion.md` |

🔴 **El `README.md` de la raíz de este repo no se puede reescribir desde esta documentación**, pero
**no debe usarse como fuente**. La verdad de esta pieza vive en `documentations/`.

---

## 5 · Forma del código (aceptado hoy, con nombre)

- ⚠️ **`NewRouterWithLimiter` es una función de 285 líneas** (`internal/web/server.go:74-359`), de
  las que ~101 son código real y el resto comentario. Es el **único** punto donde se ve el sistema
  entero, y no hay ninguna partición por dominio. Partirlo tiene un coste real: hoy el orden de la
  cadena de middleware se lee de un tirón, y ese orden **es el diseño**.
- ⚠️ **Tres `panic()` en el arranque** (`internal/web/server.go:81`, `:153`, `:397`): proxies
  inválidos, opciones de `iam` inválidas y plantillas que no compilan. Es deliberado y está escrito,
  pero convierte a `NewRouter` en una función que **puede matar el proceso desde un test**.
- ⚠️ **15 funciones `flashCodeFor*`** en un fichero de 1.297 líneas
  (`internal/web/flash.go:815-1281`, de `flashCodeFor` a `flashCodeForPedirInfo`). Cada una traduce la misma familia de códigos HTTP con matices
  por pantalla: es duplicación **intencionada** (cada texto es distinto), y a la vez el sitio con más
  superficie para que dos pantallas contesten distinto al mismo 404.
- ✅ **Sin código muerto detectable.** `golangci-lint run` con `unused` activo → **0 issues** (medido
  el 2026-08-30, v2.12.2). ⚠️ Eso no cubre lo que el linter no ve: un invariante que vive en una
  **cadena** —como `rutaSugerenciaCompleta`— no lo compila nadie. Ver D-4.
- ✅ **Sin credenciales en el repo.** `grep -rniE 'password\s*=\s*"|secret\s*=\s*"|api_key|apikey'`
  sin tests → cero; `.gitignore:16-18` excluye `.env` y `.env.*` salvo `.env.example`, y el ejemplo
  no lleva secretos.

---

## 6 · Deuda de entorno (fuera del código, pero es de esta pieza)

### D-7 🔴 16 nombres de variable compartidos con `wapp-platform-console`, en la misma máquina

La familia `WAPP_CONSOLE_*` entera —ambiente, cookies, CORS, HSTS, rate-limit y los seis plazos— la
leen **las dos consolas**, y en UAT las dos corren en el **mismo host** (`:8107` y `:8106`). La lista
completa está en `contratos.md` §3.1.

Hoy no colisionan **solo** porque cada unidad `systemd` tiene su propio `EnvironmentFile`. Una
variable de esa familia exportada en el entorno del host —o un despliegue que decida usar entorno
compartido— se aplica a las dos a la vez, y **no hay forma de darle un valor distinto a cada una**.
Un `WAPP_CONSOLE_COOKIE_SECURE=false` puesto para depurar una consola apaga el `Secure` de la otra.

**Es una colisión esperando a ocurrir.** Lo que sí está desambiguado: la dirección de escucha
(`WAPP_CLIENT_CONSOLE_HTTP_ADDR`) y los proxies de confianza. El BFF se salva porque su familia es
`WAPP_GUARDIAN_*`.

**Cómo se cierra:** estrenar `WAPP_CLIENT_CONSOLE_*` para todo lo que es política **de esta**
consola, aceptando el nombre `WAPP_CONSOLE_*` como alias legado durante una transición (que es
justamente el mecanismo que `wapp-platform-console` ya usa para tres de sus variables). Coste: tocar
`internal/config/config.go:89-131` y los dos `.env` de UAT.

### D-8 🔴 El auditor de variables del ecosistema **no mira este servicio**

El script `auditar-env-vs-codigo.sh` (en el repo de documentación del ecosistema) audita **86** de
las 104 variables del VPS y sale **verde**. Las 18 que faltan son las de esta consola: su mapa no
tiene la fila, porque el repo nació después del script.

**Es exactamente el fallo que el propio script advierte en su comentario**: *«un `.env` sin fila no
se audita y su silencio parecería un verde»*. Se auditaron a mano el 2026-08-30 —las 18 tienen
lector— pero el auditor no lo sabía.

**Cómo se cierra:** añadir la fila del `.env` de esta consola al mapa del script. Y, de paso, la
segunda mitad que le falta al script: solo mira **puesta → sin lector**, no la contraria
(**código → sin poner**), que es la que produce los defaults silenciosos.

### D-9 ⚠️ El único tag no cubre lo desplegado

`v0.1.0` apunta a `8c96797` («login, miembros, roles y sesiones»). Todo lo mudado del BFF —editor,
bandeja, catálogo, multi-empresa— está **sin versionar**, aunque `main` esté desplegado en UAT. Hoy
no rompe nada (el despliegue va por SHA, verificado con el buildinfo), pero no hay forma de nombrar
«la versión que corre» con un tag.

### D-10 ⚠️ El log de UAT crece sin rotación

`client-console.log` (JSON, ~59 KB hoy) se escribe en modo **append puro** y **no hay entrada de
`logrotate`** para nada de wApp. Con 138 GB libres no es urgente, pero es crecimiento sin freno ni
supervisión. Deuda del despliegue, no del código.

---

## 7 · Huecos declarados de esta documentación

- **No se abrió ninguna pantalla en un navegador.** Todo el mapa de pantallas sale de plantillas,
  handlers y del volcado de `router.Routes()`. Los contrastes de color de `app.css` se leyeron en el
  CSS, y la doctrina de este proyecto dice que **el CSS no se audita leyendo reglas**.
- **No se leyó `identity`**, así que D-2 queda calificado como **NO VERIFICADO**.
- **Corrección a un informe previo:** se dijo que esta consola «documenta `last_seen_at` como campo
  que sirve». **Es al revés**: `internal/apiclient/sessions.go:36-39` declara explícitamente que
  `last_seen_at` **no se declara** porque *«un campo que nadie lee es una promesa de que alguien lo
  mantiene»*. El defecto de ese campo es del cloud y **no llega a las pantallas de esta consola**.
- **No existe ficha de esta pieza en el manual de usuario del ecosistema.** Hay ficha de la consola
  BFF y de la consola de plataforma; **no hay ninguna de la consola del cliente**, que es la que la
  dueña abre cada día y sirve 44 rutas y ocho pantallas de negocio. Es deuda del repo de
  documentación, y se apunta aquí porque nadie más la estaba mirando.
