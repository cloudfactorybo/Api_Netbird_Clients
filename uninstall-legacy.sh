#!/bin/bash
# ============================================================
# uninstall-legacy.sh — Busca y quita las APIs NetBird anteriores
# sin importar cómo se instalaron: systemd, Docker, proceso suelto o cron.
#   - netbird-monitor (:8080, estado del cliente): la reemplaza api-netbird-clients.
#   - netbird-agent   (:9092, inventario por la API del management): NO la
#     reemplaza; solo se quita con --incluir-inventario.
#
# Uso:
#   sudo ./uninstall-legacy.sh                    # solo revisa y muestra (no cambia nada)
#   sudo ./uninstall-legacy.sh --borrar           # respalda y quita netbird-monitor
#   sudo ./uninstall-legacy.sh --borrar --incluir-inventario   # también netbird-agent
#
# NO toca el cliente NetBird (netbird, /etc/netbird, /var/lib/netbird) ni la API nueva.
# ============================================================

set -u

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'
ok()   { echo -e "   ${GREEN}✅ $1${NC}"; }
warn() { echo -e "   ${YELLOW}⚠️  $1${NC}"; }
item() { echo -e "   ${YELLOW}•${NC} $1"; }

BORRAR=0
INVENTARIO=0
for a in "$@"; do
    case "$a" in
        --borrar) BORRAR=1 ;;
        --incluir-inventario) INVENTARIO=1 ;;
        -h|--help) sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) echo "Opción desconocida: $a (usa --help)"; exit 1 ;;
    esac
done
[ "$(id -u)" -eq 0 ] || { echo -e "${RED}Ejecutar con sudo.${NC}"; exit 1; }

# Nombres de las APIs viejas (nunca coinciden con el cliente NetBird ni con api-netbird-clients)
PATRON='netbird-monitor|netbird-agent'
PUERTOS='8080|9092'
# Lo que se quita con --borrar
QUITAR='netbird-monitor'
[ $INVENTARIO -eq 1 ] && QUITAR='netbird-monitor|netbird-agent'
se_quita() { [[ $1 =~ ($QUITAR) ]]; }

echo -e "${BLUE}============================================================"
echo "   🔎 API NetBird anterior en $(hostname)"
echo -e "============================================================${NC}"

# ── systemd ─────────────────────────────────────────────────
UNITS=""
if command -v systemctl >/dev/null 2>&1; then
    for u in $(systemctl list-unit-files --type=service --no-legend 2>/dev/null | awk '{print $1}'); do
        exec_start=$(systemctl show -p ExecStart --value "$u" 2>/dev/null)
        if [[ $u =~ ^($PATRON)\.service$ ]] || [[ $exec_start =~ ($PATRON) ]]; then
            UNITS="$UNITS $u"
        fi
    done
fi
echo -e "\n${YELLOW}systemd${NC}"
if [ -n "$UNITS" ]; then
    for u in $UNITS; do
        item "$u  ($(systemctl is-active "$u" 2>/dev/null), $(systemctl is-enabled "$u" 2>/dev/null))  $(systemctl show -p FragmentPath --value "$u")"
        item "   ExecStart: $(systemctl show -p ExecStart --value "$u" | grep -oE 'path=[^ ;]+' | head -1)"
    done
else ok "ningún servicio"; fi

# ── Docker ──────────────────────────────────────────────────
CONTS=""
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    CONTS=$(docker ps -a --format '{{.Names}} {{.Image}}' | grep -E "$PATRON" | grep -v '^api-netbird-clients ' | awk '{print $1}' | tr '\n' ' ')
fi
echo -e "\n${YELLOW}Docker${NC}"
if [ -n "$CONTS" ]; then
    for c in $CONTS; do item "contenedor $c  ($(docker inspect -f '{{.State.Status}} {{.Config.Image}}' "$c"))"; done
else ok "ningún contenedor"; fi

