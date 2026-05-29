package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/heroiclabs/nakama-common/runtime"
)

const (
	systemUserID     = "00000000-0000-0000-0000-000000000000"
	poseTTL          = 15 * time.Second
	demoWorldSeed    = 421337
	worldTileSize    = 3900
	worldGridColumns = 10
	worldGridRows    = 8
	collectionState  = "encounter_state"
	collectionPose   = "encounter_player_pose"
	sourceNakamaHTTP = "nakama-http"
)

type serverState struct {
	logger runtime.Logger
	nk     runtime.NakamaModule
}

type responseError struct {
	Status  int
	Message string
}

type routeParts []string

func (e responseError) Error() string {
	return e.Message
}

func InitModule(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, initializer runtime.Initializer) error {
	state := &serverState{logger: logger, nk: nk}

	if err := initializer.RegisterRpc("encounter_health", state.rpcHealth); err != nil {
		return err
	}
	if err := initializer.RegisterRpc("encounter_complete_scoped", state.rpcCompleteScopedEncounter); err != nil {
		return err
	}
	if err := initializer.RegisterRpc("encounter_region_stream_state", state.rpcRegionStreamState); err != nil {
		return err
	}
	if err := initializer.RegisterRpc("encounter_player_pose", state.rpcPlayerPose); err != nil {
		return err
	}
	if err := initializer.RegisterRpc("encounter_party_players", state.rpcPartyPlayers); err != nil {
		return err
	}
	if err := initializer.RegisterRpc("encounter_party_runtime_state", state.rpcPartyRuntimeState); err != nil {
		return err
	}

	if err := initializer.RegisterHttp("/health", state.handleHealth, http.MethodGet); err != nil {
		return err
	}
	worldHandlers := []string{
		"/worlds/{worldId}/seed",
		"/worlds/{worldId}/regions/{regionId}/stream-state",
		"/worlds/{worldId}/parties/{partyId}/players",
		"/worlds/{worldId}/parties/{partyId}/runtime-state",
		"/worlds/{worldId}/parties/{partyId}/players/{playerId}/pose",
		"/worlds/{worldId}/regions/{regionId}/parties/{partyId}/players/{playerId}/encounters/completed",
		"/worlds/{worldId}/regions/{regionId}/parties/{partyId}/players/{playerId}/encounters/{encounterId}/completed",
	}
	for _, path := range worldHandlers {
		if err := initializer.RegisterHttp(path, state.handleWorlds, http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodOptions); err != nil {
			return err
		}
	}

	logger.Info("Nakama Encounter Backend module loaded.")
	return nil
}

func (s *serverState) rpcHealth(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	return mustJSON(map[string]any{
		"ok":          true,
		"service":     "nakama-encounter-backend",
		"backend":     "nakama",
		"storage":     "nakama-storage",
		"worldSeed":   demoWorldSeed,
		"worldConfig": worldConfig(),
		"features":    healthFeatures(),
	}), nil
}

func (s *serverState) rpcCompleteScopedEncounter(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	var req map[string]any
	if err := json.Unmarshal([]byte(payload), &req); err != nil {
		return "", runtime.NewError("invalid payload", 3)
	}
	result, err := s.completeScopedEncounter(ctx, stringField(req, "worldId"), stringField(req, "regionId"), stringField(req, "partyId"), stringField(req, "playerId"), stringField(req, "encounterId"), req)
	if err != nil {
		return "", runtime.NewError(err.Error(), 13)
	}
	return result, nil
}

func (s *serverState) rpcRegionStreamState(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	var req map[string]any
	if err := json.Unmarshal([]byte(payload), &req); err != nil {
		return "", runtime.NewError("invalid payload", 3)
	}
	result, err := s.upsertRegionStreamState(ctx, stringField(req, "worldId"), stringField(req, "regionId"), req)
	if err != nil {
		return "", runtime.NewError(err.Error(), 13)
	}
	return result, nil
}

func (s *serverState) rpcPlayerPose(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	var req map[string]any
	if err := json.Unmarshal([]byte(payload), &req); err != nil {
		return "", runtime.NewError("invalid payload", 3)
	}
	result, err := s.upsertPlayerPose(ctx, stringField(req, "worldId"), stringField(req, "partyId"), stringField(req, "playerId"), req)
	if err != nil {
		return "", runtime.NewError(err.Error(), 13)
	}
	return result, nil
}

