package ingest

import "strings"

// ChunkText splits text into chunks of at most maxChars runes, preferring to
// break on paragraph boundaries ("\n\n"). Paragraphs are greedily packed into
// a chunk until the next paragraph would overflow it; a single paragraph
// longer than maxChars is hard-split into maxChars-sized, rune-safe pieces.
// Empty input yields nil. No chunk is ever empty.
func ChunkText(text string, maxChars int) []string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}

	paragraphs := strings.Split(trimmed, "\n\n")
	var chunks []string
	var buf strings.Builder

	flush := func() {
		if buf.Len() == 0 {
			return
		}
		chunks = append(chunks, buf.String())
		buf.Reset()
	}

	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}

		if len([]rune(para)) > maxChars {
			flush()
			chunks = append(chunks, hardSplit(para, maxChars)...)
			continue
		}

		if buf.Len() > 0 && buf.Len()+len("\n\n")+len(para) > maxChars {
			flush()
		}
		if buf.Len() > 0 {
			buf.WriteString("\n\n")
		}
		buf.WriteString(para)
	}
	flush()

	return chunks
}

// hardSplit breaks a single paragraph into maxChars-sized, rune-safe pieces.
func hardSplit(para string, maxChars int) []string {
	runes := []rune(para)
	var pieces []string
	for len(runes) > 0 {
		end := min(maxChars, len(runes))
		pieces = append(pieces, string(runes[:end]))
		runes = runes[end:]
	}
	return pieces
}
