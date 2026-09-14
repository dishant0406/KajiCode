package tui

// Quadrant half-blocks: one terminal cell splits into a 2x2 grid of sub-samples,
// so a single cell can show four colour samples instead of the two a lone "▀"
// carries. It is the densest structure the bundled terminal font can actually
// draw: the font has every block glyph in U+2580-U+259F (verified from its
// cmap), but none of the sextant (U+1FB00), octant (U+1CD00) or Braille
// (U+2800) glyphs used by more detailed renderers, so those would be tofu.

// quadrantGlyph is indexed by a 4-bit fill mask: bit0 upper-left, bit1
// upper-right, bit2 lower-left, bit3 lower-right. Every glyph is in
// U+2580-U+259F, which the bundled font covers.
var quadrantGlyph = [16]rune{
	' ',
	'▘', '▝', '▀',
	'▖', '▌', '▞', '▛',
	'▗', '▚', '▐', '▜',
	'▄', '▙', '▟', '█',
}

// quadrantCell returns the terminal cell at (x, y) as a two-colour glyph: the
// two most distinct of the cell's four sub-samples become the foreground and
// background, and each sub-sample is assigned to whichever is nearer. The
// resulting mask picks the block glyph that fills exactly those quadrants, so
// the cell keeps its internal 2x2 structure rather than flattening to one hue.
func (t *attachmentThumb) quadrantCell(x, y, cols, rows int) (fg, bg rgb, glyph rune) {
	s := t.quadrantSamples(x, y, cols, rows)
	fg, bg = twoMostDistinct(s)
	mask := 0
	for i, c := range s {
		if dist2(c, fg) <= dist2(c, bg) {
			mask |= 1 << i
		}
	}
	return fg, bg, quadrantGlyph[mask]
}

// quadrantSamples averages the four sub-quadrants of one terminal cell. A cell
// spans two sample rows (rows*2 over the grid height), matching the 2:1 tall
// shape of a text cell, and the sub-quadrants halve that box in each axis.
func (t *attachmentThumb) quadrantSamples(x, y, cols, rows int) [4]rgb {
	x0 := x * t.w / cols
	x1 := (x + 1) * t.w / cols
	if x1 <= x0 {
		x1 = x0 + 1
	}
	y0 := y * t.h / (rows * 2)
	y1 := (y + 1) * t.h / (rows * 2)
	if y1 <= y0 {
		y1 = y0 + 1
	}
	xm := (x0 + x1) / 2
	ym := (y0 + y1) / 2
	return [4]rgb{
		t.average(x0, xm, y0, ym), // upper-left
		t.average(xm, x1, y0, ym), // upper-right
		t.average(x0, xm, ym, y1), // lower-left
		t.average(xm, x1, ym, y1), // lower-right
	}
}

// twoMostDistinct returns the pair of samples farthest apart, so the cell's two
// colours straddle its actual contrast instead of an arbitrary split. All-equal
// samples return the same colour twice, which renders as a solid block.
func twoMostDistinct(s [4]rgb) (a, b rgb) {
	best := -1
	for i := 0; i < 4; i++ {
		for j := i + 1; j < 4; j++ {
			if d := dist2(s[i], s[j]); d > best {
				best, a, b = d, s[i], s[j]
			}
		}
	}
	return a, b
}

// dist2 is the squared RGB distance between two samples.
func dist2(a, b rgb) int {
	dr := int(a.r) - int(b.r)
	dg := int(a.g) - int(b.g)
	db := int(a.b) - int(b.b)
	return dr*dr + dg*dg + db*db
}
