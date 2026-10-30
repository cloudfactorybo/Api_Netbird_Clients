#!/bin/bash
# ============================================================
# Instalador de api-netbird-clients (Docker)
# API HTTPS de solo lectura con el estado del cliente NetBird
# local: P2P/relay, latencia, handshake, ping y /metrics.
#
# TLS: el certificado lo genera la propia API con la biblioteca
# estándar de Go, con SAN dinámico (IP de NetBird, FQDN, IPs locales).
#
# Uso:
#   sudo ./install-api-netbird.sh [--version vX.Y.Z] [--dir /opt/api-netbird-clients] [--yes]
#   sudo ./install-api-netbird.sh --binary ./api-netbird-clients   (binario local, sin descargar)
#
# Sin preguntas (--yes) toma los valores por defecto o estas variables:
#   API_PORT=8443  LISTEN_MODE=1..4  LISTEN_IPS="ip1 ip2"  API_TOKEN=...
#   TLS_YEARS=5  PING_ENABLED=true  INCLUDE_LOCAL_IPS=s  EXTRA_IPS="..."  EXTRA_DNS="..."
#   NETBIRD_SOCKET=/var/run/netbird.sock  NETBIRD_HOST_BIN=/usr/bin/netbird
# ============================================================

set -e

main() {

# ── Si se ejecuta con "curl ... | bash", leer las respuestas desde la terminal ──
if [ ! -t 0 ] && (: < /dev/tty) 2>/dev/null; then exec < /dev/tty; fi

stty erase ^H 2>/dev/null || true

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

ok()   { echo -e "   ${GREEN}✅ $1${NC}"; }
warn() { echo -e "   ${YELLOW}⚠️  $1${NC}"; }
fail() { echo -e "   ${RED}❌ $1${NC}"; exit 1; }

REPO="${API_NETBIRD_REPO:-cloudfactorybo/Api_Netbird_Clients}"
BIN_NAME="api-netbird-clients"

# ── Argumentos ──────────────────────────────────────────────
VERSION="${API_NETBIRD_VERSION:-}"
INSTALL_DIR="${API_NETBIRD_DIR:-/opt/api-netbird-clients}"
LOCAL_BINARY=""
YES=0
while [ $# -gt 0 ]; do
    case "$1" in
        --version) VERSION="$2"; shift 2 ;;
        --version=*) VERSION="${1#*=}"; shift ;;
        --dir) INSTALL_DIR="$2"; shift 2 ;;
        --dir=*) INSTALL_DIR="${1#*=}"; shift ;;
        --binary) LOCAL_BINARY="$2"; shift 2 ;;
        --binary=*) LOCAL_BINARY="${1#*=}"; shift ;;
        -y|--yes) YES=1; shift ;;
        -h|--help) sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) echo "Opción desconocida: $1 (usa --help)"; exit 1 ;;
    esac
done

# ask VAR "pregunta" "default": con --yes (o si VAR ya viene en el entorno) no pregunta.
ask() {
    local __var=$1 __q=$2 __def=$3 __ans
    if [ -n "${!__var}" ]; then return; fi
    if [ $YES -eq 1 ]; then printf -v "$__var" '%s' "$__def"; return; fi
    read -r -p "$__q [${__def}]: " __ans
    printf -v "$__var" '%s' "${__ans:-$__def}"
}

es_ip() { [[ $1 =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; }
dedup() { echo "$@" | tr ' ' '\n' | awk 'NF && !seen[$0]++' | tr '\n' ' ' | sed 's/ $//'; }

echo -e "${BLUE}"
echo "============================================================"
echo "   🛰️  API NETBIRD CLIENTS — instalación con Docker"
echo "   🔐 HTTPS (TLS de la biblioteca estándar de Go) + token"
echo "============================================================"
echo -e "${NC}"

# ============================================================
# ── VERIFICACIÓN DEL SISTEMA ────────────────────────────────
# ============================================================
echo -e "${YELLOW}🔎 VERIFICANDO EL SISTEMA${NC}"

[ "$(uname -s)" = "Linux" ] || fail "Sistema no soportado: $(uname -s). Solo Linux."
case "$(uname -m)" in
    x86_64|amd64)  ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) fail "Arquitectura no soportada: $(uname -m). Solo amd64 y arm64." ;;
