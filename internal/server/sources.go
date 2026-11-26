package server

import (
	"strings"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/nameday"
	rh "github.com/hejmo-home/hejmo/internal/religiousholiday"
)

// Ticket 377: the source named next to every holiday and name-day choice. One builder per kind makes the text for the
// settings page and for Settings > Licences, so the two cannot disagree.

// sourceLine is the line under a choice: "Source: …" for one source, "Sources: …; …" for several. Unchecked says
// that a source has entries that were not yet checked against it (the page adds the .src-unchecked note).
type sourceLine struct {
	Text      string
	Unchecked bool
}

// joinSources writes the texts of the sources as the line of a choice.
func (s *server) joinSources(lang string, texts []string) string {
	joined := strings.Join(texts, "; ")
	if len(texts) > 1 {
		return s.i18n.Format(lang, i18n.Msg("src.lines", "sources", joined))
	}
	return s.i18n.Format(lang, i18n.Msg("src.line", "source", joined))
}

// rhSourceUnchecked says whether a holiday source has entries that were not yet checked: a table with such entries,
// and the fixed days of the Alevi federation, which are all unchecked.
func rhSourceUnchecked(id string) bool {
	if id == rh.SrcAABF {
		return true
	}
	for _, t := range rh.Tables() {
		if t.ID == id {
			return t.UncheckedEntries > 0
		}
	}
	return false
}

// rhSourceLine is the line of a religion: the text of every source of its days (rh.source.<id>, with the years of its
// table), in order of first appearance.
func (s *server) rhSourceLine(lang string, r rh.Religion, o rh.Options) sourceLine {
	var texts []string
	var line sourceLine
	for _, id := range rh.SourcesOf(r, o) {
		texts = append(texts, s.rhSourceName(lang, id))
		line.Unchecked = line.Unchecked || rhSourceUnchecked(id)
	}
	line.Text = s.joinSources(lang, texts)
	return line
}

// nameDaySourceLine is the line of a name-day list, from the sources of its row in Licences.
func (s *server) nameDaySourceLine(lang string, l nameday.List) sourceLine {
	row, ok := s.nameDayRow(lang, l)
	if !ok {
		return sourceLine{}
	}
	return s.licenceSourceLine(lang, row)
}

// slavaSourceLine is the line of the Slava, from its row in Licences.
func (s *server) slavaSourceLine(lang string) sourceLine {
	return s.licenceSourceLine(lang, s.slavaRow(lang))
}

func (s *server) licenceSourceLine(lang string, row nameDayLicence) sourceLine {
	texts := make([]string, len(row.Sources))
	for i, src := range row.Sources {
		texts[i] = src.Text
	}
	return sourceLine{Text: s.joinSources(lang, texts), Unchecked: row.Unchecked}
}

// rhSourceOf is a source line of a religion card. Islam and Alevism have one for each Islamic calendar (the page shows
// the one of the calendar chosen), so the choice works before saving and without JavaScript.
type rhSourceOf struct {
	Islamic   string // "diyanet" or "umm_al_qura" for the lines that follow the Islamic calendar; else empty
	Text      string
	Unchecked bool
}

func (s *server) rhSourcesOf(lang string, r rh.Religion) []rhSourceOf {
	if r != rh.Muslim && r != rh.Alevi {
		l := s.rhSourceLine(lang, r, rh.Options{})
		return []rhSourceOf{{Text: l.Text, Unchecked: l.Unchecked}}
	}
	var out []rhSourceOf
	for _, c := range []rh.IslamicCalendar{rh.Diyanet, rh.UmmAlQura} {
		l := s.rhSourceLine(lang, r, rh.Options{Islamic: c})
		out = append(out, rhSourceOf{Islamic: string(c), Text: l.Text, Unchecked: l.Unchecked})
	}
	return out
}
