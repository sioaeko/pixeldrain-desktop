package main

import "sync/atomic"

// The UI comes in Korean and English. uiLang holds the resolved language
// ("ko" or "en") for messages produced on the Go side.
var uiLang atomic.Value

// L picks the message for the current UI language.
func L(ko, en string) string {
	if lang, _ := uiLang.Load().(string); lang == "en" {
		return en
	}
	return ko
}

// setLanguage applies the language setting ("system", "ko" or "en") and
// returns the resolved language.
func setLanguage(setting string) string {
	lang := setting
	if lang != "ko" && lang != "en" {
		lang = systemLanguage()
	}
	uiLang.Store(lang)
	return lang
}

// msgError is an error whose text follows the UI language. Package-level
// sentinels use it so errors.Is keeps working.
type msgError struct{ ko, en string }

func (e *msgError) Error() string { return L(e.ko, e.en) }

func newError(ko, en string) error { return &msgError{ko, en} }
