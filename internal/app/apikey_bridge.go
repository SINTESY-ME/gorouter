package app

import "github.com/jhon/gorouter/internal/infra/apikey"

// apikeyGenerate delegates to the apikey package. Kept here so app doesn't
// import infrastructure broadly — only the small, focused apikey package.
func apikeyGenerate(secret string) (string, error) {
	return apikey.Generate(secret)
}