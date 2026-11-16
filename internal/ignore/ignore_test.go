package ignore

import (
	"reflect"
	"testing"
)

func TestSelectedDirectory(t *testing.T) {
	got, err := Patterns([]Item{{Path: "Фото/2024", IsDir: true}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"#escape=\\",
		"!/Фото",
		"!/Фото/2024",
		"!/Фото/2024/**",
		"*",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestFileSelectionRejected(t *testing.T) {
	if _, err := Patterns([]Item{{Path: "Документы/report.pdf"}}, nil); err == nil {
		t.Fatal("expected error for file selection")
	}
}

func TestExcludeInsideSelection(t *testing.T) {
	got, err := Patterns(
		[]Item{{Path: "Фото/2024", IsDir: true}},
		[]Item{{Path: "Фото/2024/private", IsDir: true}},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"#escape=\\",
		"/Фото/2024/private",
		"!/Фото",
		"!/Фото/2024",
		"!/Фото/2024/**",
		"*",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestMultipleSelections(t *testing.T) {
	got, err := Patterns([]Item{
		{Path: "Фото/2024", IsDir: true},
		{Path: "Музыка", IsDir: true},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"#escape=\\",
		"!/Музыка",
		"!/Музыка/**",
		"!/Фото",
		"!/Фото/2024",
		"!/Фото/2024/**",
		"*",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRootSelection(t *testing.T) {
	got, err := Patterns([]Item{{Path: ".", IsDir: true}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("root selection should not ignore anything, got %#v", got)
	}
}

func TestEmptySelection(t *testing.T) {
	got, err := Patterns(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"*"}) {
		t.Fatalf("got %#v", got)
	}
}

func TestExcludedParentRejected(t *testing.T) {
	if _, err := Patterns(
		[]Item{{Path: "Фото/2024", IsDir: true}},
		[]Item{{Path: "Фото", IsDir: true}},
	); err == nil {
		t.Fatal("expected error for excluded parent")
	}
}

func TestSamePathRejected(t *testing.T) {
	if _, err := Patterns(
		[]Item{{Path: "Фото", IsDir: true}},
		[]Item{{Path: "Фото", IsDir: true}},
	); err == nil {
		t.Fatal("expected error for same path")
	}
}

func TestTraversalRejected(t *testing.T) {
	for _, p := range []string{"../etc/passwd", "a/../../b", "/etc/passwd"} {
		if _, err := Patterns([]Item{{Path: p}}, nil); err == nil {
			t.Fatalf("expected error for %q", p)
		}
	}
}

func TestSpecialCharsEscaped(t *testing.T) {
	got, err := Patterns([]Item{{Path: "a[1]/b*{c}", IsDir: true}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"#escape=\\",
		"!/a\\[1\\]",
		"!/a\\[1\\]/b\\*\\{c\\}",
		"!/a\\[1\\]/b\\*\\{c\\}/**",
		"*",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRootWithExclude(t *testing.T) {
	got, err := Patterns(
		[]Item{{Path: ".", IsDir: true}},
		[]Item{{Path: "tmp", IsDir: true}},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"#escape=\\", "/tmp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