esac
ok "Plataforma: linux/${ARCH}"

[ "$(id -u)" -eq 0 ] || warn "No se ejecuta como root: puede faltar permiso para ${INSTALL_DIR} o el socket de NetBird (usa sudo)."

command -v docker >/dev/null 2>&1 || fail "Docker no está instalado. Instálalo: https://docs.docker.com/engine/install/"
docker info >/dev/null 2>&1 || fail "No se puede hablar con Docker (¿daemon apagado o falta sudo?)."
docker compose version >/dev/null 2>&1 || fail "Falta el plugin 'docker compose' (v2)."
ok "Docker $(docker version --format '{{.Server.Version}}' 2>/dev/null) + compose $(docker compose version --short 2>/dev/null)"

if command -v curl >/dev/null 2>&1; then
    HTTP_GET="curl -fsSL --max-time 30"; HTTP_DL() { curl -fsSL --max-time 120 "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
    HTTP_GET="wget -qO- --timeout=30"; HTTP_DL() { wget -q --timeout=120 "$1" -O "$2"; }
else
    HTTP_GET=""; [ -n "$LOCAL_BINARY" ] || fail "Se necesita curl o wget para descargar la API."
fi
command -v sha256sum >/dev/null 2>&1 || fail "Falta sha256sum (coreutils)."

# ── Cliente NetBird del host ────────────────────────────────
echo ""
echo -e "${YELLOW}🛰️  CLIENTE NETBIRD${NC}"
NB_BIN="${NETBIRD_HOST_BIN:-$(command -v netbird 2>/dev/null || true)}"
[ -n "$NB_BIN" ] || fail "No se encontró el comando 'netbird'. Instala el cliente: https://docs.netbird.io/get-started/install/linux"
NB_BIN=$(readlink -f "$NB_BIN")
ok "Binario: ${NB_BIN}"
# El contenedor es 'scratch': el binario del host se monta dentro y debe ser estático.
if command -v ldd >/dev/null 2>&1 && ldd "$NB_BIN" 2>&1 | grep -q '=>'; then
    fail "El binario de NetBird es dinámico; esta instalación necesita el binario estático oficial."
fi

NB_SOCKET="${NETBIRD_SOCKET:-/var/run/netbird.sock}"
NB_SOCKET=$(readlink -f "$NB_SOCKET" 2>/dev/null || echo "$NB_SOCKET")
[ -S "$NB_SOCKET" ] || fail "No existe el socket ${NB_SOCKET}. ¿Está corriendo el daemon? (systemctl status netbird)"
ok "Socket del daemon: ${NB_SOCKET}"

NB_JSON=$(timeout 15 "$NB_BIN" status --json --daemon-addr "unix://${NB_SOCKET}" 2>/dev/null || true)
[ -n "$NB_JSON" ] || fail "'netbird status --json' no respondió. Revisa: netbird status"
if command -v jq >/dev/null 2>&1; then
    NB_FQDN=$(echo "$NB_JSON" | jq -r '.fqdn // empty')
    NB_IP=$(echo "$NB_JSON" | jq -r '.netbirdIp // empty' | cut -d/ -f1)
    NB_VER=$(echo "$NB_JSON" | jq -r '.daemonVersion // empty')
    NB_MGMT=$(echo "$NB_JSON" | jq -r '.management.connected')
else
    # Sin jq: los campos del host vienen después de la lista de peers.
    NB_FQDN=$(echo "$NB_JSON" | grep -oE '"fqdn": *"[^"]*"' | tail -1 | sed 's/.*"\([^"]*\)"$/\1/')
    NB_IP=$(echo "$NB_JSON" | grep -oE '"netbirdIp": *"[0-9.]+/[0-9]+"' | head -1 | sed 's/.*"\([0-9.]*\)\/.*/\1/')
    NB_VER=$(echo "$NB_JSON" | grep -oE '"daemonVersion": *"[^"]*"' | head -1 | sed 's/.*"\([^"]*\)"$/\1/')
    NB_MGMT=$(echo "$NB_JSON" | tr -d '\n ' | grep -oE '"management":\{[^}]*\}' | grep -q '"connected":true' && echo true || echo false)
fi
ok "Peer: ${NB_FQDN:-?}  IP NetBird: ${NB_IP:-?}  versión: ${NB_VER:-?}"
[ "$NB_MGMT" = "true" ] && ok "Conectado al management" || warn "El cliente NO está conectado al management (la API igual se instala)."

# ============================================================
# ── VERSIÓN DE LA API ───────────────────────────────────────
# ============================================================
echo ""
echo -e "${YELLOW}📦 VERSIÓN DE LA API${NC}"
if [ -n "$LOCAL_BINARY" ]; then
    [ -x "$LOCAL_BINARY" ] || fail "No existe o no es ejecutable: ${LOCAL_BINARY}"
    VERSION=$("$LOCAL_BINARY" version 2>/dev/null || echo local)
    ok "Binario local: ${LOCAL_BINARY} (${VERSION})"
else
    if [ -z "$VERSION" ]; then
        VERSION=$($HTTP_GET "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
            | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/' || true)
        [ -n "$VERSION" ] || fail "No se encontró ninguna release en ${REPO}. Indica --version vX.Y.Z o --binary."
    fi
    ok "Versión: ${VERSION}"
fi
[[ $VERSION =~ ^[A-Za-z0-9._-]+$ ]] || fail "Versión inválida: ${VERSION}"

# ============================================================
# ── INSTALACIÓN EXISTENTE ───────────────────────────────────
# ============================================================
OLD_TOKEN=""
if [ -f "${INSTALL_DIR}/.env" ]; then
    echo ""
    echo -e "${YELLOW}📦 INSTALACIÓN EXISTENTE en ${INSTALL_DIR}${NC}"
    BACKUP_DIR="${INSTALL_DIR}/backup-$(date +%Y%m%d-%H%M%S)"
    mkdir -p "$BACKUP_DIR"
    for f in .env docker-compose.yml Dockerfile data/tls; do
        [ -e "${INSTALL_DIR}/$f" ] && cp -a "${INSTALL_DIR}/$f" "$BACKUP_DIR/"
    done
    chmod 700 "$BACKUP_DIR"
    ok "Respaldo creado en ${BACKUP_DIR}"
    OLD_TOKEN=$(grep -E '^API_TOKEN=' "${INSTALL_DIR}/.env" | cut -d= -f2-)
    OLD_PORT=$(grep -E '^API_PORT=' "${INSTALL_DIR}/.env" | cut -d= -f2-)
    echo "   El certificado en data/tls se conserva (misma huella) salvo que cambien los SAN."
fi

# ── API anterior con systemd (netbird-monitor, HTTP :8080) ──
if command -v systemctl >/dev/null 2>&1 && systemctl list-unit-files 2>/dev/null | grep -q '^netbird-monitor\.service'; then
    echo ""
    warn "Está instalada la API anterior (servicio netbird-monitor, HTTP :8080)."
    ask STOP_OLD "   ¿Detenerla y deshabilitarla? Sus archivos no se borran (S/n)" "S"
    if [[ $STOP_OLD =~ ^[Ss] ]]; then
        systemctl disable --now netbird-monitor.service >/dev/null 2>&1 && ok "netbird-monitor detenido y deshabilitado" \
            || warn "No se pudo detener netbird-monitor (¿falta sudo?)"
    fi
fi

# ============================================================
# ── CONFIGURACIÓN ───────────────────────────────────────────
# ============================================================
echo ""
echo -e "${YELLOW}🌐 RED${NC}"
puerto_ocupado() { command -v ss >/dev/null 2>&1 && ss -ltnH 2>/dev/null | awk '{print $4}' | grep -qE "[:.]$1\$"; }
ask API_PORT "Puerto HTTPS de la API" "${OLD_PORT:-8443}"
until [[ $API_PORT =~ ^[0-9]+$ ]] && [ "$API_PORT" -ge 1 ] && [ "$API_PORT" -le 65535 ]; do
    echo -e "${RED}❌ Puerto inválido.${NC}"; read -r -p "Puerto: " API_PORT
done
if puerto_ocupado "$API_PORT" && ! docker ps --format '{{.Names}}' | grep -qx "$BIN_NAME"; then
    warn "El puerto ${API_PORT} ya está en uso en este host."
fi

IGNORAR_IFACE="docker|br-|veth|virbr|cni|flannel|kube|tun|tap|zt|wt|lo"
IPS_LOCALES=$(ip -4 -o addr show scope global 2>/dev/null | awk '{print $2" "$4}' \
    | grep -Ev "^(${IGNORAR_IFACE})" | awk '{print $2}' | cut -d/ -f1 | sort -u | tr '\n' ' ')
NB_IP_EN_HOST=0
if [ -n "$NB_IP" ] && ip -4 -o addr show 2>/dev/null | grep -q " ${NB_IP}/"; then NB_IP_EN_HOST=1; fi

echo "   ¿En qué direcciones escucha la API?"
echo "     1) IP de NetBird (${NB_IP:-?}) + 127.0.0.1   ← recomendado: solo por el túnel y local"
echo "     2) Todas las interfaces (0.0.0.0)"
echo "     3) Solo 127.0.0.1 (el Elastic Agent corre en este mismo host)"
echo "     4) Escribir las IPs"
DEF_MODE=1; [ $NB_IP_EN_HOST -eq 1 ] || DEF_MODE=3
ask LISTEN_MODE "Opción" "$DEF_MODE"
case "$LISTEN_MODE" in
    1) [ $NB_IP_EN_HOST -eq 1 ] || warn "La IP ${NB_IP} no está en este host ahora; la API reintentará al arrancar."
       LISTEN_IPS="127.0.0.1 ${NB_IP}" ;;
    2) LISTEN_IPS="0.0.0.0" ;;
    3) LISTEN_IPS="127.0.0.1" ;;
    4) ask LISTEN_IPS "IPs separadas por espacio" "127.0.0.1 ${NB_IP}" ;;
    *) fail "Opción inválida: ${LISTEN_MODE}" ;;
