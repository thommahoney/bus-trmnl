package paprika

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"testing"
)

const sampleJSON = `{
	"name": "Buttermilk Pancakes",
	"servings": "makes 12",
	"total_time": "25 min",
	"source": "Joy of Cooking",
	"ingredients": "2 cups flour\n2 tbsp sugar\n\n2 cups buttermilk",
	"directions": "Whisk dry ingredients.\nFold in wet.\n\nCook on a griddle."
}`

func gzipBytes(s string) []byte {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	gw.Write([]byte(s))
	gw.Close()
	return buf.Bytes()
}

func TestParsePlainJSON(t *testing.T) {
	recs, err := Parse([]byte(sampleJSON))
	if err != nil {
		t.Fatalf("Parse plain: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d recipes, want 1", len(recs))
	}
	r := recs[0]
	if r.Title != "Buttermilk Pancakes" {
		t.Errorf("title = %q", r.Title)
	}
	if r.Time != "25 min" || r.Servings != "makes 12" || r.Source != "Joy of Cooking" {
		t.Errorf("meta = %q / %q / %q", r.Time, r.Servings, r.Source)
	}
	// Blank separator lines are dropped.
	if len(r.Ingredients) != 3 {
		t.Errorf("ingredients = %v", r.Ingredients)
	}
	if len(r.Steps) != 3 {
		t.Errorf("steps = %v", r.Steps)
	}
}

func TestParseGzippedSingle(t *testing.T) {
	recs, err := Parse(gzipBytes(sampleJSON))
	if err != nil {
		t.Fatalf("Parse gzip: %v", err)
	}
	if len(recs) != 1 || recs[0].Title != "Buttermilk Pancakes" {
		t.Fatalf("got %+v", recs)
	}
}

func TestParseZipOfGzipped(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"a.paprikarecipe", "b.paprikarecipe"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(gzipBytes(sampleJSON))
	}
	zw.Close()

	recs, err := Parse(buf.Bytes())
	if err != nil {
		t.Fatalf("Parse zip: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d recipes, want 2", len(recs))
	}
}

func TestParseGarbage(t *testing.T) {
	if _, err := Parse([]byte("not a recipe")); err == nil {
		t.Fatal("expected error on garbage input")
	}
}
