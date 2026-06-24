// Package paprika parses Paprika Recipe Manager export files into the
// normalized recipe model.
//
// Paprika exports come in three shapes, all handled here:
//   - .paprikarecipes — a ZIP archive whose entries are each a gzipped JSON
//     recipe (a multi-recipe backup).
//   - .paprikarecipe  — usually a single gzipped JSON recipe…
//   - …but some single exports are plain (uncompressed) JSON.
//
// Detection is by content, not extension, so a mislabeled file still parses.
package paprika

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"

	"github.com/thommahoney/bus-trmnl/internal/recipe"
)

// maxEntry caps how much a single decompressed recipe JSON may be, guarding
// against a zip bomb on the public upload endpoint.
const maxEntry = 8 << 20

// jsonRecipe maps the Paprika export JSON fields we use. Paprika stores
// ingredients and directions as single newline-joined strings.
type jsonRecipe struct {
	Name        string `json:"name"`
	Ingredients string `json:"ingredients"`
	Directions  string `json:"directions"`
	Servings    string `json:"servings"`
	TotalTime   string `json:"total_time"`
	CookTime    string `json:"cook_time"`
	PrepTime    string `json:"prep_time"`
	Source      string `json:"source"`
	SourceURL   string `json:"source_url"`
}

// Parse reads a Paprika export and returns every recipe it contains. It tries
// the ZIP archive form first, then falls back to a single gzipped-or-plain JSON
// recipe.
func Parse(data []byte) ([]recipe.Recipe, error) {
	if recs, err := parseZip(data); err == nil && len(recs) > 0 {
		return recs, nil
	}
	rec, err := decodeEntry(data)
	if err != nil {
		return nil, fmt.Errorf("not a recognizable Paprika file: %w", err)
	}
	return []recipe.Recipe{rec}, nil
}

func parseZip(data []byte) ([]recipe.Recipe, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var recs []recipe.Recipe
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		raw, err := io.ReadAll(io.LimitReader(rc, maxEntry))
		rc.Close()
		if err != nil {
			return nil, err
		}
		rec, err := decodeEntry(raw)
		if err != nil {
			return nil, fmt.Errorf("zip entry %q: %w", f.Name, err)
		}
		recs = append(recs, rec)
	}
	return recs, nil
}

// decodeEntry decodes one recipe from either gzipped or plain JSON.
func decodeEntry(b []byte) (recipe.Recipe, error) {
	if len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b {
		gz, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return recipe.Recipe{}, err
		}
		defer gz.Close()
		un, err := io.ReadAll(io.LimitReader(gz, maxEntry))
		if err != nil {
			return recipe.Recipe{}, err
		}
		b = un
	}
	var jr jsonRecipe
	if err := json.Unmarshal(b, &jr); err != nil {
		return recipe.Recipe{}, err
	}
	if jr.Name == "" && jr.Ingredients == "" && jr.Directions == "" {
		return recipe.Recipe{}, fmt.Errorf("no recipe fields found")
	}
	return jr.toRecipe(), nil
}

func (jr jsonRecipe) toRecipe() recipe.Recipe {
	return recipe.Recipe{
		Title:       jr.Name,
		Servings:    jr.Servings,
		Time:        firstNonEmpty(jr.TotalTime, jr.CookTime, jr.PrepTime),
		Source:      firstNonEmpty(jr.Source, jr.SourceURL),
		Ingredients: recipe.SplitLines(jr.Ingredients),
		Steps:       recipe.SplitLines(jr.Directions),
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
