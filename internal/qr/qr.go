// Package qr is a small QR code encoder (byte mode, error correction level
// L, versions 1-20: up to 858 bytes), enough for WireGuard client configs
// (task 036). No QR library is in the allowed dependencies.
package qr

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"strings"
)

// blockSpec is the level-L layout of a version: EC codewords per block and
// two groups of (blocks, data codewords per block).
type blockSpec struct {
	ec             int
	n1, d1, n2, d2 int
}

var specs = [21]blockSpec{
	{},
	{7, 1, 19, 0, 0}, {10, 1, 34, 0, 0}, {15, 1, 55, 0, 0}, {20, 1, 80, 0, 0}, {26, 1, 108, 0, 0},
	{18, 2, 68, 0, 0}, {20, 2, 78, 0, 0}, {24, 2, 97, 0, 0}, {30, 2, 116, 0, 0}, {18, 2, 68, 2, 69},
	{20, 4, 81, 0, 0}, {24, 2, 92, 2, 93}, {26, 4, 107, 0, 0}, {30, 3, 115, 1, 116}, {22, 5, 87, 1, 88},
	{24, 5, 98, 1, 99}, {28, 1, 107, 5, 108}, {30, 5, 120, 1, 121}, {28, 3, 113, 4, 114}, {28, 3, 107, 5, 108},
}

var alignment = [21][]int{
	{}, {}, {6, 18}, {6, 22}, {6, 26}, {6, 30}, {6, 34},
	{6, 22, 38}, {6, 24, 42}, {6, 26, 46}, {6, 28, 50}, {6, 30, 54}, {6, 32, 58}, {6, 34, 62},
	{6, 26, 46, 66}, {6, 26, 48, 70}, {6, 26, 50, 74}, {6, 30, 54, 78}, {6, 30, 56, 82}, {6, 30, 58, 86}, {6, 34, 62, 90},
}

func (s blockSpec) data() int { return s.n1*s.d1 + s.n2*s.d2 }

// Code is an encoded symbol: Modules[y][x] is true for dark.
type Code struct {
	Version int
	Size    int
	Modules [][]bool
}

// ErrTooLong means the data does not fit version 20.
var ErrTooLong = errors.New("qr: data too long")

// Encode makes the smallest symbol that holds data.
func Encode(data []byte) (*Code, error) {
	v := 0
	for ver := 1; ver <= 20; ver++ {
		countBits := 8
		if ver >= 10 {
			countBits = 16
		}
		if 4+countBits+8*len(data) <= 8*specs[ver].data() {
			v = ver
			break
		}
	}
	if v == 0 {
		return nil, ErrTooLong
	}
	codewords := interleave(v, dataCodewords(v, data))
	c := newCode(v)
	c.place(codewords)
	best, bestScore := -1, 1<<30
	for m := 0; m < 8; m++ {
		t := c.clone()
		t.applyMask(m)
		t.drawFormat(m)
		if s := t.penalty(); s < bestScore {
			best, bestScore = m, s
		}
	}
	c.applyMask(best)
	c.drawFormat(best)
	c.drawVersion()
	return &Code{Version: v, Size: c.size, Modules: c.dark}, nil
}

// --- data encoding ---

type bits struct {
	b []byte
	n int
}

func (w *bits) put(v uint, n int) {
	for i := n - 1; i >= 0; i-- {
		if w.n%8 == 0 {
			w.b = append(w.b, 0)
		}
		if v>>uint(i)&1 == 1 {
			w.b[w.n/8] |= 0x80 >> uint(w.n%8)
		}
		w.n++
	}
}

func dataCodewords(v int, data []byte) []byte {
	capacity := specs[v].data()
	var w bits
	w.put(0b0100, 4)
	if v >= 10 {
		w.put(uint(len(data)), 16)
	} else {
		w.put(uint(len(data)), 8)
	}
	for _, c := range data {
		w.put(uint(c), 8)
	}
	for i := 0; i < 4 && w.n < capacity*8; i++ { // terminator
		w.put(0, 1)
	}
	for w.n%8 != 0 {
		w.put(0, 1)
	}
	for pad := 0; len(w.b) < capacity; pad++ {
		if pad%2 == 0 {
			w.b = append(w.b, 0xec)
		} else {
			w.b = append(w.b, 0x11)
		}
	}
	return w.b
}

// --- Reed-Solomon over GF(256), polynomial 0x11d ---

