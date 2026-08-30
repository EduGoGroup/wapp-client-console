# `wapp-client-console` — documentación de la pieza

**Qué es.** La consola web del **cliente**: la UI Go con la que la dueña de una empresa (un
*tenant*) y su equipo operan **su propia empresa** — sus teléfonos, su gente, sus flujos, su
bandeja de solicitudes y su catálogo. Es *server-side rendering* endurecido, **sin una línea de
JavaScript**, sin SPA y **sin API propia**: habla solo con la API pública de la plataforma
(`:8103 /api/v1`) y con el emisor de identidad (`identity`). Escucha en `127.0.0.1:8107`.

**Para qué existe.** El ADR-0047 del ecosistema («el destino de la UI de negocio es la consola Go,
no el KMP») fijó que las seis pantallas de negocio que vivían provisionalmente en
`wapp-guardian-bff` se mudaran aquí. Las olas 6–9 del Plan 047 ejecutaron esa mudanza: el BFF se
quedó con la aduana (login, alta de cuenta, espera) y lo técnico (variables, integraciones,
proveedor de IA), y **todo el negocio vive en este repo**.

**Lo que NO es.** No es la consola de plataforma (`wapp-platform-console`, `:8106`, para
operadores nuestros) y **no puede** hablar con el plano admin `:8100`: aquí no existe ni la
variable de entorno que lo nombraría, y eso es una decisión, no un olvido.

---

## Los documentos

| Documento | Qué contiene |
|---|---|
| [`constitucion.md`](constitucion.md) | 🔴 **Léelo primero.** Los invariantes que no se pueden violar (los del ecosistema que aplican y los propios: PRG por mutación, «esta consola no sabe si el WhatsApp salió», la empresa la guarda el servidor, fail-closed del gate por plan), la tecnología real del `go.mod`, las convenciones y **las trampas conocidas**. |
| [`arquitectura.md`](arquitectura.md) | Capas, mapa de paquetes, punto de entrada y binario, la cadena de middleware en orden, y diagramas de cómo viaja una petición. |
| [`contratos.md`](contratos.md) | Las **44 rutas** HTTP con la regla de conteo, los ~38 endpoints que consume, las **21 variables de entorno** con su valor por defecto, las cuatro cookies y qué ficheros escribe (ninguno). |
| [`operacion.md`](operacion.md) | Cómo se arranca en local, los `make` reales y qué valida cada uno, cómo se publica y cómo se depura. Incluye por qué **un PR no valida nada** y por qué hay que **contar los SKIP**. |
| [`deuda.md`](deuda.md) | La deuda viva con `fichero:línea`: el error tragado del refresco, los comentarios caducados, la colisión de `WAPP_CONSOLE_*` con la otra consola y el candado de rutas que este repo no tiene. |

---

## Lo mínimo para no romper nada

1. **El PRG se aplica donde hubo mutación.** Un POST que falla la validación **antes** de llamar a
   la API responde **400 repintando**; todo lo demás (éxito o error de la API) responde **303 +
   flash**. Es la decisión D-047.16 y está escrita en `internal/web/admin_handler.go:133-167`.
2. **Esta consola no puede saber si un WhatsApp salió.** El envío es un paso posterior del cloud y
   no devuelve error: con la sesión caída se ve el mismo 200. Los textos de confirmación **nunca**
   pueden decir «enviado» a secas.
3. **La empresa activa la guarda el servidor** (INV-8). El `tenant_id` no viaja en ninguna llamada
   salvo en la elección explícita de empresa, que es la única excepción y está vigilada por test.
4. **Cero JavaScript, cero base de datos, cero ficheros escritos.** Las tres cosas están vigiladas
   o son verificables con un `grep`; romper cualquiera cambia la pieza de categoría.
5. **El código compartido no se copia: se importa de `wapp-shared`.** Y está prohibido importar
   ningún repo `edugo-*`.
