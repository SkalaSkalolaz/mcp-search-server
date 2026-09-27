package search

import "unicode/utf8"

// TruncateStringBytes обрезает строку до maxLen байт, не разрывая UTF-8.
func TruncateStringBytes(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	// Отступаем назад, пока не найдём валидную границу руны.
	for maxLen > 0 && !utf8.RuneStart(s[maxLen]) {
		maxLen--
	}
	return s[:maxLen]
}