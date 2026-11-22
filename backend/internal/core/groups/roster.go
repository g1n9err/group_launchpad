package groups

import "launchpad/internal/core"

type RosterUpdate struct {
	Students  []string
	Lecturers []string
	TAs       []string
}

func ValidateRosterUpdate(students, lecturers, tas *[]string) (RosterUpdate, error) {
	var update RosterUpdate
	fields := make(map[string]string)
	if students == nil && lecturers == nil && tas == nil {
		return update, core.Validation("provide students or lecturers", nil)
	}
	if students != nil {
		update.Students, fields["studentEmails"] = normalizeStudentEmails(*students)
		if update.Students == nil {
			update.Students = []string{}
		}
		if fields["studentEmails"] == "" {
			delete(fields, "studentEmails")
		}
	}
	if lecturers != nil {
		update.Lecturers, _ = normalizeStaffEmails(*lecturers, fields, "lecturerEmails")
		if update.Lecturers == nil {
			update.Lecturers = []string{}
		}
	}
	if tas != nil {
		update.TAs, _ = normalizeStaffEmails(*tas, fields, "taEmails")
		if update.TAs == nil {
			update.TAs = []string{}
		}
	}
	for _, lecturer := range update.Lecturers {
		for _, ta := range update.TAs {
			if lecturer == ta {
				fields["taEmails"] = "an email cannot have both staff roles"
			}
		}
	}
	for _, student := range update.Students {
		for _, email := range append(append([]string{}, update.Lecturers...), update.TAs...) {
			if student == email {
				fields["studentEmails"] = "an email cannot be both student and staff"
			}
		}
	}
	if len(fields) != 0 {
		return RosterUpdate{}, core.Validation("invalid roster", fields)
	}
	return update, nil
}