func (s *serverState) rpcPartyPlayers(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	var req map[string]any
	if payload != "" {
		if err := json.Unmarshal([]byte(payload), &req); err != nil {
			return "", runtime.NewError("invalid payload", 3)
		}
	}
	result, err := s.getPartyPlayers(ctx, stringField(req, "worldId"), stringField(req, "partyId"))
	if err != nil {
		return "", runtime.NewError(err.Error(), 13)
	}
	return result, nil
}

func (s *serverState) rpcPartyRuntimeState(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	var req map[string]any
	if payload != "" {
		if err := json.Unmarshal([]byte(payload), &req); err != nil {
			return "", runtime.NewError("invalid payload", 3)
		}
	}
	result, err := s.getPartyRuntimeState(ctx, stringField(req, "worldId"), stringField(req, "partyId"))
	if err != nil {
		return "", runtime.NewError(err.Error(), 13)
	}
	return result, nil
}

func (s *serverState) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"service":     "nakama-encounter-backend",
		"backend":     "nakama",
		"storage":     "nakama-storage",
		"worldSeed":   demoWorldSeed,
		"worldConfig": worldConfig(),
		"features":    healthFeatures(),
	})
}

func (s *serverState) handleWorlds(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		writeJSON(w, http.StatusNoContent, map[string]any{})
		return
	}

	parts := splitPath(r.URL.Path)
	if len(parts) < 2 || parts[0] != "worlds" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}

	result, err := s.routeWorldRequest(r.Context(), r.Method, parts, r.Body)
	if err != nil {
		var routeErr responseError
		if errors.As(err, &routeErr) {
			writeJSON(w, routeErr.Status, map[string]any{"error": routeErr.Message})
			return
		}
		s.logger.Error("Nakama encounter HTTP error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal server error"})
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(result))
}

func (s *serverState) routeWorldRequest(ctx context.Context, method string, parts routeParts, body io.ReadCloser) (string, error) {
	if len(parts) == 3 && parts[2] == "seed" {
		if method == http.MethodGet {
			return mustJSON(map[string]any{
				"worldId": parts[1],
				"seed":    demoWorldSeed,
				"config":  worldConfig(),
			}), nil
		}
	}

	if len(parts) == 5 && parts[2] == "regions" && parts[4] == "stream-state" {
		worldID := parts[1]
		regionID := parts[3]
		if method == http.MethodGet {
			return s.readStorageJSON(ctx, collectionState, regionStreamKey(worldID, regionID), defaultRegionState(worldID, regionID))
		}
		if method == http.MethodPost {
			payload, err := readBodyMap(body)
			if err != nil {
				return "", responseError{Status: http.StatusBadRequest, Message: "invalid json"}
			}
			return s.upsertRegionStreamState(ctx, worldID, regionID, payload)
		}
	}

	if len(parts) == 5 && parts[2] == "parties" && parts[4] == "players" {
		if method == http.MethodGet {
			return s.getPartyPlayers(ctx, parts[1], parts[3])
		}
	}

	if len(parts) == 5 && parts[2] == "parties" && parts[4] == "runtime-state" {
		if method == http.MethodGet {
			return s.getPartyRuntimeState(ctx, parts[1], parts[3])
		}
	}

	if len(parts) == 7 && parts[2] == "parties" && parts[4] == "players" && parts[6] == "pose" {
		if method == http.MethodPost {
			payload, err := readBodyMap(body)
			if err != nil {
				return "", responseError{Status: http.StatusBadRequest, Message: "invalid json"}
			}
			return s.upsertPlayerPose(ctx, parts[1], parts[3], parts[5], payload)
		}
	}

	if len(parts) == 10 && parts[2] == "regions" && parts[4] == "parties" && parts[6] == "players" && parts[8] == "encounters" && parts[9] == "completed" {
		if method == http.MethodGet {
			return s.getScopedProgress(ctx, parts[1], parts[3], parts[5], parts[7])
		}
		if method == http.MethodDelete {
			return s.deleteScopedProgress(ctx, parts[1], parts[3], parts[5], parts[7])
		}
	}

	if len(parts) == 11 && parts[2] == "regions" && parts[4] == "parties" && parts[6] == "players" && parts[8] == "encounters" && parts[10] == "completed" {
		if method == http.MethodPost {
			payload, err := readBodyMap(body)
			if err != nil {
				return "", responseError{Status: http.StatusBadRequest, Message: "invalid json"}
			}
			return s.completeScopedEncounter(ctx, parts[1], parts[3], parts[5], parts[7], parts[9], payload)
		}
	}

	return "", responseError{Status: http.StatusNotFound, Message: "not found"}
}

