# api-netbird-clients

API HTTPS de solo lectura que expone el estado del **cliente NetBird local**: por cada peer, si la conexión es **P2P o va por relay**, la latencia, el último handshake de WireGuard, los bytes y un **ping ICMP** (RTT, jitter y pérdida). También publica **`/metrics`** en formato Prometheus para Elastic Agent.

Se instala con Docker en cada equipo que tenga NetBird (los colectores de la oficina, routers de clientes), con un único script interactivo.

- **TLS nativo de Go** (`crypto/tls` y `crypto/x509`). La API genera su propio certificado con SAN dinámico: IP de NetBird, FQDN e IPs locales. No hace falta openssl.
- **Token** obligatorio (`Authorization: Bearer` o `X-API-Key`).
- **Imagen `scratch` de unos 7 MB**: solo el binario estático. Sin shell, sistema de archivos de solo lectura y únicamente la capability `NET_RAW` para el ping.
- **Sin colector ni base de datos**: lee `netbird status --json` del daemon local, lo cachea 10 s y responde.

## Instalación

En el equipo con NetBird (Linux amd64 o arm64, con Docker y compose v2):

```bash
curl -fsSL https://raw.githubusercontent.com/cloudfactorybo/Api_Netbird_Clients/main/get-api-netbird.sh | bash -s -- --run
```

El script baja el instalador de la última release, verifica su SHA-256 y lo ejecuta. El instalador:

1. **Verifica el entorno**: Docker, el binario `netbird` (debe ser estático) y el socket del daemon (`/var/run/netbird.sock`). También lee la IP y el FQDN de NetBird.
2. **Pregunta, con valores detectados por defecto**:
   - el puerto (8443);
   - dónde escuchar (IP de NetBird + 127.0.0.1 por defecto);
   - los SAN del certificado;
   - la vigencia del certificado;
   - si activar el ping.
3. **Muestra un resumen editable**, igual que en `kafka-installer`.
4. **Descarga el binario de la release** y verifica su SHA-256. Luego genera `Dockerfile`, `docker-compose.yml`, `.env` (600) y `comandos.sh`.
5. **Construye la imagen, la arranca y la prueba**: espera al healthcheck y hace una consulta HTTPS con el token. Al final muestra la URL, el token y la **huella SHA-256 del certificado**.

**Otras formas de ejecutarlo:**

```bash
sudo ./install-api-netbird.sh --version v1.0.0        # versión concreta
sudo ./install-api-netbird.sh --dir /opt/otra-ruta    # otro directorio (por defecto /opt/api-netbird-clients)
sudo API_PORT=9443 LISTEN_MODE=3 ./install-api-netbird.sh --yes   # sin preguntas
```

**Variables para `--yes`:** `API_PORT`, `LISTEN_MODE` (1-4), `LISTEN_IPS`, `API_TOKEN`, `TLS_YEARS`, `PING_ENABLED`, `INCLUDE_LOCAL_IPS`, `EXTRA_IPS`, `EXTRA_DNS`, `NETBIRD_SOCKET`, `NETBIRD_HOST_BIN`.

**Reinstalar o actualizar:** vuelve a ejecutar el instalador. Respalda la instalación anterior en `backup-*`, ofrece conservar el token y mantiene el certificado (la misma huella) si los SAN no cambian.

**API anterior:** si encuentra el servicio `netbird-monitor` (HTTP :8080), ofrece detenerlo sin borrar sus archivos.

## Endpoints

Todos piden token, excepto `/healthz`.

| Endpoint | Qué devuelve |
|---|---|
| `GET /healthz` | `up` o `down`. Lo usa el healthcheck de Docker |
| `GET /api/v1/health` | `healthy`, `degraded` o `unhealthy`, con los motivos, el estado de management y signal, los relays y los conteos |
| `GET /api/v1/status` | Todo: host, versiones, management, signal, relays, conteos y la lista de peers |
| `GET /api/v1/status/raw` | La salida original de `netbird status --json` |
| `GET /api/v1/peers?status=connected&type=p2p\|relayed` | Peers filtrados, dentro de `{"peers": [...]}` |
| `GET /api/v1/peers/{ip\|fqdn\|nombre}` | Un peer con su ping |
| `GET /api/v1/ping` | RTT mínimo, promedio y máximo, jitter y pérdida de cada peer conectado (cacheado 30 s) |
| `GET /metrics` | Formato Prometheus |

