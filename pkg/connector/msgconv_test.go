package connector

import (
	"reflect"
	"strings"
	"testing"

	"maunium.net/go/mautrix/event"
)

func TestGGToMatrixPlainOnly(t *testing.T) {
	c := ggToMatrix("line 1\r\nline 2", ggSpanOpen+"line 1<br>line 2</span>")
	if c.Body != "line 1\nline 2" {
		t.Errorf("body = %q", c.Body)
	}
	if c.FormattedBody != "" {
		t.Errorf("unexpected formatted body %q", c.FormattedBody)
	}
}

func TestGGToMatrixFormatting(t *testing.T) {
	c := ggToMatrix("bold & <x>", `<span style="color:#ff0000">`+`<b>bold</b> &amp; &lt;x&gt; <a href="javascript:alert(1)">bad</a><a href="https://e.org">ok</a><i>open`)
	want := `<b>bold</b> &amp; &lt;x&gt; bad<a href="https://e.org">ok</a><i>open</i>`
	if c.Format != event.FormatHTML || c.FormattedBody != want {
		t.Errorf("formatted = %q, want %q", c.FormattedBody, want)
	}
}

func TestGGToMatrixHTMLOnly(t *testing.T) {
	c := ggToMatrix("", "a<br>b")
	if c.Body != "a\nb" {
		t.Errorf("body = %q", c.Body)
	}
}

func TestMatrixToGGPlain(t *testing.T) {
	got := matrixToGG(&event.MessageEventContent{MsgType: event.MsgText, Body: "a <b>\nc"}, 100)
	want := []string{ggSpanOpen + "a &lt;b&gt;<br>c</span>"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}
}

func TestMatrixToGGFormatted(t *testing.T) {
	got := matrixToGG(&event.MessageEventContent{
		MsgType:       event.MsgText,
		Body:          "x",
		Format:        event.FormatHTML,
		FormattedBody: `<mx-reply><blockquote>old</blockquote></mx-reply><p><strong>hi</strong> <a href="https://e.org">link</a> <a href="https://f.org">https://f.org</a></p><p>two<br/>lines</p><ul><li>item</li></ul><code>x</code>`,
	}, 1000)
	want := ggSpanOpen + `<b>hi</b> link (https://e.org) https://f.org<br>two<br>lines<br>• item<br>x</span>`
	if len(got) != 1 || got[0] != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestMatrixToGGEmote(t *testing.T) {
	got := matrixToGG(&event.MessageEventContent{MsgType: event.MsgEmote, Body: "waves"}, 100)
	if got[0] != ggSpanOpen+"* waves</span>" {
		t.Errorf("got %q", got)
	}
}

func TestSplitText(t *testing.T) {
	text := strings.Repeat("word ", 10) + "\n" + strings.Repeat("x", 30)
	parts := splitText(text, 40)
	for _, p := range parts {
		if len([]rune(p)) > 40 {
			t.Errorf("part too long: %q", p)
		}
	}
	if strings.Join(parts, "") == "" || len(parts) < 2 {
		t.Fatalf("parts = %q", parts)
	}
	if got := splitText(strings.Repeat("ż", 25), 10); len(got) != 3 || got[0] != strings.Repeat("ż", 10) {
		t.Errorf("hard split = %q", got)
	}
	if got := splitText("short", 10); len(got) != 1 {
		t.Errorf("short = %q", got)
	}
}

func TestMatrixToGGLongMessageIsSplit(t *testing.T) {
	got := matrixToGG(&event.MessageEventContent{MsgType: event.MsgText, Body: strings.Repeat("abc ", 100), Format: event.FormatHTML, FormattedBody: "<b>x</b>"}, 150)
	if len(got) < 3 {
		t.Fatalf("got %d parts", len(got))
	}
	for _, p := range got {
		if strings.Contains(p, "<b>") {
			t.Errorf("split part keeps formatting: %q", p)
		}
	}
}
