package contract

import (
	"testing"
	"time"
)

func validDoc() CanonicalDoc {
	return CanonicalDoc{
		Source:   "postgres",
		SourceID: "documents:42",
		DocID:    "postgres:documents:42",
		TenantID: 7,
		Op:       OpUpsert,
		Tier:     TierUrgent,
		Version:  3,
		CommitTS: time.Now(),
		Title:    "hello",
		Body:     "world",
	}
}

func TestValidateOK(t *testing.T) {
	d := validDoc()
	if err := d.Validate(); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*CanonicalDoc){
		"no source":  func(d *CanonicalDoc) { d.Source = "" },
		"no doc_id":  func(d *CanonicalDoc) { d.DocID = "" },
		"bad op":     func(d *CanonicalDoc) { d.Op = "frobnicate" },
		"bad tier":   func(d *CanonicalDoc) { d.Tier = "someday" },
		"neg ver":    func(d *CanonicalDoc) { d.Version = -1 },
		"zero ts":    func(d *CanonicalDoc) { d.CommitTS = time.Time{} },
		"empty body": func(d *CanonicalDoc) { d.Title = ""; d.Body = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d := validDoc()
			mutate(&d)
			if err := d.Validate(); err == nil {
				t.Fatalf("expected error for %s, got nil", name)
			}
		})
	}
}

func TestPointIDDeterministic(t *testing.T) {
	d1 := validDoc()
	d2 := validDoc()
	d2.Version = 99 // version must not affect point id
	if d1.PointID() != d2.PointID() {
		t.Fatalf("point id should depend only on doc_id: %s != %s", d1.PointID(), d2.PointID())
	}
	// Different doc_id -> different point id.
	d3 := validDoc()
	d3.DocID = "postgres:documents:43"
	if d1.PointID() == d3.PointID() {
		t.Fatal("different doc_id should yield different point id")
	}
	// Well-formed UUID length.
	if len(d1.PointID()) != 36 {
		t.Fatalf("expected 36-char uuid, got %q", d1.PointID())
	}
}

func TestNamespacedDocID(t *testing.T) {
	if got := NamespacedDocID("postgres", "documents:42"); got != "postgres:documents:42" {
		t.Fatalf("unexpected: %s", got)
	}
}

func TestText(t *testing.T) {
	d := validDoc()
	if d.Text() != "hello\n\nworld" {
		t.Fatalf("unexpected text: %q", d.Text())
	}
	d.Body = ""
	if d.Text() != "hello" {
		t.Fatalf("title-only: %q", d.Text())
	}
}
