package apiclient

import "time"

// Client agrupa los clientes de dominio del plano público sobre UN solo Transport, y por tanto sobre
// un solo pool de conexiones.
//
// ⚠️ Un solo pool, pero DOS plazos: el Transport lleva el http.Client general (15s, o lo que ponga la
// config) y el de inferencia (55s), y la única llamada que usa el segundo es
// IntakesClient.SuggestIntakeQuote. El porqué está en transport.go.
//
// Los OCHO van en CAMPOS CON NOMBRE y no embebidos: `Members`, `Roles`, `Sessions`, `Invitations` y
// `Tenants` tienen los cinco un `List`, y embebiéndolos la llamada `api.List(...)` sería un selector
// ambiguo que ni siquiera compila. Con nombre, además, la llamada dice el plano al que va
// —`api.Roles.Assign(…)` sobre una ruta que empieza por /members— que es justo la distinción que el
// scope hace y el prefijo esconde.
//
// 🔴 `Editor` es el caso que lo demuestra sin discusión: tiene DOS listados (`ListFlows` y
// `ListTriggers`) que son dos planos distintos con dos scopes distintos, y el nombre del campo es lo
// único que separa `api.Editor.ListFlows(…)` de una sopa de métodos en la raíz.
type Client struct {
	Members      *MembersClient
	Roles        *RolesClient
	Entitlements *EntitlementsClient
	Sessions     *SessionsClient
	Invitations  *InvitationsClient
	// Editor es el plano de FLUJOS y REGLAS DE DISPARO: qué conversación se recorre y qué la
	// arranca (Plan 047 · Ola 6, mudado del BFF).
	Editor *EditorClient
	// Intakes es la BANDEJA: las solicitudes que llegaron por WhatsApp, su borrador, su corrección y
	// su aprobación (Plan 047 · Ola 7, mudado del BFF). Es el campo con más métodos —diez— y por eso
	// es también el que mejor explica por qué van con NOMBRE: `api.Intakes.ListIntakes(…)` dice el
	// plano al que va; un `List` más en la raíz sería el tercer selector ambiguo del paquete.
	Intakes *IntakesClient
	// Tenants es el plano de la empresa DEL SUJETO (no el de operadores): entre cuáles puede elegir
	// quien tiene la sesión y cuál elige. Es el único que acepta un `tenantID`; el porqué, en
	// tenants.go.
	Tenants *TenantsClient
}

// New construye el cliente contra la API pública con el plazo por petición de la consola
// (WAPP_CONSOLE_UPSTREAM_TIMEOUT_SECS). Un timeout <= 0 cae al valor por defecto del paquete.
func New(baseURL string, timeout time.Duration) *Client {
	t := NewTransport(baseURL, timeout)
	return &Client{
		Members:      NewMembersClient(t),
		Roles:        NewRolesClient(t),
		Entitlements: NewEntitlementsClient(t),
		Sessions:     NewSessionsClient(t),
		Invitations:  NewInvitationsClient(t),
		Editor:       NewEditorClient(t),
		Intakes:      NewIntakesClient(t),
		Tenants:      NewTenantsClient(t),
	}
}
