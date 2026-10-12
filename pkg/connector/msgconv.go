package connector

import (
	"html"
	"io"
	"strings"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
	"maunium.net/go/mautrix/event"
)

// ggSpanOpen is the wrapper that the official GG clients and libgadu put around a message.
const ggSpanOpen = `<span style="color:#000000; font-family:'MS Shell Dlg 2'; font-size:9pt; ">`

// formatTags maps the HTML tags that both sides support to the tag the bridge writes.
var formatTags = map[string]string{
	"b": "b", "strong": "b",
	"i": "i", "em": "i",
	"u": "u",
}

func normalizeNewlines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// ggToMatrix converts a GG message to Matrix content. It uses plain as the
// body and adds a formatted body only if the GG HTML has formatting.
func ggToMatrix(plain, ggHTML string) *event.MessageEventContent {
	content := &event.MessageEventContent{
		MsgType: event.MsgText,
		Body:    normalizeNewlines(plain),
	}
	if ggHTML == "" {
		return content
	}
	formatted, hasFormatting := sanitizeGGHTML(ggHTML)
	if hasFormatting {
		content.Format = event.FormatHTML
		content.FormattedBody = formatted
	}
	if content.Body == "" {
		content.Body = htmlToPlain(ggHTML)
	}
	return content
}

// sanitizeGGHTML keeps only b, i, u, br and links from GG HTML. It drops
// colors and fonts, because every GG message carries them.
func sanitizeGGHTML(in string) (out string, hasFormatting bool) {
	var b strings.Builder
	open := map[string]int{}
	z := xhtml.NewTokenizer(strings.NewReader(in))
	for {
		tt := z.Next()
		switch tt {
		case xhtml.ErrorToken:
			if z.Err() != io.EOF {
				return html.EscapeString(in), false
			}
			// Close tags that the GG HTML left open.
			for _, tag := range []string{"u", "i", "b"} {
				for ; open[tag] > 0; open[tag]-- {
					b.WriteString("</" + tag + ">")
				}
			}
			return b.String(), hasFormatting
		case xhtml.TextToken:
			b.WriteString(html.EscapeString(normalizeNewlines(string(z.Text()))))
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			tag := string(name)
			switch {
			case tag == "br":
				b.WriteString("<br>")
			case tag == "img":
				b.WriteString("[image]")
			case tag == "a" && hasAttr:
				href := attr(z, "href")
				if isSafeURL(href) && tt == xhtml.StartTagToken {
					b.WriteString(`<a href="` + html.EscapeString(href) + `">`)
					open["a"]++
					hasFormatting = true
				}
			case formatTags[tag] != "" && tt == xhtml.StartTagToken:
				t := formatTags[tag]
				b.WriteString("<" + t + ">")
				open[t]++
				hasFormatting = true
			}
		case xhtml.EndTagToken:
			name, _ := z.TagName()
			t := string(name)
			if mapped := formatTags[t]; mapped != "" {
				t = mapped
			} else if t != "a" {
				continue
			}
			if open[t] > 0 {
				open[t]--
				b.WriteString("</" + t + ">")
			}
		}
	}
}

func attr(z *xhtml.Tokenizer, key string) string {
	for {
		k, v, more := z.TagAttr()
		if string(k) == key {
			return string(v)
		}
		if !more {
			return ""
		}
	}
}

func isSafeURL(u string) bool {
	l := strings.ToLower(u)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "mailto:")
}

// htmlToPlain returns the text of an HTML fragment, with <br> and block ends as new lines.
func htmlToPlain(in string) string {
	var b strings.Builder
	z := xhtml.NewTokenizer(strings.NewReader(in))
	for {
		switch z.Next() {
		case xhtml.ErrorToken:
			return strings.TrimRight(b.String(), "\n")
		case xhtml.TextToken:
			b.Write(z.Text())
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			if name, _ := z.TagName(); string(name) == "br" {
				b.WriteByte('\n')
			}
		case xhtml.EndTagToken:
			switch name, _ := z.TagName(); string(name) {
			case "p", "div", "li", "blockquote", "h1", "h2", "h3", "h4", "h5", "h6", "pre":
				b.WriteByte('\n')
			}
		}
	}
}

// plainToGG converts plain text to GG HTML.
func plainToGG(text string) string {
	escaped := html.EscapeString(normalizeNewlines(text))
	return ggSpanOpen + strings.ReplaceAll(escaped, "\n", "<br>") + "</span>"
}

