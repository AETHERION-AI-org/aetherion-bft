#!/usr/bin/env bash
#
#   Roll a new node binary across the Aetherion Network validators.
#
#     ./upgrade-nodes.sh --binary ./aetherion-bft-linux-amd64            # dry run
#     ./upgrade-nodes.sh --binary ./aetherion-bft-linux-amd64 --confirm  # do it
#     ./upgrade-nodes.sh --rollback                                      # put the old one back
#
#   Three things this script exists to get right, all of them learned the hard way.
#
#   Nodes must run the same binary. A node whose build predates a fork cannot execute the
#   fork's system transaction, so it rejects the block, and its sync stops dead at the
#   boundary while looking merely "behind". One of ours sat like that for 30 hours.
#
#   The sealing node goes last. While it is the only active validator, stopping it stops
#   the chain: there is nobody else to produce a block. Every other node can be restarted
#   with no consequence at all, so they go first and prove the binary before the one that
#   matters is touched.
#
#   Every node keeps its previous binary. A rollback that needs a download is not a
#   rollback.
#
set -euo pipefail

# node#1 is the genesis node: public RPC, explorer, bootnode, and currently the only
# validator producing blocks. Ordered last on purpose.
NODES=(
  "node2:46.224.18.225:/opt/validator2/aetherion-data:manual"
  "node3:95.216.190.151:/opt/aetherion/data:systemd"
  "node4:62.238.20.59:/opt/aetherion/data:systemd"
  "node1:89.167.111.230:/opt/aetherion-data:systemd"
)

BIN_PATH="/usr/local/bin/polygon-edge"
BIN_PATH_INSTALLER="/usr/local/bin/aetherion-bft"
PUBLIC_RPC="https://rpc.aetherion-ai.org"
SSH_OPTS="-o ConnectTimeout=20 -o StrictHostKeyChecking=accept-new"

BINARY=""
CONFIRM=""
ROLLBACK=""

while [ $# -gt 0 ]; do
  case "$1" in
    --binary)   BINARY="$2"; shift 2 ;;
    --confirm)  CONFIRM=1; shift ;;
    --rollback) ROLLBACK=1; shift ;;
    *) echo "unknown argument: $1" >&2; exit 1 ;;
  esac
done

B=$'\033[1m'; R=$'\033[0m'
BLUE=$'\033[38;2;45;125;255m'; SOFT=$'\033[38;2;110;168;255m'
CYAN=$'\033[38;2;47;242;232m'; RED=$'\033[38;2;216;76;76m'; GREY=$'\033[38;2;138;147;166m'

step() { printf '\n%s%s▸ %s%s\n' "$B" "$BLUE" "$1" "$R"; }
ok()   { printf '  %s✓%s %s\n' "$CYAN" "$R" "$1"; }
info() { printf '  %s·%s %s\n' "$GREY" "$R" "$1"; }
warn() { printf '  %s!%s %s\n' "$SOFT" "$R" "$1"; }
die()  { printf '\n  %s✗ %s%s\n\n' "$RED" "$1" "$R" >&2; exit 1; }

chain_head() {
  curl -fsS --max-time 10 -X POST "$PUBLIC_RPC" -H 'Content-Type: application/json' \
    -d '{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}' 2>/dev/null \
    | sed -n 's/.*"result":"\([^"]*\)".*/\1/p' || true
}

# The chain must be producing before we touch anything, and again after each node. A node
# that comes back healthy while the chain has stopped is not a success.
assert_chain_alive() {
  local a b
  a=$(chain_head); [ -n "$a" ] || die "Chain RPC is not answering. Refusing to upgrade anything."
  sleep 6
  b=$(chain_head); [ -n "$b" ] || die "Chain RPC stopped answering."
  if [ $((b)) -le $((a)) ]; then
    die "Chain is not producing blocks (stuck at $((a))). Refusing to continue."
  fi
  ok "Chain alive: $((a)) -> $((b))"
}

