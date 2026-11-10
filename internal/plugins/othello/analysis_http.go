package othello

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"
)

const (
	EdaxTimestampHeader = "X-Edax-Timestamp"
	EdaxSignatureHeader = "X-Edax-Signature"

	edaxHTTPRequestBodyLimit = 128 * 1024
	edaxHTTPMaxClockSkew     = time.Minute
)

type EdaxHTTPAnalyzeRequest struct {
	RequestID   string `json:"requestID"`
	PositionKey string `json:"positionKey"`
	State       *State `json:"state"`
}

type EdaxHTTPAnalyzeResponse struct {
	RequestID     string `json:"requestID"`
	PositionKey   string `json:"positionKey"`
	BestMove      *int   `json:"bestMove,omitempty"`
	Score         int    `json:"score"`
	Depth         int    `json:"depth"`
	ElapsedMs     int64  `json:"elapsedMs"`
	EngineVersion string `json:"engineVersion"`
}

func NewEdaxHTTPHandler(secret string) http.Handler {
	analyzer := newSingleWorkerEdaxAnalyzer()
	return newEdaxHTTPHandler(secret, analyzer, time.Now)
}

func SelfTestEdax(ctx context.Context) error {
	const blackPlayerID = "self-test-black"
	state := &State{
		Status:        StatusPlaying,
		Board:         freshBoard(),
		CurrentPlayer: blackPlayerID,
		BlackPlayerID: blackPlayerID,
		WhitePlayerID: "self-test-white",
		Log:           []Action{},
		PlayerOrder:   []string{blackPlayerID, "self-test-white"},
		Roster: map[string]*Player{
			blackPlayerID: {
				PlayerID: blackPlayerID,
				Side:     SideBlack,
			},
		},
	}
	result, err := newSingleWorkerEdaxAnalyzer().Analyze(ctx, state)
	if err != nil {
		return err
	}
	if result.BestMove == nil ||
		len(moveAt(state.Board, *result.BestMove, SideBlack).Flips) == 0 {
		return errors.New("Edax self-test returned an illegal move")
	}
	return nil
}

func newSingleWorkerEdaxAnalyzer() *edaxAnalyzer {
	return &edaxAnalyzer{
		binaryPath: edaxBinaryPath,
		evalPath:   edaxEvalFilePath,
		gate:       newEdaxPriorityGate(1),
	}
}

func SignEdaxHTTPRequest(
	secret string,
	at time.Time,
	body []byte,
) (timestamp, signature string) {
	timestamp = strconv.FormatInt(at.Unix(), 10)
	return timestamp, edaxHTTPRequestSignature(secret, timestamp, body)
}

func newEdaxHTTPHandler(
	secret string,
	analyzer positionAnalyzer,
	now func() time.Time,
) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeEdaxHTTPError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		writeEdaxHTTPJSON(w, http.StatusOK, map[string]string{
			"status": "ok",
			"engine": analysisEngineVersion,
		})
	})
	mux.HandleFunc("/analyze", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeEdaxHTTPError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		body, err := readEdaxHTTPRequestBody(w, r)
		if err != nil {
			status := http.StatusBadRequest
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				status = http.StatusRequestEntityTooLarge
			}
			writeEdaxHTTPError(w, status, "invalid request body")
			return
		}
		if !validEdaxHTTPRequest(
			secret,
			r.Header.Get(EdaxTimestampHeader),
			r.Header.Get(EdaxSignatureHeader),
			body,
			now(),
		) {
			writeEdaxHTTPError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		var request EdaxHTTPAnalyzeRequest
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil ||
			decoder.Decode(&struct{}{}) != io.EOF {
			writeEdaxHTTPError(w, http.StatusBadRequest, "invalid request")
			return
		}
		if request.RequestID == "" ||
			len(request.RequestID) > 256 ||
			request.PositionKey == "" ||
			request.State == nil ||
			request.PositionKey != positionKey(request.State) {
			writeEdaxHTTPError(w, http.StatusBadRequest, "invalid position")
			return
		}
		analysis := request.State.Analysis
		if analysis == nil ||
			analysis.Status != analysisStatusPending ||
			analysis.RequestID != request.RequestID ||
			analysis.PositionKey != request.PositionKey {
			writeEdaxHTTPError(w, http.StatusBadRequest, "invalid position")
			return
		}
		result, err := analyzer.Analyze(r.Context(), request.State)
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, context.DeadlineExceeded) ||
				errors.Is(err, context.Canceled) {
				status = http.StatusGatewayTimeout
			}
			log.Printf(
				"Edax analysis failed requestID=%q positionKey=%q: %v",
				request.RequestID,
				request.PositionKey,
				err,
			)
			writeEdaxHTTPError(w, status, "analysis unavailable")
			return
		}
		writeEdaxHTTPJSON(w, http.StatusOK, EdaxHTTPAnalyzeResponse{
			RequestID:     request.RequestID,
			PositionKey:   request.PositionKey,
			BestMove:      result.BestMove,
			Score:         result.Score,
			Depth:         result.Depth,
			ElapsedMs:     result.ElapsedMs,
			EngineVersion: analysisEngineVersion,
		})
	})
	return mux
}

func readEdaxHTTPRequestBody(
	w http.ResponseWriter,
	r *http.Request,
) ([]byte, error) {
	defer r.Body.Close()
	return io.ReadAll(http.MaxBytesReader(w, r.Body, edaxHTTPRequestBodyLimit))
}

func validEdaxHTTPRequest(
	secret,
	timestamp,
	signature string,
	body []byte,
	now time.Time,
) bool {
	if secret == "" || timestamp == "" || signature == "" {
		return false
	}
	unixSeconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	signedAt := time.Unix(unixSeconds, 0)
	skew := now.Sub(signedAt)
	if skew < 0 {
		skew = -skew
	}
	if skew > edaxHTTPMaxClockSkew {
		return false
	}
	provided, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	expected, err := hex.DecodeString(
		edaxHTTPRequestSignature(secret, timestamp, body),
	)
	return err == nil && hmac.Equal(provided, expected)
}

func edaxHTTPRequestSignature(
	secret,
	timestamp string,
	body []byte,
) string {
	bodySum := sha256.Sum256(body)
	message := timestamp + "\n" + hex.EncodeToString(bodySum[:])
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

func writeEdaxHTTPError(w http.ResponseWriter, status int, message string) {
	writeEdaxHTTPJSON(w, status, map[string]string{"error": message})
}

func writeEdaxHTTPJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
