package codex

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

const (
	SessionHeader         = "Session_id"
	SessionHeaderHyphen   = "Session-Id"
	TurnMetadataHeader    = "X-Codex-Turn-Metadata"
	WindowIDHeader        = "X-Codex-Window-Id"
	ClientRequestIDHeader = "X-Client-Request-Id"
	BetaFeaturesHeader    = "X-Codex-Beta-Features"
	ThreadIDHeader        = "Thread-Id"
	// ResponsesLiteHeader uses the canonical spelling ("Openai"): Go's
	// http.Header canonicalizes keys, so lookups match regardless of case, and
	// the wire name is case-insensitive per RFC 9110.
	ResponsesLiteHeader = "X-Openai-Internal-Codex-Responses-Lite"
)

type TurnMetadata struct {
	InstallationID      string         `json:"installation_id"`
	SessionID           string         `json:"session_id"`
	ThreadID            string         `json:"thread_id"`
	TurnID              string         `json:"turn_id"`
	WindowID            string         `json:"window_id"`
	RequestKind         string         `json:"request_kind"`
	ThreadSource        string         `json:"thread_source"`
	Sandbox             string         `json:"sandbox"`
	TurnStartedAtUnixMS int64          `json:"turn_started_at_unix_ms"`
	Workspaces          map[string]any `json:"workspaces,omitempty"`
}

// PassthroughHeaders lists client metadata that Codex-compatible upstreams use
// to select protocol behavior. Headers a Codex client actually sends are copied
// verbatim; headers missing from non-Codex inbound requests are fabricated in
// OutboundTransformer.TransformRequest so the upstream always sees a complete
// Codex session shape.
var PassthroughHeaders = []string{
	TurnMetadataHeader,
	WindowIDHeader,
	ClientRequestIDHeader,
	BetaFeaturesHeader,
	ThreadIDHeader,
	ResponsesLiteHeader,
}

func ExtractSessionIDFromTurnMetadata(raw string) string {
	if raw == "" {
		return ""
	}

	var payload TurnMetadata
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ""
	}

	return strings.TrimSpace(payload.SessionID)
}

// NormalizeTurnMetadataInstallationID replaces the machine-specific
// installation_id while preserving the rest of the client's metadata. Invalid
// or non-object JSON is reported to the caller so it can be replaced with a
// valid AxonHub-generated envelope.
func NormalizeTurnMetadataInstallationID(raw, installationID string) (string, bool) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || payload == nil {
		return "", false
	}

	payload["installation_id"] = installationID
	normalized, err := json.Marshal(payload)
	if err != nil {
		return "", false
	}

	return string(normalized), true
}

func installationIDForAccount(accountID string) string {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return ""
	}

	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(accountID)).String()
}

func (t *OutboundTransformer) installationIDForSession(sessionID string) string {
	if t == nil || len(t.installationIDs) == 0 {
		return ""
	}

	return t.installationIDs[hashUint64(sessionID)%uint64(len(t.installationIDs))]
}

