package store

import (
	"context"
	"time"
)

// Seed populates the store with the curated project catalogue researched
// from the owner's GitHub account and local repositories. It is idempotent:
// entries are upserted and existing admin overrides (visibility,
// classification, featured) are preserved because UpsertProject does not
// touch those columns.
//
// Classification rules applied here (owner's policy):
//   - pure forks/clones of other people's work  -> clone, hidden
//   - original repositories                    -> original, shown
//   - forks the owner substantially rewrote    -> rewritten, shown
//   - everything sorts by last-updated, newest first
func Seed(ctx context.Context, s Store) error {
	// One-time slug renames for databases seeded before a slug changed.
	// Preserves admin overrides and analytics by updating in place.
	if err := s.RenameProjectSlug(ctx, "my-app", "blind-tech-community"); err != nil {
		return err
	}
	for _, p := range seedProjects {
		if err := s.UpsertProject(ctx, p); err != nil {
			return err
		}
	}
	// One welcome post so the blog has a starting point (only if empty).
	existing, err := s.ListPosts(ctx, true)
	if err != nil {
		return err
	}
	if len(existing) == 0 {
		now := time.Now().UTC()
		_, err := s.CreatePost(ctx, Post{
			Slug:        "welcome",
			Title:       "Welcome to my blog",
			Summary:     "A personal space for thoughts, experiences, and notes on what I'm building and learning.",
			Body:        welcomePostMD,
			Tags:        []string{"thoughts"},
			Published:   true,
			PublishedAt: now,
		})
		return err
	}
	return nil
}

const welcomePostMD = `Hello, and welcome.

I'm **Ashu Choudhury** — a software developer who loves building products, exploring new ideas, and taking on challenging problems.

This blog is a personal space where I share my thoughts, experiences with software development, and notes on what I'm building and learning along the way.

Thanks for stopping by.`

// proj is a compact constructor for seed projects.
func proj(slug, name, tagline, summary, lang string, year string, pushed string,
	cls Classification, visible, featured bool, stack, features []string, accent, mono string) Project {
	return Project{
		Slug:           slug,
		Name:           name,
		Tagline:        tagline,
		Summary:        summary,
		Description:    summary,
		Language:       lang,
		Classification: cls,
		Visible:        visible,
		Featured:       featured,
		Stack:          stack,
		Features:       features,
		Year:           year,
		Accent:         accent,
		Mono:           mono, RepoURL: "https://github.com/ashu-choudhury/" + slug,
		Source:   "github",
		PushedAt: pushed,
	}
}

// localProj is proj with a local (non-GitHub) source — no repo URL.
func localProj(slug, name, tagline, summary, lang string, year string, pushed string,
	cls Classification, visible, featured bool, stack, features []string, accent, mono string) Project {
	p := proj(slug, name, tagline, summary, lang, year, pushed, cls, visible, featured, stack, features, accent, mono)
	p.RepoURL = ""
	p.Source = "local"
	return p
}

