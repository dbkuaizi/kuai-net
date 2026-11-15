package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
)

//go:embed client_locales.json
var clientLocaleJSON []byte

type clientLanguage struct {
	Code   string `json:"code"`
	Native string `json:"native"`
}

var clientLocales = func() struct {
	Languages []clientLanguage             `json:"languages"`
	Texts     map[string]map[string]string `json:"texts"`
} {
	var data struct {
		Languages []clientLanguage             `json:"languages"`
		Texts     map[string]map[string]string `json:"texts"`
	}
	if err := json.Unmarshal(clientLocaleJSON, &data); err != nil {
		panic(err)
	}
	return data
}()

func clientText(language, key string) string {
	if text := clientLocales.Texts[language][key]; text != "" {
		return text
	}
	if text := clientLocales.Texts["en"][key]; text != "" {
		return text
	}
	return key
}

// Keep errors structured until they reach the UI so language changes also
// apply to errors and persisted history. Raw network errors remain diagnostic.
type clientFault struct {
	Key    string
	Detail string
}

func (e clientFault) Error() string { return e.Key + ": " + e.Detail }

func fault(key string) error { return clientFault{Key: key} }

func localizedError(language string, err error) string {
	var f clientFault
	if errors.As(err, &f) {
		text := clientText(language, f.Key)
		if f.Detail != "" {
			text += ": " + f.Detail
		}
		return text
	}
	return fmt.Sprint(err)
}
