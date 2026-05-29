'use strict';

const http = require('node:http');
const fs = require('node:fs/promises');
const path = require('node:path');
const { randomUUID } = require('node:crypto');

const PORT = Number(process.env.PORT || 8080);
const HOST = process.env.HOST || '0.0.0.0';
const DATA_FILE = process.env.DATA_FILE || path.join(__dirname, '..', 'data', 'progress.json');
const PLAYER_POSE_TTL_MS = Number(process.env.PLAYER_POSE_TTL_MS || 15000);
const WORLD_SEED = Number(process.env.WORLD_SEED || 421337);
const WORLD_TILE_SIZE = Number(process.env.WORLD_TILE_SIZE || 3900);
const WORLD_GRID_COLUMNS = Number(process.env.WORLD_GRID_COLUMNS || 10);
const WORLD_GRID_ROWS = Number(process.env.WORLD_GRID_ROWS || 8);
const WORLD_CONFIG = {
  tileSize: WORLD_TILE_SIZE,
  gridColumns: WORLD_GRID_COLUMNS,
  gridRows: WORLD_GRID_ROWS,
  worldWidth: WORLD_TILE_SIZE * WORLD_GRID_COLUMNS,
  worldHeight: WORLD_TILE_SIZE * WORLD_GRID_ROWS
};
let mutationQueue = Promise.resolve();

async function ensureStore() {
  await fs.mkdir(path.dirname(DATA_FILE), { recursive: true });
  try {
    const raw = await fs.readFile(DATA_FILE, 'utf8');
    return JSON.parse(raw);
  } catch (error) {
    if (error.code !== 'ENOENT') {
      throw error;
    }
    const empty = { players: {}, scopes: {}, regionStreamStates: {}, playerPoses: {}, worldSeeds: { 'demo-world': WORLD_SEED } };
    await fs.writeFile(DATA_FILE, JSON.stringify(empty, null, 2));
    return empty;
  }
}

async function saveStore(store) {
  const tempFile = `${DATA_FILE}.${process.pid}.${randomUUID()}.tmp`;
  await fs.writeFile(tempFile, JSON.stringify(store, null, 2));
  await fs.rename(tempFile, DATA_FILE);
}

async function mutateStore(mutator) {
  const mutation = mutationQueue.then(async () => {
    const store = await ensureStore();
    const result = await mutator(store);
    await saveStore(store);
    return result;
  });

  mutationQueue = mutation.catch(() => {});
  return mutation;
}

function sendJson(response, statusCode, payload) {
  const body = JSON.stringify(payload);
  response.writeHead(statusCode, {
    'content-type': 'application/json; charset=utf-8',
    'cache-control': 'no-store',
    'access-control-allow-origin': '*',
    'access-control-allow-methods': 'GET,POST,DELETE,OPTIONS',
    'access-control-allow-headers': 'content-type'
  });
  response.end(body);
}

async function readJson(request) {
  const chunks = [];
  for await (const chunk of request) {
    chunks.push(chunk);
    if (Buffer.concat(chunks).length > 1024 * 1024) {
      throw Object.assign(new Error('Request body is too large'), { statusCode: 413 });
    }
  }

  const raw = Buffer.concat(chunks).toString('utf8').trim();
  return raw.length > 0 ? JSON.parse(raw) : {};
}

function getPlayer(store, playerId) {
  if (!store.players[playerId]) {
    store.players[playerId] = {
      playerId,
      completedEncounterIds: [],
      completions: {}
    };
  }
  return store.players[playerId];
}

function getScopeKey(scope) {
  return `${scope.worldId}:${scope.regionId}:${scope.partyId}`;
}

function getScopedProgress(store, scope, playerId) {
  if (!store.scopes) {
    store.scopes = {};
  }

  const scopeKey = getScopeKey(scope);
  if (!store.scopes[scopeKey]) {
    store.scopes[scopeKey] = {
      ...scope,
      parties: {}
    };
  }

  const scopedWorld = store.scopes[scopeKey];
  if (!scopedWorld.parties[scope.partyId]) {
    scopedWorld.parties[scope.partyId] = {
      partyId: scope.partyId,
      players: {},
      completedEncounterIds: [],
      completions: {}
    };
  }

  const party = scopedWorld.parties[scope.partyId];
  if (!party.players[playerId]) {
    party.players[playerId] = {
      playerId,
      completedEncounterIds: []
    };
  }

  return { scopeKey, scopedWorld, party, player: party.players[playerId] };
}

function completeScopedEncounter(store, scope, playerId, encounterId, body) {
  const now = new Date().toISOString();
  const scoped = getScopedProgress(store, scope, playerId);

  if (!scoped.party.completedEncounterIds.includes(encounterId)) {
    scoped.party.completedEncounterIds.push(encounterId);
  }

  if (!scoped.player.completedEncounterIds.includes(encounterId)) {
    scoped.player.completedEncounterIds.push(encounterId);
  }

  scoped.party.completions[encounterId] = {
    ...scope,
    playerId,
    encounterId,
    completedAt: now,
    reward: body.reward || null,
    source: body.source || 'ue-client'
  };

  return scoped;
}

