package render

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
	"time"
)

func sampleRecipeIn() RecipeIn {
	return RecipeIn{
		Title:       "Buttermilk Pancakes",
		Servings:    "makes 12",
		Time:        "25 min",
		Source:      "Joy of Cooking",
		Ingredients: []string{"2 cups flour", "2 tbsp sugar", "2 tsp baking powder", "2 cups buttermilk", "2 eggs"},
		Steps: []string{
			"Whisk the dry ingredients together in a large bowl.",
			"Whisk the wet ingredients separately, then fold into the dry until just combined; lumps are fine.",
			"Rest the batter five minutes. Cook on a buttered griddle until bubbles form, then flip.",
		},
		Now:    time.Now(),
		Width:  DefaultWidth,
		Height: DefaultHeight,
	}
}

func TestRecipeRendersValidPNG(t *testing.T) {
	out, err := Recipe(sampleRecipeIn())
	if err != nil {
		t.Fatalf("Recipe: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	if b := img.Bounds(); b.Dx() != DefaultWidth || b.Dy() != DefaultHeight {
		t.Fatalf("size = %v, want %dx%d", b, DefaultWidth, DefaultHeight)
	}
	// Must stay under the TRMNL X firmware image cap.
	if len(out) > 745000 {
		t.Errorf("png is %d bytes, over the e-ink cap", len(out))
	}
}

func TestRecipeLongStepsTruncate(t *testing.T) {
	in := sampleRecipeIn()
	long := strings.Repeat("Stir continuously over medium heat until thickened, scraping the bottom. ", 6)
	for i := 0; i < 40; i++ {
		in.Steps = append(in.Steps, long)
	}
	// Should still render without error or overflow panic.
	if _, err := Recipe(in); err != nil {
		t.Fatalf("Recipe with many steps: %v", err)
	}
}

func TestRecipeLongIngredientsTruncate(t *testing.T) {
	in := sampleRecipeIn()
	for i := 0; i < 60; i++ {
		in.Ingredients = append(in.Ingredients, "1 cup of some fairly long ingredient name number")
	}
	if _, err := Recipe(in); err != nil {
		t.Fatalf("Recipe with many ingredients: %v", err)
	}
}

func TestNormalizeText(t *testing.T) {
	cases := map[string]string{
		"3 ½ cups water":       "3 1/2 cups water",
		"30g (⅓ cup) parmesan": "30g (1/3 cup) parmesan",
		"**For the polenta**":  "For the polenta",
		"I didn’t add salt":    "I didn't add salt",
		"¼ tsp + ¾ cup":        "1/4 tsp + 3/4 cup",
	}
	for in, want := range cases {
		if got := normalizeText(in); got != want {
			t.Errorf("normalizeText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsHeadingLine(t *testing.T) {
	// Strong signals: headers everywhere.
	for _, h := range []string{"**Polenta**", "For the sauce:"} {
		if !isHeadingLine(h, false) {
			t.Errorf("%q should be a heading", h)
		}
	}
	// Short titles are headings only in steps (allowShort), not ingredients.
	if isHeadingLine("Make Dough", false) {
		t.Error(`"Make Dough" should not be a heading among ingredients`)
	}
	if !isHeadingLine("Make Dough", true) {
		t.Error(`"Make Dough" should be a heading among steps`)
	}
	// Real content is never a heading.
	for _, s := range []string{"2 tbsp butter", "Whisk the dry ingredients together."} {
		if isHeadingLine(s, true) {
			t.Errorf("%q should not be a heading", s)
		}
	}
}

func TestRecipeEmpty(t *testing.T) {
	in := RecipeIn{Title: "No recipe pinned", Width: DefaultWidth, Height: DefaultHeight, Now: time.Now()}
	if _, err := Recipe(in); err != nil {
		t.Fatalf("empty Recipe: %v", err)
	}
}