# ── Procesos y puertos ──────────────────────────────────────
echo -e "\n${YELLOW}Procesos${NC}"
PIDS=$(pgrep -x -- "$PATRON" 2>/dev/null | grep -vx "$$" | tr '\n' ' ')
# Lo que escucha en 8080/9092 (puede ser la API vieja con otro nombre)
PUERTO_INFO=$(ss -ltnpH 2>/dev/null | awk -v p=":($PUERTOS)\$" '$4 ~ p')
if [ -n "$PIDS" ]; then
    for p in $PIDS; do item "PID $p: $(tr '\0' ' ' < /proc/"$p"/cmdline 2>/dev/null | cut -c1-120)"; done
else ok "ningún proceso con esos nombres"; fi
if [ -n "$PUERTO_INFO" ]; then
    echo "$PUERTO_INFO" | while read -r l; do item "escucha: $(echo "$l" | awk '{print $4, $6}')"; done
    warn "Revisa que lo que escucha en 8080/9092 sea la API vieja: este script solo quita lo que coincide por nombre."
fi

# ── cron ────────────────────────────────────────────────────
echo -e "\n${YELLOW}cron${NC}"
CRON=$( (for u in $(cut -d: -f1 /etc/passwd); do crontab -l -u "$u" 2>/dev/null | sed "s/^/$u: /"; done
         cat /etc/crontab /etc/cron.d/* 2>/dev/null) | grep -E "$PATRON" | grep -v '^\s*#')
if [ -n "$CRON" ]; then echo "$CRON" | while read -r l; do item "$l"; done
else ok "ninguna entrada"; fi

# ── Archivos ────────────────────────────────────────────────
echo -e "\n${YELLOW}Archivos${NC}"
RUTAS=""
for r in /opt/netbird-monitor /opt/netbird-agent /etc/netbird-agent /etc/netbird-monitor \
         /var/log/netbird-monitor /var/log/netbird-agent \
         /etc/systemd/system/netbird-monitor.service /etc/systemd/system/netbird-agent.service \
         /usr/local/bin/netbird-monitor /usr/local/bin/netbird-agent; do
    [ -e "$r" ] && RUTAS="$RUTAS $r"
done
# Copias del código o binarios en los home (solo se listan, no se borran)
OTROS=$(find /root /home -maxdepth 3 \( -name 'api_netbird*' -o -name 'netbird-monitor' -o -name 'netbird-agent' \) 2>/dev/null | head -20)
if [ -n "$RUTAS" ]; then for r in $RUTAS; do item "$r ($(du -sh "$r" 2>/dev/null | cut -f1))"; done
else ok "nada en /opt, /etc, /var/log, /usr/local/bin"; fi
[ -n "$OTROS" ] && echo "$OTROS" | while read -r l; do item "$l  (en un home: no se borra, revísalo a mano)"; done

# ── Qué se quita y qué se conserva ──────────────────────────
Q_UNITS=""; Q_CONTS=""; Q_RUTAS=""; CONSERVA=""
for u in $UNITS; do
    if se_quita "$u $(systemctl show -p ExecStart --value "$u" 2>/dev/null)"; then Q_UNITS="$Q_UNITS $u"; else CONSERVA="$CONSERVA $u"; fi
done
for c in $CONTS; do
    if se_quita "$c $(docker inspect -f '{{.Config.Image}}' "$c")"; then Q_CONTS="$Q_CONTS $c"; else CONSERVA="$CONSERVA $c"; fi
done
for r in $RUTAS; do
    if se_quita "$r"; then Q_RUTAS="$Q_RUTAS $r"; else CONSERVA="$CONSERVA $r"; fi
done
Q_PIDS=$(pgrep -x -- "$QUITAR" 2>/dev/null | grep -vx "$$" | tr '\n' ' ')
Q_CRON=$(echo "$CRON" | grep -E "$QUITAR" || true)

echo ""
if [ -n "${CONSERVA// /}" ]; then
    echo -e "${BLUE}Se conserva (inventario netbird-agent, :9092; usa --incluir-inventario para quitarlo):${NC}"
    for x in $CONSERVA; do item "$x"; done
    echo ""
fi
if [ -z "$(echo "$Q_UNITS$Q_CONTS$Q_PIDS$Q_RUTAS$Q_CRON" | tr -d ' ')" ]; then
    echo -e "${GREEN}No hay nada que quitar. Puedes instalar api-netbird-clients.${NC}"
    exit 0
fi
echo -e "${YELLOW}Se quitaría:${NC}"
for x in $Q_UNITS $Q_CONTS $Q_RUTAS; do item "$x"; done
for p in $Q_PIDS; do item "proceso $p"; done
[ -n "$Q_CRON" ] && echo "$Q_CRON" | while read -r l; do item "cron: $l"; done
if [ $BORRAR -eq 0 ]; then
    echo -e "\n${BLUE}Solo revisión: no se cambió nada.${NC} Para quitarlo:  ${GREEN}sudo bash $0 --borrar${NC}"
    exit 0
fi

# ============================================================
# ── QUITAR (con respaldo) ───────────────────────────────────
# ============================================================
echo -e "\n${YELLOW}🗑️  QUITANDO${NC}"
BK="/root/api-netbird-legacy-$(date +%Y%m%d-%H%M%S).tar.gz"
UNIT_FILES=""
for u in $Q_UNITS; do UNIT_FILES="$UNIT_FILES $(systemctl show -p FragmentPath --value "$u")"; done
BK_LIST=$(for f in $Q_RUTAS $UNIT_FILES; do [ -e "$f" ] && echo "$f"; done)
if [ -n "$BK_LIST" ]; then
    # shellcheck disable=SC2086
    tar czf "$BK" $BK_LIST 2>/dev/null
    chmod 600 "$BK"
    ok "Respaldo: $BK (puede tener tokens viejos: bórralo cuando ya no haga falta)"
fi
[ -n "$Q_CRON" ] && echo "$Q_CRON" > "${BK%.tar.gz}-cron.txt" && chmod 600 "${BK%.tar.gz}-cron.txt"

for u in $Q_UNITS; do
    f=$(systemctl show -p FragmentPath --value "$u")
    systemctl disable --now "$u" >/dev/null 2>&1 && ok "servicio $u detenido y deshabilitado"
    [[ $f == /etc/systemd/system/* ]] && rm -f "$f"
done
if [ -n "$Q_UNITS" ]; then systemctl daemon-reload; systemctl reset-failed >/dev/null 2>&1; fi

for c in $Q_CONTS; do docker rm -f "$c" >/dev/null && ok "contenedor $c eliminado"; done

for p in $(pgrep -x -- "$QUITAR" 2>/dev/null | grep -vx "$$"); do kill "$p" 2>/dev/null && ok "proceso $p detenido"; done

if [ -n "$Q_CRON" ]; then
    for u in $(cut -d: -f1 /etc/passwd); do
        if crontab -l -u "$u" 2>/dev/null | grep -qE "$QUITAR"; then
            crontab -l -u "$u" | grep -vE "$QUITAR" | crontab -u "$u" - && ok "cron de $u limpiado"
        fi
    done
    grep -lE "$QUITAR" /etc/cron.d/* /etc/crontab 2>/dev/null | while read -r f; do warn "revisa a mano: $f"; done
fi

for r in $Q_RUTAS; do rm -rf "$r" && ok "borrado $r"; done

echo ""
if ss -ltnpH 2>/dev/null | grep -qE "$QUITAR"; then
    warn "Algo de la API vieja sigue escuchando: ss -ltnp | grep -E ':(8080|9092)'"
else
    ok "La API vieja ya no escucha"
fi
echo -e "${GREEN}Listo. Ya puedes instalar api-netbird-clients.${NC}"
