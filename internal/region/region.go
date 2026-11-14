// Package region lists the DACH states and cantons (ISO 3166-2). Public
// holidays depend on them (work package 3).
package region

// Region is a German state, Austrian state or Swiss canton.
type Region struct {
	Code    string // ISO 3166-2
	Country string // ISO 3166-1 alpha-2
	Name    string // local name
}

var all = []Region{
	{"DE-BW", "DE", "Baden-Württemberg"}, {"DE-BY", "DE", "Bayern"}, {"DE-BE", "DE", "Berlin"},
	{"DE-BB", "DE", "Brandenburg"}, {"DE-HB", "DE", "Bremen"}, {"DE-HH", "DE", "Hamburg"},
	{"DE-HE", "DE", "Hessen"}, {"DE-MV", "DE", "Mecklenburg-Vorpommern"}, {"DE-NI", "DE", "Niedersachsen"},
	{"DE-NW", "DE", "Nordrhein-Westfalen"}, {"DE-RP", "DE", "Rheinland-Pfalz"}, {"DE-SL", "DE", "Saarland"},
	{"DE-SN", "DE", "Sachsen"}, {"DE-ST", "DE", "Sachsen-Anhalt"}, {"DE-SH", "DE", "Schleswig-Holstein"},
	{"DE-TH", "DE", "Thüringen"},

	{"AT-1", "AT", "Burgenland"}, {"AT-2", "AT", "Kärnten"}, {"AT-3", "AT", "Niederösterreich"},
	{"AT-4", "AT", "Oberösterreich"}, {"AT-5", "AT", "Salzburg"}, {"AT-6", "AT", "Steiermark"},
	{"AT-7", "AT", "Tirol"}, {"AT-8", "AT", "Vorarlberg"}, {"AT-9", "AT", "Wien"},

	{"CH-AG", "CH", "Aargau"}, {"CH-AI", "CH", "Appenzell Innerrhoden"}, {"CH-AR", "CH", "Appenzell Ausserrhoden"},
	{"CH-BE", "CH", "Bern"}, {"CH-BL", "CH", "Basel-Landschaft"}, {"CH-BS", "CH", "Basel-Stadt"},
	{"CH-FR", "CH", "Freiburg"}, {"CH-GE", "CH", "Genf"}, {"CH-GL", "CH", "Glarus"},
	{"CH-GR", "CH", "Graubünden"}, {"CH-JU", "CH", "Jura"}, {"CH-LU", "CH", "Luzern"},
	{"CH-NE", "CH", "Neuenburg"}, {"CH-NW", "CH", "Nidwalden"}, {"CH-OW", "CH", "Obwalden"},
	{"CH-SG", "CH", "St. Gallen"}, {"CH-SH", "CH", "Schaffhausen"}, {"CH-SO", "CH", "Solothurn"},
	{"CH-SZ", "CH", "Schwyz"}, {"CH-TG", "CH", "Thurgau"}, {"CH-TI", "CH", "Tessin"},
	{"CH-UR", "CH", "Uri"}, {"CH-VD", "CH", "Waadt"}, {"CH-VS", "CH", "Wallis"},
	{"CH-ZG", "CH", "Zug"}, {"CH-ZH", "CH", "Zürich"},
}

// All returns every region, grouped by country.
func All() []Region { return append([]Region(nil), all...) }

// Lookup finds a region by ISO 3166-2 code.
func Lookup(code string) (Region, bool) {
	for _, r := range all {
		if r.Code == code {
			return r, true
		}
	}
	return Region{}, false
}