node_bin() {  # which path this node's binary lives at
  local ip="$1"
  if ssh $SSH_OPTS "root@$ip" "[ -x $BIN_PATH_INSTALLER ]" 2>/dev/null; then
    echo "$BIN_PATH_INSTALLER"
  else
    echo "$BIN_PATH"
  fi
}

stop_node() {  # stop_node <ip> <mode>
  local ip="$1" mode="$2"
  if [ "$mode" = "systemd" ]; then
    ssh $SSH_OPTS "root@$ip" 'systemctl stop aetherion-node 2>/dev/null || true' || true
  else
    ssh $SSH_OPTS "root@$ip" 'pkill -f "polygon-edge server" 2>/dev/null || true' || true
  fi
  ssh $SSH_OPTS "root@$ip" 'for i in $(seq 1 15); do pgrep -f "server --data-dir" >/dev/null || exit 0; sleep 1; done; exit 1' \
    || warn "process still up after 15s; forcing"
  ssh $SSH_OPTS "root@$ip" 'pkill -9 -f "server --data-dir" 2>/dev/null || true' || true
}

start_node() {  # start_node <ip> <mode> <datadir>
  local ip="$1" mode="$2" dir="$3"
  if [ "$mode" = "systemd" ]; then
    ssh $SSH_OPTS "root@$ip" 'systemctl start aetherion-node'
  else
    # node2 predates the installer and runs from a raw setsid, not a unit.
    ssh $SSH_OPTS "root@$ip" "cd $(dirname "$dir") && setsid $BIN_PATH server \
      --data-dir $dir --chain $dir/genesis.json \
      --grpc-address 127.0.0.1:9632 --libp2p 0.0.0.0:1478 --jsonrpc 127.0.0.1:8545 \
      --seal --log-level INFO >$(dirname "$dir")/node.log 2>&1 </dev/null & echo started"
  fi
}

# A node is healthy when its own RPC answers AND its height is moving. "The process is up"
# is not health: the node that stalled at the fork boundary had a perfectly live process.
verify_node() {  # verify_node <name> <ip>
  local name="$1" ip="$2" a b
  for _ in $(seq 1 30); do
    a=$(ssh $SSH_OPTS "root@$ip" 'curl -fsS --max-time 4 -X POST http://127.0.0.1:8545 -H "Content-Type: application/json" -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"eth_blockNumber\",\"params\":[]}" 2>/dev/null | sed -n "s/.*\"result\":\"\([^\"]*\)\".*/\1/p"' 2>/dev/null || true)
    [ -n "$a" ] && break
    sleep 2
  done
  [ -n "$a" ] || { warn "$name: RPC never answered"; return 1; }

  sleep 12
  b=$(ssh $SSH_OPTS "root@$ip" 'curl -fsS --max-time 4 -X POST http://127.0.0.1:8545 -H "Content-Type: application/json" -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"eth_blockNumber\",\"params\":[]}" 2>/dev/null | sed -n "s/.*\"result\":\"\([^\"]*\)\".*/\1/p"' 2>/dev/null || true)
  [ -n "$b" ] || { warn "$name: RPC stopped answering"; return 1; }

  if [ $((b)) -le $((a)) ]; then
    warn "$name: height stuck at $((a)) after 12s"
    ssh $SSH_OPTS "root@$ip" 'journalctl -u aetherion-node -n 3 --no-pager 2>/dev/null | tail -2' || true

    return 1
  fi

  ok "$name: healthy ($((a)) -> $((b)))"

  return 0
}

rollback_node() {  # rollback_node <name> <ip> <mode> <datadir>
  local name="$1" ip="$2" mode="$3" dir="$4" bin
  bin=$(node_bin "$ip")
  warn "$name: rolling back"
  stop_node "$ip" "$mode"
  ssh $SSH_OPTS "root@$ip" "[ -f $bin.previous ] && cp -f $bin.previous $bin && chmod 0755 $bin" \
    || die "$name: no previous binary to roll back to. Fix by hand."
  start_node "$ip" "$mode" "$dir"
  verify_node "$name" "$ip" && ok "$name: rolled back and healthy" || die "$name: rollback did not recover. Fix by hand."
}

