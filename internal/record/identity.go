package record

import (
	"crypto/sha256"
	"encoding/hex"
)

const ContentIDPrefix = "sha256:"

func contentID(data []byte) string {
	digest := sha256.Sum256(data)
	return ContentIDPrefix + hex.EncodeToString(digest[:])
}