var seedProjects = []Project{
	// ---- Flagship local work (source: local) ----------------------------
	localProj("ashu-dex-protector", "AshuDex Protector",
		"Enterprise-grade Android binary protection platform",
		"An Android application protection platform that keeps app logic invisible to decompilers: RAM-only in-memory execution via InMemoryDexClassLoader, AES-256 + GZIP encrypted bytecode, and a native C/Rust anti-tamper engine that validates the APK signing certificate before decryption. Shipped to multiple clients as a distribution pipeline.",
		"Kotlin + Rust", "2026", "2026-07-29",
		ClassificationOriginal, true, true,
		[]string{"Android", "Kotlin", "Rust", "NDK", "AES-256", "InMemoryDexClassLoader"},
		[]string{
			"Zero-disk-footprint RAM-only execution — decompilers extract zero application bytecode",
			"Native C/Rust anti-tamper engine with 224/256-bit signing-certificate validation",
			"Post-build pipeline interception with per-client release builds",
			"AES-256 CTR + GZIP bytecode encryption decrypted directly into volatile memory",
			"Gradle Kotlin DSL multi-module build (plugin, runtime, native, sample app)",
		},
		"accent-ashu-dex-protector", "ADP"),

	localProj("blind-tech-community", "Blind Tech Community",
		"The flagship Android app of the Blind Tech Community product line",
		"The flagship Android application (version 37+) of the Blind Tech Community product line — built with Kotlin and Jetpack Compose, Room via KSP, and native code through the NDK. It includes a screen reader, a multi-language TTS engine, voice typing, a custom keyboard and dozens of tools, paired with a multi-backend service layer spanning Python OCR, Node.js and PHP/MySQL APIs. Localized across four languages and shipped on Google Play.",
		"Kotlin / Full-stack", "2026", "2026-07-29",
		ClassificationOriginal, true, true,
		[]string{"Android", "Kotlin", "Jetpack Compose", "Room", "KSP", "NDK", "Python", "Node.js", "PHP", "MySQL"},
		[]string{
			"Full product line: screen reader, multi-language TTS, voice typing, custom keyboard and more",
			"~400 Kotlin files across app, SDK and plugin modules",
			"Jetpack Compose UI with Kotlin serialization and KSP code generation",
			"Room database with generated DAOs and multiple feature domains",
			"Native code via NDK with automated versioning (versionCode 37+)",
			"Backends in Python (OCR), Node.js and PHP with MySQL storage",
			"Localized in English, Arabic, Spanish and Portuguese; shipped on Google Play",
		},
		"accent-blind-tech-community", "BTC"),

	localProj("nano-screen-reader", "Nano Screen Reader",
		"A screen reader and virtual screen reader for Android",
		"A full screen reader built into the Blind Tech Community app: it reads any app aloud and lets users navigate, browse and control the phone by touch, gestures and voice — with granular reading controls, screen search, table navigation and verbosity settings. The foundation of a flagship accessibility product shipped on Google Play.",
		"Kotlin", "2026", "2026-07-29",
		ClassificationOriginal, true, true,
		[]string{"Android", "Kotlin", "Accessibility", "Screen reader", "Play Store"},
		[]string{
			"Reads any app aloud via the accessibility service — touch exploration, gestures, virtual cursor",
			"Granular reading: word/line/paragraph, screen search, tables, verbosity and proofreading",
			"Sound schemes, speech-rate and punctuation controls, screen curtain",
			"Shipped as part of the Blind Tech Community app on Google Play",
		},
		"accent-nano-screen-reader", "NSR"),

	localProj("instant-tts", "Instant TTS",
		"Multi-language text-to-speech with automatic language switching",
		"A multi-language TTS engine that automatically detects the language of text and switches voices instantly while reading — no manual language selection, fully on-device. Backed by a voice catalog, language detection and per-language routing with cached, instant speech.",
		"Kotlin", "2026", "2026-07-29",
		ClassificationOriginal, true, true,
		[]string{"Android", "Kotlin", "TTS", "Multi-language", "On-device AI"},
		[]string{
			"Automatic language detection and instant voice switching while reading",
			"Multi-language voice catalog with per-language on-device engines",
			"Sentence-aware routing with cached audio for continuous, instant speech",
			"Ships inside the Blind Tech Community app",
		},
		"accent-instant-tts", "ITT"),

	localProj("windows-accessibility-suite", "Windows Accessibility Suite",
		"A 30+ feature desktop application for Windows",
		"A voice-first desktop suite for Windows: OCR, PDF toolkit, screen recorder, media downloader, text-to-speech, voice typing, send-files, password saver, radio, news and 20+ more tools — packaged as an MSIX installer and designed to be used entirely without sight.",
		"Python", "2026", "2026-07-29",
		ClassificationOriginal, true, false,
		[]string{"Python", "Windows", "MSIX", "OCR", "Accessibility"},
		[]string{
			"30+ integrated tools — OCR, PDF, screen recording, media download, TTS and more",
			"Fully voice-first: every feature usable with the screen reader",
			"Packaged as an MSIX installer with auto-updates",
		},
		"accent-windows-accessibility-suite", "WAS"),

	localProj("vi-games", "VI Games",
		"Audio games for the blind",
		"A collection of games playable entirely through sound — designed for blind and low-vision players and published on Google Play.",
		"Kotlin", "2026", "2026-07-01",
		ClassificationOriginal, true, false,
		[]string{"Android", "Kotlin", "Audio games", "Play Store"},
		[]string{
			"Playable entirely through sound — no vision required",
			"Published on Google Play",
		},
		"accent-vi-games", "VG"),

	// ---- Rewritten forks (actively developed by the owner) --------------
	proj("container2wasm", "container2wasm",
		"Container to WASM converter",
		"A converter that turns OCI container images into WebAssembly modules. Forked upstream and actively developed — the owner is the most recent author on the mainline history.",
		"Go", "2026", "2026-08-05",
		ClassificationRewritten, true, false,
		[]string{"Go", "WebAssembly", "OCI", "Containers"},
		[]string{"Converts container images to WASM runtimes", "Active fork with ongoing commits"},
		"accent-container2wasm", "C2W"),

	proj("opencode", "opencode",
		"The open-source coding agent",
		"An open-source terminal-based AI coding agent. Forked and actively developed with the owner as the most recent committer on the working branch.",
		"TypeScript", "2026", "2026-06-22",
		ClassificationRewritten, true, false,
		[]string{"TypeScript", "AI", "CLI", "Agents"},
		[]string{"Terminal-native AI coding agent", "Active fork with ongoing commits"},
		"accent-opencode", "OC"),

	proj("ai-gen-dev", "AI-Gen-Dev",
		"Intelligent developer automation toolkit",
		"A CLI toolkit that uses the Gemini API to automate the grunt work of software development: conventional commit messages, READMEs, changelogs, code review and pull-request templates — generated from the actual state of your repository. Published on npm.",
		"JavaScript", "2025", "2026-02-03",
		ClassificationRewritten, true, true,
		[]string{"Node.js", "TypeScript", "Gemini API", "CLI"},
		[]string{
			"AI-powered, conventionally formatted git commit messages from your staged changes",
			"README and CHANGELOG generation that analyzes project structure and content",
			"AI-assisted code review for single files or everything you've staged",
			"PR title and body generation, ready to drop into .github templates",
			"Secure local storage of your Gemini API key via a config command",
		},
		"accent-ai-gen-dev", "AID"),

	// ---- GitHub originals -----------------------------------------------
	proj("advanced-nvda-remote", "advanced-nvda-remote",
		"Advanced remote access for NVDA",
		"A Rust implementation of remote access for the NVDA screen reader — extending remote control with a modern systems-language core.",
		"Rust", "2026", "2026-06-15",
		ClassificationOriginal, true, false,
		[]string{"Rust", "NVDA", "Networking"},
		[]string{"Rust-based remote access core", "Extends NVDA remote control capabilities"},
		"accent-advanced-nvda-remote", "ANR"),

	proj("ibmtts-go-server", "IBMTTS Go Server",
		"IBMTTS Android Bridge — Eloquence for Termux",
		"A high-performance Go bridge server that runs the 32-bit Windows ETI Eloquence (IBMTTS) text-to-speech engine on Android via Termux, Box86 and Wine — embedding the engine DLLs and streaming low-latency PCM audio over TCP with a simple JSON protocol.",
		"Go", "2026", "2026-05-14",
		ClassificationOriginal, true, false,
		[]string{"Go", "TTS", "Wine", "Termux", "TCP"},
		[]string{
			"Ultra-low-latency direct PCM streaming over TCP",
			"Self-contained binary that embeds the entire engine (ECI.DLL + .SYN files)",
			"JSON protocol for speak / stop / speed / pitch / volume / voice",
			"Termux-ready headless execution via Box86 and Wine",
		},
		"accent-ibmtts-go-server", "IBT"),

	proj("nvgt-vscode-extension", "NVGT VS Code Extension",
		"VS Code language support for NVGT",
		"A Visual Studio Code extension (TypeScript, MIT) bringing language support for NVGT — the nonvisual gaming toolkit — to VS Code.",
		"TypeScript", "2026", "2026-04-11",
		ClassificationOriginal, true, false,
		[]string{"TypeScript", "VS Code", "Language server"},
		[]string{"Language support for the NVGT game toolkit", "Published VS Code extension"},
		"accent-nvgt-vscode-extension", "NVE"),

	proj("indian-rail-kotlin", "indian-rail-kotlin",
		"Indian Railways API client in Kotlin",
		"A Kotlin client library for Indian Railways data.",
		"Kotlin", "2026", "2026-03-31",
		ClassificationOriginal, true, false,
		[]string{"Kotlin", "REST API", "JVM"},
		[]string{"Kotlin-first API client", "Typed models for railway data"},
		"accent-indian-rail-kotlin", "IRK"),

	proj("news-scraper-google", "news-scraper-google",
		"Kotlin/JVM library for Google News",
		"A Kotlin/JVM library for fetching Google News RSS headlines — by top stories, section or keyword — and resolving a selected headline into the full article content from the original publisher page. Published via JitPack.",
		"Kotlin", "2026", "2026-03-25",
		ClassificationOriginal, true, false,
		[]string{"Kotlin", "JVM", "RSS", "JitPack"},
		[]string{
			"Fetch top headlines, sections (BUSINESS, TECHNOLOGY, SPORTS…) and keyword searches",
			"Resolves Google News wrapper links to original publisher URLs",
			"Extracts full article content, author and image when the publisher page is accessible",
			"Published on JitPack as com.github.ashu-choudhury:news-scraper-google",
		},
		"accent-news-scraper-google", "NSG"),

	proj("ai-assistant", "AI Virtual Assistant",
		"A voice-first digital companion with real tool use",
		"A Python virtual assistant that listens for a wake word, understands natural language intent, and actually executes tasks: playing music, searching the web, controlling the mouse and keyboard, setting timers, and analyzing screenshots through Gemini function calling.",
		"Python", "2025", "2025-10-31",
		ClassificationOriginal, true, true,
		[]string{"Python", "Gemini API", "Speech", "Automation"},
		[]string{
			"Wake-word voice interaction plus a keyboard-shortcut-driven GUI",
			"Gemini function calling for music playback, Google/YouTube search and more",
			"System control: shell commands, mouse movement, typing, screenshots",
			"Customizable TTS voices and natural-language settings",
			"Runs with a single command via UV-managed Python environments",
		},
		"accent-ai-assistant", "AVA"),

	proj("jiosaavn-dart", "jiosaavn_dart",
		"A Dart API wrapper for the JioSaavn music catalog",
		"A clean, typed Dart library giving developers seamless access to JioSaavn's music catalog: search songs, fetch full song details, decrypt media URLs, pull lyrics, and explore albums and playlists through a simple intuitive API.",
		"Dart", "2024", "2025-10-19",
		ClassificationOriginal, true, false,
		[]string{"Dart", "Flutter", "REST APIs"},
		[]string{
			"Song search and detailed song information by ID",
			"Automatic decryption of media URLs for direct playback links",
			"Lyrics retrieval where available",
			"Album and playlist data with tracklists",
			"Well-defined Dart models (Song, Album, Playlist) and clean error handling",
		},
		"accent-jiosaavn-dart", "JS"),

	proj("axios-mongo-cache", "axios-mongo-cache",
		"Two-layer HTTP caching for Axios, backed by MongoDB",
		"An Axios interceptor that slashes latency with a two-layer caching strategy: an ultra-fast in-memory cache for immediate access, plus an optional persistent MongoDB-backed cache for durability across restarts — with automatic detection of local MongoDB instances.",
		"JavaScript", "2025", "2025-10-08",
		ClassificationOriginal, true, false,
		[]string{"Node.js", "JavaScript", "MongoDB", "Axios"},
		[]string{
			"Drop-in Axios interceptor with per-request caching rules",
			"Two-layer strategy: in-memory for speed, MongoDB for persistence",
			"Automatic local MongoDB discovery — zero config to get started",
			"Configurable TTL, cacheable HTTP methods and collection name",
			"clearCache() and close() utilities for clean lifecycle management",
		},
		"accent-axios-mongo-cache", "AMC"),

	proj("clipsync", "ClipSync",
		"Clipboard sync across Android and desktop",
		"A Kotlin project for syncing the clipboard between Android and desktop devices (Android + desktop modules).",
		"Kotlin", "2025", "2025-09-07",
		ClassificationOriginal, true, false,
		[]string{"Kotlin", "Android", "Desktop", "KMP"},
		[]string{"Android and desktop clients", "Clipboard sharing across devices"},
		"accent-clipsync", "CS"),

	proj("live-internet-speed-tester-nvda-addon", "Live Internet Speed Tester (NVDA add-on)",
		"Real-time network speed in your screen reader",
		"A Python NVDA add-on that reports how much data you are currently uploading and downloading — in real time — read out through the screen reader.",
		"Python", "2025", "2025-06-24",
		ClassificationOriginal, true, false,
		[]string{"Python", "NVDA", "Networking"},
		[]string{"Live upload/download speed reporting", "NVDA add-on distribution"},
		"accent-live-internet-speed-tester", "LST"),

	// ---- Hidden in seed (clones / noise); admin can flip these ----------
	proj("software-releases", "Software Releases",
		"Release builds distribution",
		"Repository used to distribute release builds.",
		"", "2026", "2026-06-17",
		ClassificationOriginal, false, false,
		[]string{}, []string{},
		"accent-software-releases", "REL"),

	proj("node-app", "node-app",
		"Sample Node application",
		"A small sample Node/HTML application.",
		"HTML", "2025", "2025-05-04",
		ClassificationOriginal, false, false,
		[]string{"HTML"}, []string{},
		"accent-node-app", "NA"),

	proj("nvgt", "nvgt",
		"The Nonvisual Gaming Toolkit (upstream)",
		"Fork of the Nonvisual Gaming Toolkit — a scripting engine for audio games. Upstream work retained.",
		"C++", "2026", "2026-08-10",
		ClassificationClone, false, false,
		[]string{"C++", "Audio games"}, []string{},
		"accent-nvgt", "NVGT"),

	proj("android_mcp", "android_mcp",
		"Android MCP server (upstream)",
		"Fork of a production-grade MCP server giving AI agents control over Android devices via ADB and UIAutomator. Upstream work retained.",
		"TypeScript", "2026", "2026-03-26",
		ClassificationClone, false, false,
		[]string{"TypeScript", "MCP", "ADB"}, []string{},
		"accent-android-mcp", "AM"),

	proj("playaural", "PlayAural",
		"Audio-first multiplayer gaming platform (upstream)",
		"Fork of PlayAural — an audio-first multiplayer online gaming platform. Upstream work retained.",
		"Python", "2026", "2026-05-08",
		ClassificationClone, false, false,
		[]string{"Python", "Audio games"}, []string{},
		"accent-playaural", "PA"),

	proj("scrcpy", "scrcpy",
		"Display and control your Android device (upstream)",
		"Fork of scrcpy. Upstream work retained.",
		"C", "2026", "2026-04-25",
		ClassificationClone, false, false,
		[]string{"C", "Android"}, []string{},
		"accent-scrcpy", "SC"),

	proj("vscode", "vscode",
		"Visual Studio Code (upstream)",
		"Fork of the VS Code editor source. Upstream work retained.",
		"TypeScript", "2026", "2026-04-08",
		ClassificationClone, false, false,
		[]string{"TypeScript"}, []string{},
		"accent-vscode", "VSC"),

	proj("orca", "orca",
		"GNOME Orca screen reader (upstream)",
		"Mirror of the GNOME Orca screen reader. Upstream work retained.",
		"Python", "2026", "2026-03-26",
		ClassificationClone, false, false,
		[]string{"Python", "Screen reader"}, []string{},
		"accent-orca", "ORCA"),

	proj("nvgt-dev-gemini-cli-skill", "nvgt-dev-gemini-cli-skill",
		"NVGT development skills for Gemini CLI (upstream)",
		"Fork of NVGT development skills for the Gemini CLI. Upstream work retained.",
		"Python", "2026", "2026-06-02",
		ClassificationClone, false, false,
		[]string{"Python", "Gemini", "CLI"}, []string{},
		"accent-nvgt-gemini", "NGS"),

	proj("top_speed", "top_speed",
		"An audio racing game (upstream)",
		"Fork of top_speed — an audio racing game. Upstream work retained.",
		"C#", "2026", "2026-03-13",
		ClassificationClone, false, false,
		[]string{"C#", "Audio games"}, []string{},
		"accent-top-speed", "TS"),

	proj("xplorer", "xPlorer",
		"Enhanced file explorer tools (upstream)",
		"Fork of xPlorer — tools to make file explorer access more powerful. Upstream work retained.",
		"Python", "2025", "2025-12-11",
		ClassificationClone, false, false,
		[]string{"Python"}, []string{},
		"accent-xplorer", "XP"),

	proj("vectras-vm-android", "Vectras VM Android",
		"QEMU virtual machine app for Android (upstream)",
		"Fork of Vectras VM — a QEMU-based virtual machine app for Android. Upstream work retained.",
		"Java", "2026", "2026-02-19",
		ClassificationClone, false, false,
		[]string{"Java", "QEMU", "Android"}, []string{},
		"accent-vectras", "VVM"),

	proj("amazing-python-scripts", "Amazing Python Scripts",
		"Curated Python scripts collection (upstream)",
		"Fork of a curated collection of Python automation scripts. Upstream work retained.",
		"Jupyter Notebook", "2026", "2026-02-20",
		ClassificationClone, false, false,
		[]string{"Python", "Automation"}, []string{},
		"accent-amazing-python", "APS"),

	proj("extras", "Scoop Extras bucket",
		"Scoop package bucket (upstream)",
		"Fork of the ScoopInstaller Extras bucket — manifests for the Scoop package manager. Upstream work retained.",
		"PowerShell", "2026", "2026-05-17",
		ClassificationClone, false, false,
		[]string{"PowerShell", "Scoop"}, []string{},
		"accent-extras", "EX"),

	proj("python", "Python examples",
		"Python examples collection (upstream)",
		"Fork of a Python examples repository. Upstream work retained.",
		"Python", "2026", "2026-03-23",
		ClassificationClone, false, false,
		[]string{"Python"}, []string{},
		"accent-python", "PY"),
}
