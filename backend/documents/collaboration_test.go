package documents

import (
	"errors"
	"sync"
	"testing"
)

func TestContentUpdateRequiresRevisionBeforeMoving(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.Create(CreateInput{Path: "shared.md", Type: "document", Content: "original"}); err != nil {
		t.Fatal(err)
	}
	original, err := service.Get("shared.md")
	if err != nil {
		t.Fatal(err)
	}
	content, destination := "replacement", "moved.md"
	_, err = service.UpdateWithResult(UpdateInput{Path: original.Path, Content: &content, NewPath: &destination})
	if !errors.Is(err, ErrRevisionRequired) {
		t.Fatalf("update without revision = %v", err)
	}
	current, err := service.Get(original.Path)
	if err != nil || current.Revision != original.Revision {
		t.Fatalf("original changed: %#v, %v", current, err)
	}
	if _, err := service.Get(destination); !errors.Is(err, ErrNotFound) {
		t.Fatalf("destination exists: %v", err)
	}
}

func TestConcurrentAgentsKeepIndependentProposals(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.ConfigureApplication(ApplicationSettingsInput{
		Profile: ProfileSettings{Type: ProfileHuman}, AIPermissions: Permissions{View: true, Edit: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.Create(CreateInput{Path: "shared.md", Type: "document", Content: "---\nai_editable: true\n---\n\noriginal"}); err != nil {
		t.Fatal(err)
	}
	original, err := service.Get("shared.md")
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for _, content := range []string{"agent one", "agent two", "agent three"} {
		group.Add(1)
		go func(content string) {
			defer group.Done()
			_, err := service.callMCPTool("update", map[string]string{
				"path": original.Path, "content": content, "baseRevision": original.Revision,
			})
			if err != nil {
				t.Errorf("agent proposal: %v", err)
			}
		}(content)
	}
	group.Wait()
	proposals := service.Proposals()
	if len(proposals) != 3 {
		t.Fatalf("retained %d proposals, want 3", len(proposals))
	}
	seen := make(map[string]bool)
	for _, proposal := range proposals {
		if seen[proposal.ID] || proposal.OriginalContent != original.Content || proposal.Status != ProposalInReview {
			t.Fatalf("invalid independent proposal: %#v", proposal)
		}
		seen[proposal.ID] = true
	}
	current, err := service.Get(original.Path)
	if err != nil || current.Revision != original.Revision {
		t.Fatalf("agents changed stored content: %#v, %v", current, err)
	}
	if err := service.AcceptProposal(proposals[0].ID, nil); err != nil {
		t.Fatal(err)
	}
	var conflict *EditConflictError
	if err := service.AcceptProposal(proposals[1].ID, nil); !errors.As(err, &conflict) {
		t.Fatalf("stale merge = %v, want edit conflict", err)
	}
	current, err = service.Get(original.Path)
	if err != nil || current.Revision != conflict.Current.Revision {
		t.Fatalf("conflict did not preserve latest document: %#v, %v", current, err)
	}
	if err := service.RejectProposal(proposals[2].ID); err != nil {
		t.Fatal(err)
	}
	for _, proposal := range service.Proposals() {
		want := ProposalInReview
		if proposal.ID == proposals[0].ID {
			want = ProposalMerged
		} else if proposal.ID == proposals[2].ID {
			want = ProposalRejected
		}
		if proposal.Status != want {
			t.Fatalf("proposal %s status = %s, want %s", proposal.ID, proposal.Status, want)
		}
	}
}

func TestAgentCannotMergeProposal(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.Create(CreateInput{Path: "shared.md", Type: "document", Content: "---\nai_editable: true\n---\n\noriginal"}); err != nil {
		t.Fatal(err)
	}
	original, err := service.Get("shared.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfigureApplication(ApplicationSettingsInput{
		Profile: ProfileSettings{Type: ProfileAI}, AIPermissions: Permissions{View: true, Edit: true},
	}); err != nil {
		t.Fatal(err)
	}
	content := "agent change"
	result, err := service.UpdateWithResult(UpdateInput{Path: original.Path, Content: &content, BaseRevision: original.Revision})
	if err != nil || result.Proposal == nil {
		t.Fatalf("submit proposal: %#v, %v", result, err)
	}
	if err := service.AcceptProposal(result.Proposal.ID, nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("agent merge = %v, want forbidden", err)
	}
	current, err := service.Get(original.Path)
	if err != nil || current.Revision != original.Revision {
		t.Fatalf("agent merge changed document: %#v, %v", current, err)
	}
}
