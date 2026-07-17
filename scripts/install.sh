#!/usr/bin/env bash
#
#   AETHERION BFT node installer
#
#   curl -fsSL https://raw.githubusercontent.com/AETHERION-AI-org/aetherion-bft/main/scripts/install.sh | sudo bash
#
#   Installs a full node by default. Offers a validator path that generates keys,
#   waits for the operator address to be funded, and joins the block-producing set.
#
#   Everything it writes lives under /opt/aetherion. Nothing leaves the machine
#   except the key backup you explicitly download, over a link only you are given.
#
set -euo pipefail

REPO="AETHERION-AI-org/aetherion-bft"
CHAIN_ID=100892
REGISTRY="0x6ebA8468F754404C1c93ae94C2D1973683eb749A"
MIN_STAKE=1000
RPC_PUBLIC="https://rpc.aetherion-ai.org"
EXPLORER="https://explorer.aetherion-ai.org"
# Bootnodes are not a server flag: the node reads them from genesis.json, which
# ships in this repository with the network's own list.

HOME_DIR="/opt/aetherion"
DATA_DIR="$HOME_DIR/data"
BIN="/usr/local/bin/aetherion-bft"
SERVICE="aetherion-node"

# ---------------------------------------------------------------------------
# presentation
# ---------------------------------------------------------------------------
# The Aetherion palette, straight from the design system (frontend/app/globals.css):
#
#   accent        #2D7DFF   primary royal blue   headings, structure
#   accent-soft   #6EA8FF   secondary blue       prompts, spinners, cautions
#   check-cyan    #2FF2E8   checks and status    the tick, and nothing else
#   success       #34D399   confirmed good       completion lines
#   crimson       #D84C4C   danger               failures only
#   text-primary  #EAF0F8   foreground
#   text-muted    #8A93A6   secondary text
#
# check-cyan is reserved for status marks by the design system, so it is worn by the
# tick alone. There is no amber in the palette and none is invented here: a caution is
# accent-soft carrying a "!", told apart by its glyph rather than by a colour that does
# not belong to the brand.
#
# Truecolor where the terminal has it, the nearest xterm-256 step where it does not,
# and nothing at all when output is not a terminal, so piping to a log stays readable.
if [ -t 1 ] && [ "$(tput colors 2>/dev/null || echo 0)" -ge 8 ]; then
  B=$'\033[1m'; D=$'\033[2m'; R=$'\033[0m'
  case "${COLORTERM:-}" in
    truecolor|24bit)
      BLUE=$'\033[38;2;45;125;255m'    # accent
      SOFT=$'\033[38;2;110;168;255m'   # accent-soft
      CYAN=$'\033[38;2;47;242;232m'    # check-cyan
      GREEN=$'\033[38;2;52;211;153m'   # success
      RED=$'\033[38;2;216;76;76m'      # oracle-crimson
      GREY=$'\033[38;2;138;147;166m'   # text-muted
      ;;
    *)
      BLUE=$'\033[38;5;33m'; SOFT=$'\033[38;5;75m'; CYAN=$'\033[38;5;51m'
      GREEN=$'\033[38;5;79m'; RED=$'\033[38;5;167m'; GREY=$'\033[38;5;246m'
      ;;
  esac
else
  B=""; D=""; R=""; BLUE=""; SOFT=""; CYAN=""; GREEN=""; RED=""; GREY=""
fi

banner() {
  printf '\n%s' "$BLUE"
  cat <<'ART'
     ###    ######## ######## ##     ## ######## ########  ####  #######  ##    ##
    ## ##   ##          ##    ##     ## ##       ##     ##  ##  ##     ## ###   ##
   ##   ##  ##          ##    ##     ## ##       ##     ##  ##  ##     ## ####  ##
  ##     ## ######      ##    ######### ######   ########   ##  ##     ## ## ## ##
  ######### ##          ##    ##     ## ##       ##   ##    ##  ##     ## ##  ####
  ##     ## ##          ##    ##     ## ##       ##    ##   ##  ##     ## ##   ###
  ##     ## ########    ##    ##     ## ######## ##     ## ####  #######  ##    ##
ART
  printf '%s' "$R"
  printf '%s                    B  F  T     C  O  N  S  E  N  S  U  S     N  O  D  E%s\n\n' "$D$SOFT" "$R"
}

hr()   { printf '%s%s%s\n' "$D$GREY" "$(printf '─%.0s' $(seq 1 72))" "$R"; }
step() { printf '\n%s%s▸ %s%s\n' "$B" "$BLUE" "$1" "$R"; }
ok()   { printf '  %s✓%s %s\n' "$CYAN" "$R" "$1"; }
info() { printf '  %s·%s %s\n' "$GREY" "$R" "$1"; }
warn() { printf '  %s!%s %s\n' "$SOFT" "$R" "$1"; }
die()  { printf '\n  %s✗ %s%s\n\n' "$RED" "$1" "$R" >&2; exit 1; }
kv()   { printf '  %s%-22s%s %s\n' "$GREY" "$1" "$R" "$2"; }

