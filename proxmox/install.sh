#!/usr/bin/env bash
# MagiSend — Proxmox VE LXC installer.
#
# Run on the Proxmox VE host as root:
#   bash <(curl -fsSL https://raw.githubusercontent.com/Mati-l33t/magisend/main/proxmox/install.sh)
#
# Creates an unprivileged Debian LXC and installs the latest MagiSend release in it.
set -euo pipefail

REPO=Mati-l33t/magisend
INSTALLER="https://raw.githubusercontent.com/$REPO/main/install.sh"
TITLE="MagiSend Installer"

YW="\033[33m"
GN="\033[1;92m"
RD="\033[01;31m"
OR="\033[38;5;208m"
CL="\033[m"
BOLD="\033[1m"

info() { echo -e "  ${YW}⏳ $1...${CL}"; }
ok() { echo -e "  ${GN}✔  $1${CL}"; }
die() {
  echo -e "  ${RD}✖  $1${CL}" >&2
  exit 1
}

# Runs a command in the new container. The host's language setting is not
# carried in, because the container has no such locale yet.
ct() {
  pct exec "$CTID" -- env LANG=C.UTF-8 LC_ALL=C.UTF-8 DEBIAN_FRONTEND=noninteractive APT_LISTCHANGES_FRONTEND=none "$@"
}

