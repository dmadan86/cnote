package sites

import (
	"fmt"
	"math"
)

// The renderer (lib/sites.ts themeStyle) mixes border, tint and muted from the three theme colours and picks black or
// white for text on the primary. This mirrors it so a palette is checked on every pair it actually draws.

type rgb [3]float64

func parseRGB(h string) rgb {
	var r, g, b int
	fmt.Sscanf(h, "#%02x%02x%02x", &r, &g, &b)
	return rgb{float64(r), float64(g), float64(b)}
}

// over is colour a at alpha t drawn over b, in sRGB space as the browser's color-mix(..., transparent) does.
func over(a, b rgb, t float64) rgb {
	return rgb{a[0]*t + b[0]*(1-t), a[1]*t + b[1]*(1-t), a[2]*t + b[2]*(1-t)}
}

func lum(c rgb) float64 {
	f := func(n float64) float64 {
		v := n / 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return .2126*f(c[0]) + .7152*f(c[1]) + .0722*f(c[2])
}

func ratio(a, b rgb) float64 {
	x, y := lum(a), lum(b)
	if x < y {
		x, y = y, x
	}
	return (x + .05) / (y + .05)
}

// onColor is the text colour on the primary: the same black-or-white choice as onColor in lib/sites.ts.
func onColor(p rgb) rgb {
	if lum(p) > .179 {
		return rgb{0, 0, 0}
	}
	return rgb{255, 255, 255}
}

// themeIssues checks text at 4.5:1 and the two-tone focus ring at 3:1.
func themeIssues(theme map[string]any) []string {
	col := func(k string) (rgb, bool) {
		s, ok := theme[k].(string)
		return parseRGB(s), ok && colorPattern.MatchString(s)
	}
	primary, ok1 := col("primary")
	bg, ok2 := col("background")
	text, ok3 := col("text")
	if !ok1 || !ok2 || !ok3 {
		return []string{"colours missing"}
	}
	var out []string
	check := func(name string, fg, back rgb) {
		if r := ratio(fg, back); r < 4.5 {
			out = append(out, fmt.Sprintf("%s %.2f", name, r))
		}
	}
	tint := over(primary, bg, .07)
	on := onColor(primary)
	check("text on background", text, bg)
	check("muted on background", over(text, bg, .7), bg)
	check("accent on background", primary, bg)
	check("text on tinted section", text, tint)
	check("muted on tinted section", over(text, tint, .7), tint)
	check("accent on tinted section", primary, tint)
	check("button text on accent", on, primary)
	check("banner text on accent", over(on, primary, .9), primary)
	// Buttons have adjacent black and white focus rings, so at least one must
	// contrast with each surface on which a button can appear.
	for name, surface := range map[string]rgb{"background": bg, "tinted section": tint, "primary section": primary} {
		r := math.Max(ratio(rgb{0, 0, 0}, surface), ratio(rgb{255, 255, 255}, surface))
		if r < 3 {
			out = append(out, fmt.Sprintf("focus ring on %s %.2f", name, r))
		}
	}
	return out
}
