package generator

import "testing"

func TestSnakeToPascal(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"page_themes_update", "PageThemesUpdate"},
		{"links_create", "LinksCreate"},
		{"workflow_steps_create_relationship", "WorkflowStepsCreateRelationship"},
		{"simple", "Simple"},
		{"", ""},
		{"a", "A"},
		{"a_b_c", "ABC"},
		{"already_Pascal", "AlreadyPascal"},
		{"__leading_underscores", "LeadingUnderscores"},
		{"trailing__", "Trailing"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := snakeToPascal(tc.input)
			if got != tc.expected {
				t.Errorf("snakeToPascal(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}
