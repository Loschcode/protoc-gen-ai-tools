package generator

import (
	"strings"
	"testing"

	"github.com/Loschcode/protoc-gen-ai-tools/internal/annotations"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/types/descriptorpb"
)

// toolFieldDescriptionOptions builds (ai.tools.v1.tool_field) with the
// descriptive fields set: 3 = usage_notes, 4 = description.
func toolFieldDescriptionOptions(usageNotes, description string) *descriptorpb.FieldOptions {
	var inner []byte
	if usageNotes != "" {
		inner = protowire.AppendTag(inner, 3, protowire.BytesType)
		inner = protowire.AppendString(inner, usageNotes)
	}
	if description != "" {
		inner = protowire.AppendTag(inner, 4, protowire.BytesType)
		inner = protowire.AppendString(inner, description)
	}
	if len(inner) == 0 {
		return &descriptorpb.FieldOptions{}
	}

	var raw []byte
	raw = protowire.AppendTag(raw, extToolField, protowire.BytesType)
	raw = protowire.AppendBytes(raw, inner)

	opts := &descriptorpb.FieldOptions{}
	opts.ProtoReflect().SetUnknown(raw)
	return opts
}

func TestUsageNotesAppendToDescription(t *testing.T) {
	const comment = "The shortlink (URL slug) for the link."
	const notes = "Send an empty string unless the user explicitly asked for a slug."

	got := composeDescription(comment, notes, "")

	if !strings.Contains(got, comment) {
		t.Fatalf("factual description was dropped; usage_notes must append, not replace.\ngot: %q", got)
	}
	if !strings.Contains(got, notes) {
		t.Fatalf("usage_notes missing from description.\ngot: %q", got)
	}
	if !strings.Contains(got, comment+"\n\n"+notes) {
		t.Fatalf("expected comment then blank line then notes.\ngot: %q", got)
	}
}

func TestDescriptionOverridesComment(t *testing.T) {
	const comment = "Public wording that would mislead a model."
	const override = "Model-facing wording."

	got := composeDescription(comment, "", override)

	if strings.Contains(got, comment) {
		t.Fatalf("description must replace the comment outright.\ngot: %q", got)
	}
	if got != override {
		t.Fatalf("expected %q, got %q", override, got)
	}
}

func TestDescriptionOverrideStillTakesUsageNotes(t *testing.T) {
	got := composeDescription("comment", "notes", "override")

	if strings.Contains(got, "comment") {
		t.Fatalf("override should have replaced the comment.\ngot: %q", got)
	}
	if !strings.Contains(got, "override") || !strings.Contains(got, "notes") {
		t.Fatalf("expected override plus notes.\ngot: %q", got)
	}
}

func TestNoAnnotationsFallsBackToComment(t *testing.T) {
	if got := composeDescription("just a comment", "", ""); got != "just a comment" {
		t.Fatalf("expected the bare comment, got %q", got)
	}
}

func TestUsageNotesParsedFromWire(t *testing.T) {
	opts := toolFieldDescriptionOptions("some notes", "some description")
	raw := opts.ProtoReflect().GetUnknown()
	if len(raw) == 0 {
		t.Fatal("expected extension bytes to be written")
	}
	// Round-trips through the same parser the generator uses.
	parsed := annotations.ParseToolFieldOptions(raw)
	if parsed.UsageNotes != "some notes" {
		t.Fatalf("usage_notes: got %q", parsed.UsageNotes)
	}
	if parsed.Description != "some description" {
		t.Fatalf("description: got %q", parsed.Description)
	}
}
