package notes

import (
	"strings"
	"testing"
)

func TestLocateEdits(t *testing.T) {
	text := "# A\n- Книга\n- Шарф\n- Книга про шарф"
	for _, c := range []struct {
		name  string
		edits []Edit
		want  string
		err   string
	}{
		{"one", []Edit{{"- Шарф", "- Шапка"}}, "# A\n- Книга\n- Шапка\n- Книга про шарф", ""},
		{"several out of order", []Edit{{"про шарф", "про шапку"}, {"# A", "# Б"}}, "# Б\n- Книга\n- Шарф\n- Книга про шапку", ""},
		{"multiline", []Edit{{"- Шарф\n- Книга про", "- Книга про"}}, "# A\n- Книга\n- Книга про шарф", ""},
		{"delete", []Edit{{"\n- Шарф", ""}}, "# A\n- Книга\n- Книга про шарф", ""},
		{"missing", []Edit{{"- Варежки", "x"}}, "", "not found"},
		{"duplicate", []Edit{{"- Книга", "x"}}, "", "more than once"},
		{"overlap", []Edit{{"Книга\n- Шарф", "x"}, {"Шарф\n- Книга про", "y"}}, "", "overlap"},
		{"empty old", []Edit{{"", "x"}}, "", "must not be empty"},
		{"none", nil, "", "must not be empty"},
	} {
		spans, err := locateEdits(text, c.edits)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: got %v, want error with %q", c.name, err, c.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got := replaceSpans(text, spans); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestNormalizeEdits(t *testing.T) {
	got := normalizeEdits([]Edit{{"a\r\nb", "c\r\nd"}})
	if got[0].Old != "a\nb" || got[0].New != "c\nd" {
		t.Errorf("got %+v", got)
	}
}
