package main

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
)

// A distributed application cannot keep its shared OAuth identity confidential.
// This only avoids plaintext credentials in release files; user tokens stay local.
func decodeGoogleApp(keyText, bundle string) (string, string) {
	key, err := base64.StdEncoding.DecodeString(keyText)
	if err != nil {
		return "", ""
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", ""
	}
	data, err := base64.StdEncoding.DecodeString(bundle)
	if err != nil || len(data) < gcm.NonceSize() {
		return "", ""
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	if err != nil {
		return "", ""
	}
	var app []string
	if json.Unmarshal(plain, &app) != nil || len(app) != 2 {
		return "", ""
	}
	return app[0], app[1]
}