esac
LISTEN_IPS=$(dedup $LISTEN_IPS)
for ip in $LISTEN_IPS; do es_ip "$ip" || fail "IP inválida para escuchar: ${ip}"; done

echo ""
echo -e "${YELLOW}🔐 CERTIFICADO TLS (lo genera la API al arrancar)${NC}"
[ -n "$IPS_LOCALES" ] && echo "   IPs físicas detectadas: ${IPS_LOCALES}"
ask INCLUDE_LOCAL_IPS "¿Incluir las IPs físicas en el certificado? (S/n)" "S"
ask EXTRA_IPS "IPs adicionales (NAT, VPN — Enter para omitir)" ""
ask EXTRA_DNS "Nombres DNS adicionales (Enter para omitir)" ""
ask TLS_YEARS "Años de vigencia del certificado" "5"
until [[ $TLS_YEARS =~ ^[0-9]+$ ]] && [ "$TLS_YEARS" -ge 1 ] && [ "$TLS_YEARS" -le 20 ]; do
    echo -e "${RED}❌ Entre 1 y 20 años.${NC}"; read -r -p "Años: " TLS_YEARS
done

armar_sans() {
    SAN_LIST="localhost 127.0.0.1 $(hostname -s 2>/dev/null) $(hostname -f 2>/dev/null) ${NB_FQDN} ${NB_IP}"
    [[ $INCLUDE_LOCAL_IPS =~ ^[Ss] ]] && SAN_LIST="$SAN_LIST $IPS_LOCALES"
    for ip in $LISTEN_IPS $EXTRA_IPS; do
        if es_ip "$ip"; then [ "$ip" = "0.0.0.0" ] || SAN_LIST="$SAN_LIST $ip"
        else echo -e "   ${RED}⚠️  IP ignorada por formato inválido: $ip${NC}"; fi
    done
    SAN_LIST=$(dedup $SAN_LIST $EXTRA_DNS)
}
armar_sans

