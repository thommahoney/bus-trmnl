package screen

import (
	"context"
	"time"

	"github.com/thommahoney/bus-trmnl/internal/pin"
	"github.com/thommahoney/bus-trmnl/internal/render"
)

// Recipe renders whatever recipe is currently pinned. It is a focus-mode screen:
// when a recipe is pinned (via an upload) the server shows only this, frozen,
// until the pin expires. With nothing pinned it shows a placeholder, so the
// /latest?screen=recipe preview always works.
type Recipe struct {
	pins *pin.Store
}

// NewRecipe creates the recipe screen backed by the given pin store.
func NewRecipe(pins *pin.Store) *Recipe { return &Recipe{pins: pins} }

// Name implements Screen.
func (r *Recipe) Name() string { return "recipe" }

// Render implements Screen.
func (r *Recipe) Render(ctx context.Context, now time.Time, width, height int) ([]byte, error) {
	in := render.RecipeIn{Now: now, Width: width, Height: height}
	if rec, _, ok := r.pins.Current(); ok {
		in.Title = rec.Title
		in.Servings = rec.Servings
		in.Time = rec.Time
		in.Source = rec.Source
		in.Ingredients = rec.Ingredients
		in.Steps = rec.Steps
	} else {
		in.Title = "No recipe pinned"
		in.Source = "upload a Paprika recipe to display it here"
	}
	return render.Recipe(in)
}
