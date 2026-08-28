package grok

import "strings"

// streamLoopGuard detects a repeated suffix while keeping bounded state. It is
// intentionally conservative: at least four consecutive 128+ byte blocks are
// required, so normal prose repetition and short acknowledgements pass.
type streamLoopGuard struct {
	text strings.Builder
}

func (g *streamLoopGuard) Add(value string) bool {
	if g == nil || value == "" {
		return false
	}
	g.text.WriteString(value)
	current := g.text.String()
	if len(current) > 16<<10 {
		current = current[len(current)-(16<<10):]
		g.text.Reset()
		g.text.WriteString(current)
	}
	for _, size := range []int{512, 256, 128} {
		if len(current) < size*4 {
			continue
		}
		block := current[len(current)-size:]
		if strings.TrimSpace(block) == "" {
			continue
		}
		if current[len(current)-size*2:len(current)-size] == block &&
			current[len(current)-size*3:len(current)-size*2] == block &&
			current[len(current)-size*4:len(current)-size*3] == block {
			return true
		}
	}
	return false
}