echo ""
echo -e "${YELLOW}🔑 TOKEN DE ACCESO${NC}"
generar_token() {
    if command -v openssl >/dev/null 2>&1; then openssl rand -hex 32
    else head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'; fi
}
if [ -z "$API_TOKEN" ] && [ -n "$OLD_TOKEN" ]; then
    ask REUSE_TOKEN "¿Conservar el token actual? Los consumidores no cambian nada (S/n)" "S"
    [[ $REUSE_TOKEN =~ ^[Ss] ]] && API_TOKEN="$OLD_TOKEN"
fi
[ -n "$API_TOKEN" ] || API_TOKEN=$(generar_token)
[ ${#API_TOKEN} -ge 16 ] || fail "El token debe tener al menos 16 caracteres."
ok "Token listo (se muestra al final)"

echo ""
echo -e "${YELLOW}📡 PING ICMP A LOS PEERS${NC}"
echo "   Mide RTT, jitter y pérdida de los peers conectados (requiere que la política permita ICMP)."
ask PING_ANS "¿Activar el ping? (S/n)" "S"
if [[ ${PING_ENABLED:-$PING_ANS} =~ ^([Ss]|true)$ ]]; then PING_ENABLED=true; else PING_ENABLED=false; fi

# ── Resumen ─────────────────────────────────────────────────
while [ $YES -eq 0 ]; do
    echo ""
    echo -e "${BLUE}============================================================${NC}"
    echo -e "${GREEN}📝 RESUMEN DE CONFIGURACIÓN:${NC}"
    echo -e "${BLUE}============================================================${NC}"
    echo -e "   1) Directorio:        ${YELLOW}${INSTALL_DIR}${NC}"
    echo -e "   2) Puerto:            ${YELLOW}${API_PORT}${NC}"
    echo -e "   3) Escucha en:        ${YELLOW}${LISTEN_IPS}${NC}"
    echo -e "   4) SAN del cert:      ${YELLOW}${SAN_LIST}${NC}"
    echo -e "   5) Vigencia cert:     ${YELLOW}${TLS_YEARS} años${NC}"
    echo -e "   6) Ping ICMP:         ${YELLOW}${PING_ENABLED}${NC}"
    echo -e "   7) Token:             ${YELLOW}${API_TOKEN:0:6}…(${#API_TOKEN} caracteres)${NC}"
    echo -e "      Versión API:       ${YELLOW}${VERSION}${NC}   NetBird: ${YELLOW}${NB_BIN}${NC}"
    echo -e "${BLUE}============================================================${NC}"
    read -r -p "¿Corregir algún dato? (1-7, c=continuar, q=salir): " OPCION
    case $OPCION in
        1) read -r -p "Directorio: " INSTALL_DIR ;;
        2) read -r -p "Puerto: " API_PORT
           until [[ $API_PORT =~ ^[0-9]+$ ]] && [ "$API_PORT" -ge 1 ] && [ "$API_PORT" -le 65535 ]; do read -r -p "Puerto: " API_PORT; done ;;
        3) read -r -p "IPs donde escuchar (separadas por espacio): " LISTEN_IPS
           LISTEN_IPS=$(dedup $LISTEN_IPS); armar_sans ;;
        4) read -r -p "IPs adicionales: " EXTRA_IPS; read -r -p "Nombres DNS adicionales: " EXTRA_DNS; armar_sans ;;
        5) read -r -p "Años: " TLS_YEARS; [[ $TLS_YEARS =~ ^[0-9]+$ ]] && [ "$TLS_YEARS" -ge 1 ] || TLS_YEARS=5 ;;
        6) [ "$PING_ENABLED" = true ] && PING_ENABLED=false || PING_ENABLED=true ;;
        7) read -r -p "Token (Enter = generar uno nuevo): " API_TOKEN; [ -n "$API_TOKEN" ] || API_TOKEN=$(generar_token)
           [ ${#API_TOKEN} -ge 16 ] || { echo -e "${RED}❌ Mínimo 16 caracteres; se genera uno.${NC}"; API_TOKEN=$(generar_token); } ;;
        [Cc]) echo -e "${GREEN}✅ Continuando...${NC}"; break ;;
        [Qq]) echo -e "${RED}❌ Cancelado.${NC}"; exit 1 ;;
        *) echo -e "${RED}❌ Opción inválida.${NC}" ;;
    esac