function getRegionStreamKey(worldId, regionId) {
  return `${worldId}:${regionId}`;
}

function getPartyPoseKey(worldId, partyId) {
  return `${worldId}:${partyId}`;
}

function getActivePartyPoses(store, worldId, partyId) {
  if (!store.playerPoses) {
    store.playerPoses = {};
  }

  const poseKey = getPartyPoseKey(worldId, partyId);
  const partyPoses = store.playerPoses[poseKey] || {};
  const now = Date.now();
  return Object.values(partyPoses).filter((pose) => {
    const updatedAtMs = Date.parse(pose.updatedAt || 0);
    return Number.isFinite(updatedAtMs) && now - updatedAtMs <= PLAYER_POSE_TTL_MS;
  });
}

function parseRoute(request) {
  const url = new URL(request.url, `http://${request.headers.host || 'localhost'}`);
  const parts = url.pathname.split('/').filter(Boolean).map(decodeURIComponent);
  return { url, parts };
}

async function handleRequest(request, response) {
  if (request.method === 'OPTIONS') {
    sendJson(response, 204, {});
    return;
  }

  const requestId = randomUUID();
  const { parts } = parseRoute(request);

  if (request.method === 'GET' && parts.length === 1 && parts[0] === 'health') {
    sendJson(response, 200, {
      ok: true,
      service: 'encounter-progress-backend',
      worldSeed: WORLD_SEED,
      worldConfig: WORLD_CONFIG,
      features: ['encounter-progress', 'region-stream-state', 'party-player-poses', 'world-seed', 'world-config']
    });
    return;
  }

  if (request.method === 'GET' && parts.length === 3 && parts[0] === 'worlds' && parts[2] === 'seed') {
    const store = await ensureStore();
    sendJson(response, 200, {
      worldId: parts[1],
      seed: Number((store.worldSeeds && store.worldSeeds[parts[1]]) || WORLD_SEED),
      config: WORLD_CONFIG
    });
    return;
  }

  if (
    parts.length >= 5 &&
    parts[0] === 'worlds' &&
    parts[2] === 'parties' &&
    parts[4] === 'players'
  ) {
    const worldId = parts[1];
    const partyId = parts[3];

    if (request.method === 'GET' && parts.length === 5) {
      const store = await ensureStore();
      sendJson(response, 200, {
        worldId,
        partyId,
        players: getActivePartyPoses(store, worldId, partyId)
      });
      return;
    }

    if (request.method === 'POST' && parts.length === 7 && parts[6] === 'pose') {
      const playerId = parts[5];
      const body = await readJson(request);
      const poseKey = getPartyPoseKey(worldId, partyId);
      const pose = await mutateStore((store) => {
        if (!store.playerPoses) {
          store.playerPoses = {};
        }
        if (!store.playerPoses[poseKey]) {
          store.playerPoses[poseKey] = {};
        }

        store.playerPoses[poseKey][playerId] = {
          worldId,
          partyId,
          playerId,
          x: Number(body.x || 0),
          y: Number(body.y || 0),
          z: Number(body.z || 0),
          yaw: Number(body.yaw || 0),
          provider: body.provider || null,
          source: body.source || 'ue-client',
          updatedAt: new Date().toISOString()
        };

        return store.playerPoses[poseKey][playerId];
      });

      sendJson(response, 200, {
        ok: true,
        requestId,
        pose
      });
      return;
    }
  }

  if (
    parts.length === 5 &&
    parts[0] === 'worlds' &&
    parts[2] === 'regions' &&
    parts[4] === 'stream-state'
  ) {
    const worldId = parts[1];
    const regionId = parts[3];
    const streamKey = getRegionStreamKey(worldId, regionId);

    if (request.method === 'GET') {
      const store = await ensureStore();
      if (!store.regionStreamStates) {
        store.regionStreamStates = {};
      }
      sendJson(response, 200, store.regionStreamStates[streamKey] || {
        worldId,
        regionId,
        loaded: false,
        nearbyPlayerCount: 0
      });
      return;
    }

    if (request.method === 'POST') {
      const body = await readJson(request);
      const state = await mutateStore((store) => {
        if (!store.regionStreamStates) {
          store.regionStreamStates = {};
        }

        store.regionStreamStates[streamKey] = {
          worldId,
          regionId,
          loaded: Boolean(body.loaded),
          nearbyPlayerCount: Number(body.nearbyPlayerCount || 0),
          partyId: body.partyId || null,
          playerId: body.playerId || null,
          source: body.source || 'ue-server',
          updatedAt: new Date().toISOString()
        };

        return store.regionStreamStates[streamKey];
      });

      sendJson(response, 200, {
        ok: true,
        requestId,
        streamKey,
        ...state
      });
      return;
    }
  }

  if (
    parts.length >= 10 &&
    parts[0] === 'worlds' &&
    parts[2] === 'regions' &&
    parts[4] === 'parties' &&
    parts[6] === 'players' &&
    parts[8] === 'encounters'
  ) {
    const scope = {
      worldId: parts[1],
      regionId: parts[3],
      partyId: parts[5]
    };
    const playerId = parts[7];

    if (request.method === 'GET' && parts.length === 10 && parts[9] === 'completed') {
      const store = await ensureStore();
      const scoped = getScopedProgress(store, scope, playerId);
      sendJson(response, 200, {
        ...scope,
        playerId,
        partyCompletedEncounterIds: scoped.party.completedEncounterIds,
        playerCompletedEncounterIds: scoped.player.completedEncounterIds,
        completions: scoped.party.completions
      });
      return;
    }

    if (request.method === 'POST' && parts.length === 11 && parts[10] === 'completed') {
      const encounterId = parts[9];
      const body = await readJson(request);
      const completed = await mutateStore((store) => completeScopedEncounter(store, scope, playerId, encounterId, body));

      sendJson(response, 200, {
        ok: true,
        requestId,
        scopeKey: completed.scopeKey,
        ...scope,
        playerId,
        encounterId,
        partyCompletedEncounterIds: completed.party.completedEncounterIds,
        playerCompletedEncounterIds: completed.player.completedEncounterIds
      });
      return;
    }

    if (request.method === 'DELETE' && parts.length === 10 && parts[9] === 'completed') {
      await mutateStore((store) => {
        const scoped = getScopedProgress(store, scope, playerId);
        scoped.party.completedEncounterIds = [];
        scoped.party.completions = {};
        scoped.player.completedEncounterIds = [];
      });
      sendJson(response, 200, {
        ok: true,
        requestId,
        ...scope,
        playerId,
        partyCompletedEncounterIds: [],
        playerCompletedEncounterIds: []
      });
      return;
    }
  }

  if (parts.length >= 3 && parts[0] === 'players' && parts[2] === 'encounters') {
    const playerId = parts[1];
    const store = await ensureStore();
    const player = getPlayer(store, playerId);

    if (request.method === 'GET' && parts.length === 4 && parts[3] === 'completed') {
      sendJson(response, 200, {
        playerId,
        completedEncounterIds: player.completedEncounterIds,
        completions: player.completions
      });
      return;
    }

    if (request.method === 'POST' && parts.length === 5 && parts[4] === 'completed') {
      const encounterId = parts[3];
      const body = await readJson(request);
      const scope = {
        worldId: body.worldId || 'demo-world',
        regionId: body.regionId || 'demo-region',
        partyId: body.partyId || 'solo'
      };

      const completed = await mutateStore((store) => {
        const player = getPlayer(store, playerId);
        const now = new Date().toISOString();

        if (!player.completedEncounterIds.includes(encounterId)) {
          player.completedEncounterIds.push(encounterId);
        }

        player.completions[encounterId] = {
          ...scope,
          encounterId,
          completedAt: now,
          reward: body.reward || null,
          source: body.source || 'ue-client'
        };

        return completeScopedEncounter(store, scope, playerId, encounterId, body);
      });

      sendJson(response, 200, {
        ok: true,
        requestId,
        playerId,
        encounterId,
        completedEncounterIds: completed.player.completedEncounterIds
      });
      return;
    }

    if (request.method === 'DELETE' && parts.length === 4 && parts[3] === 'completed') {
      await mutateStore((store) => {
        const player = getPlayer(store, playerId);
        player.completedEncounterIds = [];
        player.completions = {};
      });
      sendJson(response, 200, { ok: true, requestId, playerId, completedEncounterIds: [] });
      return;
    }
  }

  sendJson(response, 404, { ok: false, requestId, error: 'Not found' });
}


const server = http.createServer((request, response) => {
  // --- LOGGING ---
  const now = new Date().toISOString();
  const ip = request.headers['x-forwarded-for'] || request.socket.remoteAddress;
  const method = request.method;
  const url = request.url;
  const userAgent = request.headers['user-agent'] || '';
  console.log(`[${now}] [${ip}] ${method} ${url} UA: ${userAgent}`);

  handleRequest(request, response).catch((error) => {
    const statusCode = error.statusCode || 500;
    sendJson(response, statusCode, {
      ok: false,
      error: error.message || 'Internal server error'
    });
  });
});

server.listen(PORT, HOST, () => {
  console.log(`Encounter progress backend listening on http://${HOST}:${PORT}`);
  console.log(`Using data file: ${DATA_FILE}`);
});
