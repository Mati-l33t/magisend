package main

import (
	_ "embed"
	"encoding/json"
)

// The English API fallback and stable codes are shared with the frontend's
// translation validator. Additional UI languages require no Go changes.
//
//go:embed web/backend-errors-contract.json
var errorContract []byte

type apiError struct{ code, message string }

var apiErrors = func() map[string]apiError {
	var catalog map[string][2]string
	if e := json.Unmarshal(errorContract, &catalog); e != nil {
		panic("invalid API error catalog")
	}
	result := make(map[string]apiError, len(catalog)*2)
	for code, text := range catalog {
		value := apiError{code: code, message: text[0]}
		result[code] = value
		result[text[1]] = value
	}
	result["Användarnamn: 3–40 gemener/siffror. Lösenord: 12–72 byte"] = result["invalid_credentials"]
	result["Kunde inte skapa kontot. Kontrollera namn och lösenord"] = result["account_create_failed"]
	return result
}()
