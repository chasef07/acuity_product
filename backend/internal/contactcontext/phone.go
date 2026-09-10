// Package contactcontext owns normalization of unverified communication context.
package contactcontext

import (
	"errors"
	"regexp"
	"strings"
)

var ErrInvalidInput = errors.New("invalid phone context")
var canonicalPhone = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

func NormalizePhone(value string) (string, error) {
	value = strings.TrimSpace(value)
	if canonicalPhone.MatchString(value) {
		return value, nil
	}
	var digits strings.Builder
	openParenthesis := -1
	closeParenthesis := -1
	for index, character := range value {
		switch {
		case character >= '0' && character <= '9':
			digits.WriteRune(character)
		case character == '+' && index == 0:
		case character == ' ' || character == '-' || character == '.':
		case character == '(' && openParenthesis == -1:
			openParenthesis = index
		case character == ')' && closeParenthesis == -1:
			closeParenthesis = index
		default:
			return "", ErrInvalidInput
		}
	}
	if (openParenthesis == -1) != (closeParenthesis == -1) ||
		(openParenthesis >= 0 && closeParenthesis <= openParenthesis) {
		return "", ErrInvalidInput
	}
	normalized := digits.String()
	if len(normalized) == 10 {
		normalized = "1" + normalized
	}
	normalized = "+" + normalized
	if !canonicalPhone.MatchString(normalized) {
		return "", ErrInvalidInput
	}
	return normalized, nil
}