func (s *serverState) upsertRegionStreamState(ctx context.Context, worldID, regionID string, payload map[string]any) (string, error) {
	if worldID == "" || regionID == "" {
		return "", responseError{Status: http.StatusBadRequest, Message: "worldId and regionId are required"}
	}
	payload["worldId"] = worldID
	payload["regionId"] = regionID
	payload["updatedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	if payload["source"] == nil {
		payload["source"] = sourceNakamaHTTP
	}
	payload["backend"] = "nakama"
	payload["storageOwner"] = systemUserID
	value := mustJSON(payload)
	if err := s.writeStorageJSON(ctx, collectionState, regionStreamKey(worldID, regionID), value); err != nil {
		return "", err
	}
	return value, nil
}

func (s *serverState) getScopedProgress(ctx context.Context, worldID, regionID, partyID, playerID string) (string, error) {
	return s.readStorageJSON(ctx, collectionState, scopedProgressKey(worldID, regionID, partyID, playerID), defaultScopedProgress(worldID, regionID, partyID, playerID))
}

func (s *serverState) completeScopedEncounter(ctx context.Context, worldID, regionID, partyID, playerID, encounterID string, payload map[string]any) (string, error) {
	if worldID == "" || regionID == "" || partyID == "" || playerID == "" || encounterID == "" {
		return "", responseError{Status: http.StatusBadRequest, Message: "worldId, regionId, partyId, playerId and encounterId are required"}
	}

	var progress map[string]any
	raw, err := s.readStorageJSON(ctx, collectionState, scopedProgressKey(worldID, regionID, partyID, playerID), defaultScopedProgress(worldID, regionID, partyID, playerID))
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal([]byte(raw), &progress); err != nil {
		return "", err
	}

	progress["partyCompletedEncounterIds"] = appendUniqueString(interfaceStringSlice(progress["partyCompletedEncounterIds"]), encounterID)
	progress["playerCompletedEncounterIds"] = appendUniqueString(interfaceStringSlice(progress["playerCompletedEncounterIds"]), encounterID)

	completions, _ := progress["completions"].(map[string]any)
	if completions == nil {
		completions = map[string]any{}
	}
	completions[encounterID] = map[string]any{
		"worldId":      worldID,
		"regionId":     regionID,
		"partyId":      partyID,
		"playerId":     playerID,
		"encounterId":  encounterID,
		"reward":       payload["reward"],
		"source":       valueOrDefault(payload["source"], sourceNakamaHTTP),
		"completedAt":  time.Now().UTC().Format(time.RFC3339Nano),
		"backend":      "nakama",
		"storageOwner": systemUserID,
	}
	progress["completions"] = completions

	value := mustJSON(progress)
	if err := s.writeStorageJSON(ctx, collectionState, scopedProgressKey(worldID, regionID, partyID, playerID), value); err != nil {
		return "", err
	}
	return value, nil
}

func (s *serverState) deleteScopedProgress(ctx context.Context, worldID, regionID, partyID, playerID string) (string, error) {
	if err := s.nk.StorageDelete(ctx, []*runtime.StorageDelete{{
		Collection: collectionState,
		Key:        scopedProgressKey(worldID, regionID, partyID, playerID),
		UserID:     systemUserID,
	}}); err != nil {
		return "", err
	}
	return mustJSON(map[string]any{
		"ok":                          true,
		"partyCompletedEncounterIds":  []string{},
		"playerCompletedEncounterIds": []string{},
		"backend":                     "nakama",
		"storageOwner":                systemUserID,
	}), nil
}

