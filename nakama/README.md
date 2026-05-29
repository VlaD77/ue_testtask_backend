# Nakama Backend

Nakama/PostgreSQL implementation of the same backend contract used by the Node REST server.

Use this when you want the demo to run against a real multiplayer backend stack instead of the local JSON-file service.

## What Runs

- Nakama `3.31.0`
- PostgreSQL `15`
- Go runtime plugin `encounter.so`
- UE-facing HTTP routes on host port `8081`
- Nakama Console on host port `7351`

The Unreal client can switch to this backend in PIE:

```text
EncounterDebugUseNakamaBackend
```

## Run

From the backend repository root:

```bash
cd nakama
docker compose up -d --build
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

## API

The routes match the Node REST backend:

```text
GET    /health
GET    /worlds/:worldId/seed
GET    /worlds/:worldId/parties/:partyId/players
POST   /worlds/:worldId/parties/:partyId/players/:playerId/pose
GET    /worlds/:worldId/regions/:regionId/stream-state
POST   /worlds/:worldId/regions/:regionId/stream-state
GET    /worlds/:worldId/regions/:regionId/parties/:partyId/players/:playerId/encounters/completed
POST   /worlds/:worldId/regions/:regionId/parties/:partyId/players/:playerId/encounters/:encounterId/completed
DELETE /worlds/:worldId/regions/:regionId/parties/:partyId/players/:playerId/encounters/completed
```

The module also registers Nakama RPCs for the same operations:

```text
encounter_health
encounter_complete_scoped
encounter_region_stream_state
encounter_player_pose
encounter_party_players
```

The current UE client uses the HTTP routes so it can switch between Node and Nakama without adding a Nakama SDK dependency.

## Storage

Data is written to Nakama Storage under a system-owned storage user:

```text
00000000-0000-0000-0000-000000000000
```

Storage collections:

```text
encounter_state
encounter_player_pose
```

World config is fixed to the generated demo world:

```text
seed=421337
tileSize=3900
gridColumns=10
gridRows=8
```

## Stop

```bash
docker compose down
```

Remove persisted PostgreSQL data:

```bash
docker compose down -v
```

## Notes

The Dockerfile uses `heroiclabs/nakama-pluginbuilder:3.31.0` to compile the Go plugin, then copies the plugin into `heroiclabs/nakama:3.31.0`.

On Apple Silicon the compose file pins `linux/amd64`, because the Nakama images used here are amd64. The first build can take a bit longer, but the result is stable and matches the module dependency versions.