# These run under `set -e`, so both must end on a zero status no matter where their
# output is going. A trailing `[ -t 1 ] && ...` returns 1 when stdout is a file or a
# pipe, which is enough to kill the installer mid-step without printing a thing.
spin_pid=""
spin_start() {
  if [ ! -t 1 ]; then
    printf '  %s\n' "$1"

    return 0
  fi
  local msg="$1"
  ( local f='⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏' i=0
    while :; do
      i=$(( (i+1) % 10 ))
      printf '\r  %s%s%s %s' "$SOFT" "${f:$i:1}" "$R" "$msg"
      sleep 0.1
    done ) & spin_pid=$!
  disown 2>/dev/null || true

  return 0
}
spin_stop() {
  [ -n "$spin_pid" ] && kill "$spin_pid" 2>/dev/null
  spin_pid=""
  [ -t 1 ] && printf '\r\033[K'

  return 0
}

# Unattended mode. A human gets the prompts; automation answers them up front through
# the environment. Same code path either way, so what we test is what users run.
#
#   AETH_UNATTENDED=1     answer every prompt from the environment, never block
#   AETH_MODE=            full | validator
#   AETH_STAKE=           how much AETH to lock (validator mode)
#   AETH_BACKUP_PASS=     passphrase for the encrypted key archive
#
# Unattended installs write the key archive to disk instead of serving it over a
# one-time link: if you are scripting this you already have shell access to the box,
# so the download dance protects nothing and only gets in the way.
UNATTENDED="${AETH_UNATTENDED:-}"

# Prompts write to /dev/tty, never to stdout: stdout is the answer, and these functions
# are called inside $( ). Anything printed there ends up inside the value instead of on
# screen, which is how escape codes leaked into the captured text as stray brackets.
#
# The terminal's own echo is off too. Reading with -s and printing the keystroke back by
# hand is the only way the line looks the same whether the answer came from a keypress,
# from Enter taking the default, or from the environment in an unattended run.

# ask_key <prompt> <default> <valid-chars> -> one keystroke, no Enter needed
ask_key() {
  local ans
  if [ -n "$UNATTENDED" ]; then
    printf '  %s?%s %s %s%s%s\n' "$SOFT" "$R" "$1" "$B" "$2" "$R" >&2
    echo "$2"

    return 0
  fi

  printf '  %s?%s %s %s(Enter for %s)%s ' "$SOFT" "$R" "$1" "$D$GREY" "$2" "$R" > /dev/tty
  while :; do
    # Losing the tty (a dropped connection) must end the run, not silently accept the
    # default. Only a real Enter, which reads successfully and returns nothing, does that.
    if ! IFS= read -r -n 1 -s ans < /dev/tty; then
      printf '\n' > /dev/tty
      die "Lost the terminal. Re-run the installer to continue."
    fi
    [ -z "$ans" ] && ans="$2"
    case "$ans" in
      *[!"$3"]* ) continue ;;
    esac
    break
  done
  printf '%s%s%s\n' "$B" "$ans" "$R" > /dev/tty
  echo "$ans"
}

# ask <prompt> <default> -> a typed answer, Enter to accept the default
ask() {
  local ans
  if [ -n "$UNATTENDED" ]; then
    printf '  %s?%s %s %s%s%s\n' "$SOFT" "$R" "$1" "$B" "$2" "$R" >&2
    echo "$2"

    return 0
  fi

  printf '  %s?%s %s %s(Enter for %s)%s ' "$SOFT" "$R" "$1" "$D$GREY" "$2" "$R" > /dev/tty
  if ! IFS= read -r ans < /dev/tty; then
    printf '\n' > /dev/tty
    die "Lost the terminal. Re-run the installer to continue."
  fi
  echo "${ans:-$2}"
}

cleanup() {
  spin_stop
  stop_backup_server
  # Leave the terminal as we found it: a read interrupted mid-keystroke can leave echo
  # off, and then the shell the operator returns to is typing blind.
  [ -t 0 ] && stty echo 2>/dev/null
  printf '\033[?25h' 2>/dev/null   # cursor back on

  return 0
}

# INT and TERM must actually end the run. A trap that only cleans up and returns hands
# control straight back to the interrupted command: bash resumes, the read that was
# cancelled reports failure, `|| ans=""` swallows it, and the loop asks again. Ctrl+C
# then does nothing at all, which is exactly what it did.
on_interrupt() {
  cleanup
  printf '\n  %sInterrupted.%s Nothing is lost: the node keeps running as a service, and\n' "$SOFT" "$R" >&2
  printf '  re-running the installer picks up where it left off.\n\n' >&2
  exit 130
}

trap cleanup EXIT
trap on_interrupt INT TERM