done

LISTEN_ADDRS=$(for ip in $LISTEN_IPS; do printf '%s:%s,' "$ip" "$API_PORT"; done | sed 's/,$//')
PRIMER_IP=$(echo "$LISTEN_IPS" | awk '{print $1}'); [ "$PRIMER_IP" = "0.0.0.0" ] && PRIMER_IP=127.0.0.1

# ============================================================
# ── BINARIO ─────────────────────────────────────────────────
# ============================================================
echo ""
echo -e "${YELLOW}⬇️  BINARIO DE LA API${NC}"
mkdir -p "${INSTALL_DIR}/bin" "${INSTALL_DIR}/data"
chmod 700 "${INSTALL_DIR}/data" 2>/dev/null || true
# El contenedor corre como root sin CAP_DAC_OVERRIDE: data/ tiene que ser de root.
[ "$(id -u)" -eq 0 ] && chown -R 0:0 "${INSTALL_DIR}/data"
DEST="${INSTALL_DIR}/bin/${BIN_NAME}"
if [ -n "$LOCAL_BINARY" ]; then
    [ "$(readlink -f "$LOCAL_BINARY")" = "$(readlink -f "$DEST")" ] || cp "$LOCAL_BINARY" "$DEST"
else
    ASSET="${BIN_NAME}_linux_${ARCH}"
    URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
    TMP=$(mktemp); trap 'rm -f "$TMP" "${TMP}.sha256"' EXIT
    HTTP_DL "$URL" "$TMP" || fail "No se pudo descargar ${URL}"
    HTTP_DL "${URL}.sha256" "${TMP}.sha256" || fail "Falta ${ASSET}.sha256 en la release: no se instala sin verificar."
    [ "$(awk '{print $1}' "${TMP}.sha256")" = "$(sha256sum "$TMP" | awk '{print $1}')" ] \
        || fail "El checksum no coincide: el binario pudo ser alterado. Se cancela."
    ok "Checksum SHA-256 verificado"
    mv "$TMP" "$DEST"