var gfExp, gfLog [512]byte

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfLog[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11d
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

func rsGenerator(degree int) []byte {
	g := []byte{1}
	for i := 0; i < degree; i++ {
		next := make([]byte, len(g)+1)
		for j, c := range g {
			next[j] ^= c
			next[j+1] ^= gfMul(c, gfExp[i])
		}
		g = next
	}
	return g
}

func rsRemainder(data []byte, ec int) []byte {
	gen := rsGenerator(ec)
	rem := make([]byte, ec)
	for _, d := range data {
		factor := d ^ rem[0]
		copy(rem, rem[1:])
		rem[ec-1] = 0
		for i := 0; i < ec; i++ {
			rem[i] ^= gfMul(gen[i+1], factor)
		}
	}
	return rem
}

func interleave(v int, data []byte) []byte {
	s := specs[v]
	var blocks, ecs [][]byte
	off := 0
	for g, n := range []int{s.n1, s.n2} {
		size := s.d1
		if g == 1 {
			size = s.d2
		}
		for i := 0; i < n; i++ {
			b := data[off : off+size]
			off += size
			blocks = append(blocks, b)
			ecs = append(ecs, rsRemainder(b, s.ec))
		}
	}
	var out []byte
	maxLen := s.d1
	if s.d2 > maxLen {
		maxLen = s.d2
	}
	for i := 0; i < maxLen; i++ {
		for _, b := range blocks {
			if i < len(b) {
				out = append(out, b[i])
			}
		}
	}
	for i := 0; i < s.ec; i++ {
		for _, e := range ecs {
			out = append(out, e[i])
		}
	}
	return out
}

// --- module matrix ---

type matrix struct {
	version  int
	size     int
	dark     [][]bool
	reserved [][]bool // function patterns, format and version areas
}

func newCode(v int) *matrix {
	size := 17 + 4*v
	m := &matrix{version: v, size: size, dark: make([][]bool, size), reserved: make([][]bool, size)}
	for i := range m.dark {
		m.dark[i] = make([]bool, size)
		m.reserved[i] = make([]bool, size)
	}
	m.drawFunctionPatterns()
	return m
}

func (m *matrix) clone() *matrix {
	c := &matrix{version: m.version, size: m.size, dark: make([][]bool, m.size), reserved: m.reserved}
	for i := range m.dark {
		c.dark[i] = append([]bool(nil), m.dark[i]...)
	}
	return c
}

func (m *matrix) set(x, y int, dark bool) {
	m.dark[y][x] = dark
	m.reserved[y][x] = true
}

func (m *matrix) drawFunctionPatterns() {
	for i := 0; i < m.size; i++ { // timing
		m.set(6, i, i%2 == 0)
		m.set(i, 6, i%2 == 0)
	}
	for _, p := range [][2]int{{3, 3}, {m.size - 4, 3}, {3, m.size - 4}} { // finders + separators
		for dy := -4; dy <= 4; dy++ {
			for dx := -4; dx <= 4; dx++ {
				x, y := p[0]+dx, p[1]+dy
				if x < 0 || y < 0 || x >= m.size || y >= m.size {
					continue
				}
				d := max(abs(dx), abs(dy))
				m.set(x, y, d != 2 && d != 4)
			}
		}
	}
	pos := alignment[m.version]
	for i, ax := range pos {
		for j, ay := range pos {
			if (i == 0 && j == 0) || (i == 0 && j == len(pos)-1) || (i == len(pos)-1 && j == 0) {
				continue // overlaps a finder
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					m.set(ax+dx, ay+dy, max(abs(dx), abs(dy)) != 1)
				}
			}
		}
	}
	// reserve format areas (drawn later) and the dark module
	for i := 0; i < 9; i++ {
		m.reserved[8][i], m.reserved[i][8] = true, true
	}
	for i := 0; i < 8; i++ {
		m.reserved[8][m.size-1-i], m.reserved[m.size-1-i][8] = true, true
	}
	m.set(8, m.size-8, true)
	if m.version >= 7 {
		for i := 0; i < 6; i++ {
			for j := 0; j < 3; j++ {
				m.reserved[i][m.size-11+j], m.reserved[m.size-11+j][i] = true, true
			}
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// place writes the codewords in the zigzag order, then remainder bits as 0.
func (m *matrix) place(cw []byte) {
	i := 0
	total := len(cw) * 8
	for right := m.size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := 0; vert < m.size; vert++ {
			for j := 0; j < 2; j++ {
				x := right - j
				upward := (right+1)&2 == 0
				y := vert
				if upward {
					y = m.size - 1 - vert
				}
				if m.reserved[y][x] {
					continue
				}
				if i < total {
					m.dark[y][x] = cw[i/8]>>(7-uint(i%8))&1 == 1
					i++
				}
			}
		}
	}
}

func maskBit(mask, x, y int) bool {
	switch mask {
	case 0:
		return (x+y)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (x+y)%3 == 0
	case 4:
		return (x/3+y/2)%2 == 0
	case 5:
		return x*y%2+x*y%3 == 0
	case 6:
		return (x*y%2+x*y%3)%2 == 0
	default:
		return ((x+y)%2+x*y%3)%2 == 0
	}
}

func (m *matrix) applyMask(mask int) {
	for y := 0; y < m.size; y++ {
		for x := 0; x < m.size; x++ {
			if !m.reserved[y][x] && maskBit(mask, x, y) {
				m.dark[y][x] = !m.dark[y][x]
			}
		}
	}
}

func bch(data, gen uint, genBits int) uint {
	v := data << uint(genBits-1)
	for i := bitLen(v) - 1; i >= genBits-1; i-- {
		if v>>uint(i)&1 == 1 {
			v ^= gen << uint(i-(genBits-1))
		}
	}
	return v
}

func bitLen(v uint) int {
	n := 0
	for v > 0 {
		n++
		v >>= 1
	}
	return n
}

// drawFormat writes the 15-bit format info (level L = 01) for mask.
func (m *matrix) drawFormat(mask int) {
	data := uint(0b01<<3 | mask)
	f := (data<<10 | bch(data, 0x537, 11)) ^ 0x5412
	bit := func(i int) bool { return f>>uint(i)&1 == 1 }
	for i := 0; i <= 5; i++ {
		m.dark[i][8] = bit(i)
	}
	m.dark[7][8] = bit(6)
	m.dark[8][8] = bit(7)
	m.dark[8][7] = bit(8)
	for i := 9; i < 15; i++ {
		m.dark[8][14-i] = bit(i)
	}
	for i := 0; i < 8; i++ {
		m.dark[8][m.size-1-i] = bit(i)
	}
	for i := 8; i < 15; i++ {
		m.dark[m.size-15+i][8] = bit(i)
	}
	m.dark[m.size-8][8] = true
}

func (m *matrix) drawVersion() {
	if m.version < 7 {
		return
	}
	v := uint(m.version)<<12 | bch(uint(m.version), 0x1f25, 13)
	for i := 0; i < 18; i++ {
		d := v>>uint(i)&1 == 1
		a, b := i/3, m.size-11+i%3
		m.dark[b][a] = d
		m.dark[a][b] = d
	}
}

// penalty is the standard mask evaluation (rules 1-4).
func (m *matrix) penalty() int {
	n := m.size
	score := 0
	for pass := 0; pass < 2; pass++ { // rule 1: runs of 5+
		for i := 0; i < n; i++ {
			run, prev := 0, false
			for j := 0; j < n; j++ {
				c := m.dark[i][j]
				if pass == 1 {
					c = m.dark[j][i]
				}
				if j > 0 && c == prev {
					run++
				} else {
					if run >= 5 {
						score += run - 2
					}
					run = 1
				}
				prev = c
			}
			if run >= 5 {
				score += run - 2
			}
		}
	}
	for y := 0; y < n-1; y++ { // rule 2: 2x2 blocks
		for x := 0; x < n-1; x++ {
			c := m.dark[y][x]
			if c == m.dark[y][x+1] && c == m.dark[y+1][x] && c == m.dark[y+1][x+1] {
				score += 3
			}
		}
	}
	pat1 := []bool{true, false, true, true, true, false, true, false, false, false, false}
	pat2 := []bool{false, false, false, false, true, false, true, true, true, false, true}
	for i := 0; i < n; i++ { // rule 3: finder-like patterns
		for j := 0; j+11 <= n; j++ {
			for _, pat := range [][]bool{pat1, pat2} {
				h, v := true, true
				for k := 0; k < 11; k++ {
					if m.dark[i][j+k] != pat[k] {
						h = false
					}
					if m.dark[j+k][i] != pat[k] {
						v = false
					}
				}
				if h {
					score += 40
				}
				if v {
					score += 40
				}
			}
		}
	}
	dark := 0 // rule 4: balance
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if m.dark[y][x] {
				dark++
			}
		}
	}
	pct := dark * 100 / (n * n)
	score += abs(pct/5*5-50) / 5 * 10
	return score
}

// SVG renders the code with a 4-module quiet zone; each module is scale px.
func (c *Code) SVG(scale int) string {
	full := (c.Size + 8) * scale
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" shape-rendering="crispEdges">`, full, full, full, full)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, full, full)
	for y, row := range c.Modules {
		for x, d := range row {
			if d {
				fmt.Fprintf(&b, "M%d %dh%dv%dh-%dz", (x+4)*scale, (y+4)*scale, scale, scale, scale)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String()
}

// Image renders the code as a grayscale image (for tests and PNG export).
func (c *Code) Image(scale int) image.Image {
	full := (c.Size + 8) * scale
	img := image.NewGray(image.Rect(0, 0, full, full))
	for y := 0; y < full; y++ {
		for x := 0; x < full; x++ {
			mx, my := x/scale-4, y/scale-4
			if mx >= 0 && my >= 0 && mx < c.Size && my < c.Size && c.Modules[my][mx] {
				img.SetGray(x, y, color.Gray{0})
			} else {
				img.SetGray(x, y, color.Gray{255})
			}
		}
	}
	return img
}
