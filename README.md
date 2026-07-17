<div align="center">

<br>

# ⬢ &nbsp;A E T H E R I O N &nbsp; B F T

### The Byzantine fault tolerant consensus client powering the **Aetherion Network**

**Sub-second finality** &nbsp;·&nbsp; **EVM-equivalent** &nbsp;·&nbsp; **Native AETH** &nbsp;·&nbsp; **Operator-free emission**

<br>

[![License](https://img.shields.io/badge/license-Apache_2.0-2D7DFF?style=for-the-badge&labelColor=05080F)](./LICENSE)
[![Chain ID](https://img.shields.io/badge/chain_id-100892-34D399?style=for-the-badge&labelColor=05080F)](#-network-parameters)
[![Consensus](https://img.shields.io/badge/consensus-AETHERION_BFT-6EA8FF?style=for-the-badge&labelColor=05080F)](#-architecture)
[![Go](https://img.shields.io/badge/go-1.20+-9FC2FF?style=for-the-badge&labelColor=05080F)](https://go.dev)

<br>

[**Quickstart**](#-quickstart) &nbsp;•&nbsp; [**Bootstrap nodes**](#-bootstrap-nodes) &nbsp;•&nbsp; [**Run a validator**](#️-run-a-validator) &nbsp;•&nbsp; [**Explorer**](https://explorer.aetherion-ai.org) &nbsp;•&nbsp; [**RPC**](https://rpc.aetherion-ai.org)

<br>

</div>

---

> [!IMPORTANT]
> **Economics you don't have to trust.**
> Block production, validator rewards and the entire AETH emission are executed by the chain itself,
> as part of producing each block. There is no privileged wallet that "runs" the distribution, and no
> step where a human can redirect it. Every number the network reports can be replayed by anyone
> against a public RPC.

<br>

## ✨ Highlights

|             | Capability | What it means |
| :---------: | :--------- | :------------ |
| ⚡ | **AETHERION BFT consensus** | Optimistic fast-path validator agreement with single-round finality for the vast majority of blocks. Byzantine fault tolerant, with deterministic guarantees. |
| 🔷 | **EVM-equivalent execution** | Existing Ethereum contracts, tooling and wallets run unchanged. No rewrites, no surprises. |
| 🪙 | **Native AETH** | AETH is the network's native asset: gas, staking, governance and rewards, all in one unit. |
| 📉 | **Deterministic emission** | A fixed genesis supply and a per-epoch reward that only ever halves, on a published schedule. Monetary policy is a formula, not a meeting. |
| 🛡️ | **Capped validator power** | Stake-weighted rewards with hard-capped voting weight, so no operator can seize the chain regardless of stake. |
| 🔐 | **BLS-secured validator set** | Proof-of-possession verified BLS keys secure the validator set and every epoch transition. |

<br>

## 🌐 Network parameters

<div align="center">

| Parameter | Value |
| :-- | :-- |
| **Network** | Aetherion Network |
| **Chain ID** | `100892` |
| **Native currency** | `AETH` (18 decimals) |
| **Consensus** | AETHERION BFT |
| **Total supply** | `21,000,000 AETH`, fixed at genesis |
| **Epoch length** | `300` blocks · ~10 minutes |
| **Emission** | `50 AETH` / epoch, halving every `210,240` epochs (~4 years) |
| **Public RPC** | <https://rpc.aetherion-ai.org> |
| **Explorer** | <https://explorer.aetherion-ai.org> |

</div>

<br>

## 🏗 Architecture

```mermaid
flowchart TB
    subgraph P2P["🌐 Networking"]
        LP["libp2p discovery<br/>gossip · sync"]
    end
    subgraph CON["⚡ AETHERION BFT"]
        VS["Validator set<br/>BLS + PoP"]
        SEAL["Block sealing<br/>single-round finality"]
        EP["Epoch transition<br/>rewards + emission"]
    end
    subgraph EXE["🔷 Execution"]
        EVM["EVM<br/>EVM-equivalent"]
        TRIE["State trie"]
        HALV["Emission schedule<br/>halving"]
    end
    subgraph API["🔌 Interfaces"]
        RPC["JSON-RPC · WS"]
        GRPC["gRPC"]
    end

    LP --> CON
    VS --> SEAL --> EP
    EP --> HALV
    SEAL --> EVM --> TRIE
    HALV --> TRIE
    EXE --> API
```

Each epoch is a fixed span of **300 blocks**. At its boundary the protocol emits a *system
transaction*: no wallet signs it and no person presses a button. Releasing the reward and splitting
it is simply part of producing the block.

```mermaid
flowchart LR
    B["300 blocks<br/>≈ 10 min"] --> E["Epoch close"]
    E --> S["System transaction<br/>(no signer)"]
    S --> R["50 AETH released"]
    R --> D["Split across<br/>protocol pools"]
    D --> V["Validators paid<br/>stake-weighted"]
```

<br>

## ⚡ Quickstart

One command. It installs a full node by default, or walks you through becoming a
validator.

```bash
curl -fsSL https://raw.githubusercontent.com/AETHERION-AI-org/aetherion-bft/main/scripts/install.sh | sudo bash
```

It downloads the release binary and checks it against the published SHA256SUMS, fetches
the network's genesis, generates your keys, hands you an encrypted backup of them over a
one-time link, and installs a systemd service that survives reboots.

> [!TIP]
> Interrupted? Run it again. It resumes where it left off rather than starting over, and
> it never repeats anything that costs money.

<details>
<summary><b>Build from source instead</b></summary>

<br>

**Requirements:** Go `1.20+`, `make`, and a C toolchain.

```bash
git clone https://github.com/AETHERION-AI-org/aetherion-bft.git
cd aetherion-bft
CGO_ENABLED=0 go build -trimpath -o aetherion-bft .
```

This produces the `aetherion-bft` node binary in the working directory. The network's
`genesis.json` is in the repository root.

</details>

<br>

## 🔗 Bootstrap nodes

A new node syncs from genesis by dialing the network's bootstrap nodes, then discovers the rest of
the validator set automatically.

```text
/ip4/89.167.111.230/tcp/1478/p2p/16Uiu2HAmLoUGNMxjpdZfPuq6NGhSCiZivGQw9GEh8BaMXA3vUwW4
/ip4/46.224.18.225/tcp/1478/p2p/16Uiu2HAkzpcTyxTZG92G3P53xatp8BAXucakaTPmQHL6ErHF992z
```

### Run a full node

```bash
./aetherion-bft server \
  --data-dir ./aetherion-data \
  --chain genesis.json \
  --libp2p 0.0.0.0:1478 \
  --jsonrpc 0.0.0.0:8545 \
  --bootnode /ip4/89.167.111.230/tcp/1478/p2p/16Uiu2HAmLoUGNMxjpdZfPuq6NGhSCiZivGQw9GEh8BaMXA3vUwW4 \
  --bootnode /ip4/46.224.18.225/tcp/1478/p2p/16Uiu2HAkzpcTyxTZG92G3P53xatp8BAXucakaTPmQHL6ErHF992z \
  --log-level INFO
```

> [!TIP]
> Once synced, the node serves a standard Ethereum JSON-RPC endpoint on `http://127.0.0.1:8545`.
> Point any Ethereum library, wallet or explorer at chain ID `100892` and it works out of the box.

<br>

## 🛡️ Run a validator

Validators produce blocks and earn stake-weighted rewards. **Nobody approves you.** The
chain admits an operator on two facts it verifies for itself: a BLS proof-of-possession,
which proves you hold the block-signing key, and being the transaction's sender, which
proves you hold the operator key. The only barrier is the deposit, and that one is real.

```bash
curl -fsSL https://raw.githubusercontent.com/AETHERION-AI-org/aetherion-bft/main/scripts/install.sh | sudo bash
```

Choose **Validator** and the installer does the rest: it shows you the address to fund,
waits for the AETH to arrive, registers you on-chain, waits until your node has caught up
with the chain, and locks your stake.

| Step | What happens |
| :-- | :-- |
| **Fund** | You send at least `1000 AETH` to the operator address it prints. The deposit is locked, not spent, and can be unbonded later. |
| **Register** | The node calls `registerSelf(blsKey, proofOfPossession)`. The registry verifies the proof against the network's BLS precompile. |
| **Sync** | The node replays the chain. This takes hours. |
| **Stake** | The deposit is locked and you join the block-producing set at the next epoch boundary. |

> [!IMPORTANT]
> Staking waits for your node to finish syncing, and that is not a formality. A validator
> that joins the set while still catching up cannot sign the blocks it is now responsible
> for, and its voting power counts against the quorum everyone else needs. The installer
> will not let you do it.

> [!CAUTION]
> Your keys live in `/opt/aetherion/data` and must **never** leave the machine. The
> installer encrypts a backup of them locally, before it touches the network, and serves
> it over a link that dies the moment you confirm the download. Lose them and you lose
> the node's identity and its stake. There is no recovery.

<details>
<summary><b>Doing it by hand</b></summary>

<br>

```bash
# generate keys
aetherion-bft secrets init --data-dir /opt/aetherion/data --insecure

# your operator address and node id (public, safe to share)
aetherion-bft secrets output --data-dir /opt/aetherion/data --validator
aetherion-bft secrets output --data-dir /opt/aetherion/data --node-id

# fund the operator address, then register yourself
aetherion-bft validator-register --data-dir /opt/aetherion/data \
  --registry 0x6ebA8468F754404C1c93ae94C2D1973683eb749A --insecure

# once synced, lock the deposit (amount in wei)
aetherion-bft validator-stake --data-dir /opt/aetherion/data \
  --registry 0x6ebA8468F754404C1c93ae94C2D1973683eb749A \
  --amount 1000000000000000000000 --insecure
```

`validator-pop` prints the proof-of-possession on its own if you would rather submit the
registration from a wallet. Both keys are read locally, used to sign, and never printed.

</details>

<br>

## 📉 Emission schedule

The per-epoch reward can only ever shrink. It is arithmetic, not a decision.

<div align="center">

| Era | Reward / epoch | Approx. duration |
| :-: | :-- | :-- |
| **1** | `50.0000 AETH` | now → ~4 years |
| **2** | `25.0000 AETH` | ~4 → ~8 years |
| **3** | `12.5000 AETH` | ~8 → ~12 years |
| **4** | `6.2500 AETH` | ~12 → ~16 years |
| **…** | halving every `210,240` epochs | → `21,000,000 AETH` cap |

</div>

<br>

## 📁 Repository layout

| Path | Contents |
| :-- | :-- |
| `consensus/` | AETHERION BFT engine, validator set, epoch logic |
| `state/` | EVM execution, state trie, emission & halving schedule |
| `blockchain/` | Block import, storage, canonical chain |
| `txpool/` | Transaction pool and gossip |
| `jsonrpc/` | JSON-RPC and WebSocket API |
| `network/` | libp2p peer-to-peer networking and discovery |
| `crypto/` · `bls/` | ECDSA and BLS cryptography, proof-of-possession |
| `command/` | The `aetherion-bft` CLI (`server`, `secrets`, `genesis`, …) |
| `server/` | Node assembly and lifecycle |
| `contracts/` | System and network contract bindings |

<br>

## 🔒 Security

Found a vulnerability? Please report it responsibly: see [`SECURITY.md`](./SECURITY.md).
Do **not** open a public issue for security-sensitive reports.

<br>

## 📄 License

Licensed under the **Apache License 2.0**. See [`LICENSE`](./LICENSE).

<br>

<div align="center">

<sub>⬢ &nbsp;Built for a network whose numbers you can check yourself.</sub>

</div>
