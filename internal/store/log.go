package store

import (
	"sort"

	"github.com/rajanrx/outbox-md/internal/domain"
)

// kindOrder is a deterministic tiebreak for events that share a one-second
// created_at timestamp.
var kindOrder = map[string]int{"created": 0, "comment": 1, "proposal": 2, "edit": 3, "approval": 4}

const excerptMax = 80

func excerpt(content string, start, end int) string {
	r := []rune(content)
	if start < 0 {
		start = 0
	}
	if end > len(r) {
		end = len(r)
	}
	if start >= end {
		return ""
	}
	out := []rune(string(r[start:end]))
	if len(out) > excerptMax {
		return string(out[:excerptMax]) + "…"
	}
	return string(out)
}

// ListDecisionLog returns the document's decision timeline, ascending by
// (time, kind-priority), derived live from versions, comments, suggestions, and
// approvals. No writes.
func (s *Store) ListDecisionLog(docID string) ([]domain.LogEntry, error) {
	out := []domain.LogEntry{}

	// versions → created (ordinal 1) / edit (ordinal > 1); keep content + ordinal
	// maps for comment excerpts and approval ordinals.
	verContent := map[string]string{}
	verOrdinal := map[string]int{}
	vrows, err := s.DB.Query(`SELECT id, ordinal, content, created_by, created_at FROM versions WHERE doc_id=? ORDER BY ordinal`, docID)
	if err != nil {
		return nil, err
	}
	for vrows.Next() {
		var id, content, by, at string
		var ord int
		if err := vrows.Scan(&id, &ord, &content, &by, &at); err != nil {
			vrows.Close()
			return nil, err
		}
		verContent[id] = content
		verOrdinal[id] = ord
		kind := "edit"
		if ord == 1 {
			kind = "created"
		}
		out = append(out, domain.LogEntry{Time: at, Kind: kind, Actor: by, Version: ord})
	}
	vrows.Close()
	if err := vrows.Err(); err != nil {
		return nil, err
	}

	// comments → comment; excerpt sliced from the against-version content.
	crows, err := s.DB.Query(`SELECT against_version_id, anchor_start, anchor_end, author_identity, created_at FROM comments WHERE doc_id=?`, docID)
	if err != nil {
		return nil, err
	}
	for crows.Next() {
		var avid, by, at string
		var start, end int
		if err := crows.Scan(&avid, &start, &end, &by, &at); err != nil {
			crows.Close()
			return nil, err
		}
		out = append(out, domain.LogEntry{Time: at, Kind: "comment", Actor: by, Detail: excerpt(verContent[avid], start, end)})
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return nil, err
	}

	// suggestions (joined to the doc via comments) → proposal.
	srows, err := s.DB.Query(`SELECT sg.created_by, sg.created_at FROM suggestions sg JOIN comments c ON sg.comment_id=c.id WHERE c.doc_id=?`, docID)
	if err != nil {
		return nil, err
	}
	for srows.Next() {
		var by, at string
		if err := srows.Scan(&by, &at); err != nil {
			srows.Close()
			return nil, err
		}
		out = append(out, domain.LogEntry{Time: at, Kind: "proposal", Actor: by})
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return nil, err
	}

	// approvals → approval; the earliest is "approved", later ones "re-approved".
	// rowid tiebreak keeps same-second approvals in insertion order so the
	// first-vs-re-approval distinction is deterministic.
	arows, err := s.DB.Query(`SELECT version_id, approved_by, note, created_at FROM approvals WHERE doc_id=? ORDER BY created_at, rowid`, docID)
	if err != nil {
		return nil, err
	}
	firstApproval := true
	for arows.Next() {
		var vid, by, note, at string
		if err := arows.Scan(&vid, &by, &note, &at); err != nil {
			arows.Close()
			return nil, err
		}
		out = append(out, domain.LogEntry{Time: at, Kind: "approval", Actor: by, Detail: note, Version: verOrdinal[vid], ReApproval: !firstApproval})
		firstApproval = false
	}
	arows.Close()
	if err := arows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Time != out[j].Time {
			return out[i].Time < out[j].Time
		}
		return kindOrder[out[i].Kind] < kindOrder[out[j].Kind]
	})
	return out, nil
}