header() {
  clear 2> /dev/null || true
  echo -e "${OR}${BOLD}"
  cat << 'EOF'
   __  __             _ ____                 _
  |  \/  | __ _  __ _(_) ___|  ___ _ __   __| |
  | |\/| |/ _` |/ _` | \___ \ / _ \ '_ \ / _` |
  | |  | | (_| | (_| | |___) |  __/ | | | (_| |
  |_|  |_|\__,_|\__, |_|____/ \___|_| |_|\__,_|
                |___/
EOF
  echo -e "${CL}  ${BOLD}Private, self-hosted file sharing${CL} — Proxmox LXC installer"
  echo -e "  https://github.com/$REPO"
  echo
}

ask() { # title text default -> value (empty on cancel)
  whiptail --backtitle "$TITLE" --title "$1" --inputbox "$2" 10 64 "$3" 3>&1 1>&2 2>&3
}

valid_ip_cidr() { [[ $1 =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}/[0-9]{1,2}$ ]]; }
valid_ip() { [[ $1 =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; }

check_host() {
  [ "$(id -u)" -eq 0 ] || die "Run this script as root on the Proxmox VE host"
  for tool in pct pveam pvesm pvesh whiptail; do
    command -v "$tool" > /dev/null || die "'$tool' not found — run this on a Proxmox VE host"
  done
}

pick_storage() { # content type: rootdir | vztmpl
  local content=$1 label=$2 entries=() name
  while read -r name; do entries+=("$name" " "); done < <(pvesm status -content "$content" 2> /dev/null | awk 'NR>1 {print $1}')
  [ ${#entries[@]} -gt 0 ] || die "No storage supports $label"
  if [ ${#entries[@]} -eq 2 ]; then
    echo "${entries[0]}"
    return
  fi
  whiptail --backtitle "$TITLE" --title "$label" --menu "Choose the storage for the $label:" 16 60 6 \
    "${entries[@]}" 3>&1 1>&2 2>&3 || die "Cancelled"
}

# The newest Debian 13 template already on any storage that holds templates,
# so nothing is downloaded when one exists, wherever it is kept.
existing_template() {
  local store
  for store in $(pvesm status -content vztmpl 2> /dev/null | awk 'NR>1 {print $1}'); do
    pveam list "$store" 2> /dev/null | awk 'NR>1 {print $1}' | grep -E ':vztmpl/debian-13-standard_' || true
  done | sort -t/ -k2 -V | tail -n1
}

download_template() { # storage -> volume id of a freshly downloaded Debian template
  local storage=$1 available
  pveam update > /dev/null 2>&1 || true
  available=$(pveam available --section system 2> /dev/null | awk '{print $2}' | grep -E '^debian-13-standard' | sort -V | tail -n1 || true)
  [ -n "$available" ] || available=$(pveam available --section system 2> /dev/null | awk '{print $2}' | grep -E '^debian-12-standard' | sort -V | tail -n1 || true)
  [ -n "$available" ] || die "No Debian template available from pveam"
  pveam download "$storage" "$available" > /dev/null 2>&1 || die "Template download failed"
  echo "$storage:vztmpl/$available"
}

defaults() {
  CTID=$(pvesh get /cluster/nextid)
  HOSTNAME_=magisend
  DISK=4
  CORES=1
  RAM=512
  BRIDGE=vmbr0
  NET=dhcp
  GATEWAY=""
  VLAN=""
  PASSWORD=""
  PORT=8080
  PUBLIC_URL=""
  PROXIES=""
}

advanced() {
  local value choice
  value=$(ask "CONTAINER ID" "Container ID:" "$CTID") || die "Cancelled"
  [[ $value =~ ^[0-9]+$ ]] && ! pct status "$value" > /dev/null 2>&1 || die "Container ID $value is invalid or already used"
  CTID=$value
  value=$(ask "HOSTNAME" "Hostname:" "$HOSTNAME_") || die "Cancelled"
  [[ $value =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]] || die "Invalid hostname"
  HOSTNAME_=$value
  value=$(ask "DISK SIZE" "Disk size in GB (stored files live here):" "$DISK") || die "Cancelled"
  [[ $value =~ ^[0-9]+$ ]] && [ "$value" -ge 2 ] || die "Disk size must be a number of at least 2"
  DISK=$value
  value=$(ask "CPU CORES" "CPU cores:" "$CORES") || die "Cancelled"
  [[ $value =~ ^[0-9]+$ ]] && [ "$value" -ge 1 ] || die "Invalid core count"
  CORES=$value
  value=$(ask "RAM" "Memory in MB:" "$RAM") || die "Cancelled"
  [[ $value =~ ^[0-9]+$ ]] && [ "$value" -ge 256 ] || die "Memory must be at least 256 MB"
  RAM=$value
  value=$(ask "NETWORK BRIDGE" "Network bridge:" "$BRIDGE") || die "Cancelled"
  [[ $value =~ ^[A-Za-z0-9._-]+$ ]] || die "Invalid bridge name"
  BRIDGE=$value
  choice=$(whiptail --backtitle "$TITLE" --title "IP ADDRESS" --menu "IP configuration:" 12 60 2 \
    dhcp "Automatic (DHCP)" static "Static address" 3>&1 1>&2 2>&3) || die "Cancelled"
  if [ "$choice" = static ]; then
    NET=$(ask "STATIC IP" "Address with prefix, e.g. 192.168.1.50/24:" "") || die "Cancelled"
    valid_ip_cidr "$NET" || die "Invalid address"
    GATEWAY=$(ask "GATEWAY" "Gateway, e.g. 192.168.1.1:" "") || die "Cancelled"
    valid_ip "$GATEWAY" || die "Invalid gateway"
  fi
  VLAN=$(ask "VLAN" "VLAN tag (leave empty for none):" "") || die "Cancelled"
  [ -z "$VLAN" ] || [[ $VLAN =~ ^[0-9]+$ ]] || die "Invalid VLAN tag"
  local again
  while true; do
    PASSWORD=$(whiptail --backtitle "$TITLE" --title "ROOT PASSWORD" --passwordbox \
      "Root password for the container, at least 5 characters.\nLeave empty to sign in on the console automatically." 11 64 3>&1 1>&2 2>&3) || die "Cancelled"
    [ -z "$PASSWORD" ] && break
    if [ ${#PASSWORD} -lt 5 ] || [[ $PASSWORD == *" "* ]]; then
      whiptail --backtitle "$TITLE" --title "ROOT PASSWORD" --msgbox "Use at least 5 characters and no spaces." 8 50
      continue
    fi
    again=$(whiptail --backtitle "$TITLE" --title "ROOT PASSWORD" --passwordbox "Type the password again:" 10 64 3>&1 1>&2 2>&3) || die "Cancelled"
    [ "$PASSWORD" = "$again" ] && break
    whiptail --backtitle "$TITLE" --title "ROOT PASSWORD" --msgbox "The passwords did not match. Try again." 8 50
  done
  value=$(ask "PORT" "Port MagiSend listens on:" "$PORT") || die "Cancelled"
  [[ $value =~ ^[0-9]+$ ]] && [ "$value" -ge 1 ] && [ "$value" -le 65535 ] || die "Invalid port"
  PORT=$value
  PUBLIC_URL=$(ask "PUBLIC ADDRESS" "Public HTTPS address if you use a reverse proxy, e.g. https://share.example.com (optional):" "") || die "Cancelled"
  [ -z "$PUBLIC_URL" ] || [[ $PUBLIC_URL =~ ^https?://[A-Za-z0-9.-]+(:[0-9]+)?$ ]] || die "Invalid public address"
  PROXIES=$(ask "REVERSE PROXY" "IP address of that reverse proxy, e.g. 192.168.1.20 (optional):" "") || die "Cancelled"
  [[ $PROXIES =~ ^[0-9A-Fa-f:.,/\ ]*$ ]] || die "Invalid proxy address"
}

# The sign-in greeting inside the container: who provides it, the system and
# where MagiSend answers. It replaces Debian's own sign-in text.
login_info() {
  pct exec "$CTID" -- bash -c 'cat > /etc/profile.d/00_magisend-details.sh' << 'GREETING'
# Shown when signing in to the MagiSend container.
_YW="\033[33m"
_GN="\033[1;92m"
_OR="\033[1;38;5;208m"
_CL="\033[m"
_IP=$(hostname -I 2> /dev/null | awk '{print $1}')
_PORT=$(sed -n 's/^MAGISEND_LISTEN=.*:\([0-9]*\)$/\1/p' /etc/magisend/environment 2> /dev/null)
_OS=$(. /etc/os-release && echo "$NAME - Version: $VERSION_ID")
echo ""
echo -e "${_OR}MagiSend LXC Container${_CL}"
echo -e "    🌐   ${_YW}Provided by: ${_GN}MagicByMati DEV ${_YW}| GitHub: ${_GN}https://github.com/Mati-l33t/magisend${_CL}"
echo ""
echo -e "    🖥️   ${_YW}OS: ${_GN}${_OS}${_CL}"
echo -e "    🏠   ${_YW}Hostname: ${_GN}$(hostname)${_CL}"
echo -e "    💡   ${_YW}IP Address: ${_GN}${_IP}${_CL}"
echo -e "    🔗   ${_YW}MagiSend: ${_GN}http://${_IP}:${_PORT:-8080}${_CL}"
echo -e "    🔄   ${_YW}Update: ${_GN}/opt/magisend/update.sh${_CL}"
echo ""
unset _YW _GN _OR _CL _IP _PORT _OS
GREETING
  pct exec "$CTID" -- bash -c ': > /etc/motd; chmod -x /etc/update-motd.d/* 2> /dev/null || true'
}

# The container's Notes panel in the Proxmox web interface.
notes() {
  cat << NOTES
<div align='center'>
  <a href='https://github.com/$REPO' target='_blank' rel='noopener noreferrer'>
    <img src='https://raw.githubusercontent.com/$REPO/main/proxmox/logo.png' alt='MagiSend' width='96' height='96'/>
  </a>
  <h2 style='font-size: 24px; margin: 20px 0;'>MagiSend LXC</h2>
  <p style='margin: 16px 0;'>
    <a href='https://github.com/$REPO/releases/latest' target='_blank' rel='noopener noreferrer'>
      <img src='https://img.shields.io/github/v/release/$REPO?label=release&color=ff5f00' alt='Latest release' />
    </a>
  </p>
  <span style='margin: 0 10px;'>
    <i class="fa fa-github fa-fw" style="color: #f5f5f5;"></i>
    <a href='https://github.com/$REPO' target='_blank' rel='noopener noreferrer' style='text-decoration: none; color: #ff7a1a;'>GitHub</a>
  </span>
  <span style='margin: 0 10px;'>
    <i class="fa fa-comments fa-fw" style="color: #f5f5f5;"></i>
    <a href='https://github.com/$REPO/discussions' target='_blank' rel='noopener noreferrer' style='text-decoration: none; color: #ff7a1a;'>Discussions</a>
  </span>
  <span style='margin: 0 10px;'>
    <i class="fa fa-exclamation-circle fa-fw" style="color: #f5f5f5;"></i>
    <a href='https://github.com/$REPO/issues' target='_blank' rel='noopener noreferrer' style='text-decoration: none; color: #ff7a1a;'>Issues</a>
  </span>
</div>
NOTES
}

summary() {
  echo -e "  ${BOLD}Container${CL}"
  echo -e "  ID ${OR}$CTID${CL} · hostname ${OR}$HOSTNAME_${CL} · ${OR}$CORES${CL} core(s) · ${OR}$RAM${CL} MB · ${OR}$DISK${CL} GB · bridge ${OR}$BRIDGE${CL} · IP ${OR}$NET${CL}${VLAN:+ · VLAN $VLAN}"
  echo -e "  MagiSend port ${OR}$PORT${CL}${PUBLIC_URL:+ · public address $PUBLIC_URL}"
  echo
}

main() {
  check_host
  header
  defaults
  local mode
  mode=$(whiptail --backtitle "$TITLE" --title "SETTINGS" --menu "Create the MagiSend container with:" 12 64 2 \
    default "Default settings (1 core, 512 MB, 4 GB, DHCP)" \
    advanced "Advanced settings" 3>&1 1>&2 2>&3) || die "Cancelled"
  [ "$mode" = advanced ] && advanced
  header
  summary

  local cstore tmpl net
  cstore=$(pick_storage rootdir "container disk")
  tmpl=$(existing_template)
  if [ -z "$tmpl" ]; then
    local tstore
    tstore=$(pick_storage vztmpl "container template")
    info "Downloading the Debian template"
    tmpl=$(download_template "$tstore")
  fi
  ok "Template $tmpl"

  net="name=eth0,bridge=$BRIDGE,ip=$NET"
  [ -n "$GATEWAY" ] && net+=",gw=$GATEWAY"
  [ -n "$VLAN" ] && net+=",tag=$VLAN"
  local password=()
  [ -n "$PASSWORD" ] && password=(--password "$PASSWORD")
  info "Creating container $CTID"
  pct create "$CTID" "$tmpl" --hostname "$HOSTNAME_" --cores "$CORES" --memory "$RAM" --swap 512 \
    --rootfs "$cstore:$DISK" --net0 "$net" --unprivileged 1 --features nesting=1 --onboot 1 \
    --ostype debian --description "$(notes)" "${password[@]}" > /dev/null || die "Container creation failed"
  pct start "$CTID"
  ok "Container $CTID started"

  info "Waiting for network"
  for _ in $(seq 1 60); do
    pct exec "$CTID" -- getent hosts github.com > /dev/null 2>&1 && break
    sleep 2
  done
  pct exec "$CTID" -- getent hosts github.com > /dev/null 2>&1 || die "Container has no internet access"
  ok "Network ready"

  # Without a password the console signs in by itself; the console's login
  # prompt is restarted so the change applies right away.
  if [ -z "$PASSWORD" ]; then
    ct bash -c 'mkdir -p /etc/systemd/system/container-getty@1.service.d && printf "[Service]\nExecStart=\nExecStart=-/sbin/agetty --autologin root --noclear --keep-baud tty%%I 115200,38400,9600 \$TERM\n" > /etc/systemd/system/container-getty@1.service.d/override.conf && systemctl daemon-reload && systemctl restart container-getty@1.service'
  fi

  info "Installing MagiSend (this takes about a minute)"
  ct bash -c "apt-get update -qq && apt-get -y -qq upgrade > /dev/null && apt-get install -y -qq curl ca-certificates locales > /dev/null" ||
    die "Package installation failed"
  # An English UTF-8 locale, so programs in the container stop warning about
  # the language setting a terminal brings along.
  ct bash -c "sed -i 's/^# *en_US.UTF-8 UTF-8/en_US.UTF-8 UTF-8/' /etc/locale.gen && locale-gen > /dev/null 2>&1 && update-locale LANG=en_US.UTF-8" ||
    die "Locale setup failed"
  ct env MAGISEND_PORT="$PORT" MAGISEND_PUBLIC_URL="$PUBLIC_URL" MAGISEND_TRUSTED_PROXIES="$PROXIES" \
    bash -c "curl -fsSL '$INSTALLER' | bash" || die "MagiSend installation failed (see above)"

  login_info

  local ip
  ip=$(pct exec "$CTID" -- hostname -I 2> /dev/null | awk '{print $1}')
  echo
  ok "MagiSend is installed in container $CTID"
  echo
  echo -e "  Open ${OR}${BOLD}http://${ip}:${PORT}${CL} and create the administrator account."
  [ -n "$PUBLIC_URL" ] && echo -e "  Public address: ${OR}$PUBLIC_URL${CL} (point your reverse proxy to http://${ip}:${PORT})"
  echo -e "  Updates: Administration → Updates, or run ${BOLD}/opt/magisend/update.sh${CL} in the container."
  echo
}

main "$@"
