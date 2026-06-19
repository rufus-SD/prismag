package orchestrator

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildPlanMapsAgents(t *testing.T) {
	reg := testRegistry(t)
	plan, err := BuildPlan("@@opus: design auth\n@@composer: build it", Options{Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Context != "ide" {
		t.Errorf("context = %q", plan.Context)
	}
	if len(plan.Blocks) != 2 {
		t.Fatalf("blocks = %d", len(plan.Blocks))
	}
	if plan.Blocks[0].Agent != "opus-planner" {
		t.Errorf("opus agent = %q", plan.Blocks[0].Agent)
	}
	if plan.Blocks[1].Agent != "composer-implementer" {
		t.Errorf("composer agent = %q", plan.Blocks[1].Agent)
	}
	if plan.Blocks[0].Model == "" || plan.Blocks[0].Task != "design auth" {
		t.Errorf("block0 = %+v", plan.Blocks[0])
	}
}

func TestBuildPlanNoAgentNote(t *testing.T) {
	reg := testRegistry(t)
	plan, err := BuildPlan("@@fast: summarize", Options{Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Blocks[0].Agent != "" {
		t.Errorf("fast should have no agent, got %q", plan.Blocks[0].Agent)
	}
	if plan.Blocks[0].Note == "" {
		t.Error("expected a note when no subagent is configured")
	}
}

func TestBuildPlanRoutesUntaggedToDefault(t *testing.T) {
	reg := registryWithDefault(t, "opus")
	plan, err := BuildPlan("create a folder on my desktop", Options{Registry: reg})
	if err != nil {
		t.Fatalf("untagged prompt with a default should route, got %v", err)
	}
	if len(plan.Blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(plan.Blocks))
	}
	if plan.Blocks[0].Alias != "opus" {
		t.Errorf("alias = %q, want opus", plan.Blocks[0].Alias)
	}
	if plan.Blocks[0].Task != "create a folder on my desktop" {
		t.Errorf("task = %q", plan.Blocks[0].Task)
	}
}

func TestBuildPlanUntaggedNoDefaultErrors(t *testing.T) {
	reg := testRegistry(t) // no default configured
	_, err := BuildPlan("create a folder on my desktop", Options{Registry: reg})
	if err == nil {
		t.Fatal("expected error: untagged prompt with no default")
	}
	if !strings.Contains(err.Error(), "default") {
		t.Errorf("error should hint at the default escape hatch: %v", err)
	}
}

func TestBuildPlanMalformedTagStillErrors(t *testing.T) {
	reg := registryWithDefault(t, "opus")
	// A line that looks tagged but isn't valid must not silently fall through
	// to the default — it's a typo, surface it.
	_, err := BuildPlan("@@ missing colon", Options{Registry: reg})
	if err == nil || !strings.Contains(err.Error(), "valid tag") {
		t.Fatalf("expected malformed-tag error, got %v", err)
	}
}

func TestBuildPlanUnknownAlias(t *testing.T) {
	reg := testRegistry(t)
	_, err := BuildPlan("@@nope: x", Options{Registry: reg})
	if err == nil {
		t.Fatal("expected error for unknown alias")
	}
}

func TestPlanMarkdownAndJSON(t *testing.T) {
	reg := testRegistry(t)
	plan, err := BuildPlan("shared note\n@@opus: design auth", Options{Registry: reg, SharedContext: "ctx blob"})
	if err != nil {
		t.Fatal(err)
	}

	md := plan.Markdown()
	if !strings.Contains(md, "ROUTING PLAN") || !strings.Contains(md, "opus-planner") {
		t.Errorf("markdown = %q", md)
	}
	if !strings.Contains(md, "ctx blob") {
		t.Errorf("markdown should include shared context: %q", md)
	}

	js, err := plan.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var round Plan
	if err := json.Unmarshal([]byte(js), &round); err != nil {
		t.Fatalf("json invalid: %v", err)
	}
	if len(round.Blocks) != 1 || round.Blocks[0].Agent != "opus-planner" {
		t.Errorf("round-tripped plan = %+v", round)
	}
}
