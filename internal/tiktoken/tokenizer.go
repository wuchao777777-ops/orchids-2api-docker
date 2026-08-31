package tiktoken

import (
	"math"
	"unicode/utf8"
)

func isASCIIWordByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

type Estimator struct {
	tokens float64
	inWord bool
}

func (e *Estimator) Add(text string) {
	if text == "" {
		return
	}
	for _, r := range text {
		if r < 128 {
			if isASCIIWordByte(byte(r)) {
				e.inWord = true
			} else {
				if e.inWord {
					e.tokens += 1
					e.inWord = false
				}
				if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
					e.tokens += 1
				}
			}
			continue
		}

		if e.inWord {
			e.tokens += 1
			e.inWord = false
		}
		e.tokens += 1.5
	}
}

func (e *Estimator) AddBytes(text []byte) {
	if len(text) == 0 {
		return
	}
	for len(text) > 0 {
		if text[0] < utf8.RuneSelf {
			b := text[0]
			if isASCIIWordByte(b) {
				e.inWord = true
			} else {
				if e.inWord {
					e.tokens += 1
					e.inWord = false
				}
				if b != ' ' && b != '\t' && b != '\n' && b != '\r' {
					e.tokens += 1
				}
			}
			text = text[1:]
			continue
		}

		if e.inWord {
			e.tokens += 1
			e.inWord = false
		}

		_, size := utf8.DecodeRune(text)
		e.tokens += 1.5
		text = text[size:]
	}
}

func (e *Estimator) Count() int {
	tokens := e.tokens
	if e.inWord {
		tokens += 1
	}
	return int(math.Round(tokens))
}

func (e *Estimator) Reset() {
	e.tokens = 0
	e.inWord = false
}

// EstimateTextTokens estimates token count for mixed ASCII and CJK text.
func EstimateTextTokens(text string) int {
	if text == "" {
		return 0
	}
	var estimator Estimator
	estimator.Add(text)
	return estimator.Count()
}