# ---------------------------------------------------------------------------
# preflight
# ---------------------------------------------------------------------------
preflight() {
  step "Preflight"

  [ "$(id -u)" -eq 0 ] || die "Run as root: curl -fsSL <url> | sudo bash"
  [ "$(uname -s)" = "Linux" ] || die "Linux only (found $(uname -s))"

  case "$(uname -m)" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) die "Unsupported architecture: $(uname -m)" ;;
  esac
  ok "Linux / $ARCH"

  for c in curl tar; do
    command -v "$c" >/dev/null || die "Missing required command: $c"
  done

  # Best-effort install of the optional-but-wanted tools.
  local missing=()
  command -v zip     >/dev/null || missing+=(zip)
  command -v openssl >/dev/null || missing+=(openssl)
  command -v python3 >/dev/null || missing+=(python3)
  if [ ${#missing[@]} -gt 0 ]; then
    info "Installing: ${missing[*]}"
    if   command -v apt-get >/dev/null; then apt-get update -qq >/dev/null 2>&1 && apt-get install -y -qq "${missing[@]}" >/dev/null 2>&1 || true
    elif command -v dnf     >/dev/null; then dnf install -y -q "${missing[@]}" >/dev/null 2>&1 || true
    elif command -v yum     >/dev/null; then yum install -y -q "${missing[@]}" >/dev/null 2>&1 || true
    fi
  fi
  for c in zip openssl python3; do
    command -v "$c" >/dev/null || die "Could not install '$c'. Install it and re-run."
  done
  ok "Dependencies present"

  local free_kb; free_kb=$(df -Pk /opt 2>/dev/null | awk 'NR==2{print $4}' || echo 0)
  if [ "${free_kb:-0}" -lt 20971520 ]; then
    warn "Less than 20 GB free on /opt. The chain grows over time."
  else
    ok "Disk space OK ($(( free_kb / 1048576 )) GB free)"
  fi
}

# ---------------------------------------------------------------------------
# binary + genesis
# ---------------------------------------------------------------------------
install_binary() {
  step "Node binary"

  if [ -x "$BIN" ]; then
    info "Existing binary found, replacing it"
  fi

  local tag url tmp
  tag=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null \
        | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p;T;q' || true)

  if [ -n "$tag" ]; then
    url="https://github.com/$REPO/releases/download/$tag/aetherion-bft-linux-$ARCH"
    tmp=$(mktemp)
    spin_start "Downloading $tag (linux/$ARCH)"
    if curl -fsSL -o "$tmp" "$url" 2>/dev/null; then
      spin_stop
      # Verify against the published checksum file. A release without one is not trusted.
      local sums expected actual
      sums=$(curl -fsSL "https://github.com/$REPO/releases/download/$tag/SHA256SUMS" 2>/dev/null || true)
      expected=$(printf '%s\n' "$sums" | awk -v f="aetherion-bft-linux-$ARCH" '$2 == f {print $1; exit}')
      actual=$(sha256sum "$tmp" | awk '{print $1}')
      if [ -z "$expected" ]; then
        rm -f "$tmp"; die "Release $tag has no SHA256SUMS. Refusing to install an unverified binary."
      fi
      [ "$expected" = "$actual" ] || { rm -f "$tmp"; die "Checksum mismatch. Expected $expected, got $actual."; }
      install -m 0755 "$tmp" "$BIN"; rm -f "$tmp"
      ok "Installed $tag (sha256 verified)"
      return
    fi
    spin_stop
    warn "No prebuilt binary for $tag/$ARCH, building from source"
  else
    warn "No published release found, building from source"
  fi

  command -v go >/dev/null || die "Go is required to build from source. Install Go 1.20+ and re-run."
  local src; src=$(mktemp -d)
  spin_start "Building from source (a few minutes)"
  git clone -q --depth 1 "https://github.com/$REPO.git" "$src" 2>/dev/null \
    || { spin_stop; die "git clone failed"; }
  ( cd "$src" && CGO_ENABLED=0 go build -trimpath -o "$BIN" . ) >/dev/null 2>&1 \
    || { spin_stop; die "go build failed"; }
  spin_stop; rm -rf "$src"
  chmod 0755 "$BIN"
  ok "Built from source"
}

install_genesis() {
  step "Network genesis"
  mkdir -p "$DATA_DIR"
  curl -fsSL -o "$HOME_DIR/genesis.json" \
    "https://raw.githubusercontent.com/$REPO/main/genesis.json" \
    || die "Could not download genesis.json"
  local id
  id=$(python3 -c "import json;print(json.load(open('$HOME_DIR/genesis.json'))['params']['chainID'])" 2>/dev/null || echo "")
  [ "$id" = "$CHAIN_ID" ] || die "Genesis chainID is '$id', expected $CHAIN_ID"
  ok "Genesis verified (chain $CHAIN_ID, sha256 $(sha256sum "$HOME_DIR/genesis.json" | cut -c1-16)…)"
}

# ---------------------------------------------------------------------------
# keys + backup
# ---------------------------------------------------------------------------
generate_keys() {
  step "Node identity"
  if [ -f "$DATA_DIR/consensus/validator.key" ]; then
    ok "Existing keys found, keeping them"
  else
    "$BIN" secrets init --data-dir "$DATA_DIR" --insecure >/dev/null 2>&1 \
      || die "Key generation failed"
    ok "Fresh keys generated"
  fi
  chmod -R go-rwx "$DATA_DIR/consensus" 2>/dev/null || true

  # `secrets output` can print one field at a time, so ask it for exactly the value
  # wanted rather than pattern-matching a human-readable block. (Note it takes no
  # --insecure flag; only `secrets init` does.)
  OPERATOR=$("$BIN" secrets output --data-dir "$DATA_DIR" --validator 2>/dev/null \
             | tr -d '[:space:]' || true)
  NODE_ID=$("$BIN" secrets output --data-dir "$DATA_DIR" --node-id 2>/dev/null \
             | tr -d '[:space:]' || true)

  case "$OPERATOR" in
    0x[0-9a-fA-F]*) [ ${#OPERATOR} -eq 42 ] || die "Operator address looks malformed: $OPERATOR" ;;
    *) die "Could not read the operator address from this node's secrets" ;;
  esac

  kv "Operator address" "$B$OPERATOR$R"
  # Not `[ -n "$NODE_ID" ] && kv ...`: as the last line of a function that returns 1
  # when the id is missing, which under `set -e` would kill the install right here.
  if [ -n "$NODE_ID" ]; then
    kv "Node ID" "$NODE_ID"
  fi
}

BACKUP_SRV_PID=""
BACKUP_DIR=""
TUNNEL_PID=""

stop_backup_server() {
  [ -n "$BACKUP_SRV_PID" ] && kill "$BACKUP_SRV_PID" 2>/dev/null || true
  [ -n "$TUNNEL_PID" ]     && kill "$TUNNEL_PID"     2>/dev/null || true
  BACKUP_SRV_PID=""; TUNNEL_PID=""
  [ -n "$BACKUP_DIR" ] && rm -rf "$BACKUP_DIR" 2>/dev/null || true
  BACKUP_DIR=""
}

backup_keys() {
  if is_done "backup" && [ -z "${AETH_FORCE_BACKUP:-}" ]; then
    step "Key backup"
    ok "Already backed up on an earlier run"
    info "Archive: $HOME_DIR/backups/ (re-run with AETH_FORCE_BACKUP=1 to make a new one)"

    return 0
  fi
  step "Key backup"
  cat <<EOF

  ${B}Your keys exist only on this machine.${R}
  ${GREY}Lose them and you lose the node's identity and its stake. There is no
  recovery, no support desk, and no one who can reissue them. Back them up now.${R}

EOF

  # Passphrase. The archive travels over a tunnel we do not own, so it is encrypted
  # here, on this machine, before it ever touches the network.
  local pass pass2
  if [ -n "$UNATTENDED" ]; then
    pass="${AETH_BACKUP_PASS:-}"
    [ ${#pass} -ge 8 ] || die "AETH_BACKUP_PASS must be at least 8 characters in unattended mode"
  else
    while :; do
      read -r -s -p "  Choose a passphrase for the backup: " pass </dev/tty; echo
      [ ${#pass} -ge 8 ] || { warn "At least 8 characters, please."; continue; }
      read -r -s -p "  Repeat it: " pass2 </dev/tty; echo
      [ "$pass" = "$pass2" ] && break
      warn "They do not match. Again."
    done
  fi

  BACKUP_DIR=$(mktemp -d); chmod 700 "$BACKUP_DIR"
  local plain="$BACKUP_DIR/keys.zip"
  ( cd "$DATA_DIR" && zip -q -r "$plain" consensus ) || die "Could not archive the keys"

  local hash short enc name
  hash=$(sha256sum "$plain" | awk '{print $1}')
  short=${hash:0:16}
  name="aetherion-keys-${short}.zip.enc"
  enc="$BACKUP_DIR/$name"

  # AES-256 with PBKDF2. Not zip's own encryption, which is broken by design.
  openssl enc -aes-256-cbc -pbkdf2 -iter 200000 -salt \
    -in "$plain" -out "$enc" -pass pass:"$pass" || die "Encryption failed"
  shred -u "$plain" 2>/dev/null || rm -f "$plain"
  unset pass pass2

  local enc_hash; enc_hash=$(sha256sum "$enc" | awk '{print $1}')

  # Scripted install: hand the archive over as a file. Whoever is automating this
  # already has shell access, so a one-time download link would guard nothing.
  if [ -n "$UNATTENDED" ]; then
    mkdir -p "$HOME_DIR/backups"; chmod 700 "$HOME_DIR/backups"
    mv "$enc" "$HOME_DIR/backups/$name"
    rm -rf "$BACKUP_DIR"; BACKUP_DIR=""
    ok "Backup written to $HOME_DIR/backups/$name"
    kv "sha256" "$enc_hash"
    warn "Copy it off this machine. Nothing else holds these keys."
    mark_done "backup"

    return
  fi

  # A single-file server: one unguessable path, no directory listing, no other route.
  local token port
  token=$(head -c 18 /dev/urandom | od -An -tx1 | tr -d ' \n')
  port=$(python3 -c "import socket;s=socket.socket();s.bind(('',0));print(s.getsockname()[1]);s.close()")

  python3 - "$enc" "$token" "$name" "$port" <<'PY' >/dev/null 2>&1 &
import sys, http.server, socketserver
path, token, name, port = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
want = f"/{token}/{name}"

class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path != want:            # nothing else exists: no listing, no probing
            self.send_error(404); return
        with open(path, "rb") as f:
            body = f.read()
        self.send_response(200)
        self.send_header("Content-Type", "application/octet-stream")
        self.send_header("Content-Disposition", f'attachment; filename="{name}"')
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def do_HEAD(self): self.send_error(404)
    def log_message(self, *a): pass

socketserver.TCPServer.allow_reuse_address = True
socketserver.TCPServer(("0.0.0.0", port), H).serve_forever()
PY
  BACKUP_SRV_PID=$!
  sleep 1

  # Prefer a real HTTPS URL. Browsers are hostile to bare http:// downloads, and the
  # tunnel also works when the box has no public IP or a closed firewall.
  local url=""
  if ! command -v cloudflared >/dev/null; then
    spin_start "Setting up an HTTPS tunnel"
    local cf_arch="amd64"; [ "$ARCH" = "arm64" ] && cf_arch="arm64"
    curl -fsSL -o /usr/local/bin/cloudflared \
      "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${cf_arch}" 2>/dev/null \
      && chmod +x /usr/local/bin/cloudflared || true
    spin_stop
  fi
  if command -v cloudflared >/dev/null; then
    local cflog="$BACKUP_DIR/cf.log"
    cloudflared tunnel --url "http://127.0.0.1:$port" --no-autoupdate >"$cflog" 2>&1 &
    TUNNEL_PID=$!
    spin_start "Opening HTTPS tunnel"
    for _ in $(seq 1 30); do
      url=$(grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' "$cflog" 2>/dev/null | head -1 || true)
      [ -n "$url" ] && break
      sleep 1
    done
    spin_stop
  fi

  local link
  if [ -n "$url" ]; then
    link="$url/$token/$name"
    # cloudflared prints the hostname before the edge can actually route to it, so the
    # link works only some seconds after it appears. Handing it over early means the
    # operator clicks a dead link and concludes the installer is broken. Wait until the
    # link really serves the file before showing it.
    spin_start "Waiting for the tunnel to go live"
    local live=""
    for _ in $(seq 1 40); do
      if [ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "$link" 2>/dev/null || echo 000)" = "200" ]; then
        live=1
        break
      fi
      sleep 2
    done
    spin_stop
    if [ -n "$live" ]; then
      ok "HTTPS tunnel is live"
    else
      warn "Tunnel came up but is not serving; falling back to plain HTTP"
      kill "$TUNNEL_PID" 2>/dev/null || true
      TUNNEL_PID=""
      url=""
    fi
  fi

  if [ -z "$url" ]; then
    local ip; ip=$(curl -fsS --max-time 8 https://api.ipify.org 2>/dev/null || hostname -I | awk '{print $1}')
    link="http://$ip:$port/$token/$name"
    warn "Tunnel unavailable, serving over plain HTTP instead"
    info "The archive itself is encrypted, so the download stays confidential."
  fi

  hr
  printf '\n  %sDownload your backup now, in a browser on your own computer:%s\n\n' "$B" "$R"
  printf '     %s%s%s\n\n' "$SOFT$B" "$link" "$R"
  printf '  %sVerify it after downloading:%s\n' "$B" "$R"
  printf '     %ssha256sum %s%s\n' "$GREY" "$name" "$R"
  printf '     %s%s%s\n\n' "$GREY" "$enc_hash" "$R"
  printf '  %sDecrypt it when you need it:%s\n' "$B" "$R"
  printf '     %sopenssl enc -d -aes-256-cbc -pbkdf2 -iter 200000 -in %s -out keys.zip%s\n\n' "$GREY" "$name" "$R"
  printf '  %sThis link dies the moment you answer below, and is served by this\n  installer alone. It is not reachable afterwards.%s\n\n' "$D$GREY" "$R"
  hr

  local a
  while :; do
    a=$(ask_key "Downloaded and verified it? y/n" "n" "ynYN")
    case "$a" in
      y|Y) break ;;
      *) printf '  %sTake your time. The link stays live until you answer y.%s\n' "$GREY" "$R" ;;
    esac
  done

  stop_backup_server
  mark_done "backup"
  ok "Backup confirmed. Download server stopped and the archive wiped from this host."
}

# ---------------------------------------------------------------------------
# validator path
# ---------------------------------------------------------------------------
rpc() {  # rpc <method> <params-json>
  curl -fsS --max-time 10 -X POST "$RPC_PUBLIC" -H 'Content-Type: application/json' \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"$1\",\"params\":$2}" 2>/dev/null || echo '{}'
}

rpc_head() {  # rpc_head <endpoint> -> decimal head, empty when the endpoint is not up
  local r
  # `|| true`: curl -f exits non-zero on a refused connection, pipefail promotes it, and
  # set -e would kill the installer. An endpoint that is not answering yet is the normal
  # case here, not a failure.
  r=$(curl -fsS --max-time 8 -X POST "$1" -H 'Content-Type: application/json' \
      -d '{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}' 2>/dev/null \
      | sed -n 's/.*"result":"\([^"]*\)".*/\1/p' || true)
  [ -n "$r" ] || return 0
  printf '%d' "$r"
}

balance_wei() {
  rpc eth_getBalance "[\"$1\",\"latest\"]" | sed -n 's/.*"result":"\([^"]*\)".*/\1/p'
}

wei_to_aeth() { python3 -c "print(f'{int('${1:-0x0}',16)/10**18:,.4f}')" 2>/dev/null || echo "0"; }

await_funding() {
  printf '\n'
  local want
  want=$(ask "How much AETH will you lock as stake? (minimum $MIN_STAKE)" "${AETH_STAKE:-$MIN_STAKE}")
  if ! python3 -c "import sys;sys.exit(0 if float('$want') >= $MIN_STAKE else 1)" 2>/dev/null; then
    warn "Below the $MIN_STAKE AETH minimum. Using $MIN_STAKE."
    want=$MIN_STAKE
  fi
  STAKE_AETH="$want"

  local need_wei
  need_wei=$(python3 -c "print(int($STAKE_AETH * 10**18))")

  cat <<EOF

  ${B}Fund this address to produce blocks${R}

     ${SOFT}${B}${OPERATOR}${R}

  ${GREY}This node will lock ${B}${STAKE_AETH} AETH${R}${GREY} as stake. The deposit stays yours: it is
  locked, not spent, and can be unbonded later. Send a little extra to cover
  gas. Until it arrives this node still runs as a full node, which serves RPC
  and relays blocks but does not seal them or earn rewards.${R}

  ${GREY}Watch it arrive: ${EXPLORER}/address/${OPERATOR}${R}

EOF

  local bal aeth start
  start=$(date +%s)
  while :; do
    bal=$(balance_wei "$OPERATOR"); bal=${bal:-0x0}
    aeth=$(wei_to_aeth "$bal")
    if python3 -c "import sys;sys.exit(0 if int('$bal',16) >= $need_wei else 1)" 2>/dev/null; then
      spin_stop
      ok "Funded: $aeth AETH"
      return 0
    fi
    spin_stop
    if [ -t 1 ]; then
      printf '\r\033[K  %s⠿%s waiting for funds  %s%s AETH%s / %s AETH  %s(%ss)%s' \
        "$SOFT" "$R" "$B" "$aeth" "$R" "$STAKE_AETH" "$D$GREY" "$(( $(date +%s) - start ))" "$R"
    elif [ $(( ($(date +%s) - start) % 60 )) -eq 0 ]; then
      printf '  waiting for funds: %s / %s AETH\n' "$aeth" "$STAKE_AETH"
    fi
    sleep 1
  done
}

# is_whitelisted <address> -> 0 if the registry already admitted this operator.
# getValidator returns a tuple opening with a dynamic `bytes`, so word 0 is the offset
# to the tuple head and `whitelisted` is that head's sixth word.
is_whitelisted() {
  local res
  res=$(rpc eth_call "[{\"to\":\"$REGISTRY\",\"data\":\"0x1904bb2e$(printf '%064s' "${1#0x}" | tr ' ' '0')\"},\"latest\"]" \
        | sed -n 's/.*"result":"0x\([0-9a-fA-F]*\)".*/\1/p')
  [ -n "$res" ] || return 1
  python3 - "$res" <<'PY'
import sys
raw = bytes.fromhex(sys.argv[1])
if len(raw) < 32: sys.exit(1)
base = int.from_bytes(raw[0:32], "big")
s = base + 5*32
if s + 32 > len(raw): sys.exit(1)
sys.exit(0 if int.from_bytes(raw[s:s+32], "big") else 1)
PY
}

# A validator that joins the set while still syncing cannot sign the blocks it is now
# responsible for, and its share of the voting power becomes dead weight. In a small set
# that is enough to put quorum out of reach for everyone, so an unsynced node staking
# does not just fail to help: it can stop the chain. Stake is therefore gated on having
# caught up, and this is not optional.
await_sync() {
  step "Waiting to catch up"

  cat >&2 <<EOF

  ${GREY}This takes hours: the node is replaying the whole chain. It runs as a systemd
  service, so it keeps syncing whether or not you stay connected, and it comes
  back by itself after a crash or a reboot.

  ${B}You can close this terminal.${R}${GREY} Nothing is lost. When you want to finish up, run
  the same install command again and it will pick up exactly where it left off.

  Progress any time:  ${R}systemctl status ${SERVICE}${GREY}
  Live logs:          ${R}journalctl -u ${SERVICE} -f${GREY}${R}

EOF

  local local_head chain_head lag start
  start=$(date +%s)
  while :; do
    chain_head=$(rpc_head "$RPC_PUBLIC")
    local_head=$(rpc_head "http://127.0.0.1:8545")
    if [ -z "$local_head" ] || [ -z "$chain_head" ]; then
      [ -t 1 ] && printf '\r\033[K  %s⠿%s waiting for the node to answer' "$SOFT" "$R"
      sleep 5
      continue
    fi
    lag=$(( chain_head - local_head ))
    if [ "$lag" -le 5 ]; then
      printf '\r\033[K'
      ok "Synced (block $local_head)"

      return 0
    fi
    if [ -t 1 ]; then
      printf '\r\033[K  %s⠿%s syncing  %s%s%s / %s  %s(%s behind, %ss elapsed)%s' \
        "$SOFT" "$R" "$B" "$local_head" "$R" "$chain_head" "$D$GREY" "$lag" \
        "$(( $(date +%s) - start ))" "$R"
    elif [ $(( ($(date +%s) - start) % 300 )) -lt 10 ]; then
      printf '  syncing: %s / %s (%s behind)\n' "$local_head" "$chain_head" "$lag"
    fi
    sleep 10
  done
}

# current_stake_wei <address> -> the operator's locked stake, in wei, from the chain.
# getValidator returns a tuple opening with a dynamic `bytes`, so word 0 is the offset to
# the tuple head and `stake` is that head's second word.
current_stake_wei() {
  local res
  res=$(rpc eth_call "[{\"to\":\"$REGISTRY\",\"data\":\"0x1904bb2e$(printf '%064s' "${1#0x}" | tr ' ' '0')\"},\"latest\"]" \
        | sed -n 's/.*"result":"0x\([0-9a-fA-F]*\)".*/\1/p')
  [ -n "$res" ] || { echo ""; return 0; }
  python3 - "$res" <<'PY_INNER' 2>/dev/null || echo ""
import sys
raw = bytes.fromhex(sys.argv[1])
if len(raw) < 32: sys.exit(1)
base = int.from_bytes(raw[0:32], "big")
s = base + 32
if s + 32 > len(raw): sys.exit(1)
print(int.from_bytes(raw[s:s+32], "big"))
PY_INNER
}

join_validator_set() {
  step "Joining the validator set"

  local pop_out bls pop
  pop_out=$("$BIN" validator-pop --data-dir "$DATA_DIR" --chain-id "$CHAIN_ID" \
            --registry "$REGISTRY" --insecure 2>/dev/null) || die "Could not build the proof-of-possession"
  bls=$(printf '%s\n' "$pop_out" | sed -n 's/.*BLS public key *= *\(0x[0-9a-fA-F]*\).*/\1/p;T;q')
  pop=$(printf '%s\n' "$pop_out" | sed -n 's/.*Proof-of-possession *= *\(0x[0-9a-fA-F]*\).*/\1/p;T;q')
  [ -n "$bls" ] && [ -n "$pop" ] || die "Could not parse the proof-of-possession"
  ok "Proof-of-possession generated"

  cat > "$HOME_DIR/validator-registration.txt" <<EOF
Aetherion Network (chain $CHAIN_ID) validator registration
Generated $(date -u +"%Y-%m-%dT%H:%M:%SZ")

operator            = $OPERATOR
blsPublicKey        = $bls
proofOfPossession   = $pop
registry            = $REGISTRY

Governance calls: registerValidator(operator, blsPublicKey, proofOfPossession)
Then this node locks its own stake (>= $MIN_STAKE AETH) from the operator address.
EOF
  chmod 600 "$HOME_DIR/validator-registration.txt"

  # Registration is permissionless: the chain checks the proof-of-possession and that
  # the caller is the operator, and that is the whole gate. Nobody is asked, nothing is
  # approved, and there is no queue to wait in.
  if is_whitelisted "$OPERATOR"; then
    ok "Already registered"
  else
    step "Registering on-chain"
    spin_start "Registering (the chain verifies your proof-of-possession)"
    local rout
    if rout=$("$BIN" validator-register --data-dir "$DATA_DIR" --registry "$REGISTRY" \
              --chain-id "$CHAIN_ID" --jsonrpc "$RPC_PUBLIC" --insecure 2>&1); then
      spin_stop
      ok "Registered"
      printf '%s\n' "$rout" | sed -n 's/^\(Transaction\|Block\)/  &/p'
    else
      spin_stop
      warn "Registration failed:"
      printf '%s\n' "$rout" | tail -2 | sed 's/^/    /'
      info "Your proof-of-possession is saved in $HOME_DIR/validator-registration.txt"
      return
    fi
  fi

  await_sync

  step "Locking stake"

  # Ask the chain, not the state file, what is already locked. A resumed run must never
  # be able to pay twice, and only the chain knows the truth about that.
  local target_wei have_wei need_wei
  target_wei=$(python3 -c "print(int($STAKE_AETH * 10**18))")
  have_wei=$(current_stake_wei "$OPERATOR")
  if [ -z "$have_wei" ]; then
    die "Could not read the current stake from the registry. Refusing to stake blind."
  fi

  if [ "$have_wei" != "0" ]; then
    info "Already locked: $(python3 -c "print(f'{$have_wei/10**18:,.4f}')") AETH"
  fi

  need_wei=$(python3 -c "print(max(0, $target_wei - $have_wei))")
  if [ "$need_wei" = "0" ]; then
    ok "Stake already at or above $STAKE_AETH AETH, nothing to add"
    mark_done "stake"
    info "You join the block-producing set at the next epoch boundary (~10 minutes)."

    return 0
  fi

  spin_start "Locking $(python3 -c "print(f'{$need_wei/10**18:,.4f}')") AETH as stake"
  local out
  if out=$("$BIN" validator-stake --data-dir "$DATA_DIR" --registry "$REGISTRY" \
           --amount "$need_wei" --jsonrpc "$RPC_PUBLIC" --insecure 2>&1); then
    spin_stop
    ok "Stake locked"
    mark_done "stake"
    printf '%s\n' "$out" | sed -n 's/^\(Transaction\|Block\|Total stake\)/  &/p'
  else
    spin_stop
    warn "Could not lock the stake:"
    printf '%s\n' "$out" | tail -2 | sed 's/^/    /'
    info "Retry with:"
    printf '     %s%s validator-stake --data-dir %s --registry %s --amount %s --insecure%s\n' \
      "$GREY" "$BIN" "$DATA_DIR" "$REGISTRY" "$need_wei" "$R"
    return
  fi

  info "You join the block-producing set at the next epoch boundary (~10 minutes)."
}

# ---------------------------------------------------------------------------
# service
# ---------------------------------------------------------------------------
install_service() {
  step "System service"
  local seal_flag=""
  [ "$MODE" = "validator" ] && seal_flag=" --seal"

  cat > "/etc/systemd/system/$SERVICE.service" <<EOF
[Unit]
Description=Aetherion Network Node (AETHERION BFT)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$BIN server --data-dir $DATA_DIR --chain $HOME_DIR/genesis.json \\
  --libp2p 0.0.0.0:1478 --jsonrpc 0.0.0.0:8545 --grpc-address 127.0.0.1:9632 \\
  --json-rpc-batch-request-limit 1000$seal_flag --log-level INFO
Restart=always
RestartSec=5
LimitNOFILE=65535
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  systemctl enable -q "$SERVICE" 2>/dev/null || true
  systemctl restart "$SERVICE"
  ok "Service $SERVICE enabled and started"

  spin_start "Waiting for the node to answer"
  local head=""
  for _ in $(seq 1 45); do
    # `|| true`: this loop exists precisely because the node is not answering yet, so a
    # failed curl must not be fatal. Without it the first poll killed the installer.
    head=$(curl -fsS --max-time 3 -X POST http://127.0.0.1:8545 -H 'Content-Type: application/json' \
      -d '{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}' 2>/dev/null \
      | sed -n 's/.*"result":"\([^"]*\)".*/\1/p' || true)
    [ -n "$head" ] && break
    sleep 2
  done
  spin_stop
  [ -n "$head" ] || { warn "No RPC response yet. Check: journalctl -u $SERVICE -f"; return; }
  ok "Node is live at block $((head))"
}

summary() {
  local head net
  head=$(rpc eth_blockNumber "[]" | sed -n 's/.*"result":"\([^"]*\)".*/\1/p')
  net=$((${head:-0x0}))
  printf '\n'; hr
  printf '\n  %s%s Your node is running%s\n\n' "$GREEN$B" "✓" "$R"
  kv "Mode"        "$MODE"
  kv "Operator"    "$OPERATOR"
  kv "Data"        "$DATA_DIR"
  kv "RPC"         "http://127.0.0.1:8545"
  kv "Network head" "$net"
  printf '\n  %sLogs%s     journalctl -u %s -f\n' "$B" "$R" "$SERVICE"
  printf '  %sStop%s     systemctl stop %s\n' "$B" "$R" "$SERVICE"
  printf '  %sExplorer%s %s/address/%s\n\n' "$B" "$R" "$EXPLORER" "$OPERATOR"
  hr; printf '\n'
}

# ---------------------------------------------------------------------------
main() {
  banner
  printf '  %sThis installs a node for the Aetherion Network (chain %s).%s\n' "$GREY" "$CHAIN_ID" "$R"
  printf '  %sIt writes to %s and installs a systemd service.%s\n' "$GREY" "$HOME_DIR" "$R"

  preflight

  step "Node type"
  printf '  %s1%s  Full node    %s— syncs, serves RPC, relays blocks. No stake needed.%s\n' "$B" "$R" "$GREY" "$R"
  printf '  %s2%s  Validator    %s— everything above, plus produces blocks and earns\n                  rewards. Locks at least %s AETH as stake.%s\n\n' "$B" "$R" "$GREY" "$MIN_STAKE" "$R"
  local choice; choice=$(ask_key "Which one?" "${AETH_MODE:-1}" "12")
  case "$choice" in
    2|validator|v) MODE="validator" ;;
    *)             MODE="full" ;;
  esac
  ok "Installing as: $MODE node"

  install_binary
  install_genesis
  generate_keys
  backup_keys

  # Start syncing before anything else that waits. A fresh node has the whole chain
  # to download, and there is no reason for that to sit idle behind a funding prompt.
  # Sealing is a no-op until the registry lists this operator as active, so a
  # validator can safely run with --seal from the start.
  install_service

  if [ "$MODE" = "validator" ]; then
    step "Funding"
    await_funding
    join_validator_set
  fi

  summary
}

main "$@"
