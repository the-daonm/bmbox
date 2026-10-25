package bmc

import (
	"testing"
)

func TestResolvePassword(t *testing.T) {
	dir := t.TempDir()
	w := &dirWriter{secrets: map[string]bool{}}

	gen, generated, err := ResolvePassword(dir, "", w)
	if err != nil || !generated || len(gen) != generatedLen || !w.secrets["password"] {
		t.Fatalf("generate: %q generated=%v err=%v secrets=%v", gen, generated, err, w.secrets)
	}
	again, generated, err := ResolvePassword(dir, "", w)
	if err != nil || generated || again != gen {
		t.Fatalf("second run must reuse the stored password: %q generated=%v err=%v", again, generated, err)
	}
	given, _, err := ResolvePassword(dir, "s3cret", w)
	if err != nil || given != "s3cret" {
		t.Fatalf("given: %q, %v", given, err)
	}
	if stored, _ := ReadPassword(dir); stored != "s3cret" {
		t.Errorf("stored = %q, want the given password", stored)
	}
	// Dropping the password from the topology keeps the last one.
	if kept, _, _ := ResolvePassword(dir, "", w); kept != "s3cret" {
		t.Errorf("kept = %q", kept)
	}
}

func TestRandomPasswordsDiffer(t *testing.T) {
	a, _ := randomPassword(generatedLen)
	b, _ := randomPassword(generatedLen)
	if a == b {
		t.Errorf("two random passwords are equal: %q", a)
	}
}
