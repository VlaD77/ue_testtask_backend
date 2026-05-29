# Encounter Backend

Backend for the Unreal co-op encounter demo. It keeps the world seed/config, completed encounter IDs, region stream state, and lightweight party presence.

There are two supported ways to run it:

- **Node REST**, fastest for local testing and simple hosting.
- **Nakama**, closer to a real multiplayer backend stack, with PostgreSQL and Nakama Storage.

Both expose the same UE-facing HTTP routes. The Unreal client only changes the base URL/provider.

## Option A: Node REST

Run directly:

```bash
npm start
```

Or with Docker:

```bash
docker compose up --build
```

Default URL:

```text
http://127.0.0.1:8080
```

Health check:

```bash
curl -sS http://127.0.0.1:8080/health
```

The Node server stores data in `data/progress.json` by default. For hosting, point `DATA_FILE` at a persistent volume:

```bash
HOST=0.0.0.0 PORT=8080 DATA_FILE=/var/lib/encounters/progress.json npm start
```

## Option B: Nakama

Run from the Nakama folder:

```bash
cd nakama
docker compose up -d --build
```

Default UE-facing URL:

```text
http://127.0.0.1:8081
```

Health check:

```bash
curl -sS http://127.0.0.1:8081/health
```

Nakama Console:

```text
http://127.0.0.1:7351
```

Development login:

```text
admin / password
```

## UE Provider Switch

The client config contains both URLs:

```ini
BackendBaseUrl="http://127.0.0.1:8080"
NakamaBaseUrl="http://127.0.0.1:8081"
```

In PIE, switch without rebuilding:

```text
EncounterDebugUseLocalBackend
EncounterDebugUseNakamaBackend
```

For a shared LAN or hosted Unreal session, pass the provider and backend URLs at UE startup instead of editing config files on every machine:

```bash
# Host listen server through Nakama-compatible REST
BACKEND_PROVIDER=Nakama BACKEND_HOST=192.168.1.50 \
  ../client/BuildScripts/Run_ListenServer_Mac.sh
```

```bash
# Join the same UE server and same backend party
BACKEND_PROVIDER=Nakama BACKEND_HOST=192.168.1.50 PLAYER_ID=player02 \
  ../client/BuildScripts/Join_ListenServer_Mac.sh 192.168.1.50:7777
```

Use `BACKEND_PROVIDER=Node` for the Node service on `8080`. The UE listen/dedicated server is the authoritative gameplay server for replicated players, enemies, chests, and encounter state. Node/Nakama keeps the shared service state: world seed, party runtime generation, completed encounter IDs, region stream-state, and optional remote-party presence.

## API Used By UE

OpenAPI spec: `openapi.yaml`

```text
GET    /health
GET    /worlds/:worldId/seed
GET    /worlds/:worldId/regions/:regionId/stream-state
POST   /worlds/:worldId/regions/:regionId/stream-state
GET    /worlds/:worldId/parties/:partyId/players
GET    /worlds/:worldId/parties/:partyId/runtime-state
POST   /worlds/:worldId/parties/:partyId/players/:playerId/pose
GET    /worlds/:worldId/regions/:regionId/parties/:partyId/players/:playerId/encounters/completed
POST   /worlds/:worldId/regions/:regionId/parties/:partyId/players/:playerId/encounters/:encounterId/completed
DELETE /worlds/:worldId/regions/:regionId/parties/:partyId/players/:playerId/encounters/completed
```

Node also keeps the older `/players/:playerId/encounters/...` routes so old local data and simple manual tests still work.

## World Config

`GET /worlds/demo-world/seed` returns the shared deterministic world setup:

```json
{
  "worldId": "demo-world",
  "seed": 421337,
  "config": {
    "tileSize": 3900,
    "gridColumns": 10,
    "gridRows": 8,
    "worldWidth": 39000,
    "worldHeight": 31200
  }
}
```

The Node version can override these with:

```text
WORLD_SEED
WORLD_TILE_SIZE
WORLD_GRID_COLUMNS
WORLD_GRID_ROWS
```

## Data Model

The service stores:

- completed encounter IDs by `worldId / regionId / partyId / playerId`;
- party-level completed IDs, so co-op members can share encounter progress;
- stream state by `worldId / regionId`;
- fresh player poses by `worldId / partyId / playerId`;
- party runtime state by `worldId / partyId`;
- backend metadata (`backend`, `storageOwner`, `source`) to make Node and Nakama responses easy to compare.

Party poses expire from responses after `PLAYER_POSE_TTL_MS` in Node, default `15000`. Nakama uses the same 15 second TTL in the runtime module.

Party runtime state tracks `generation` and `runtimeSeed` for transient gameplay like mobs and chests. When a party had active players and then becomes empty, the backend advances the generation, calculates a new deterministic runtime seed from the world seed and party ID, and marks transient region stream state as unloaded. Completed encounter progress is not cleared. This gives the next player session a fresh but deterministic set of mobs/chests while keeping the world seed stable.

Node can disable this reset for testing:

```text
PARTY_EMPTY_REGENERATION_ENABLED=false
```

## Quick Smoke Test

```bash
curl -sS http://127.0.0.1:8080/health
curl -sS http://127.0.0.1:8080/worlds/demo-world/seed
curl -sS -X POST http://127.0.0.1:8080/worlds/demo-world/regions/tile_r00_c00_forest/stream-state \
  -H 'content-type: application/json' \
  -d '{"loaded":true,"nearbyPlayerCount":2,"partyId":"demo-party","source":"ue-server"}'
curl -sS http://127.0.0.1:8080/worlds/demo-world/parties/demo-party/runtime-state
```

Use port `8081` for the same calls when Nakama is running.
