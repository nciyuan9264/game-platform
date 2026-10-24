package migration

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"testing"
)

func TestTransformJSONRewritesExactIdentityReferences(t *testing.T) {
	userID := int64(7)
	aliases := map[string]string{"Alice-Cooper-7": "7"}
	players := map[string]identity{
		"7": {
			SourcePlayerID: "Alice-Cooper-7",
			PlayerID:       "7",
			UserID:         &userID,
			DisplayName:    "Alice-Cooper-7",
		},
	}
	raw := []byte(`{
		"players":{"Alice-Cooper-7":{"playerID":"Alice-Cooper-7","displayName":"Alice-Cooper-7"}},
		"currentPlayer":"Alice-Cooper-7",
		"note":"winner=Alice-Cooper-7"
	}`)

	transformed, err := transformJSON(raw, aliases, players)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(transformed, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["currentPlayer"] != "7" || decoded["note"] != "winner=Alice-Cooper-7" {
		t.Fatalf("transformed values = %+v", decoded)
	}
	profile := decoded["players"].(map[string]any)["7"].(map[string]any)
	if profile["playerID"] != "7" ||
		profile["userID"] != "7" ||
		profile["displayName"] != "Alice-Cooper-7" {
		t.Fatalf("rebuilt profile = %+v", profile)
	}
}

func TestTransformJSONRejectsKeyCollision(t *testing.T) {
	_, err := transformJSON(
		[]byte(`{"Alice-7":{"score":1},"7":{"score":2}}`),
		map[string]string{"Alice-7": "7"},
		nil,
	)
	if !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("transformJSON() error = %v, want ErrIdentityConflict", err)
	}
}

func TestTransformCompressedJSONProducesReadableGzip(t *testing.T) {
	var source bytes.Buffer
	writer := gzip.NewWriter(&source)
	if _, err := writer.Write([]byte(`{"currentPlayer":"Alice-7"}`)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	transformed, err := transformCompressedJSON(
		source.Bytes(),
		map[string]string{"Alice-7": "7"},
		nil,
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(transformed))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if string(decoded) != `{"currentPlayer":"7"}` {
		t.Fatalf("decoded payload = %s", decoded)
	}
}

func TestCanonicalDisplayNameSeparatesLegacyCompositeID(t *testing.T) {
	for _, test := range []struct {
		name        string
		sourceID    string
		playerID    string
		displayName string
		want        string
	}{
		{
			name:     "empty legacy snapshot",
			sourceID: "Alice-Cooper-7",
			playerID: "7",
			want:     "Alice-Cooper",
		},
		{
			name:        "composite legacy snapshot",
			sourceID:    "Alice-Cooper-7",
			playerID:    "7",
			displayName: "Alice-Cooper-7",
			want:        "Alice-Cooper",
		},
		{
			name:        "independent display name",
			sourceID:    "old-name-7",
			playerID:    "7",
			displayName: "Current Name",
			want:        "Current Name",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := canonicalDisplayName(test.sourceID, test.playerID, test.displayName)
			if got != test.want {
				t.Fatalf("canonicalDisplayName() = %q, want %q", got, test.want)
			}
		})
	}
}
