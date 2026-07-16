<div align="center">

# AETHERION BFT

**The high-performance Byzantine fault tolerant consensus client powering the Aetherion Network.**

Sub-second finality · EVM-equivalent execution · native AETH · deterministic epoch emission

[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-2D7DFF.svg)](./LICENSE)
[![Chain ID](https://img.shields.io/badge/chain--id-100892-34D399.svg)](#network-parameters)
[![Go](https://img.shields.io/badge/go-1.20%2B-6EA8FF.svg)](https://go.dev)

</div>

---

## Overview

**AETHERION BFT** is the reference node implementation of the **Aetherion Network** — a Layer-1,
EVM-equivalent blockchain built around a modern Byzantine fault tolerant consensus engine.

The protocol is designed for one thing above all: **verifiable, operator-free economics**. Block
production, validator rewards and the network's AETH emission are executed by the chain itself as
part of producing each block. There is no privileged wallet that "runs" the distribution and no step
where a human can redirect it. Every figure the network reports can be replayed by anyone against a
public RPC.

- **AETHERION BFT consensus** — an optimistic fast-path for validator agreement with single-round
  finality for the vast majority of blocks. Byzantine fault tolerant with deterministic guarantees.
- **EVM-equivalent execution** — deploy and run existing Ethereum smart contracts, tooling and
  wallets unchanged.
- **Native AETH** — AETH is the network's native asset: gas, staking, governance and rewards.
- **Deterministic emission with halving** — a fixed genesis supply and a per-epoch reward that only
  ever halves on a published schedule. Monetary policy is a formula, not a meeting.
- **Native validator staking & delegation** — stake-weighted rewards with hard-capped voting power,
  so no single operator can seize the chain regardless of stake.
- **BLS-backed validator set** — proof-of-possession verified BLS keys secure the validator set and
  its epoch transitions.

## Network parameters

| Parameter            | Value                                    |
| -------------------- | ---------------------------------------- |
| Network name         | Aetherion Network                        |
| Chain ID             | `100892`                                 |
| Native currency      | AETH (18 decimals)                       |
| Consensus            | AETHERION BFT                            |
| Total supply         | 21,000,000 AETH (fixed at genesis)       |
| Epoch length         | 300 blocks (~10 minutes)                 |
| Emission             | 50 AETH per epoch, halving every 210,240 epochs (~4 years) |
| Public RPC           | `https://rpc.aetherion-ai.org`           |
| Explorer             | `https://explorer.aetherion-ai.org`      |

## Building from source

Requirements: **Go 1.20+**, `make`, and a C toolchain.

```bash
git clone https://github.com/AETHERION-AI-org/aetherion-bft.git
cd aetherion-bft
go build -o aetherion-bft .
```

This produces the `aetherion-bft` node binary in the working directory.

## Joining the network

A new node syncs from genesis by dialing the network's bootstrap nodes. Point your node at the
public bootnodes below and it will discover the rest of the validator set automatically.

### Bootstrap nodes

```
/ip4/89.167.111.230/tcp/1478/p2p/16Uiu2HAmLoUGNMxjpdZfPuq6NGhSCiZivGQw9GEh8BaMXA3vUwW4
/ip4/46.224.18.225/tcp/1478/p2p/16Uiu2HAkzpcTyxTZG92G3P53xatp8BAXucakaTPmQHL6ErHF992z
```

### Run a full (non-validating) node

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

Once synced, the node exposes a standard JSON-RPC endpoint at `http://127.0.0.1:8545` that is
compatible with existing Ethereum clients and tooling.

### Become a validator

Validators produce blocks and earn stake-weighted rewards. Initialise your node's secrets, register
the resulting BLS and ECDSA public keys with the on-chain validator registry, then run the node with
sealing enabled:

```bash
# Generate the node's validator keys (public output only)
./aetherion-bft secrets init --data-dir ./aetherion-data

# Inspect the public identifiers to register on-chain
./aetherion-bft secrets output --data-dir ./aetherion-data

# Run as a sealing validator
./aetherion-bft server \
  --data-dir ./aetherion-data \
  --chain genesis.json \
  --libp2p 0.0.0.0:1478 \
  --jsonrpc 0.0.0.0:8545 \
  --seal \
  --log-level INFO
```

Voting weight is capped per validator, so additional stake increases rewards but never grants a
single operator control of consensus.

## JSON-RPC

AETHERION BFT serves the standard Ethereum JSON-RPC API (`eth_*`, `net_*`, `web3_*`, plus
subscriptions over WebSocket). Any Ethereum-compatible library, wallet or block explorer works out
of the box against the network's chain ID `100892`.

## Repository layout

| Path            | Contents                                                         |
| --------------- | --------------------------------------------------------------- |
| `consensus/`    | AETHERION BFT consensus engine, validator set and epoch logic   |
| `state/`        | EVM execution, state trie and the emission / halving schedule   |
| `blockchain/`   | Block import, storage and the canonical chain                   |
| `txpool/`       | Transaction pool and gossip                                     |
| `jsonrpc/`      | JSON-RPC and WebSocket API                                      |
| `network/`      | libp2p peer-to-peer networking and discovery                    |
| `crypto/` `bls/`| ECDSA and BLS cryptography, proof-of-possession                 |
| `command/`      | The `aetherion-bft` CLI (server, secrets, genesis, and more)    |
| `server/`       | Node assembly and lifecycle                                     |
| `contracts/`    | System and network contract bindings                            |

## Security

Please report vulnerabilities responsibly. See [`SECURITY.md`](./SECURITY.md).

## License

Licensed under the **Apache License, Version 2.0**. See [`LICENSE`](./LICENSE) for the full text.
