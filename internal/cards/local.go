package cards

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"unicode/utf8"
)

// DrawPNG is the local renderer: the card drawn with the standard library in
// the two fixed bitmap faces, on the design system's white surface with ink,
// muted metadata and hairlines. It needs no network, no browser and no quota,
// which is why it is the default and the fallback for the Cloudflare renderer.
func DrawPNG(c Card) ([]byte, error) {
	img := image.NewPaletted(image.Rect(0, 0, Width, Height), palette)
	d := drawer{img: img}
	if c.Kind == Gate {
		drawGate(d, c)
		return encodePNG(img)
	}
	const left, right = 64, Width - 64
	y := 52
	// Header: the wordmark and the room.
	d.text(regular, left, y, "SwarmMemo", muted)
	heading := clipRunes(OneLine(c.Heading, 60), 60)
	d.text(regular, right-utf8.RuneCountInString(heading)*regular.w, y, heading, muted)
	y += regular.h + 22
	cols := (right - left) / regular.w
	for _, line := range wrap(OneLine(c.Title, 400), (right-left)/bold.w, 2) {
		d.text(bold, left, y, line, ink)
		y += bold.h + 8
	}
	if c.Meta != "" {
		y += 4
		d.text(regular, left, y, clipRunes(OneLine(c.Meta, 400), cols), muted)
		y += regular.h + 6
	}
	footerTop := Height - 70
	if c.Body != "" {
		y += 18
		lineHeight := regular.h + 8
		lines := max((footerTop-16-y)/lineHeight, 0)
		for _, line := range wrap(Clean(c.Body), cols, lines) {
			d.text(regular, left, y, line, ink)
			y += lineHeight
		}
	}
	if len(c.Items) > 0 {
		y += 16
		d.hline(left, right, y)
		for _, item := range c.Items {
			if y+10+2*regular.h+8+10 > footerTop-8 {
				break
			}
			y += 10
			d.text(regular, left, y, clipRunes(OneLine(item.Meta, 400), cols), muted)
			y += regular.h + 4
			d.text(regular, left, y, clipRunes(OneLine(item.Text, 400), cols), ink)
			y += regular.h + 10
			d.hline(left, right, y)
		}
	}
	d.hline(left, right, footerTop)
	footerY := footerTop + (70-regular.h)/2
	d.text(regular, left, footerY, clipRunes(OneLine(c.Footer, 200), 60), muted)
	const tagline = "where AI agents talk"
	d.text(regular, right-len(tagline)*regular.w, footerY, tagline, muted)
	return encodePNG(img)
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// drawGate is a Gate result's card (C348): built to be read as a small
// thumbnail in a feed. Light text on near-black, the time as the one big
// thing (bold glyphs scaled up to fit), a capitals headline above it, the
// name and the rule below, and the address with a challenge in the footer.
func drawGate(d drawer, c Card) {
	for i := range d.img.Pix {
		d.img.Pix[i] = night
	}
	const left, right = 64, Width - 64
	d.text(bold, left, 44, clipRunes(OneLine(c.Heading, 40), 40), signal)
	if c.Meta != "" {
		meta := clipRunes(OneLine(c.Meta, 60), 50)
		d.text(regular, right-utf8.RuneCountInString(meta)*regular.w, 48, meta, dim)
	}
	y := 108
	title := wrap(strings.ToUpper(OneLine(c.Title, 120)), (right-left)/(2*bold.w), 2)
	for _, line := range title {
		d.scaled(bold, left, y, 2, line, light)
		y += 2*bold.h + 6
	}
	if stat := clipRunes(OneLine(c.Stat, 24), 24); stat != "" {
		n := utf8.RuneCountInString(stat)
		scale := max(min(8, (right-left)/(n*bold.w), (Height-210-y)/bold.h), 2)
		y += 10
		d.scaled(bold, left, y, scale, stat, signal)
		y += scale*bold.h + 14
	}
	for _, line := range wrap(Clean(c.Body), (right-left)/(2*regular.w), 1) {
		d.scaled(regular, left, y, 2, line, light)
		y += 2*regular.h + 8
	}
	footerTop := Height - 78
	for x := left; x < right; x++ {
		d.img.SetColorIndex(x, footerTop, rule)
	}
	footerY := footerTop + (78-bold.h)/2
	d.text(bold, left, footerY, clipRunes(OneLine(c.Footer, 60), 40), signal)
	const dare = "Can your agent beat it?"
	d.text(bold, right-len(dare)*bold.w, footerY, dare, light)
}

const (
	paper uint8 = iota
	ink
	muted
	hairline
	// The Gate card's colours: near-black, light text, the signal green,
	// dimmed metadata and its rule.
	night
	light
	signal
	dim
	rule
)

var palette = color.Palette{
	color.RGBA{0xff, 0xff, 0xff, 0xff},
	color.RGBA{0x17, 0x17, 0x17, 0xff},
	color.RGBA{0x70, 0x70, 0x70, 0xff},
	color.RGBA{0xe5, 0xe5, 0xe5, 0xff},
	color.RGBA{0x0b, 0x0f, 0x14, 0xff},
	color.RGBA{0xf5, 0xf5, 0xf4, 0xff},
	color.RGBA{0x4a, 0xde, 0x80, 0xff},
	color.RGBA{0xa3, 0xa3, 0xa3, 0xff},
	color.RGBA{0x2a, 0x31, 0x3a, 0xff},
}

type drawer struct{ img *image.Paletted }

// text draws one line starting at (x, y), the top of the glyph cell. Glyphs
// past the right edge are dropped, never wrapped: callers lay text out first.
func (d drawer) text(f *face, x, y int, s string, c uint8) {
	for _, r := range s {
		if x+f.w > Width {
			return
		}
		if r != ' ' {
			bits := f.bitmap(r)
			for row := 0; row < f.h; row++ {
				for col := 0; col < f.w; col++ {
					if bits[row*f.stride+col/8]&(0x80>>(col%8)) != 0 {
						d.img.SetColorIndex(x+col, y+row, c)
					}
				}
			}
		}
		x += f.w
	}
}

// scaled draws one line with each glyph pixel as a scale × scale block.
func (d drawer) scaled(f *face, x, y, scale int, s string, c uint8) {
	for _, r := range s {
		if x+f.w*scale > Width {
			return
		}
		if r != ' ' {
			bits := f.bitmap(r)
			for row := 0; row < f.h; row++ {
				for col := 0; col < f.w; col++ {
					if bits[row*f.stride+col/8]&(0x80>>(col%8)) == 0 {
						continue
					}
					for dy := 0; dy < scale; dy++ {
						for dx := 0; dx < scale; dx++ {
							d.img.SetColorIndex(x+col*scale+dx, y+row*scale+dy, c)
						}
					}
				}
			}
		}
		x += f.w * scale
	}
}

func (d drawer) hline(x0, x1, y int) {
	for x := x0; x < x1; x++ {
		d.img.SetColorIndex(x, y, hairline)
	}
}

// wrap lays text into at most maxLines lines of cols cells, breaking at
// spaces where it can and inside a word where it must. Paragraph breaks are
// kept, runs of blank lines collapse to one, and a truncated last line ends
// with an ellipsis.
func wrap(text string, cols, maxLines int) []string {
	if cols <= 0 || maxLines <= 0 {
		return nil
	}
	var lines []string
	truncated := false
	blank := false
	for _, para := range strings.Split(text, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			if len(lines) > 0 && !blank {
				lines = append(lines, "")
				blank = true
			}
			continue
		}
		blank = false
		line := []rune{}
		for _, word := range words {
			w := []rune(word)
			for len(w) > 0 {
				space := 0
				if len(line) > 0 {
					space = 1
				}
				if len(line)+space+len(w) <= cols {
					if space == 1 {
						line = append(line, ' ')
					}
					line = append(line, w...)
					w = nil
					continue
				}
				if len(line) > 0 {
					lines = append(lines, string(line))
					line = line[:0:0]
					continue
				}
				// A word longer than a line is split.
				line = append(line, w[:cols]...)
				w = w[cols:]
			}
		}
		if len(line) > 0 {
			lines = append(lines, string(line))
		}
		if len(lines) > maxLines {
			truncated = true
			break
		}
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > maxLines {
		lines, truncated = lines[:maxLines], true
	}
	if truncated && len(lines) > 0 {
		last := []rune(strings.TrimRight(lines[len(lines)-1], " "))
		if len(last) >= cols {
			last = last[:cols-1]
		}
		lines[len(lines)-1] = string(last) + "…"
	}
	return lines
}
