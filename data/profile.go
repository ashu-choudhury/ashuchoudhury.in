// Package data holds the site's identity content (bio, story, work
// experience, skills, socials, SEO metadata builders). Projects, posts,
// messages and analytics live in the store (SQLite), so this package never
// hard-codes the portfolio.
package data

import (
	"fmt"
	"time"
)

// BirthYear anchors the owner's age. It is always computed dynamically from
// the current year, so the site never goes stale — a visitor in 2066 sees
// the right age, not a frozen number.
const BirthYear = 2007

// Age returns the current age derived from BirthYear.
func Age() int {
	return time.Now().Year() - BirthYear
}

// Site identity. The canonical URL is overridable at deploy time via the
// SITE_URL environment variable; the name and tagline can be edited from
// the admin panel (Settings) and take effect immediately.
//
// The bare domain is canonical — www (if used at all) is expected to
// redirect to it at the hosting layer, so canonicals, sitemap, RSS and
// og:url all point at https://ashuchoudhury.in.
const (
	SiteRole   = "Android & Full-Stack Developer"
	SiteDomain = "ashuchoudhury.in"
	DefaultURL = "https://" + SiteDomain
)

var (
	SiteName = "Ashu Choudhury"
	SiteTag  = "Turning complex problems into elegant software."
)

// SetSiteIdentity updates the site name and tagline (admin Settings).
// Empty values are ignored so a partial save cannot blank the identity.
func SetSiteIdentity(name, tag string) {
	if name != "" {
		SiteName = name
	}
	if tag != "" {
		SiteTag = tag
	}
}

// Profile is the identity content shown across the site.
type Profile struct {
	Name       string
	Role       string
	Tagline    string
	Bio        []string // about me
	Work       []WorkExperience
	Highlights []string // short capacity bullets
	Socials    []SocialLink
	Skills     []SkillGroup // "what I know" — capacity sections
	StackStrip []string
}

// WorkExperience is a job / role entry in the work timeline.
type WorkExperience struct {
	Title   string
	Org     string
	Period  string
	Summary string
	Points  []string
}

// SocialLink is a single external link shown in nav/footer/contact.
type SocialLink struct {
	Label string
	URL   string
	Icon  string // matches an icon name in components/icons.templ
}

// SkillGroup is a labelled column of skills (a capacity section).
type SkillGroup struct {
	Title  string
	Icon   string
	Skills []string
}