fi
chmod 755 "$DEST"
ok "Binario: ${DEST} ($("$DEST" version 2>/dev/null))"

# ============================================================
# ── ARCHIVOS ────────────────────────────────────────────────
# ============================================================
cd "$INSTALL_DIR"

# Imagen mínima: solo el binario estático de la API (sin shell ni paquetes).
cat > Dockerfile <<EOF
FROM scratch
LABEL org.opencontainers.image.title="${BIN_NAME}" \\
      org.opencontainers.image.version="${VERSION}" \\
      org.opencontainers.image.source="https://github.com/${REPO}"
COPY bin/${BIN_NAME} /${BIN_NAME}
ENV HOME=/tmp
HEALTHCHECK --interval=30s --timeout=20s --start-period=20s --retries=3 CMD ["/${BIN_NAME}", "healthcheck"]
ENTRYPOINT ["/${BIN_NAME}"]
CMD ["serve"]
EOF

CAP_EXTRA=""
[ "$API_PORT" -lt 1024 ] && CAP_EXTRA="      - NET_BIND_SERVICE"

cat > docker-compose.yml <<EOF
# Generado por install-api-netbird.sh — ${VERSION}
name: ${BIN_NAME}

services:
  api:
    build: .
    image: ${BIN_NAME}:${VERSION}
    container_name: ${BIN_NAME}
    restart: unless-stopped
    # Red del host: la API llega a los peers por la interfaz de NetBird (wt0).
    network_mode: host
    env_file: .env
    read_only: true
    tmpfs:
      - /tmp
    cap_drop:
      - ALL
    cap_add:
      - NET_RAW            # ping ICMP
