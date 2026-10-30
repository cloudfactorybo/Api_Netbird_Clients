package netbird

import "time"

// rawStatus refleja la salida de `netbird status --json` (client/status/status.go, v0.79).
// Solo se declaran los campos que usa la API; el resto se ignora al decodificar.
type rawStatus struct {
	Peers struct {
		Total     int       `json:"total"`
		Connected int       `json:"connected"`
		Details   []rawPeer `json:"details"`
	} `json:"peers"`
	CliVersion    string     `json:"cliVersion"`
	DaemonVersion string     `json:"daemonVersion"`
	DaemonStatus  string     `json:"daemonStatus"`
	Management    rawService `json:"management"`
	Signal        rawService `json:"signal"`
	Relays        struct {
		Total     int        `json:"total"`
		Available int        `json:"available"`
		Details   []rawRelay `json:"details"`
	} `json:"relays"`
	IP              string   `json:"netbirdIp"`
	PubKey          string   `json:"publicKey"`
	KernelInterface bool     `json:"usesKernelInterface"`
	WgPort          int      `json:"wireguardPort"`
	FQDN            string   `json:"fqdn"`
	Networks        []string `json:"networks"`
	LazyConnection  bool     `json:"lazyConnectionEnabled"`
	ProfileName     string   `json:"profileName"`
}

type rawService struct {
	URL       string `json:"url"`
	Connected bool   `json:"connected"`
	Error     string `json:"error"`
}

type rawRelay struct {
	URI       string `json:"uri"`
	Available bool   `json:"available"`
	Error     string `json:"error"`
	Transport string `json:"transport"`
}

type rawPeer struct {
	FQDN             string    `json:"fqdn"`
	IP               string    `json:"netbirdIp"`
	PubKey           string    `json:"publicKey"`
	Status           string    `json:"status"`
	LastStatusUpdate time.Time `json:"lastStatusUpdate"`
	ConnType         string    `json:"connectionType"`
	IceCandidateType struct {
		Local  string `json:"local"`
		Remote string `json:"remote"`
	} `json:"iceCandidateType"`
	IceCandidateEndpoint struct {
		Local  string `json:"local"`
		Remote string `json:"remote"`
	} `json:"iceCandidateEndpoint"`
	RelayAddress           string        `json:"relayAddress"`
	LastWireguardHandshake time.Time     `json:"lastWireguardHandshake"`
	TransferReceived       int64         `json:"transferReceived"`
	TransferSent           int64         `json:"transferSent"`
	Latency                time.Duration `json:"latency"`
	Rosenpass              bool          `json:"quantumResistance"`
	Networks               []string      `json:"networks"`
}

// Status es la vista que entrega la API: valores numéricos, fechas ISO 8601 y
// campos derivados (p2p, handshake vencido) listos para Elastic.
type Status struct {
	Timestamp    time.Time `json:"timestamp"`
	Health       string    `json:"health"` // healthy | degraded | unhealthy
	HealthReason []string  `json:"health_reasons,omitempty"`
	Host         Host      `json:"host"`
	Version      Version   `json:"version"`
	DaemonStatus string    `json:"daemon_status,omitempty"`
	Management   Service   `json:"management"`
	Signal       Service   `json:"signal"`
	Relays       Relays    `json:"relays"`
	Counts       Counts    `json:"counts"`
	Peers        []Peer    `json:"peers"`
}

type Host struct {
	FQDN            string   `json:"fqdn"`
	NetbirdIP       string   `json:"netbird_ip"`
	NetbirdCIDR     string   `json:"netbird_cidr"`
	PublicKey       string   `json:"public_key"`
	KernelInterface bool     `json:"kernel_interface"`
	WireguardPort   int      `json:"wireguard_port"`
	LazyConnection  bool     `json:"lazy_connection"`
	Profile         string   `json:"profile,omitempty"`
	Networks        []string `json:"networks"`
}

type Version struct {
	CLI    string `json:"cli"`
	Daemon string `json:"daemon"`
}

type Service struct {
	Connected bool   `json:"connected"`
	URL       string `json:"url"`
	Error     string `json:"error,omitempty"`
}

type Relays struct {
	Total     int     `json:"total"`
	Available int     `json:"available"`
	Details   []Relay `json:"details"`
}

type Relay struct {
	URI       string `json:"uri"`
	Available bool   `json:"available"`
	Transport string `json:"transport,omitempty"`
	Error     string `json:"error,omitempty"`
}

type Counts struct {
	Total          int `json:"total"`
	Connected      int `json:"connected"`
	Disconnected   int `json:"disconnected"`
	P2P            int `json:"p2p"`
	Relayed        int `json:"relayed"`
	HandshakeStale int `json:"handshake_stale"`
}

type Peer struct {
	FQDN                string     `json:"fqdn"`
	Name                string     `json:"name"`
	IP                  string     `json:"ip"`
	PublicKey           string     `json:"public_key"`
	Status              string     `json:"status"`
	Connected           bool       `json:"connected"`
	ConnectionType      string     `json:"connection_type,omitempty"`
	P2P                 bool       `json:"p2p"`
	RelayAddress        string     `json:"relay_address,omitempty"`
	ICE                 ICE        `json:"ice"`
	LatencyMs           float64    `json:"latency_ms"`
	RxBytes             int64      `json:"rx_bytes"`
	TxBytes             int64      `json:"tx_bytes"`
	LastHandshake       *time.Time `json:"last_handshake,omitempty"`
	HandshakeAgeSeconds *float64   `json:"handshake_age_seconds,omitempty"`
	HandshakeStale      bool       `json:"handshake_stale"`
	LastStatusUpdate    *time.Time `json:"last_status_update,omitempty"`
	QuantumResistance   bool       `json:"quantum_resistance"`
	Networks            []string   `json:"networks"`
	NetworksCount       int        `json:"networks_count"`
}

type ICE struct {
	LocalType      string `json:"local_type,omitempty"`
	RemoteType     string `json:"remote_type,omitempty"`
	LocalEndpoint  string `json:"local_endpoint,omitempty"`
	RemoteEndpoint string `json:"remote_endpoint,omitempty"`
}