func (s *serverState) upsertPlayerPose(ctx context.Context, worldID, partyID, playerID string, payload map[string]any) (string, error) {
	if worldID == "" || partyID == "" || playerID == "" {
		return "", responseError{Status: http.StatusBadRequest, Message: "worldId, partyId and playerId are required"}
	}

	playersBefore, err := s.activePartyPlayers(ctx, worldID, partyID)
	if err != nil {
		return "", err
	}
	if _, err := s.refreshPartyRuntimeState(ctx, worldID, partyID, playersBefore); err != nil {
		return "", err
	}

	pose := map[string]any{
		"worldId":      worldID,
		"partyId":      partyID,
		"playerId":     playerID,
		"x":            numberOrZero(payload["x"]),
		"y":            numberOrZero(payload["y"]),
		"z":            numberOrZero(payload["z"]),
		"yaw":          numberOrZero(payload["yaw"]),
		"provider":     valueOrDefault(payload["provider"], "Nakama"),
		"source":       valueOrDefault(payload["source"], sourceNakamaHTTP),
		"backend":      "nakama",
		"storageOwner": systemUserID,
		"updatedAt":    time.Now().UTC().Format(time.RFC3339Nano),
	}
	value := mustJSON(pose)
	if err := s.writeStorageJSON(ctx, collectionPose, playerPoseKey(worldID, partyID, playerID), value); err != nil {
		return "", err
	}

	players, err := s.activePartyPlayers(ctx, worldID, partyID)
	if err != nil {
		return "", err
	}
	runtimeState, err := s.refreshPartyRuntimeState(ctx, worldID, partyID, players)
	if err != nil {
		return "", err
	}

	return mustJSON(map[string]any{
		"ok":      true,
		"pose":    pose,
		"runtime": runtimeState,
	}), nil
}

func (s *serverState) getPartyPlayers(ctx context.Context, worldID, partyID string) (string, error) {
	if worldID == "" || partyID == "" {
		return "", responseError{Status: http.StatusBadRequest, Message: "worldId and partyId are required"}
	}

	players, err := s.activePartyPlayers(ctx, worldID, partyID)
	if err != nil {
		return "", err
	}
	runtimeState, err := s.refreshPartyRuntimeState(ctx, worldID, partyID, players)
	if err != nil {
		return "", err
	}

	return mustJSON(map[string]any{
		"worldId": worldID,
		"partyId": partyID,
		"players": players,
		"runtime": runtimeState,
	}), nil
}

func (s *serverState) getPartyRuntimeState(ctx context.Context, worldID, partyID string) (string, error) {
	if worldID == "" || partyID == "" {
		return "", responseError{Status: http.StatusBadRequest, Message: "worldId and partyId are required"}
	}
	players, err := s.activePartyPlayers(ctx, worldID, partyID)
	if err != nil {
		return "", err
	}
	runtimeState, err := s.refreshPartyRuntimeState(ctx, worldID, partyID, players)
	if err != nil {
		return "", err
	}
	return mustJSON(runtimeState), nil
}

func (s *serverState) activePartyPlayers(ctx context.Context, worldID, partyID string) ([]map[string]any, error) {
	prefix := partyPosePrefix(worldID, partyID)
	now := time.Now()
	players := make([]map[string]any, 0)
	cursor := ""
	for {
		objects, nextCursor, err := s.nk.StorageList(ctx, systemUserID, systemUserID, collectionPose, 100, cursor)
		if err != nil {
			return nil, err
		}
		for _, object := range objects {
			if !strings.HasPrefix(object.Key, prefix) {
				continue
			}

			var pose map[string]any
			if err := json.Unmarshal([]byte(object.Value), &pose); err != nil {
				continue
			}
			updatedAt, err := time.Parse(time.RFC3339Nano, stringField(pose, "updatedAt"))
			if err != nil || now.Sub(updatedAt) > poseTTL {
				continue
			}
			players = append(players, pose)
		}
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}

	return players, nil
}

