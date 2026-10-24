package server

import (
	"encoding/json"
	"errors"
)

type apiError struct{ code, message string }

// apiErrors holds the English API fallbacks and stable codes. The catalog is
// web/backend-errors-contract.json, shared with the frontend's translation
// validator and embedded by the command. Additional UI languages require no
// Go changes.
var apiErrors map[string]apiError

// SetErrorCatalog loads the API error catalog.
func SetErrorCatalog(contract []byte) error {
	var catalog map[string][2]string
	if e := json.Unmarshal(contract, &catalog); e != nil || len(catalog) == 0 {
		return errors.New("invalid API error catalog")
	}
	result := make(map[string]apiError, len(catalog)*2)
	for code, text := range catalog {
		value := apiError{code: code, message: text[0]}
		result[code] = value
		result[text[1]] = value
	}
	result["Användarnamn: 3–40 gemener/siffror. Lösenord: 12–72 byte"] = result["invalid_credentials"]
	result["Kunde inte skapa kontot. Kontrollera namn och lösenord"] = result["account_create_failed"]
	apiErrors = result
	return nil
}