Cada peer trae `connected`, `connection_type`, `p2p`, `relay_address`, `ice.*`, `latency_ms`, `rx_bytes`, `tx_bytes`, `last_handshake` (ISO 8601), `handshake_age_seconds` y `handshake_stale`.

**Qué significa `handshake_stale`:** WireGuard renueva el handshake cada 2 minutos mientras hay tráfico. Si el último tiene más de `HANDSHAKE_STALE` (3 min), el túnel dejó de responder aunque `netbird status` siga diciendo "Connected".

### Métricas principales

```
netbird_up, netbird_health (2/1/0), netbird_management_connected, netbird_signal_connected
netbird_relay_available{uri,transport}
netbird_peers{state="known|connected|disconnected|p2p|relayed|handshake_stale"}
netbird_peer_info{peer,fqdn,ip,status,connection_type,relay,ice_local,ice_remote} 1
netbird_peer_connected, netbird_peer_p2p, netbird_peer_latency_seconds
netbird_peer_receive_bytes_total, netbird_peer_transmit_bytes_total
netbird_peer_last_handshake_timestamp_seconds, netbird_peer_handshake_stale
netbird_peer_ping_up, netbird_peer_ping_rtt_seconds{stat}, netbird_peer_ping_jitter_seconds, netbird_peer_ping_loss_ratio
```

`connection_type`, `relay` e ICE van **solo** en `netbird_peer_info`. Así, cuando un peer cambia entre P2P y relay, no se crean series nuevas en las demás métricas.

## Elastic Agent

**Integración Prometheus**, en la política del agente del mismo equipo:

- **Hosts:** `https://127.0.0.1:8443`, con **Metrics path** `/metrics` y un **Period** de `30s`.
- **Headers:** `X-API-Key: <token>`.
- **SSL:** `ca_trusted_fingerprint: <huella SHA-256 que muestra el instalador>`. Con esto el agente valida el certificado autofirmado sin `verification_mode: none`.

**Custom API (httpjson)**, si se prefiere JSON: usar `request_url` `https://127.0.0.1:8443/api/v1/peers` y `response_split` con `target: body.peers`.

## Operación

```bash
cd /opt/api-netbird-clients
./comandos.sh estado        # contenedor + /api/v1/health
./comandos.sh peers         # peers conectados
./comandos.sh ping          # RTT y pérdida
./comandos.sh metrics       # /metrics
./comandos.sh cert          # SAN, vencimiento y huella SHA-256
./comandos.sh token         # muestra el token
./comandos.sh renovar-cert  # certificado nuevo (cambia la huella)
./comandos.sh logs | reiniciar | desinstalar
```

## Configuración (`.env`)

| Variable | Por defecto | |
|---|---|---|
| `LISTEN_ADDRS` | `:8443` | Una o varias, separadas por coma |
| `API_TOKEN` | — | Obligatoria, de 16 caracteres o más |
| `TLS_CERT` / `TLS_KEY` | `/data/tls/server.{crt,key}` | Se generan si no existen |
| `TLS_SANS` | `localhost,127.0.0.1` | IPs y nombres del certificado autogenerado |
| `TLS_VALID_DAYS` | `1825` | Vigencia del certificado |
| `TLS_AUTOGEN` | `true` | `false`: usar un certificado propio y no generar nunca |
| `CACHE_TTL` | `10s` | Caché de `netbird status` |
| `HANDSHAKE_STALE` | `3m` | Antigüedad para considerar vencido un handshake |
| `PING_ENABLED` / `PING_COUNT` / `PING_TTL` | `true` / `3` / `30s` | Ping ICMP |
| `EXIT_AFTER_FAILURES` | `5` | Con N fallos seguidos del daemon, el proceso sale y Docker lo reinicia |

`EXIT_AFTER_FAILURES` existe porque, cuando el daemon de NetBird se reinicia, crea un socket nuevo. El reinicio del contenedor vuelve a montarlo.

## Desarrollo

```bash
go test -race ./...
docker build -t api-netbird-clients:dev .
```

**Publicar una versión:** `git tag v1.2.0 && git push origin v1.2.0`. El workflow de GitHub Actions:

1. prueba el código;
2. compila `linux_amd64` y `linux_arm64` (`CGO_ENABLED=0`);
3. publica la release con los binarios, `install-api-netbird.sh`, `get-api-netbird.sh` y un `.sha256` de cada uno.

## Licencia

MIT. CloudFactory Bolivia y AltairSec.
