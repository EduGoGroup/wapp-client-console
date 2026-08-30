# CLAUDE.md — `wapp-client-console`

Portal corto. **La verdad vive en [`documentations/`](documentations/README.md)**; esto solo apunta.

## Qué es esta pieza

La **consola web del cliente** del ecosistema wApp: la UI Go con la que la dueña de una empresa y su
equipo operan **su propia empresa** — teléfonos, personas, roles, invitaciones, flujos,
disparadores, bandeja de solicitudes e importación de catálogo. *Server-side rendering* endurecido,
**cero JavaScript**, **cero base de datos**, sin API propia. Escucha en `127.0.0.1:8107` y habla
**solo** con la API pública de la plataforma (`:8103`) y con `identity`.

Es la casa a la que se mudaron, en las olas 6–9 del Plan 047, las seis pantallas de negocio que
vivían provisionalmente en `wapp-guardian-bff` (ADR-0047). **No** es la consola de plataforma
(`wapp-platform-console`, `:8106`), que es la de los operadores.

## Las cinco reglas innegociables

1. **El PRG se aplica donde hubo MUTACIÓN** (D-047.16). Validación que falla **antes** de llamar a
   la API ⇒ **400 repintando**; error de la API o éxito ⇒ **303 + flash**. La única pregunta que
   decide un caso nuevo: *¿pudo escribir algo al otro lado?* Regla en
   `internal/web/admin_handler.go:133-167`.
2. **Esta consola NO puede saber si un WhatsApp salió.** El envío es el paso (4) del cloud y no
   devuelve error: con la sesión caída se ve el mismo 200. Ningún texto de confirmación puede decir
   «enviado» ni «lo recibió» (`internal/web/flash.go:500-512`).
3. **El `tenant_id` no viaja y la empresa activa la guarda el SERVIDOR** (INV-04 / INV-8). Excepción
   única y declarada: `POST /api/v1/auth/active-tenant`. Vigilado por `internal/web/inv04_test.go`,
   con test hermano de aserto positivo.
4. **El gate por plan es fail-closed POR CONSTRUCCIÓN.** Middleware de grupo, no `if` por handler;
   `Has` sobre mapa `nil` es `false`. 🔴 **No escribas un `if ent.Resolved`.**
5. **Zero-knowledge y doble llave.** La nube nunca toca credenciales ni llaves privadas: la **DEK**
   la custodia el cliente y **jamás cruza ningún contrato** (aquí no existe ni como campo), y el
   **Lease** lo emite y revoca el servidor. Lo que sí sube a la nube, a propósito, es el contenido
   de negocio. Sin broker ni Redis: la concurrencia se resuelve con Go.

**Y tres prohibiciones de forma:** no importes ningún repo `edugo-*` (se copió y se adaptó, no se
depende); el código compartido se toma de `wapp-shared` **contra el tag publicado**, nunca del árbol
de al lado; y **prohibido `replace`** a un repo de wApp (la ruta base lleva un espacio).

## Antes de tocar nada

- **Cuenta rutas con `router.Routes()`, no con `grep`.** Son **44**: 41 registros directos más un
  bucle sobre 3 hojas de estilo (`internal/web/server.go:57-61` y `:113-115`). Quien cuente líneas
  dirá 42, y tres informes ya lo hicieron.
- **Un PR no valida nada**: `ci.yml` es `workflow_dispatch`. El gate es `make ci-local` en local, y
  se lee el `rc` **sin pipe**.
- **`.env` no se lee**: el loader solo mira `os.LookupEnv`. En UAT lo lee `systemd`.
- **El `README.md` de este repo describe el primer día y afirma cosas falsas.** No lo uses de fuente.

## Índice de `documentations/`

| Fichero | Qué contiene |
|---|---|
| [`README.md`](documentations/README.md) | portal de la pieza |
| [`constitucion.md`](documentations/constitucion.md) | 🔴 invariantes, tecnología real, convenciones y trampas conocidas |
| [`arquitectura.md`](documentations/arquitectura.md) | capas, paquetes, cadena de middleware y diagramas |
| [`contratos.md`](documentations/contratos.md) | las 44 rutas, los upstreams, las 21 variables y las 4 cookies |
| [`operacion.md`](documentations/operacion.md) | arranque, gates, publicación y depuración |
| [`deuda.md`](documentations/deuda.md) | deuda viva con `fichero:línea` y cómo se cierra |
