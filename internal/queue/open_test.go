package queue

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The agent and a subcommand such as "kickd event" may create the database
// at the same moment.
func TestConcurrentFirstOpen(t *testing.T) {
	failures := 0
	for i := 0; i < 40; i++ {
		path := filepath.Join(t.TempDir(), "kickd.db")
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for j := 0; j < 2; j++ {
			wg.Add(1)
			go func(j int) {
				defer wg.Done()
				s, err := Open(path)
				errs[j] = err
				if err == nil {
					s.Close()
				}
			}(j)
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				failures++
				if failures <= 3 {
					t.Logf("open failed: %v", err)
				}
			}
		}
	}
	if failures > 0 {
		t.Fatalf("%d of 80 concurrent opens failed", failures)
	}
}

// A database that a newer kickd upgraded has a schema that this kickd does
// not know, so Open refuses it rather than misread its runs.
func TestOpenRefusesADatabaseWithANewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kickd.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	s.Close()
	want := fmt.Sprintf("schema version %d is newer than this program supports (%d)", schemaVersion+1, schemaVersion)
	if s, err := Open(path); err == nil || !strings.Contains(err.Error(), want) {
		if err == nil {
			s.Close()
		}
		t.Fatalf("Open = %v, want an error with %q", err, want)
	}
}
