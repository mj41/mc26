// gen_entity generates data/entity/entity.go from entities.json.
package generate

import (
	"fmt"
	"math"
	"path/filepath"
)

type entityJSON struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`
	Width    float64 `json:"width"`
	Height   float64 `json:"height"`
	Category string  `json:"category"`
	// the attachment points (GenEntities: passengers by index, the vehicle one)
	Passengers [][3]float64 `json:"passengers"`
	Vehicle    [][3]float64 `json:"vehicle"`
}

type entityTmplEntry struct {
	GoName      string
	ID          int
	DisplayName string
	ShortName   string
	Width       string
	Height      string
	Category    string
	Passengers  string
	Vehicle     string
}

func genEntity(jsonDir, goMCRoot string) error {
	jsonPath := filepath.Join(jsonDir, "entities.json")
	outPath := filepath.Join(goMCRoot, "data", "entity", "entity.go")

	var entities []entityJSON
	if err := readJSON(jsonPath, &entities); err != nil {
		return fmt.Errorf("genEntity: %w", err)
	}

	// Compute max ID width for alignment.
	maxID := 0
	for _, e := range entities {
		if e.ID > maxID {
			maxID = e.ID
		}
	}
	idWidth := len(fmt.Sprintf("%d", maxID)) + 1

	// Build template entries.
	var entries []entityTmplEntry
	for _, e := range entities {
		shortName := stripMinecraftPrefix(e.Name)
		entries = append(entries, entityTmplEntry{
			GoName:      snakeToCamel(shortName),
			ID:          e.ID,
			DisplayName: snakeToTitle(shortName),
			ShortName:   shortName,
			Width:       formatEntityFloat(e.Width),
			Height:      formatEntityFloat(e.Height),
			Category:    e.Category,
			Passengers:  formatPoints(e.Passengers),
			Vehicle:     formatPoint(e.Vehicle),
		})
	}

	tmpl, err := loadTemplate(goMCRoot, "entity.go.tmpl")
	if err != nil {
		return fmt.Errorf("genEntity: %w", err)
	}

	data := struct {
		Header   string
		Entities []entityTmplEntry
		IDWidth  int
	}{
		Header:   generatedHeader("gen_entity.go", "entities.json"),
		Entities: entries,
		IDWidth:  idWidth,
	}

	out, err := executeTemplate(tmpl, data)
	if err != nil {
		return fmt.Errorf("genEntity: %w", err)
	}

	if err := writeFile(outPath, out); err != nil {
		return fmt.Errorf("genEntity: %w", err)
	}
	logf("genEntity: wrote %s (%d entities)", outPath, len(entities))
	return nil
}

func roundSig(f float64, digits int) float64 {
	if f == 0 {
		return 0
	}
	d := math.Ceil(math.Log10(math.Abs(f)))
	pow := math.Pow(10, float64(digits)-d)
	return math.Round(f*pow) / pow
}

func formatEntityFloat(f float64) string {
	rounded := roundSig(f, 7)
	return fmt.Sprintf("%g", rounded)
}

func formatPoint(ps [][3]float64) string {
	if len(ps) == 0 {
		return "[3]float64{}"
	}
	p := ps[0]
	return fmt.Sprintf("[3]float64{%s, %s, %s}", formatEntityFloat(p[0]), formatEntityFloat(p[1]), formatEntityFloat(p[2]))
}

func formatPoints(ps [][3]float64) string {
	if len(ps) == 0 {
		return "nil"
	}
	s := "[][3]float64{"
	for i, p := range ps {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("{%s, %s, %s}", formatEntityFloat(p[0]), formatEntityFloat(p[1]), formatEntityFloat(p[2]))
	}
	return s + "}"
}
