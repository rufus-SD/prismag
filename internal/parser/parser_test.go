package parser

import "testing"

func TestParseSingleBlock(t *testing.T) {
	got := Parse("@@opus: design the auth flow")
	if got.Preamble != "" {
		t.Errorf("preamble = %q, want empty", got.Preamble)
	}
	if len(got.Tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(got.Tasks))
	}
	tk := got.Tasks[0]
	if tk.Alias != "opus" || tk.RawAlias != "opus" || tk.Task != "design the auth flow" || tk.Index != 0 {
		t.Errorf("task = %+v", tk)
	}
}

func TestParseMultipleBlocksInOrder(t *testing.T) {
	input := "@@opus: review the security implications\n" +
		"@@composer: write the unit tests\n" +
		"@@fast: summarize the diff"
	got := Parse(input)
	want := []string{"opus", "composer", "fast"}
	if len(got.Tasks) != len(want) {
		t.Fatalf("tasks = %d, want %d", len(got.Tasks), len(want))
	}
	for i, w := range want {
		if got.Tasks[i].Alias != w {
			t.Errorf("task[%d].Alias = %q, want %q", i, got.Tasks[i].Alias, w)
		}
		if got.Tasks[i].Index != i {
			t.Errorf("task[%d].Index = %d, want %d", i, got.Tasks[i].Index, i)
		}
	}
	if got.Tasks[1].Task != "write the unit tests" {
		t.Errorf("task[1].Task = %q", got.Tasks[1].Task)
	}
}

func TestParsePreamble(t *testing.T) {
	got := Parse("Here is the module we are working on.\n@@opus: explain it")
	if got.Preamble != "Here is the module we are working on." {
		t.Errorf("preamble = %q", got.Preamble)
	}
	if len(got.Tasks) != 1 || got.Tasks[0].Task != "explain it" {
		t.Errorf("tasks = %+v", got.Tasks)
	}
}

func TestParseCaseInsensitiveAlias(t *testing.T) {
	got := Parse("@@Opus: do a thing")
	if got.Tasks[0].Alias != "opus" {
		t.Errorf("alias = %q, want opus", got.Tasks[0].Alias)
	}
	if got.Tasks[0].RawAlias != "Opus" {
		t.Errorf("rawAlias = %q, want Opus", got.Tasks[0].RawAlias)
	}
}

func TestParseMultiLineBody(t *testing.T) {
	input := "@@composer: implement the handler\n  with retries\n  and logging"
	got := Parse(input)
	want := "implement the handler\n  with retries\n  and logging"
	if got.Tasks[0].Task != want {
		t.Errorf("task = %q, want %q", got.Tasks[0].Task, want)
	}
}

func TestParseColonInBody(t *testing.T) {
	got := Parse("@@opus: use an aspect ratio of 3:1 here")
	if len(got.Tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(got.Tasks))
	}
	if got.Tasks[0].Task != "use an aspect ratio of 3:1 here" {
		t.Errorf("task = %q", got.Tasks[0].Task)
	}
}

func TestParseIgnoresMidLineEmail(t *testing.T) {
	got := Parse("ping me at me@example.com about this\n@@fast: ack")
	if got.Preamble != "ping me at me@example.com about this" {
		t.Errorf("preamble = %q", got.Preamble)
	}
	if len(got.Tasks) != 1 || got.Tasks[0].Alias != "fast" {
		t.Errorf("tasks = %+v", got.Tasks)
	}
}

func TestParseBareAtTag(t *testing.T) {
	got := Parse("@opus: plan it")
	if len(got.Tasks) != 1 || got.Tasks[0].Alias != "opus" {
		t.Errorf("tasks = %+v", got.Tasks)
	}
}

func TestParseLeadingWhitespace(t *testing.T) {
	got := Parse("   @@fast: trim me")
	if len(got.Tasks) != 1 || got.Tasks[0].Task != "trim me" {
		t.Errorf("tasks = %+v", got.Tasks)
	}
}

func TestParseUntagged(t *testing.T) {
	got := Parse("just a plain prompt, no tags")
	if len(got.Tasks) != 0 {
		t.Errorf("tasks = %d, want 0", len(got.Tasks))
	}
	if got.Preamble != "just a plain prompt, no tags" {
		t.Errorf("preamble = %q", got.Preamble)
	}
}

func TestParseHyphenUnderscoreAlias(t *testing.T) {
	got := Parse("@@gpt-4o_mini: hi")
	if got.Tasks[0].Alias != "gpt-4o_mini" {
		t.Errorf("alias = %q", got.Tasks[0].Alias)
	}
}

func TestParseDottedAlias(t *testing.T) {
	got := Parse("@@composer2.5: check the project")
	if len(got.Tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(got.Tasks))
	}
	if got.Tasks[0].Alias != "composer2.5" {
		t.Errorf("alias = %q, want composer2.5", got.Tasks[0].Alias)
	}
	if got.Tasks[0].Task != "check the project" {
		t.Errorf("task = %q", got.Tasks[0].Task)
	}
}

func TestParseDottedAliasWithSpaceBeforeColon(t *testing.T) {
	got := Parse("@@opus4.8 : review")
	if len(got.Tasks) != 1 || got.Tasks[0].Alias != "opus4.8" {
		t.Fatalf("tasks = %+v", got.Tasks)
	}
}

func TestLooksTagged(t *testing.T) {
	if !LooksTagged("@@ not a real tag") {
		t.Error("expected LooksTagged for malformed @@ line")
	}
	if LooksTagged("@@opus: fine") {
		t.Error("valid tag should not be LooksTagged")
	}
	if LooksTagged("no tags at all") {
		t.Error("plain text should not be LooksTagged")
	}
}

func TestHasTags(t *testing.T) {
	cases := map[string]bool{
		"@@opus: x":          true,
		"no tags here":       false,
		"email a@b.com only": false,
		"@fast: y":           true,
	}
	for in, want := range cases {
		if got := HasTags(in); got != want {
			t.Errorf("HasTags(%q) = %v, want %v", in, got, want)
		}
	}
}
