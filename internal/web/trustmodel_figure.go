package web

import (
	"html/template"
	"strings"
)

// trustModelFigure is /trust-model's one diagram: proofs and payments →
// the world → flow → you → judgements, top to bottom so it reads at phone
// width. Ink only (currentColor), its own sizes and fills, so it draws in
// both themes and without the stylesheet; the paragraph above it in
// docs/TRUST_MODEL.md says the same in words.
const trustModelFigure = `<figure class="trust-model-figure"><svg viewBox="0 0 320 352" width="100%" role="img" aria-labelledby="tmf-title tmf-desc" fill="currentColor" font-size="13" font-family="inherit">
<title id="tmf-title">The one trust graph</title>
<desc id="tmf-desc">Proofs and payments, each at its price, are the world's edges, the only source. Max-flow delivers them: you keep your own costs first, up to the cap. Only what you hold above the cap flows on, over your judgements, to the agents you judged well. A penalty burns it.</desc>
<g fill="none" stroke="currentColor" stroke-width="1.5">
<rect x="10" y="8" width="300" height="52" rx="6"/>
<rect x="60" y="96" width="200" height="40" rx="20" stroke-width="2"/>
<rect x="10" y="176" width="300" height="56" rx="6" stroke-width="2.5"/>
<rect x="10" y="276" width="300" height="52" rx="6" stroke-dasharray="4 3"/>
<path d="M160 60v30M154 84l6 8 6-8M160 136v34M154 164l6 8 6-8M160 232v38M154 264l6 8 6-8" stroke-linecap="round" stroke-linejoin="round"/>
</g>
<text x="160" y="29" text-anchor="middle" font-weight="600">Proofs and payments</text>
<text x="160" y="48" text-anchor="middle" fill-opacity=".7" font-size="11">domain · wallet · GitHub · work · money · time present</text>
<text x="168" y="80" fill-opacity=".7" font-size="11">each at its price</text>
<text x="160" y="121" text-anchor="middle" font-weight="600">World: the only source</text>
<text x="168" y="157" fill-opacity=".7" font-size="11">max-flow, own costs first</text>
<text x="160" y="199" text-anchor="middle" font-weight="600">You: standing, in cents</text>
<text x="160" y="219" text-anchor="middle" fill-opacity=".7" font-size="11">kept up to the cap · a penalty burns it</text>
<text x="168" y="257" fill-opacity=".7" font-size="11">only what is above the cap</text>
<text x="160" y="297" text-anchor="middle" font-weight="600">Judgements</text>
<text x="160" y="316" text-anchor="middle" fill-opacity=".7" font-size="11">to the agents you judged well</text>
</svg></figure>`

// withTrustModelFigure puts the diagram after the page's opening
// paragraphs, before its first section.
func withTrustModelFigure(body template.HTML) template.HTML {
	s := string(body)
	i := strings.Index(s, "<h2")
	if i < 0 {
		return template.HTML(trustModelFigure) + body
	}
	return template.HTML(s[:i] + trustModelFigure + s[i:])
}
