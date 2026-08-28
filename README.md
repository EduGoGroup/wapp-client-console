# wapp-client-console

Consola web de administración del **CLIENTE** (el tenant) del ecosistema wApp: la UI Go con la que
la dueña del negocio y su equipo operan **su propia empresa** — sus sesiones, su bandeja, su
catálogo. Es server-side rendering endurecido, sin SPA y sin API propia.

Escucha en **`127.0.0.1:8107`**.

## Qué NO es

No es la consola de plataforma. En wApp hay **dos perímetros distintos** y no conviene mezclarlos:

| | `wapp-client-console` (este repo) | `wapp-platform-console` |
|---|---|---|
| Quién la usa | el **cliente** (admin del tenant) | los **operadores de plataforma** (nosotros) |
| Puerto | `127.0.0.1:8107` | `127.0.0.1:8106` |
| Plano que consume | API **pública** `:8103` (Context Token, filtrado por tenant) | listener **admin** `:8100` |
| `system` ante identity | `wapp.bff` (el mismo que el BFF: mismo perímetro) | `wapp.platform` |
| Cookies | `wapp_client_session` / `wapp_client_csrf` | `wapp_platform_session` / `wapp_platform_csrf` |

🔴 **Esta consola no declara `WAPP_ADMIN_API_BASE`, y es deliberado.** El plano admin (`:8100`) es
el de operadores —tenants, instalaciones, planes, kill-switch comercial— y esta UI no debe poder
hablar con él ni por accidente. La variable no existe y `internal/config` no tiene el campo: quien
quiera añadirlo tiene que justificar primero por qué la UI de un cliente necesita el plano de
plataforma. No basta con "no usarla": una variable declarada es una invitación a cablearla.

## Estado

**Login.** Sobre el andamiaje —cadena de middleware endurecida, configuración, arranque con apagado
ordenado y `GET /healthz`— vive ya el ciclo de sesión completo: `GET/POST /login`, `POST /logout`,
el `AuthMiddleware` con refresco proactivo y una pantalla autenticada mínima (`/`) que muestra la
empresa y el usuario de la sesión. **Las pantallas de negocio —miembros, roles, bandeja— todavía no
están**: llegan en la tanda siguiente del Plan 047, y por eso este repo aún no tiene ningún cliente
HTTP de la API pública más allá del canje.

Dos cosas del login conviene saberlas antes de tocarlo:

- **El `system` ante identity es `wapp.bff`**, el mismo que usa el BFF del cliente, porque es el
  mismo perímetro. No se estrena una clave propia: el canje de la plataforma solo acepta tres
  systems, identity-core no expone endpoint para dar de alta uno nuevo, y `ReplaceUserSystems` es
  declarativo (revoca lo que no se le manda), así que estrenar una obligaría a re-acreditar a cada
  usuario de cliente que ya existe. Está declarado en `internal/web/server.go` con ese porqué.
- **El 401 de credenciales y el 403 del System Gate dan el MISMO texto en pantalla** —no se le dice
  a un desconocido si un correo existe— pero **causas DISTINTAS en el log**, que es entonces el
  único sitio donde vive esa diferencia. En la consola de plataforma esto estaba prometido en un
  comentario y cumplido solo para una de las dos ramas, y el 2026-08-28 costó una tarde de
  diagnóstico a ciegas. Aquí lo vigilan dos tests emparejados (`auth_test.go`), y ninguno de los dos
  deja que el correo entre en el log.

Lo que estaba resuelto desde el primer commit son los **nombres de cookie**. En
`wapp-shared/web` el nombre es un **parámetro**, no una constante: sus defaults (`wapp_session` /
`wapp_csrf`) son los del BFF del cliente, y un consumidor que no los parametrice los **hereda en
silencio y compila igual**. Somos el tercer consumidor. `internal/web/session.go` fija
`wapp_client_session` / `wapp_client_csrf` y `internal/web/cookies_test.go` verifica **por el cable**
que ninguno de los seis nombres ajenos sale de aquí — comparando **literales**, nunca las constantes
del propio paquete (comparar contra la constante pasaría igual con la constante cambiada).

## Cómo se corre

```sh
cp .env.example .env      # ajusta si hace falta; los defaults ya apuntan a local
make run                  # levanta en 127.0.0.1:8107
curl -s http://127.0.0.1:8107/healthz
```

## Gates

En wApp **un PR no valida nada**: `ci.yml` es `workflow_dispatch` y la validación continua vive en
la máquina de desarrollo. Los gates son dos y hay que pasarlos antes de publicar:

```sh
make ci-local     # fmt-check + vet + lint + test (-race) + build, con el toolchain de la máquina
make ci-docker    # lo mismo dentro de golang:1.26.5-bookworm + golangci-lint v2.12.2
```

Go **1.26.5** y golangci-lint **v2.12.2** están fijados en el `Makefile`; `ci-docker` monta la raíz
`wApp/` como `/workspace` porque el módulo resuelve dependencias del ecosistema.

Dos trampas conocidas al verificarlos:

- **Lee el `rc` sin pipe.** `make ci-local | tail -30` imprime "code 0" aunque el make falle.
  Hazlo así: `make ci-local > /tmp/log 2>&1; echo "RC = $?"`.
- **`go build ./...` no compila los tests**: sale verde con un test roto. El gate son `vet` y `test`.

## Alta en el workspace

Este módulo está dado de alta en `wApp/go.work` (línea `./guardian/wapp-client-console`). Ese
fichero vive en **otro repo** y un `grep` desde aquí no lo encuentra; sin esa línea, `go build` con
el workspace activo muere con *"directory prefix . does not contain modules listed in go.work"*.
El alta **no sustituye al release**: el CI y cualquier otro clon resuelven por `go.mod`, así que
`GOWORK=off go build ./...` es lo que dice si el repo está de verdad en verde. **Prohibido el
`replace`** a un repo de wApp: la ruta base lleva un espacio y falla con *"malformed module path"*.

## Reutilización

El middleware **no se copia**: `BuildCSP`, `ValidateCSRF`, `KeyedRateLimiter`, `ParseOrigins`,
`ParseTrustedProxies`, `FlashCatalog` y compañía viven en `wapp-shared/web` (+ `web/gin`), y las
hojas de estilo comunes en `wapp-shared/ui`. Se compila contra el **tag publicado**, no contra el
árbol de al lado.

## Ramas

`main` es la rama publicada; el trabajo aterriza en `dev` y pasa a `main` al final del plan. El
workflow `sync-main-to-dev.yml` realinea `dev` tras cada push a `main` (es el único workflow que se
dispara solo).