// ProfileData is the public identity. It reflects a curious generalist
// engineer who works across Android, systems, backends and the web — not a
// single-specialty developer.
var ProfileData = Profile{
	Name:    SiteName,
	Role:    SiteRole,
	Tagline: SiteTag,
	Bio: []string{
		"I'm a curious developer who builds things at every level — from the binary up to the API, and from the API up to the UI. Android apps, Go and Rust systems, Python backends, desktop apps, websites: I'll take on almost any technical challenge.",
		"I care about quality and depth — services, lifecycles, runtimes, and the parts most people never see. That's why I can ship a production Android app one week and a native Rust library or a WebAssembly experiment the next.",
	},
	Work: []WorkExperience{
		{
			Title:   "Android & Full-Stack Developer",
			Org:     "Blind Tech Community",
			Period:  "2026",
			Summary: "Built and maintained the Blind Tech Community product line with a small team — a flagship Android application, a multi-language text-to-speech engine, a Windows desktop suite, and the backends, APIs and website around them. Shipped on Google Play and localized across four languages.",
			Points: []string{
				"Designed and built the flagship Android app — Compose UI, background services, media and platform integrations",
				"Developed a multi-language TTS engine with automatic language switching",
				"Built the service layer: OCR API, PHP and Node backends, WebRTC calling infrastructure",
				"Shipped a Windows desktop suite and the community website",
				"Owned release and distribution — Play Store, signing, versioning, localization",
			},
		},
		{
			Title:   "Independent & Open Source",
			Org:     "Personal projects",
			Period:  "2024 — present",
			Summary: "Published libraries on npm, pub.dev and JitPack; built developer tooling and experiments across Go, Rust, Dart and TypeScript — including AI-powered tools and systems programming.",
			Points: []string{
				"Published libraries and CLI tools used by other developers",
				"Explored Go, Rust, WebAssembly, TTS engines and AI tooling",
			},
		},
	},
	Highlights: []string{
		"Android at depth: services, lifecycles, accessibility, TTS engines, NDK and Rust interop",
		"Systems work in Go and Rust — servers, bridges, WebAssembly",
		"Backends: FastAPI, Node.js, PHP, MySQL and MongoDB",
		"Desktop apps, websites and published libraries",
		"Comfortable across a dozen languages — and always learning more",
	},
	Socials: []SocialLink{
		{Label: "GitHub", URL: "https://github.com/ashu-choudhury", Icon: "github"},
		{Label: "npm", URL: "https://www.npmjs.com/~ashu-choudhury", Icon: "npm"},
		{Label: "pub.dev", URL: "https://pub.dev/publishers/ashu-choudhury", Icon: "dart"},
	},
	Skills: []SkillGroup{
		{
			Title:  "Languages",
			Icon:   "code",
			Skills: []string{"Kotlin", "Go", "Rust", "Python", "TypeScript", "JavaScript", "Dart", "Java", "C/C++", "C#", "PHP", "PowerShell"},
		},
		{
			Title:  "Android platform",
			Icon:   "mobile",
			Skills: []string{"Core Android framework", "Jetpack Compose", "Accessibility services", "TTS engine & service management", "Background services & lifecycle", "Custom keyboards", "Media & playback", "NDK & Rust interop", "App signing & distribution"},
		},
		{
			Title:  "Backend & APIs",
			Icon:   "database",
			Skills: []string{"FastAPI", "PaddleOCR", "Node.js", "PHP", "MySQL", "MongoDB", "REST APIs", "WebRTC signaling", "Firebase"},
		},
		{
			Title:  "Frameworks & runtimes",
			Icon:   "terminal",
			Skills: []string{"Go + Templ + HTMX", "Astro", "Flutter", "Docker", "WebAssembly", "Hugging Face Spaces", "CLI tooling", "MCP servers"},
		},
		{
			Title:  "AI & tooling",
			Icon:   "sparkles",
			Skills: []string{"Gemini API", "Coding agents", "Function calling", "Dev automation", "Prompt engineering"},
		},
	},
	StackStrip: []string{"Kotlin", "Go", "Rust", "Python", "Compose", "NDK", "TypeScript", "Dart", "SQLite", "Gemini", "HTMX"},
}

// Story returns the programming story paragraphs. The closing paragraph is
// generated with the current age so it stays accurate forever.
func Story() []string {
	return []string{
		"I was born in 2007, and I've been curious about how things work for as long as I can remember. As a kid I'd stare at the old television and wonder what was behind the screen producing those frames — I even tried to break it open once (it was metal; I failed). I knew it wasn't magic. It had to be science. That question — what's actually happening in there? — never really left me.",
		"I'm blind, and for years that meant I couldn't use phones and computers the way everyone around me could. My first real taste of an Android phone was my mother's Micromax in 2014 — something like Android 4, barely able to run three apps at once, and mostly used by me to play a truck game I loved. I watched technology videos on YouTube by clicking and hoping: I couldn't see which video would play, and ads I couldn't skip would sometimes ruin it. But I kept watching — and kept wondering how it all worked.",
		"In 2021 everything changed: I turned on TalkBack for the first time. It was infuriating at first — I'd swipe left and wonder why the phone wasn't going left. Then I figured out the two-finger gesture, and suddenly the entire phone opened up to me. From that day I used my phone like anyone else: I installed almost everything in the Play Store, found bugs in plenty of apps, and my curiosity exploded — how do these things actually work?",
		"So I decided to learn programming — with no laptop and no keyboard, just an Android phone and the Google keyboard. I started with Python in a learning app (honestly, boring), then switched to HTML and learned it properly, typing every tag by digging through the symbols menu — a line that takes a second on a PC took me twenty on the phone. But I did it, one character at a time.",
		"In 2023 I got my first laptop, and I had to learn a screen reader on a PC all over again. It took about three months, self-taught, before I truly felt at home. After that came freedom: I could learn any language, build any project, join communities and publish open-source work — and as AI arrived, everything got faster. I'm still going, still learning, and this story is still being written.",
		fmt.Sprintf("I'm %d now — still self-taught, still curious, still taking things apart. If there's one thing that hasn't changed since that television, it's this: it's never magic, and I still want to know what's behind the screen.", Age()),
	}
}
