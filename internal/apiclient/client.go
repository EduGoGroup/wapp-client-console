package apiclient

import "time"

// Client agrupa los clientes de dominio del plano público sobre UN solo Transport (y por tanto un
// solo http.Client, con su pool de conexiones y su plazo).
//
// Los cinco van en CAMPOS CON NOMBRE y no embebidos: `Members`, `Roles`, `Sessions` e `Invitations`
// tienen los cuatro un `List`, y embebiéndolos la llamada `api.List(...)` sería un selector ambiguo
// que ni siquiera compila. Con nombre, además, la llamada dice el plano al que va —`api.Roles.Assign(…)`
// sobre una ruta que empieza por /members— que es justo la distinción que el scope hace y el prefijo
// esconde.
type Client struct {
	Members      *MembersClient
	Roles        *RolesClient
	Entitlements *EntitlementsClient
	Sessions     *SessionsClient
	Invitations  *InvitationsClient
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
	}
}
