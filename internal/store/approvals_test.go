package store

import (
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
)

func TestSetDocumentApprovalAndCreateApproval(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	doc, _, _ := s.CreateDocument("a.md", "hi", "human")

	if err := s.SetDocumentApproval(doc.ID, doc.CurrentVersionID, domain.DocApproved); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetDocument(doc.ID)
	if got.Status != domain.DocApproved || got.ApprovedVersionID != doc.CurrentVersionID {
		t.Fatalf("doc = %+v, want approved baseline pinned", got)
	}

	if _, err := s.CreateApproval(domain.Approval{
		DocID: doc.ID, VersionID: doc.CurrentVersionID, ApprovedBy: "human", Note: "ok",
	}); err != nil {
		t.Fatal(err)
	}
	apps, err := s.ListApprovals(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].VersionID != doc.CurrentVersionID {
		t.Fatalf("approvals = %+v, want one for the current version", apps)
	}
}
