package knowledge

import (
	"errors"
	"testing"
)

func TestAbitaStandardRejectsHeadingsInEntryText(t *testing.T) {
	sections := func(text string) []Section {
		result := make([]Section, len(requiredAbitaEntries))
		for i, id := range requiredAbitaEntries {
			result[i] = Section{ID: id, Title: id, Text: "Plain fact."}
		}
		result[0].Text = text
		return result
	}
	for _, text := range []string{"# Address", "## Address", "Intro\n### Parking", "  #### Parking", "######"} {
		var entry *EntryError
		if err := validateAbitaStandard(sections(text)); !errors.As(err, &entry) || entry.EntryID != requiredAbitaEntries[0] {
			t.Errorf("%q: %v", text, err)
		}
	}
	for _, text := range []string{
		"- First line\n- Second line", "1. First step\n2. Second step", "See **this**", "> Quoted", "Use `code`",
		"Visit [our site](https://example.invalid)", "Take the #5 bus.", "#hashtag on social media", "Suite #201",
	} {
		if err := validateAbitaStandard(sections(text)); err != nil {
			t.Errorf("%q: %v", text, err)
		}
	}
}

func TestAbitaStandardRejectsLineBreaksInTitles(t *testing.T) {
	for _, title := range []string{"Hours\n# Extra", "Hours\r\nExtra"} {
		sections := make([]Section, len(requiredAbitaEntries))
		for i, id := range requiredAbitaEntries {
			sections[i] = Section{ID: id, Title: id, Text: "Plain fact."}
		}
		sections[0].Title = title
		var entry *EntryError
		if err := validateAbitaStandard(sections); !errors.As(err, &entry) || entry.EntryID != requiredAbitaEntries[0] {
			t.Errorf("%q: %v", title, err)
		}
	}
}
