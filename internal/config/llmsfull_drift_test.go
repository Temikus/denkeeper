package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestLLMsFullTxt_CoversAllTOMLKeys(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	llmsPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "website", "static", "llms-full.txt")

	data, err := os.ReadFile(llmsPath)
	if err != nil {
		t.Fatalf("reading llms-full.txt: %v", err)
	}
	content := string(data)

	tags := collectTOMLTags(reflect.TypeOf(Config{}))

	var missing []string
	for _, tag := range tags {
		if !tagDocumented(content, tag) {
			missing = append(missing, tag)
		}
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("llms-full.txt is missing %d TOML config key(s) — update website/static/llms-full.txt:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// tagDocumented checks that a TOML key appears as a config key ("key = "),
// section header ("[key]", "[[key]]", "[parent.key]"), or map prefix
// ("[tools.name]", "[plugins.name]") rather than in prose.
func tagDocumented(content, tag string) bool {
	return strings.Contains(content, tag+" =") ||
		strings.Contains(content, tag+"]") ||
		strings.Contains(content, tag+".")
}

func collectTOMLTags(t reflect.Type) []string {
	seen := make(map[string]bool)
	walkType(t, seen)

	tags := make([]string, 0, len(seen))
	for tag := range seen {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

func walkType(t reflect.Type, seen map[string]bool) {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("toml")
		if tag == "" || tag == "-" {
			continue
		}
		tag, _, _ = strings.Cut(tag, ",")
		seen[tag] = true

		ft := f.Type
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Slice {
			ft = ft.Elem()
			if ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
		}
		if ft.Kind() == reflect.Map {
			ft = ft.Elem()
			if ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
		}
		if ft.Kind() == reflect.Struct {
			walkType(ft, seen)
		}
	}
}
