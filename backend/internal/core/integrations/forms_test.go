package integrations

import "testing"

// ── Mandatory identity questions (name + email on EVERY form) ──────────────

func TestNormalizeAddsMandatoryIdentityQuestions(t *testing.T) {
	// Custom questions only → Full name + Email address are prepended,
	// text, required.
	got, err := normalizeIntakeQuestions([]IntakeQuestion{
		{Title: "Phone number", Type: "text"},
		{Title: "Preferred contact", Type: "single_choice", Options: []string{"Email", "Phone"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Title != "Full name" || !got[0].Required || got[0].Type != "text" {
		t.Errorf("first = %+v, want canonical Full name", got[0])
	}
	if got[1].Title != "Email address" || !got[1].Required {
		t.Errorf("second = %+v, want canonical Email address", got[1])
	}
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
	if got[2].Title != "Phone number" || got[3].Title != "Preferred contact" {
		t.Errorf("custom order lost: %+v", got[2:])
	}
}

func TestNormalizeCanonicalizesAliasesAndForcesRequired(t *testing.T) {
	got, err := normalizeIntakeQuestions([]IntakeQuestion{
		{Title: "  ԱՆՈՒՆ ԱԶԳԱՆՈՒՆ ", Type: "paragraph"},  // Armenian alias, wrong type/flag
		{Title: "E-mail", Type: "text", Required: false}, // alias, not required
		{Title: "Which IDE do you use?", Type: "text"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Title != "Full name" || got[0].Type != "text" || !got[0].Required {
		t.Errorf("name alias → %+v", got[0])
	}
	if got[1].Title != "Email address" || !got[1].Required {
		t.Errorf("email alias → %+v", got[1])
	}
	if got[2].Title != "Which IDE do you use?" {
		t.Errorf("custom question lost: %+v", got[2])
	}
}

func TestNormalizeRejectsDuplicateIdentityQuestions(t *testing.T) {
	if _, err := normalizeIntakeQuestions([]IntakeQuestion{
		{Title: "Name", Type: "text"}, {Title: "Full name", Type: "text"},
	}); err == nil {
		t.Fatal("duplicate name question accepted")
	}
}
