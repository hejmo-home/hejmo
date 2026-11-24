package starter

import (
	"strings"

	"github.com/hejmo-home/hejmo/internal/library"
)

// spellings maps the German spelling of a file to the article of the library
// that means the same thing under another name ("Mehl" is the library's
// "Weizenmehl"). It exists so the allergy check does not miss a line only
// because the name differs. Each entry was read against the article and its
// allergens in internal/library/articles.txt; add a name only when the article
// IS the ingredient. A near miss (Cheddar is not Gouda, Spätzle are not
// Nudeln, Rinderbrühe is not Gemüsebrühe) stays free text rather than carry
// the wrong allergens or the wrong shopping line. Keys are written folded
// (library.Fold).
var spellings = map[string]string{
	// gluten
	"mehl": "wheat-flour", "mehl type 405": "wheat-flour", "mehl type 550": "wheat-flour", "weissmehl": "wheat-flour",
	"mehl, glatt": "wheat-flour", "mehl mit backpulver": "wheat-flour", "weizenvollkornmehl": "wheat-flour",
	"dinkelvollkornmehl": "spelt-flour",
	"semmelbrosel":       "bread-crumbs", "brosel": "bread-crumbs", "weissbrotbrosel": "bread-crumbs",
	"semmeln": "bread-rolls", "weissbrot": "bread", "sauerteigbrot": "bread",
	"tortillas": "tortilla-wraps", "weizentortillas": "tortilla-wraps",
	"penne": "pasta", "bandnudeln": "pasta", "buchstabennudeln": "pasta", "makkaroni": "pasta", "magronen": "pasta",
	"spirelli": "pasta", "lasagneblatter": "pasta", "lasagneplatten": "pasta", "cannelloni": "pasta",
	"feine haferflocken": "rolled-oats",
	// eggs
	"eigelb": "eggs", "eigelbe": "eggs", "eiweiss": "eggs", "bio-ei": "eggs", "bio-eier": "eggs",
	// milk
	"schlagsahne": "cream", "susse sahne": "cream", "rahm": "cream", "sauerrahm": "sour-cream", "schmand": "sour-cream",
	"vollmilch": "milk", "magerquark": "quark", "topfen": "quark", "naturjoghurt": "yoghurt", "sahnejoghurt": "yoghurt",
	"butterschmalz": "butter", "bratbutter": "butter", "ghee": "butter",
	"kase, gerieben": "grated-cheese", "gratinkase, gerieben": "grated-cheese",
	// soy
	"sojasauce": "soy-sauce",
	// mustard
	"dijonsenf": "mustard", "dijon-senf": "mustard",
	// sulphites
	"weissweinessig": "vinegar", "rotweinessig": "vinegar", "apfelessig": "vinegar", "krauteressig": "vinegar",
	"balsamico": "vinegar", "aceto balsamico": "vinegar", "weisswein": "wine", "rotwein": "wine",
	// meat, vegetables and the rest, where the library only knows another spelling
	"hahnchenfilet": "chicken-breast", "hahnchenfilets": "chicken-breast", "hahnchenbrustfilet": "chicken-breast",
	"hahnchenbrustfilets": "chicken-breast", "hahnchenschenkel": "chicken-legs",
	"rinderhackfleisch": "minced-meat", "schweinehackfleisch": "minced-meat",
	"kochschinken": "ham", "schinkenspeck": "bacon", "speckwurfel": "bacon", "fruhstucksspeck": "bacon", "pancetta": "bacon",
	"kalbsschnitzel": "schnitzel", "wiener wurstchen": "sausages",
	"mohre": "carrots", "mohren": "carrots", "bundmohren": "carrots", "fingermohren": "carrots",
	"paprikaschote": "bell-peppers", "paprikaschoten": "bell-peppers", "gelbe paprikaschote": "bell-peppers", "rote paprikaschote": "bell-peppers",
	"kirschtomaten": "tomatoes", "cherrytomaten": "tomatoes", "strauchtomaten": "tomatoes",
	"salatgurke": "cucumbers", "salatgurken": "cucumbers", "zucchino": "zucchini",
	"gemusezwiebel": "onions", "gemusezwiebeln": "onions", "rote zwiebel": "onions", "rote zwiebeln": "onions",
	"erbsen, tk": "frozen-peas", "erbsen, tiefgefroren": "frozen-peas", "erbsen, tiefgekuhlt": "frozen-peas",
	"erdapfel": "potatoes", "eisbergsalat": "lettuce", "knoblauchknolle": "garlic", "ingwerknolle": "ginger",
	"kokosmilch": "coconut-milk", "tomatenketchup": "ketchup", "sultaninen": "raisins",
	"langkornreis": "rice", "risottoreis": "rice", "jasminreis": "rice", "vollkornreis": "rice",
	"staubzucker": "icing-sugar", "vanillinzucker": "vanilla-sugar", "bourbon-vanillezucker": "vanilla-sugar",
	"zimtpulver": "cinnamon", "muskat": "nutmeg", "edelsusspaprika": "paprika-powder", "rosenpaprika": "paprika-powder",
	"maisstarke": "cornflour", "maizena": "cornflour", "trockenhefe": "yeast", "frische hefe": "yeast",
	"meersalz": "salt", "grobes salz": "salt", "schwarzer pfeffer": "pepper", "gemusebruhepulver": "vegetable-stock",
}

// spelled finds the article a German name stands for in spellings: the whole
// name, else the part before the first comma ("Eigelb, Größe M"). A name with
// an alternative ("Eigelb oder Ei") is never mapped.
func spelled(name string, arts []library.Article) (library.Article, bool) {
	if hasAlternative(name) {
		return library.Article{}, false
	}
	key := library.Fold(strings.Join(strings.Fields(name), " "))
	id, ok := spellings[key]
	if !ok {
		head, _, _ := strings.Cut(key, ",")
		id, ok = spellings[strings.TrimSpace(head)]
	}
	if !ok {
		return library.Article{}, false
	}
	for _, a := range arts {
		if a.ID == id {
			return a, true
		}
	}
	panic("starter: spellings names an article the library does not ship: " + id)
}

func hasAlternative(s string) bool {
	for _, w := range strings.Fields(strings.ToLower(s)) {
		if alternativeWord(strings.Trim(w, ",;()")) {
			return true
		}
	}
	return false
}
