package nutrition

import (
	"bufio"
	_ "embed"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Link is the food an article of the library is made of, with what Hejmo
// knows about its weight.
type Link struct {
	// FoodID is the id of a food ("bls:B101000").
	FoodID string
	// PieceG is the weight of one piece (0: not known); GPerML the weight of a
	// millilitre (0: not known).
	PieceG, GPerML float64
	// NoWeight says a weight of the article is not the weight of the food:
	// grams of coffee beans or tea leaves are not the brewed drink. Written "-"
	// in the piece column; no unit counts, the line is listed as not counted.
	NoWeight bool
}

//go:embed data/articles.tsv
var articlesText string

var (
	linksOnce sync.Once
	links     map[string]Link
)

func articleLinks() map[string]Link {
	linksOnce.Do(func() {
		links = map[string]Link{}
		sc := bufio.NewScanner(strings.NewReader(articlesText))
		for sc.Scan() {
			line := sc.Text()
			if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
				continue
			}
			f := strings.Split(line, "|")
			if len(f) != 4 {
				panic("nutrition: malformed line in articles.tsv: " + line)
			}
			l := Link{FoodID: f[1]}
			if f[2] == "-" {
				l.NoWeight, f[2] = true, ""
			}
			for i, dst := range []*float64{&l.PieceG, &l.GPerML} {
				if f[2+i] == "" {
					continue
				}
				v, err := strconv.ParseFloat(f[2+i], 64)
				if err != nil || v <= 0 {
					panic(fmt.Sprintf("nutrition: bad weight in articles.tsv: %s", line))
				}
				*dst = v
			}
			links[f[0]] = l
		}
	})
	return links
}

// ForArticle returns the food and weights of a shipped article. An article
// that is not food has none.
func ForArticle(key string) (Link, bool) {
	l, ok := articleLinks()[key]
	return l, ok
}
