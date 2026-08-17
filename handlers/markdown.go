package handlers

import (
	"bytes"
	"html"
	"log"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	ghtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// markdown is the plain goldmark renderer used where the full article HTML
// is embedded in machine-readable output (the RSS feed). No UI chrome.
var markdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM, extension.Typographer),
	goldmark.WithRendererOptions(ghtml.WithUnsafe()),
)

// renderMarkdown converts Markdown source to HTML without copy buttons.
func renderMarkdown(src string) string {
	var buf bytes.Buffer
	if err := markdown.Convert([]byte(src), &buf); err != nil {
		log.Printf("markdown: %v", err)
		return "<p>Could not render this post.</p>"
	}
	return buf.String()
}

// markdownRich is the renderer used for human-facing pages (blog posts,
// project pages, admin previews). It wraps every code block in a container
// with a language label and a copy button (see codeBlockWrapper).
var markdownRich = goldmark.New(
	goldmark.WithExtensions(extension.GFM, extension.Typographer),
	goldmark.WithParserOptions(parser.WithASTTransformers(util.Prioritized(&codeBlockTransformer{}, 100))),
	goldmark.WithRendererOptions(
		ghtml.WithUnsafe(),
		renderer.WithNodeRenderers(util.Prioritized(&codeBlockWrapperRenderer{}, 100)),
	),
)

// renderMarkdownRich converts Markdown to HTML, decorating each code block
// with a copy button handled by static/code-copy.js.
func renderMarkdownRich(src string) string {
	var buf bytes.Buffer
	if err := markdownRich.Convert([]byte(src), &buf); err != nil {
		log.Printf("markdown: %v", err)
		return "<p>Could not render this post.</p>"
	}
	return buf.String()
}

// ---------------------------------------------------------------------------
// Copy-button code blocks
//
// The AST transformer wraps every CodeBlock / FencedCodeBlock in a
// codeBlockWrapper container; the wrapper renderer emits the surrounding
// chrome (language label + copy button) and lets goldmark's default renderer
// produce the <pre><code>…</code></pre> itself.

var kindCodeBlockWrapper = ast.NewNodeKind("CodeBlockWrapper")

type codeBlockWrapper struct {
	ast.BaseBlock
	language string
}

func (n *codeBlockWrapper) Kind() ast.NodeKind { return kindCodeBlockWrapper }

func (n *codeBlockWrapper) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

// codeBlockTransformer replaces each code block with a wrapper that holds it.
type codeBlockTransformer struct{}

func (t *codeBlockTransformer) Transform(node *ast.Document, reader text.Reader, pc parser.Context) {
	_ = ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var lang string
		switch c := n.(type) {
		case *ast.FencedCodeBlock:
			if l := c.Language(reader.Source()); len(l) > 0 {
				lang = string(l)
			}
		case *ast.CodeBlock:
			// Indented code block — no language annotation.
		default:
			return ast.WalkContinue, nil
		}
		parent := n.Parent()
		if parent == nil {
			return ast.WalkContinue, nil
		}
		wrapper := &codeBlockWrapper{language: lang}
		parent.ReplaceChild(parent, n, wrapper)
		wrapper.AppendChild(wrapper, n)
		return ast.WalkSkipChildren, nil
	})
}

// codeBlockWrapperRenderer emits the chrome around code blocks.
type codeBlockWrapperRenderer struct{}

func (r *codeBlockWrapperRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindCodeBlockWrapper, r.renderWrapper)
}

func (r *codeBlockWrapperRenderer) renderWrapper(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*codeBlockWrapper)
	if entering {
		lang := n.language
		if lang == "" {
			lang = "code"
		}
		_, _ = w.WriteString(`<div class="code-block"><div class="code-block-head"><span class="code-block-lang">`)
		_, _ = w.WriteString(html.EscapeString(lang))
		_, _ = w.WriteString(`</span><button type="button" class="code-copy-btn" aria-label="Copy code to clipboard">Copy</button></div>`)
	} else {
		_, _ = w.WriteString("</div>\n")
	}
	return ast.WalkContinue, nil
}
