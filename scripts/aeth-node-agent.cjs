/**
 * Aetherion node agent.
 *
 * Runs on our own node, reads the local operator gRPC, and reports the network roster to
 * the indexer every 15 seconds.
 *
 * It lives here because the P2P layer can only be read locally, and the operator gRPC has
 * no business being reachable from the internet: it can add peers. Reporting from our own
 * machine also keeps a promise the published binary makes, that it phones nobody home.
 * Foreign nodes are observed, never asked to participate.
 *
 * What is knowable, and what is not:
 *   - peer id, address   from the P2P layer. A node cannot hide these and stay in the network.
 *   - block height       from the sync protocol, or the node's own public RPC. Unknown when
 *                        it announces nothing and firewalls its RPC. Reported as unknown,
 *                        never guessed.
 *   - operator address   NOT derivable for a foreign node. The libp2p identity and the
 *                        validator key are unrelated, and nothing on chain ties them. Only
 *                        our own nodes are mapped, from KNOWN_OPERATORS below.
 */
const { execSync } = require('child_process');

const BIN = process.env.AETH_BIN || '/usr/local/bin/polygon-edge';
const GRPC = process.env.AETH_GRPC || '127.0.0.1:9632';
const INDEXER = process.env.AETH_INDEXER_URL;
const TOKEN = process.env.AETH_AGENT_TOKEN;
const INTERVAL_MS = Number(process.env.AETH_INTERVAL_MS || 15000);

// Peer id to operator address, for the nodes we run. There is no way to derive this for
// anyone else's node, so anyone else's stays null rather than being invented.
const KNOWN_OPERATORS = JSON.parse(process.env.AETH_KNOWN_OPERATORS || '{}');

const sh = (cmd) => {
  try {
    return execSync(cmd, { encoding: 'utf8', timeout: 15000 });
  } catch {
    return '';
  }
};

async function rpcHeight(ip) {
  try {
    const res = await fetch(`http://${ip}:8545`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'eth_blockNumber', params: [] }),
      signal: AbortSignal.timeout(4000),
    });
    const json = await res.json();
    const n = parseInt(json.result, 16);

    return Number.isFinite(n) ? n : null;
  } catch {
    return null;
  }
}

function selfHeight() {
  const out = sh(
    `curl -s --max-time 4 -X POST http://127.0.0.1:8545 -H 'Content-Type: application/json' ` +
    `-d '{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}'`
  );
  const m = out.match(/"result":"(0x[0-9a-fA-F]+)"/);

  return m ? parseInt(m[1], 16) : null;
}

function selfPeerId() {
  const out = sh(`${BIN} secrets output --data-dir ${process.env.AETH_DATA_DIR || '/opt/aetherion-data'} --node-id`);
  const m = out.match(/16Uiu2HA[A-Za-z0-9]+/);

  return m ? m[0] : null;
}

async function collect() {
  const ids = sh(`${BIN} peers list --grpc-address ${GRPC}`).match(/16Uiu2HA[A-Za-z0-9]+/g) || [];
  const nodes = [];

  const me = selfPeerId();
  if (me) {
    nodes.push({
      peerId: me,
      ipAddress: process.env.AETH_SELF_IP || null,
      port: 1478,
      blockHeight: selfHeight(),
      operatorAddress: KNOWN_OPERATORS[me] || null,
    });
  }

  for (const id of ids) {
    if (id === me) continue;

    const status = sh(`${BIN} peers status --peer-id ${id} --grpc-address ${GRPC}`);
    const addr = status.match(/\/ip4\/([0-9.]+)\/tcp\/(\d+)/);
    const ip = addr ? addr[1] : null;

    // Prefer what the node announced over the sync protocol: it needs no cooperation and
    // no open port. Fall back to its public RPC, and leave it null when neither answers.
    const announced = status.match(/Latest block\s*=\s*(\d+)/);
    const height = announced ? Number(announced[1]) : (ip ? await rpcHeight(ip) : null);

    nodes.push({
      peerId: id,
      ipAddress: ip,
      port: addr ? Number(addr[2]) : null,
      blockHeight: height,
      operatorAddress: KNOWN_OPERATORS[id] || null,
    });
  }

  return nodes;
}

async function report(nodes) {
  const res = await fetch(`${INDEXER}/api/NetworkNodes/report`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Agent-Token': TOKEN },
    body: JSON.stringify({ nodes }),
    signal: AbortSignal.timeout(10000),
  });

  if (!res.ok) {
    throw new Error(`indexer replied ${res.status}`);
  }

  return res.json();
}

async function tick() {
  const nodes = await collect();
  if (nodes.length === 0) {
    console.warn('no peers seen; skipping report');

    return;
  }

  const out = await report(nodes);
  console.log(
    `${new Date().toISOString()} reported ${nodes.length} nodes ` +
    `(${nodes.filter((n) => n.blockHeight !== null).length} with a known height)`,
    JSON.stringify(out)
  );
}

if (!INDEXER || !TOKEN) {
  console.error('AETH_INDEXER_URL and AETH_AGENT_TOKEN are required');
  process.exit(1);
}

// A failed tick means the next one tries again. It never means the agent stops: an agent
// that dies on the first blip stops reporting exactly when something is wrong.
const loop = async () => {
  try {
    await tick();
  } catch (e) {
    console.warn('tick failed:', e.message);
  }
};

loop();
setInterval(loop, INTERVAL_MS);
