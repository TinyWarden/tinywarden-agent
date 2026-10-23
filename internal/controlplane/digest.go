package controlplane

import (
	"crypto/sha256"
	"encoding/hex"
)

func BodyDigest(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
