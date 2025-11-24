// Command mkicon downscales the master application icon into the sizes the web app ships.
//
// appicon.png is 1024x1024, which is the right size to keep as the master and the wrong size to send to a browser on every page load. This writes the small copies that Next.js picks up by convention:
//
//	web/app/icon.png        the favicon, injected into every page's head
//	web/app/apple-icon.png  the home screen icon on iOS
//	favicon.ico             the small multi size icon the Go server serves at /favicon.ico
//
// appicon.ico stays as it is: it is the Windows executable icon, and its seven full size entries are far too heavy to send to a browser.
//
// Run it after replacing appicon.png:
//
//	go run ./tools/mkicon
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
)

func main() {
	src := flag.String("src", "appicon.png", "master icon, expected to be square")
	flag.Parse()

	master, err := load(*src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mkicon: "+err.Error())
		os.Exit(1)
	}

	outputs := []struct {
		path string
		size int
	}{
		{filepath.Join("web", "app", "icon.png"), 256},
		{filepath.Join("web", "app", "apple-icon.png"), 180},
	}
	for _, out := range outputs {
		if err := write(out.path, resize(master, out.size)); err != nil {
			fmt.Fprintln(os.Stderr, "mkicon: "+err.Error())
			os.Exit(1)
		}
		info, _ := os.Stat(out.path)
		fmt.Printf("wrote %s (%dx%d, %d bytes)\n", out.path, out.size, out.size, info.Size())
	}

	// The three sizes a browser actually picks from, and nothing else.
	if err := writeICO("favicon.ico", master, 16, 32, 48); err != nil {
		fmt.Fprintln(os.Stderr, "mkicon: "+err.Error())
		os.Exit(1)
	}
	info, _ := os.Stat("favicon.ico")
	fmt.Printf("wrote favicon.ico (16, 32, 48 px, %d bytes)\n", info.Size())
}

// writeICO builds an .ico holding one PNG per requested size.
// An ICO directory entry may carry a PNG rather than a bitmap, which every browser in use has understood for well over a decade and which keeps the file a fraction of the size of the bitmap form.
func writeICO(path string, master image.Image, sizes ...int) error {
	var images [][]byte
	for _, size := range sizes {
		var buf bytes.Buffer
		enc := png.Encoder{CompressionLevel: png.BestCompression}
		if err := enc.Encode(&buf, resize(master, size)); err != nil {
			return err
		}
		images = append(images, buf.Bytes())
	}

	var out bytes.Buffer
	// ICONDIR: reserved, type 1 (icon), image count.
	binary.Write(&out, binary.LittleEndian, uint16(0))
	binary.Write(&out, binary.LittleEndian, uint16(1))
	binary.Write(&out, binary.LittleEndian, uint16(len(images)))

	// Every 16 byte directory entry precedes the image data it points at.
	offset := 6 + 16*len(images)
	for i, data := range images {
		// A 256 pixel side is written as 0; nothing here is that large, but the rule is part of the format.
		side := byte(sizes[i] % 256)
		out.WriteByte(side)
		out.WriteByte(side)
		out.WriteByte(0)                                    // palette colours, 0 for truecolour
		out.WriteByte(0)                                    // reserved
		binary.Write(&out, binary.LittleEndian, uint16(1))  // colour planes
		binary.Write(&out, binary.LittleEndian, uint16(32)) // bits per pixel
		binary.Write(&out, binary.LittleEndian, uint32(len(data)))
		binary.Write(&out, binary.LittleEndian, uint32(offset))
		offset += len(data)
	}
	for _, data := range images {
		out.Write(data)
	}
	return os.WriteFile(path, out.Bytes(), 0o644)
}

func load(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	return img, nil
}

// resize does a box filter downscale: every destination pixel averages the source pixels it covers.
// For shrinking by a large factor, which is the only thing this tool does, that is both simpler and better looking than sampling a single source pixel, and it needs nothing outside the standard library.
func resize(src image.Image, size int) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))

	for y := range size {
		y0 := b.Min.Y + y*b.Dy()/size
		y1 := b.Min.Y + (y+1)*b.Dy()/size
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := range size {
			x0 := b.Min.X + x*b.Dx()/size
			x1 := b.Min.X + (x+1)*b.Dx()/size
			if x1 <= x0 {
				x1 = x0 + 1
			}

			// Alpha weighted, so transparent pixels do not drag colour into the edges.
			var sr, sg, sb, sa, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					r, g, bl, a := src.At(sx, sy).RGBA()
					sr += uint64(r)
					sg += uint64(g)
					sb += uint64(bl)
					sa += uint64(a)
					n++
				}
			}
			if n == 0 || sa == 0 {
				continue
			}
			// RGBA() hands back premultiplied channels, and NRGBA wants them plain, so the average is divided back out by the average alpha.
			// Without this, a semi transparent edge pixel comes out too dark.
			i := dst.PixOffset(x, y)
			dst.Pix[i+0] = unpremultiply(sr, sa)
			dst.Pix[i+1] = unpremultiply(sg, sa)
			dst.Pix[i+2] = unpremultiply(sb, sa)
			dst.Pix[i+3] = to8(sa / n)
		}
	}
	return dst
}

// to8 narrows a 16 bit channel to 8 bits.
func to8(v uint64) uint8 { return uint8(v >> 8) }

// unpremultiply divides a summed colour channel by its summed alpha, giving the plain 8 bit value.
func unpremultiply(sum, alpha uint64) uint8 {
	return uint8(min(sum*0xffff/alpha, 0xffff) >> 8)
}

func write(path string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	return enc.Encode(f, img)
}