${CAP_EXTRA}
    security_opt:
      - no-new-privileges:true
    volumes:
      - type: bind
        source: ./data
        target: /data
      # Cliente NetBird del host (binario estático), de solo lectura
      - type: bind
        source: \${NB_BIN}
        target: /usr/local/bin/netbird
        read_only: true
        bind:
          create_host_path: false
      # Socket del daemon. create_host_path=false: si no existe, Docker no crea
      # un directorio en su lugar (rompería el daemon de NetBird).
      - type: bind
        source: \${NB_SOCKET}
        target: /var/run/netbird.sock
        bind:
          create_host_path: false
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
EOF
sed -i '/^$/N;/^\n$/D' docker-compose.yml

umask 077
cat > .env <<EOF
# api-netbird-clients — generado $(date '+%Y-%m-%d %H:%M')
# Contiene el token: no compartir ni subir a git.
VERSION=${VERSION}
API_PORT=${API_PORT}
NB_BIN=${NB_BIN}
NB_SOCKET=${NB_SOCKET}

LISTEN_ADDRS=${LISTEN_ADDRS}
API_TOKEN=${API_TOKEN}
TLS_CERT=/data/tls/server.crt
TLS_KEY=/data/tls/server.key
TLS_SANS=$(echo "$SAN_LIST" | tr ' ' ',')
TLS_VALID_DAYS=$((TLS_YEARS * 365))
NETBIRD_BIN=/usr/local/bin/netbird
NETBIRD_DAEMON_ADDR=unix:///var/run/netbird.sock
CACHE_TTL=10s
HANDSHAKE_STALE=3m
EXIT_AFTER_FAILURES=5
PING_ENABLED=${PING_ENABLED}
PING_COUNT=3
PING_TTL=30s
EOF
chmod 600 .env
umask 022
ok "Archivos generados en ${INSTALL_DIR}"

# ── comandos.sh ─────────────────────────────────────────────
cat > comandos.sh <<'EOF'
#!/bin/bash
# Atajos para operar api-netbird-clients. Uso: ./comandos.sh <acción>
cd "$(dirname "$0")"
TOKEN=$(grep -E '^API_TOKEN=' .env | cut -d= -f2-)
PORT=$(grep -E '^API_PORT=' .env | cut -d= -f2-)
IP=$(grep -E '^LISTEN_ADDRS=' .env | cut -d= -f2- | cut -d, -f1 | cut -d: -f1)
[ "$IP" = "0.0.0.0" ] && IP=127.0.0.1
URL="https://${IP}:${PORT}"
case "$1" in
    estado)     docker compose ps; curl -sk -H "X-API-Key: $TOKEN" "$URL/api/v1/health"; echo ;;
    logs)       docker compose logs -f --tail 100 ;;
    status)     curl -sk -H "X-API-Key: $TOKEN" "$URL/api/v1/status" ;;
    peers)      curl -sk -H "X-API-Key: $TOKEN" "$URL/api/v1/peers?status=connected" ;;
    ping)       curl -sk -H "X-API-Key: $TOKEN" "$URL/api/v1/ping" ;;
    metrics)    curl -sk -H "X-API-Key: $TOKEN" "$URL/metrics" ;;
    token)      echo "$TOKEN" ;;
    cert)       docker compose exec -T api /api-netbird-clients cert-info ;;
    cert-pem)   docker compose exec -T api /api-netbird-clients cert-info --pem ;;
    renovar-cert)
        rm -f data/tls/server.crt data/tls/server.key && docker compose restart \
            && echo "Certificado nuevo: actualiza la huella en los consumidores (./comandos.sh cert)" ;;
    reiniciar)  docker compose restart ;;
    desinstalar)
        read -r -p "¿Borrar contenedor e imagen? Los archivos quedan en $(pwd) (s/N): " R
        [[ $R =~ ^[Ss] ]] && docker compose down --rmi local ;;
    *) echo "Uso: $0 {estado|logs|status|peers|ping|metrics|token|cert|cert-pem|renovar-cert|reiniciar|desinstalar}" ;;