# ---------------------------------------------------------------------------
if [ -n "$ROLLBACK" ]; then
  step "Rolling every node back to its previous binary"
  for entry in "${NODES[@]}"; do
    IFS=: read -r name ip dir mode <<<"$entry"
    rollback_node "$name" "$ip" "$mode" "$dir"
  done
  assert_chain_alive
  exit 0
fi

[ -n "$BINARY" ] || die "Need --binary <path to the new linux/amd64 binary>"
[ -f "$BINARY" ] || die "No such file: $BINARY"

NEW_SHA=$(sha256sum "$BINARY" | awk '{print $1}')

step "Plan"
info "New binary: $BINARY"
info "sha256:     $NEW_SHA"
printf '\n  %-7s %-18s %-10s %s\n' "NODE" "ADDRESS" "SERVICE" "CURRENT SHA"
for entry in "${NODES[@]}"; do
  IFS=: read -r name ip dir mode <<<"$entry"
  bin=$(node_bin "$ip")
  cur=$(ssh $SSH_OPTS "root@$ip" "sha256sum $bin 2>/dev/null | cut -c1-16" 2>/dev/null || echo "unreachable")
  same=""
  [ "${cur}" = "${NEW_SHA:0:16}" ] && same="  (already current)"
  printf '  %-7s %-18s %-10s %s%s\n' "$name" "$ip" "$mode" "$cur" "$same"
done
printf '\n  %sOrder matters: node1 is last. It is the only validator producing blocks, so\n  its downtime is the chain'"'"'s downtime. The others prove the binary first.%s\n' "$GREY" "$R"

if [ -z "$CONFIRM" ]; then
  printf '\n  %sDry run. Re-run with --confirm to upgrade.%s\n\n' "$SOFT" "$R"
  exit 0
fi

step "Before touching anything"
assert_chain_alive

for entry in "${NODES[@]}"; do
  IFS=: read -r name ip dir mode <<<"$entry"
  bin=$(node_bin "$ip")

  step "$name ($ip)"

  cur=$(ssh $SSH_OPTS "root@$ip" "sha256sum $bin 2>/dev/null | awk '{print \$1}'" 2>/dev/null || echo "")
  if [ "$cur" = "$NEW_SHA" ]; then
    ok "already on this binary, skipping"
    continue
  fi

  info "keeping the current binary as $bin.previous"
  ssh $SSH_OPTS "root@$ip" "cp -f $bin $bin.previous" || die "$name: could not save a rollback copy"

  info "uploading"
  scp -q $SSH_OPTS "$BINARY" "root@$ip:$bin.new" || die "$name: upload failed"

  got=$(ssh $SSH_OPTS "root@$ip" "sha256sum $bin.new | awk '{print \$1}'")
  [ "$got" = "$NEW_SHA" ] || die "$name: uploaded binary hashes $got, expected $NEW_SHA"
  ok "uploaded and hash-verified"

  info "stopping"
  stop_node "$ip" "$mode"

  ssh $SSH_OPTS "root@$ip" "mv -f $bin.new $bin && chmod 0755 $bin"
  ok "binary swapped"

  info "starting"
  start_node "$ip" "$mode" "$dir" >/dev/null

  if ! verify_node "$name" "$ip"; then
    rollback_node "$name" "$ip" "$mode" "$dir"
    die "$name did not come back healthy. It is rolled back; the rest are untouched."
  fi

  # A node can be healthy on its own and still have hurt the chain: that is precisely what
  # happens when its build disagrees with the others about a block.
  assert_chain_alive
done

step "Done"
assert_chain_alive
printf '\n  %-7s %s\n' "NODE" "SHA"
for entry in "${NODES[@]}"; do
  IFS=: read -r name ip dir mode <<<"$entry"
  bin=$(node_bin "$ip")
  printf '  %-7s %s\n' "$name" "$(ssh $SSH_OPTS "root@$ip" "sha256sum $bin | cut -c1-16" 2>/dev/null || echo '?')"
done
printf '\n  %sEvery node above must show the same sha. Different builds disagree about blocks,\n  and the disagreement only surfaces at the next fork boundary.%s\n\n' "$GREY" "$R"
