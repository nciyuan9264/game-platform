package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/store"
)

type fakeReleaseStore struct {
	nodes          []contracts.NodeInfo
	catalog        []contracts.GameCatalogEntry
	versionAction  string
	versionGame    string
	versionTarget  string
	versionExpect  string
	versionErr     error
	registerCalled bool
}

func (s *fakeReleaseStore) ListNodes(context.Context, string) ([]contracts.NodeInfo, error) {
	return s.nodes, nil
}

func (s *fakeReleaseStore) GetGameCatalogEntry(_ context.Context, gameType string) (contracts.GameCatalogEntry, error) {
	for _, entry := range s.catalog {
		if entry.GameType == gameType {
			return entry, nil
		}
	}
	return contracts.GameCatalogEntry{}, store.ErrGameCatalogNotFound
}

func (s *fakeReleaseStore) ListGameCatalog(context.Context) ([]contracts.GameCatalogEntry, error) {
	return s.catalog, nil
}

func (s *fakeReleaseStore) RegisterGameCatalogEntry(
	_ context.Context,
	entry contracts.GameCatalogEntry,
	_,
	_ string,
) (contracts.GameCatalogEntry, bool, error) {
	s.registerCalled = true
	entry.Revision = 1
	return entry, true, nil
}

func (s *fakeReleaseStore) SetActiveGameVersion(
	_ context.Context,
	gameType,
	target,
	expected,
	action,
	_,
	_ string,
) (contracts.GameCatalogEntry, error) {
	s.versionGame = gameType
	s.versionTarget = target
	s.versionExpect = expected
	s.versionAction = action
	if s.versionErr != nil {
		return contracts.GameCatalogEntry{}, s.versionErr
	}
	return contracts.GameCatalogEntry{
		GameType:            gameType,
		ActiveEngineVersion: target,
		DefaultMaxPlayers:   4,
		ReplaySchemaVersion: 1,
		EnabledForNewRooms:  true,
		Revision:            2,
	}, nil
}

func (s *fakeReleaseStore) DisableGameCatalogEntry(
	_ context.Context,
	gameType,
	_,
	_ string,
) (contracts.GameCatalogEntry, bool, error) {
	return contracts.GameCatalogEntry{GameType: gameType}, true, nil
}

func TestRegisterRequiresReadyExactVersion(t *testing.T) {
	st := &fakeReleaseStore{nodes: []contracts.NodeInfo{
		{
			NodeID:       "draining",
			Status:       contracts.NodeStatusDraining,
			GameVersions: map[string]string{contracts.GameSplendor: "splendor-v3"},
		},
		{
			NodeID:       "wrong-version",
			Status:       contracts.NodeStatusReady,
			GameVersions: map[string]string{contracts.GameSplendor: "splendor-v2"},
		},
	}}
	err := run(
		context.Background(),
		[]string{
			"register",
			"--game", contracts.GameSplendor,
			"--version", "splendor-v3",
			"--default-max-players", "4",
			"--replay-schema-version", "1",
		},
		st,
		&bytes.Buffer{},
		"test",
	)
	if err == nil {
		t.Fatal("register succeeded without a ready exact-version roomhost")
	}
	if st.registerCalled {
		t.Fatal("register reached PostgreSQL before the readiness check")
	}
}

func TestPromoteUsesCASAndWritesJSON(t *testing.T) {
	st := &fakeReleaseStore{nodes: []contracts.NodeInfo{{
		NodeID:       "splendor-b",
		Status:       contracts.NodeStatusReady,
		GameVersions: map[string]string{contracts.GameSplendor: "splendor-v3"},
	}}}
	var stdout bytes.Buffer
	err := run(
		context.Background(),
		[]string{
			"promote",
			"--game", contracts.GameSplendor,
			"--version", "splendor-v3",
			"--expected-version", "splendor-v2",
			"--artifact", "sha256:test",
		},
		st,
		&stdout,
		"release-test",
	)
	if err != nil {
		t.Fatal(err)
	}
	if st.versionGame != contracts.GameSplendor ||
		st.versionTarget != "splendor-v3" ||
		st.versionExpect != "splendor-v2" ||
		st.versionAction != contracts.GameCatalogActionPromote {
		t.Fatalf("version change = %q %q %q %q", st.versionGame, st.versionTarget, st.versionExpect, st.versionAction)
	}
	var output commandOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if !output.Changed ||
		output.Entry == nil ||
		output.Entry.ActiveEngineVersion != "splendor-v3" {
		t.Fatalf("output = %+v", output)
	}
}

func TestPromotePropagatesCASConflict(t *testing.T) {
	st := &fakeReleaseStore{
		nodes: []contracts.NodeInfo{{
			NodeID:       "acquire-b",
			Status:       contracts.NodeStatusReady,
			GameVersions: map[string]string{contracts.GameAcquire: "acquire-v3"},
		}},
		versionErr: store.ErrGameCatalogConflict,
	}
	err := run(
		context.Background(),
		[]string{
			"promote",
			"--game", contracts.GameAcquire,
			"--version", "acquire-v3",
			"--expected-version", "acquire-v2",
		},
		st,
		&bytes.Buffer{},
		"test",
	)
	if !errors.Is(err, store.ErrGameCatalogConflict) {
		t.Fatalf("promote error = %v, want ErrGameCatalogConflict", err)
	}
}

func TestStatusFiltersNodesByGame(t *testing.T) {
	st := &fakeReleaseStore{
		catalog: []contracts.GameCatalogEntry{{
			GameType:            contracts.GameAcquire,
			ActiveEngineVersion: "acquire-v2",
		}},
		nodes: []contracts.NodeInfo{
			{NodeID: "acquire-a", GameVersions: map[string]string{contracts.GameAcquire: "acquire-v2"}},
			{NodeID: "davinci-a", GameVersions: map[string]string{contracts.GameDaVinci: "davinci-v2"}},
		},
	}
	var stdout bytes.Buffer
	if err := run(
		context.Background(),
		[]string{"status", "--game", contracts.GameAcquire, "--json"},
		st,
		&stdout,
		"test",
	); err != nil {
		t.Fatal(err)
	}
	var output commandOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Catalog) != 1 ||
		len(output.Nodes) != 1 ||
		output.Nodes[0].NodeID != "acquire-a" {
		t.Fatalf("status output = %+v", output)
	}
}
