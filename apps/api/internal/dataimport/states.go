package dataimport

import (
	"strings"
	"unicode"
)

var states = []struct{ code, name string }{
	{"01", "Jammu and Kashmir"}, {"02", "Himachal Pradesh"}, {"03", "Punjab"}, {"04", "Chandigarh"},
	{"05", "Uttarakhand"}, {"06", "Haryana"}, {"07", "Delhi"}, {"08", "Rajasthan"}, {"09", "Uttar Pradesh"},
	{"10", "Bihar"}, {"11", "Sikkim"}, {"12", "Arunachal Pradesh"}, {"13", "Nagaland"}, {"14", "Manipur"},
	{"15", "Mizoram"}, {"16", "Tripura"}, {"17", "Meghalaya"}, {"18", "Assam"}, {"19", "West Bengal"},
	{"20", "Jharkhand"}, {"21", "Odisha"}, {"22", "Chhattisgarh"}, {"23", "Madhya Pradesh"}, {"24", "Gujarat"},
	{"25", "Daman and Diu"}, {"26", "Dadra and Nagar Haveli and Daman and Diu"}, {"27", "Maharashtra"},
	{"28", "Andhra Pradesh (before division)"}, {"29", "Karnataka"}, {"30", "Goa"}, {"31", "Lakshadweep"},
	{"32", "Kerala"}, {"33", "Tamil Nadu"}, {"34", "Puducherry"}, {"35", "Andaman and Nicobar Islands"},
	{"36", "Telangana"}, {"37", "Andhra Pradesh"}, {"38", "Ladakh"}, {"97", "Other Territory"}, {"99", "Centre Jurisdiction"},
}

// Alternative spellings Tally and spreadsheets use, keyed by alphanumeric-lowercase form.
var stateAliases = map[string]string{
	"orissa": "21", "uttaranchal": "05", "pondicherry": "34", "telengana": "36", "chattisgarh": "22", "tamilnadu": "33",
	"nctofdelhi": "07", "newdelhi": "07", "jammuandkashmir": "01", "jammukashmir": "01", "andamannicobar": "35",
	"andamanandnicobar": "35", "andamanandnicobarislands": "35", "dadraandnagarhaveli": "26", "dadranagarhavelianddamandiu": "26",
	"damananddiu": "25", "dadraandnagarhavelianddamananddiu": "26", "andhrapradeshnew": "37", "andhrapradeshbeforedivision": "28",
	"jk": "01", "hp": "02", "pb": "03", "ch": "04", "uk": "05", "hr": "06", "dl": "07", "rj": "08", "up": "09", "br": "10",
	"sk": "11", "ar": "12", "nl": "13", "mn": "14", "mz": "15", "tr": "16", "ml": "17", "as": "18", "wb": "19", "jh": "20",
	"od": "21", "or": "21", "cg": "22", "mp": "23", "gj": "24", "mh": "27", "ap": "37", "ka": "29", "ga": "30", "ld": "31",
	"kl": "32", "tn": "33", "py": "34", "an": "35", "ts": "36", "tg": "36", "la": "38",
}

var stateByKey = func() map[string]string {
	m := map[string]string{}
	for _, s := range states {
		m[alnum(s.name)] = s.code
	}
	m["andhrapradesh"] = "37"
	for k, v := range stateAliases {
		m[k] = v
	}
	return m
}()

func alnum(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}

// StateName returns the GST state name for a two-digit state code.
func StateName(code string) (string, bool) {
	for _, s := range states {
		if s.code == code {
			return s.name, true
		}
	}
	return "", false
}

// StateCode resolves a state name ("Maharashtra", "Orissa", "MH"), a two-digit code ("27") or "27-Maharashtra" to a GST state code.
func StateCode(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] >= '0' && s[0] <= '9' && s[1] >= '0' && s[1] <= '9' && (len(s) == 2 || !(s[2] >= '0' && s[2] <= '9')) {
		if _, ok := StateName(s[:2]); ok {
			return s[:2], true
		}
		return "", false
	}
	c, ok := stateByKey[alnum(s)]
	return c, ok
}