func (s *serverState) refreshPartyRuntimeState(ctx context.Context, worldID, partyID string, activePlayers []map[string]any) (map[string]any, error) {
	runtimeState, err := s.readPartyRuntimeState(ctx, worldID, partyID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	activePlayerCount := len(activePlayers)
	runtimeState["worldId"] = worldID
	runtimeState["partyId"] = partyID
	runtimeState["activePlayerCount"] = activePlayerCount
	runtimeState["backend"] = "nakama"
	runtimeState["storageOwner"] = systemUserID

	if activePlayerCount > 0 {
		runtimeState["wasOccupied"] = true
		runtimeState["lastActiveAt"] = now
		runtimeState["emptySince"] = nil
		return runtimeState, s.writeStorageJSON(ctx, collectionState, partyRuntimeKey(worldID, partyID), mustJSON(runtimeState))
	}

	if boolField(runtimeState, "wasOccupied") {
		generation := intField(runtimeState, "generation", 1) + 1
		runtimeState["wasOccupied"] = false
		runtimeState["emptySince"] = now
		runtimeState["lastResetAt"] = now
		runtimeState["resetReason"] = "party-empty"
		runtimeState["generation"] = generation
		runtimeState["runtimeSeed"] = makeRuntimeSeed(worldID, partyID, generation)
		runtimeState["transientState"] = map[string]any{
			"mobs":   "regenerated-from-seed",
			"chests": "regenerated-from-seed",
		}
		if err := s.resetTransientRegionStatesForParty(ctx, worldID, partyID, runtimeState); err != nil {
			return nil, err
		}
	}

	return runtimeState, s.writeStorageJSON(ctx, collectionState, partyRuntimeKey(worldID, partyID), mustJSON(runtimeState))
}

func (s *serverState) readPartyRuntimeState(ctx context.Context, worldID, partyID string) (map[string]any, error) {
	raw, err := s.readStorageJSON(ctx, collectionState, partyRuntimeKey(worldID, partyID), defaultPartyRuntimeState(worldID, partyID))
	if err != nil {
		return nil, err
	}
	var runtimeState map[string]any
	if err := json.Unmarshal([]byte(raw), &runtimeState); err != nil {
		return nil, err
	}
	return runtimeState, nil
}

func (s *serverState) resetTransientRegionStatesForParty(ctx context.Context, worldID, partyID string, runtimeState map[string]any) error {
	cursor := ""
	for {
		objects, nextCursor, err := s.nk.StorageList(ctx, systemUserID, systemUserID, collectionState, 100, cursor)
		if err != nil {
			return err
		}
		for _, object := range objects {
			if !strings.HasPrefix(object.Key, regionStreamKey(worldID, "")) {
				continue
			}
			var streamState map[string]any
			if err := json.Unmarshal([]byte(object.Value), &streamState); err != nil {
				continue
			}
			if stringField(streamState, "worldId") != worldID {
				continue
			}
			streamPartyID := stringField(streamState, "partyId")
			if streamPartyID != "" && streamPartyID != partyID {
				continue
			}
			streamState["loaded"] = false
			streamState["nearbyPlayerCount"] = 0
			streamState["resetAt"] = runtimeState["lastResetAt"]
			streamState["runtimeGeneration"] = runtimeState["generation"]
			streamState["runtimeSeed"] = runtimeState["runtimeSeed"]
			if err := s.writeStorageJSON(ctx, collectionState, object.Key, mustJSON(streamState)); err != nil {
				return err
			}
		}
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}
	return nil
}

func (s *serverState) readStorageJSON(ctx context.Context, collection, key, fallback string) (string, error) {
	objects, err := s.nk.StorageRead(ctx, []*runtime.StorageRead{{
		Collection: collection,
		Key:        key,
		UserID:     systemUserID,
	}})
	if err != nil {
		return "", err
	}
	if len(objects) == 0 {
		return fallback, nil
	}
	return objects[0].Value, nil
}

func (s *serverState) writeStorageJSON(ctx context.Context, collection, key, value string) error {
	_, err := s.nk.StorageWrite(ctx, []*runtime.StorageWrite{{
		Collection:      collection,
		Key:             key,
		UserID:          systemUserID,
		Value:           value,
		PermissionRead:  0,
		PermissionWrite: 0,
	}})
	return err
}

func readBodyMap(body io.ReadCloser) (map[string]any, error) {
	defer body.Close()
	raw, err := io.ReadAll(io.LimitReader(body, 1024*1024))
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return map[string]any{}, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET,POST,DELETE,OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "content-type,authorization")
	w.WriteHeader(status)
	if status != http.StatusNoContent {
		_, _ = w.Write([]byte(mustJSON(payload)))
	}
}

