package licenses

import (
	"strings"
	"testing"
)

func TestTextNamesEveryPart(t *testing.T) {
	for _, want := range []string{
		"kickd: LICENSE",
		"MIT License",
		"Go standard library: LICENSE",
		"The Go Authors",
		"modernc.org/sqlite",
		"go.yaml.in/yaml/v3",
	} {
		if !strings.Contains(Text, want) {
			t.Errorf("licenses.txt lacks %q", want)
		}
	}
}