func (t *OutboundTransformer) OverrideInstallationIdentity(request *httpclient.Request) error {
	if t == nil || request == nil || len(t.installationIDs) == 0 {
		return nil
	}
	if request.Headers == nil {
		request.Headers = make(http.Header)
	}

	sessionID := GetSessionIDFromHeaders(request.Headers)
	if sessionID == "" {
		sessionID = strings.TrimSpace(gjson.GetBytes(request.Body, "client_metadata.session_id").String())
	}
	installationID := t.installationIDForSession(sessionID)

	rawHeaderMetadata := request.Headers.Get(TurnMetadataHeader)
	headerMetadata, ok := NormalizeTurnMetadataInstallationID(rawHeaderMetadata, installationID)
	if !ok {
		encoded, err := json.Marshal(TurnMetadata{
			InstallationID: installationID,
			SessionID:      sessionID,
			ThreadID:       sessionID,
			TurnID:         uuid.NewString(),
			WindowID:       request.Headers.Get(WindowIDHeader),
			RequestKind:    "turn",
			ThreadSource:   "user",
			Sandbox:        "none",
		})
		if err != nil {
			return err
		}
		headerMetadata = string(encoded)
	}
	request.Headers.Set(TurnMetadataHeader, headerMetadata)

	if request.APIFormat != llm.APIFormatOpenAIResponse.String() || !gjson.ValidBytes(request.Body) {
		return nil
	}

	body, err := sjson.SetBytes(request.Body, "client_metadata.x-codex-installation-id", installationID)
	if err != nil {
		return err
	}
	bodyMetadataRaw := gjson.GetBytes(body, "client_metadata.x-codex-turn-metadata").String()
	bodyMetadata, ok := NormalizeTurnMetadataInstallationID(bodyMetadataRaw, installationID)
	if !ok {
		bodyMetadata = headerMetadata
	}
	body, err = sjson.SetBytes(body, "client_metadata.x-codex-turn-metadata", bodyMetadata)
	if err != nil {
		return err
	}
	request.Body = body

	return nil
}

const resolvedSessionIDMetadataKey = "codex_resolved_session_id"

func resolveSessionID(ctx context.Context, llmReq *llm.Request, candidates ...string) string {
	for _, candidate := range candidates {
		if candidate = strings.TrimSpace(candidate); candidate != "" {
			rememberResolvedSessionID(llmReq, candidate)
			return candidate
		}
	}
	if sessionID, ok := shared.GetSessionID(ctx); ok && strings.TrimSpace(sessionID) != "" {
		sessionID = strings.TrimSpace(sessionID)
		rememberResolvedSessionID(llmReq, sessionID)
		return sessionID
	}
	if llmReq.TransformerMetadata != nil {
		if sessionID, ok := llmReq.TransformerMetadata[resolvedSessionIDMetadataKey].(string); ok && sessionID != "" {
			return sessionID
		}
	} else {
		llmReq.TransformerMetadata = map[string]any{}
	}

	sessionID := uuid.NewString()
	rememberResolvedSessionID(llmReq, sessionID)

	return sessionID
}

func rememberResolvedSessionID(llmReq *llm.Request, sessionID string) {
	if llmReq == nil || sessionID == "" {
		return
	}
	if llmReq.TransformerMetadata == nil {
		llmReq.TransformerMetadata = map[string]any{}
	}
	llmReq.TransformerMetadata[resolvedSessionIDMetadataKey] = sessionID
	if llmReq.RawRequest != nil {
		if llmReq.RawRequest.Headers == nil {
			llmReq.RawRequest.Headers = make(http.Header)
		}
		llmReq.RawRequest.Headers.Set(SessionHeaderHyphen, sessionID)
	}
}

// turnStartedAtUnixMS returns a deterministic per-session timestamp inside the
// current 10-minute bucket: the bucket start plus an offset derived from the
// session id. Fabricated turn metadata is therefore stable across requests of
// the same session and rolls over roughly every 10 minutes.
func turnStartedAtUnixMS(sessionID string) int64 {
	const bucketMS = int64(10 * time.Minute / time.Millisecond)
	now := time.Now().UnixMilli()
	return now - now%bucketMS + int64(hashUint64(sessionID)%uint64(bucketMS))
}

func hashUint64(value string) uint64 {
	if value == "" {
		return 0
	}

	sum := sha256.Sum256([]byte(value))
	return binary.BigEndian.Uint64(sum[:8])
}

func GetSessionIDFromHeaders(headers http.Header) string {
	if headers == nil {
		return ""
	}

	sessionID := strings.TrimSpace(headers.Get(SessionHeader))
	if sessionID == "" {
		sessionID = strings.TrimSpace(headers.Get(SessionHeaderHyphen))
	}
	if sessionID != "" {
		return sessionID
	}

	return ExtractSessionIDFromTurnMetadata(strings.TrimSpace(headers.Get(TurnMetadataHeader)))
}