func splitPath(path string) routeParts {
	path = strings.Trim(path, "/")
	if path == "" {
		return routeParts{}
	}
	return strings.Split(path, "/")
}

func stringField(payload map[string]any, key string) string {
	if value, ok := payload[key].(string); ok {
		return value
	}
	return ""
}

func interfaceStringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		if strings, ok := value.([]string); ok {
			return strings
		}
		return []string{}
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if value, ok := item.(string); ok {
			result = append(result, value)
		}
	}
	return result
}

func appendUniqueString(items []string, item string) []string {
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	return append(items, item)
}

func numberOrZero(value any) float64 {
	if number, ok := value.(float64); ok {
		return number
	}
	return 0
}

func intField(payload map[string]any, key string, fallback int) int {
	switch value := payload[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		number, err := value.Int64()
		if err == nil {
			return int(number)
		}
	}
	return fallback
}

func boolField(payload map[string]any, key string) bool {
	value, ok := payload[key].(bool)
	return ok && value
}

func valueOrDefault(value any, fallback any) any {
	if value == nil {
		return fallback
	}
	return value
}

func mustJSON(value any) string {
	bytes, _ := json.Marshal(value)
	return string(bytes)
}

func worldConfig() map[string]any {
	return map[string]any{
		"tileSize":    worldTileSize,
		"gridColumns": worldGridColumns,
		"gridRows":    worldGridRows,
		"worldWidth":  worldTileSize * worldGridColumns,
		"worldHeight": worldTileSize * worldGridRows,
	}
}

func healthFeatures() []string {
	return []string{
		"encounter-progress",
		"region-stream-state",
		"party-player-poses",
		"world-seed",
		"world-config",
		"party-runtime-regeneration",
	}
}

func regionStreamKey(worldID, regionID string) string {
	return "stream:" + worldID + ":" + regionID
}

func scopedProgressKey(worldID, regionID, partyID, playerID string) string {
	return "scoped:" + worldID + ":" + regionID + ":" + partyID + ":" + playerID
}

func partyPosePrefix(worldID, partyID string) string {
	return "pose:" + worldID + ":" + partyID + ":"
}

func playerPoseKey(worldID, partyID, playerID string) string {
	return partyPosePrefix(worldID, partyID) + playerID
}

func partyRuntimeKey(worldID, partyID string) string {
	return "runtime:" + worldID + ":" + partyID
}

func makeRuntimeSeed(worldID, partyID string, generation int) uint32 {
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(fmt.Sprintf("%d:%s:%s:%d", demoWorldSeed, worldID, partyID, generation)))
	return hasher.Sum32()
}

func defaultRegionState(worldID, regionID string) string {
	return mustJSON(map[string]any{
		"worldId":           worldID,
		"regionId":          regionID,
		"loaded":            false,
		"nearbyPlayerCount": 0,
	})
}

func defaultPartyRuntimeState(worldID, partyID string) string {
	generation := 1
	return mustJSON(map[string]any{
		"worldId":           worldID,
		"partyId":           partyID,
		"generation":        generation,
		"runtimeSeed":       makeRuntimeSeed(worldID, partyID, generation),
		"activePlayerCount": 0,
		"wasOccupied":       false,
		"emptySince":        nil,
		"lastActiveAt":      nil,
		"lastResetAt":       nil,
		"resetReason":       "initial",
		"transientState": map[string]any{
			"mobs":   "seeded",
			"chests": "seeded",
		},
		"backend":      "nakama",
		"storageOwner": systemUserID,
	})
}

func defaultScopedProgress(worldID, regionID, partyID, playerID string) string {
	return mustJSON(map[string]any{
		"scope": map[string]any{
			"worldId":  worldID,
			"regionId": regionID,
			"partyId":  partyID,
		},
		"playerId":                    playerID,
		"partyCompletedEncounterIds":  []string{},
		"playerCompletedEncounterIds": []string{},
		"completions":                 map[string]any{},
		"backend":                     "nakama",
		"storageOwner":                systemUserID,
	})
}
