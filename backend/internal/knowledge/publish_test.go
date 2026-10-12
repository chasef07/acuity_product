package knowledge

import (
	"errors"
	"testing"
)

func TestAbitaStandardRejectsMarkdown(t *testing.T) {
	sections := func(text string) []Section {
		result := make([]Section, len(requiredAbitaEntries))
		for i, id := range requiredAbitaEntries {
			result[i] = Section{ID: id, Title: id, Text: "Plain fact."}
		}
		result[0].Text = text
		return result
	}
	for _, text := range []string{
		"# Address", "- First line", "* First line", "+ First line", "> Quoted", "1. First step", "2) Second step",
		"See **this**", "See __this__", "Use `code`", "Visit [our site](https://example.invalid)",
		"An *emphasized* word", "An _emphasized_ word", ">Quoted", "*front_desk email*", "*Note*—bring ID", "\"*Note*\" applies", "_Note_—bring ID",
	} {
		var entry *EntryError
		if err := validateAbitaStandard(sections(text)); !errors.As(err, &entry) || entry.EntryID != requiredAbitaEntries[0] {
			t.Errorf("%q: %v", text, err)
		}
	}
	for _, text := range []string{
		"Suite 100, 1. 5 miles north of the hospital.", "Dr. Smith sees patients 9:00 AM–5:00 PM.",
		"Email front_desk@example.invalid. Fees are $25 - $40.", "Ages 2-6 need a parent (or guardian) present.",
		"Prices: frames from $99; lenses * not included.",
	} {
		if err := validateAbitaStandard(sections(text)); err != nil {
			t.Errorf("%q: %v", text, err)
		}
	}
}
