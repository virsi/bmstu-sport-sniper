package oidc

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"golang.org/x/net/html"
)

// extractKcFormAction ищет <form id="kc-form-login" action="..."> в HTML
// и возвращает значение атрибута action.
//
// Парсим именно по id, потому что Keycloak умеет рендерить альтернативные
// формы (WebAuthn / OTP / passkey). Регуляркой не пользуемся — HTML может
// прийти с разным regxr-friendly форматированием и тэги атрибутов могут
// перемешиваться.
func extractKcFormAction(body []byte) (string, error) {
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if err := z.Err(); err != nil && err != io.EOF {
				return "", fmt.Errorf("oidc: tokenize: %w", err)
			}
			return "", ErrLoginFormNotFound
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			if string(name) != "form" || !hasAttr {
				continue
			}
			var (
				idVal     string
				actionVal string
			)
			for {
				key, val, more := z.TagAttr()
				switch string(key) {
				case "id":
					idVal = string(val)
				case "action":
					actionVal = string(val)
				}
				if !more {
					break
				}
			}
			if idVal == "kc-form-login" {
				if actionVal == "" {
					return "", fmt.Errorf("oidc: empty action on kc-form-login: %w", ErrLoginFormNotFound)
				}
				return actionVal, nil
			}
		}
	}
}

// hasLoginFormError возвращает true, если HTML содержит признаки
// «логин-форма с ошибкой» (после неуспешного POST credentials).
//
// Keycloak возвращает 200 OK + ту же форму + div.alert-error, поэтому
// 4xx по HTTP не приходит — приходится смотреть в тело.
func hasLoginFormError(body []byte) bool {
	// Дешёвый строковый поиск; alert-error / kc-feedback-error / pf-c-alert.
	// Не парсим DOM целиком, цель — only «есть/нет».
	s := bytesToLower(body)
	return strings.Contains(s, "alert-error") ||
		strings.Contains(s, "kc-feedback-error") ||
		strings.Contains(s, "pf-c-alert--danger")
}

// hasCaptchaMarker возвращает true, если в форме есть reCAPTCHA/h-captcha.
//
// Defensive: на момент написания (2026-06-02) у Keycloak BMSTU CAPTCHA
// не наблюдалась, но обработчик готов.
func hasCaptchaMarker(body []byte) bool {
	s := bytesToLower(body)
	return strings.Contains(s, "g-recaptcha") ||
		strings.Contains(s, "h-captcha") ||
		strings.Contains(s, "data-sitekey")
}

// containsLoginFormID возвращает true, если в HTML есть `id="kc-form-login"`.
// Используется как «успешный логин = форма исчезла».
func containsLoginFormID(body []byte) bool {
	return bytes.Contains(body, []byte(`id="kc-form-login"`)) ||
		bytes.Contains(body, []byte(`id='kc-form-login'`))
}

// bytesToLower возвращает строку в нижнем регистре. Аллоцирует, OK
// для редкого случая «парсим ответ Keycloak».
func bytesToLower(b []byte) string {
	return strings.ToLower(string(b))
}
