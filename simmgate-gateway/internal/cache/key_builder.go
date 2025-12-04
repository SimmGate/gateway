package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"simmgate-gateway/internal/llm"
)

// BuildExactCacheKeyFromChatRequest builds an ExactCacheKey from:
//   - the ChatRequest (for now; later your full ChatCompletionRequest),
//   - userID (cache scoping),
//   - versionID (gateway version for invalidation).
//
// It normalizes the request into a stable string, hashes it with SHA-256,
// and fills the ExactCacheKey struct.
func BuildExactCacheKeyFromChatRequest(
	req llm.ChatRequest,
	userID string,
	versionID string,
) (ExactCacheKey, error) {
	modelID := strings.TrimSpace(req.Model)

	// Simple normalization for now: model + full JSON body of request.
	body, err := json.Marshal(req)
	if err != nil {
		return ExactCacheKey{}, err
	}

	normalized := "model:" + modelID + "|body:" + string(body)

	sum := sha256.Sum256([]byte(normalized))
	hash := hex.EncodeToString(sum[:])

	return ExactCacheKey{
		UserID:    strings.TrimSpace(userID),
		ModelID:   modelID,
		VersionID: strings.TrimSpace(versionID),
		Hash:      hash,
	}, nil
}