esac
EOF
chmod 750 comandos.sh

# ============================================================
# ── ARRANQUE Y PRUEBA ───────────────────────────────────────
# ============================================================
echo ""
echo -e "${YELLOW}🚀 CONSTRUYENDO Y ARRANCANDO${NC}"
docker compose build -q >/dev/null || fail "Falló la construcción de la imagen"
docker compose up -d --force-recreate >/dev/null || fail "No se pudo arrancar el contenedor (docker compose logs)"
ok "Contenedor ${BIN_NAME} arrancado"

ESTADO=""
for _ in $(seq 1 30); do
    ESTADO=$(docker inspect -f '{{.State.Health.Status}}' "$BIN_NAME" 2>/dev/null || true)
    [ "$ESTADO" = "healthy" ] && break
    [ "$(docker inspect -f '{{.State.Running}}' "$BIN_NAME" 2>/dev/null)" = "true" ] || break
    sleep 2
done
if [ "$ESTADO" = "healthy" ]; then
    ok "Healthcheck: healthy"
else
    warn "El contenedor no quedó healthy (estado: ${ESTADO:-detenido}). Últimos logs:"
    docker compose logs --tail 20 2>&1 | sed 's/^/      /'
fi

PRUEBA=$(curl -sk --max-time 20 -H "X-API-Key: ${API_TOKEN}" "https://${PRIMER_IP}:${API_PORT}/api/v1/health" 2>/dev/null || true)
if echo "$PRUEBA" | grep -q '"health"'; then
    ok "Prueba HTTPS: $(echo "$PRUEBA" | grep -oE '"health": *"[a-z]+"' | head -1)"
else
    warn "La prueba HTTPS a https://${PRIMER_IP}:${API_PORT}/api/v1/health no respondió."
fi

CERT_JSON=$(docker compose exec -T api "/${BIN_NAME}" cert-info 2>/dev/null || true)
FP=$(echo "$CERT_JSON" | grep -oE '"sha256": *"[0-9a-f]{64}"' | grep -oE '[0-9a-f]{64}' || true)
VENCE=$(echo "$CERT_JSON" | grep -oE '"not_after": *"[^"]*"' | sed 's/.*"\([^"]*\)"$/\1/' || true)

# ── Resumen final ───────────────────────────────────────────
echo ""
echo -e "${BLUE}============================================================${NC}"
echo -e "${GREEN}✅ api-netbird-clients ${VERSION} instalada en ${INSTALL_DIR}${NC}"
echo -e "${BLUE}============================================================${NC}"
echo -e "${YELLOW}🔗 URL:${NC}    https://${PRIMER_IP}:${API_PORT}   (escucha en: ${LISTEN_IPS})"
echo -e "${YELLOW}🔑 Token:${NC}  ${GREEN}${API_TOKEN}${NC}"
echo     "          (guardado en ${INSTALL_DIR}/.env; ./comandos.sh token lo vuelve a mostrar)"
echo -e "${YELLOW}🔐 Certificado:${NC} vence ${VENCE:-?}"
echo -e "   SHA-256: ${GREEN}${FP:-?}${NC}"
echo ""
echo -e "${YELLOW}📊 Elastic Agent — integración Prometheus:${NC}"
echo "   hosts: https://${PRIMER_IP}:${API_PORT}    metrics_path: /metrics"
echo "   headers:  X-API-Key: <token>"
echo "   ssl.ca_trusted_fingerprint: ${FP:-<huella>}"
echo ""
echo -e "${YELLOW}🧪 Probar:${NC}"
echo "   curl -sk -H 'X-API-Key: <token>' https://${PRIMER_IP}:${API_PORT}/api/v1/status"
echo "   ${INSTALL_DIR}/comandos.sh {estado|peers|ping|metrics|cert|logs}"
echo ""

}

main "$@"
