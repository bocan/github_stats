package main

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// ---- shared constants ----

const (
	svgBg      = "#0d1117"
	svgBorder  = "#30363d"
	svgTitle   = "#e6edf3"
	svgText    = "#8b949e"
	svgValue   = "#e6edf3"
	svgAccent  = "#58a6ff"
	svgOrange  = "#f78166"
	svgGreen   = "#3fb950"
	svgPurple  = "#bc8cff"
	svgYellow  = "#e3b341"
	cardWidth  = 495
	cardRadius = 6
)

func svgHeader(width, height int, title string) string {
	return fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`,
		width, height, width, height,
	) + fmt.Sprintf(`
  <rect width="%d" height="%d" rx="%d" fill="%s" stroke="%s" stroke-width="1"/>
  <text x="25" y="35" font-family="'Segoe UI', Ubuntu, sans-serif" font-size="15"
        font-weight="600" fill="%s">%s</text>
  <line x1="25" y1="48" x2="%d" y2="48" stroke="%s" stroke-width="1"/>`,
		width, height, cardRadius, svgBg, svgBorder,
		svgTitle, html.EscapeString(title),
		width-25, svgBorder,
	)
}

func formatNum(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func fmtDate(d string) string {
	// "2024-01-15" -> "Jan 15"
	t, err := time.Parse("2006-01-02", d)
	if err != nil || d == "" {
		return d
	}
	return t.Format("Jan 2")
}

// ---- Stats card ----
// Layout: 2 columns x 3 rows of stat items

type statItem struct {
	icon  string
	label string
	value string
	color string
}

func RenderStats(s UserStats) string {
	name := s.Name
	if name == "" {
		name = s.Login
	}
	title := fmt.Sprintf("%s's GitHub Stats", html.EscapeString(name))

	items := []statItem{
		{"⭐", "Total Stars", formatNum(s.TotalStars), svgYellow},
		{"📦", "Public Repos", formatNum(s.PublicRepos), svgAccent},
		{"🔥", "Contributions (all time)", formatNum(s.Contributions), svgOrange},
		{"👥", "Followers", formatNum(s.Followers), svgGreen},
		{"🔀", "Pull Requests (365d)", formatNum(s.PullReqs), svgPurple},
		{"🐛", "Issues (365d)", formatNum(s.Issues), svgText},
	}

	height := 195
	var sb strings.Builder
	sb.WriteString(svgHeader(cardWidth, height, title))

	// 2 columns, 3 rows; each cell ~247px wide, rows start at y=70
	colW := cardWidth / 2
	for i, item := range items {
		col := i % 2
		row := i / 2
		x := 25 + col*colW
		y := 70 + row*42

		sb.WriteString(fmt.Sprintf(
			`<text x="%d" y="%d" font-family="'Segoe UI', Ubuntu, sans-serif" font-size="13" fill="%s">%s %s</text>`,
			x, y, svgText, item.icon, html.EscapeString(item.label),
		))
		sb.WriteString(fmt.Sprintf(
			`<text x="%d" y="%d" font-family="'Segoe UI', Ubuntu, sans-serif" font-size="18"
			      font-weight="700" fill="%s">%s</text>`,
			x, y+20, item.color, item.value,
		))
	}

	sb.WriteString(`</svg>`)
	return sb.String()
}

// ---- Language card ----

func RenderLangs(langs []LangEntry) string {
	rows := len(langs)
	if rows == 0 {
		rows = 1
	}
	height := 60 + rows*36 + 15
	title := "Most Used Languages"

	var sb strings.Builder
	sb.WriteString(svgHeader(300, height, title))

	if len(langs) == 0 {
		sb.WriteString(fmt.Sprintf(
			`<text x="150" y="90" text-anchor="middle" font-family="'Segoe UI', Ubuntu, sans-serif"
			      font-size="13" fill="%s">No data available</text>`, svgText,
		))
		sb.WriteString(`</svg>`)
		return sb.String()
	}

	barMaxW := 300 - 25 - 25 // left + right padding
	y := 62

	for _, lang := range langs {
		barW := int(float64(barMaxW) * lang.Percentage / 100)
		if barW < 2 {
			barW = 2
		}

		// Language dot + name
		sb.WriteString(fmt.Sprintf(
			`<circle cx="%d" cy="%d" r="5" fill="%s"/>`, 25, y+2, lang.Color,
		))
		sb.WriteString(fmt.Sprintf(
			`<text x="36" y="%d" font-family="'Segoe UI', Ubuntu, sans-serif" font-size="12" fill="%s">%s</text>`,
			y+6, svgText, html.EscapeString(lang.Name),
		))
		sb.WriteString(fmt.Sprintf(
			`<text x="%d" y="%d" text-anchor="end" font-family="'Segoe UI', Ubuntu, sans-serif"
			      font-size="12" font-weight="600" fill="%s">%.1f%%</text>`,
			300-25, y+6, svgValue, lang.Percentage,
		))

		// Progress bar background
		sb.WriteString(fmt.Sprintf(
			`<rect x="25" y="%d" width="%d" height="5" rx="2" fill="%s"/>`,
			y+10, barMaxW, svgBorder,
		))
		// Progress bar fill
		sb.WriteString(fmt.Sprintf(
			`<rect x="25" y="%d" width="%d" height="5" rx="2" fill="%s"/>`,
			y+10, barW, lang.Color,
		))

		y += 36
	}

	sb.WriteString(`</svg>`)
	return sb.String()
}