// matrixHTMLToGG converts a Matrix formatted body to GG HTML. It keeps b, i
// and u, turns line and block breaks into <br>, writes links as "text (url)",
// and drops reply fallbacks.
func matrixHTMLToGG(in string) string {
	var b strings.Builder
	open := map[string]int{}
	var linkHref string
	var linkText strings.Builder
	inLink := false
	skipDepth := 0
	z := xhtml.NewTokenizer(strings.NewReader(in))
	write := func(s string) {
		if inLink {
			linkText.WriteString(s)
		} else {
			b.WriteString(s)
		}
	}
	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			break
		}
		name, hasAttr := z.TagName()
		tag := string(name)
		if skipDepth > 0 {
			if tt == xhtml.StartTagToken && tag == "mx-reply" {
				skipDepth++
			} else if tt == xhtml.EndTagToken && tag == "mx-reply" {
				skipDepth--
			}
			continue
		}
		switch tt {
		case xhtml.TextToken:
			write(html.EscapeString(strings.ReplaceAll(normalizeNewlines(string(z.Text())), "\n", " ")))
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			switch {
			case tag == "mx-reply" && tt == xhtml.StartTagToken:
				skipDepth = 1
			case tag == "br":
				write("<br>")
			case tag == "a" && tt == xhtml.StartTagToken:
				inLink = true
				linkHref = ""
				if hasAttr {
					linkHref = attr(z, "href")
				}
				linkText.Reset()
			case tag == "li":
				write("• ")
			case formatTags[tag] != "" && tt == xhtml.StartTagToken:
				t := formatTags[tag]
				write("<" + t + ">")
				open[t]++
			}
		case xhtml.EndTagToken:
			switch {
			case tag == "a" && inLink:
				inLink = false
				text := linkText.String()
				b.WriteString(text)
				if linkHref != "" && html.UnescapeString(text) != linkHref && !strings.HasPrefix(linkHref, "https://matrix.to/") {
					b.WriteString(" (" + html.EscapeString(linkHref) + ")")
				}
			case formatTags[tag] != "":
				t := formatTags[tag]
				if open[t] > 0 {
					open[t]--
					write("</" + t + ">")
				}
			case tag == "p" || tag == "div" || tag == "li" || tag == "blockquote" || tag == "pre" || (len(tag) == 2 && tag[0] == 'h'):
				write("<br>")
			}
		}
	}
	if inLink {
		b.WriteString(linkText.String())
	}
	for _, t := range []string{"u", "i", "b"} {
		for ; open[t] > 0; open[t]-- {
			b.WriteString("</" + t + ">")
		}
	}
	out := strings.TrimSuffix(b.String(), "<br>")
	return ggSpanOpen + out + "</span>"
}

// splitText splits text into parts of at most limit characters. It prefers
// to split on a new line, then on a space.
func splitText(text string, limit int) []string {
	if limit <= 0 || utf8.RuneCountInString(text) <= limit {
		return []string{text}
	}
	var parts []string
	runes := []rune(text)
	for len(runes) > limit {
		cut := -1
		for i := limit; i > limit/2; i-- {
			if runes[i] == '\n' {
				cut = i
				break
			}
		}
		if cut < 0 {
			for i := limit; i > limit/2; i-- {
				if runes[i] == ' ' {
					cut = i
					break
				}
			}
		}
		if cut < 0 {
			parts = append(parts, string(runes[:limit]))
			runes = runes[limit:]
			continue
		}
		parts = append(parts, string(runes[:cut]))
		runes = runes[cut+1:]
	}
	if len(runes) > 0 {
		parts = append(parts, string(runes))
	}
	return parts
}

// matrixToGG converts Matrix content to one or more GG HTML messages.
func matrixToGG(content *event.MessageEventContent, limit int) []string {
	body := content.Body
	if content.MsgType == event.MsgEmote {
		body = "* " + body
	}
	if utf8.RuneCountInString(body) > limit && limit > 0 {
		// A long message loses its formatting, because the split points cannot follow the HTML.
		parts := splitText(normalizeNewlines(body), limit)
		out := make([]string, len(parts))
		for i, p := range parts {
			out[i] = plainToGG(p)
		}
		return out
	}
	if content.Format == event.FormatHTML && content.FormattedBody != "" {
		formatted := content.FormattedBody
		if content.MsgType == event.MsgEmote {
			formatted = "* " + formatted
		}
		return []string{matrixHTMLToGG(formatted)}
	}
	return []string{plainToGG(body)}
}
