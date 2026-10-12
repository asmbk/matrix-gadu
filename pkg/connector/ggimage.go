package connector

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"path"
	"strings"

	"golang.org/x/image/draw"
	// Matrix clients send WebP images too.
	_ "golang.org/x/image/webp"
)

// errImageTooLarge means that the image does not fit the size limit even at
// the smallest size that the bridge tries.
var errImageTooLarge = errors.New("the image does not fit the GG size limit")

// fitImage returns an image that is at most limit bytes. An image that fits
// and that GG clients can show (JPEG, PNG, GIF) stays as it is. Any other
// image becomes a JPEG, smaller in size and quality until it fits. The second
// result is the file name for GG.
func fitImage(data []byte, name string, limit int) ([]byte, string, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	if len(data) <= limit && (format == "jpeg" || format == "png" || format == "gif") {
		return data, name, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	jpegName := strings.TrimSuffix(name, path.Ext(name)) + ".jpg"
	// Start from a size that GG clients show well, then go down.
	for _, side := range []int{1600, 1280, 1024, 800, 640, 480, 320} {
		scaled := scaleDown(img, cfg.Width, cfg.Height, side)
		for _, quality := range []int{85, 70, 55} {
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: quality}); err != nil {
				return nil, "", err
			}
			if buf.Len() <= limit {
				return buf.Bytes(), jpegName, nil
			}
		}
	}
	return nil, "", errImageTooLarge
}

// scaleDown returns the image with its longer side at most maxSide pixels,
// on a white background, because JPEG has no transparency.
func scaleDown(img image.Image, w, h, maxSide int) image.Image {
	nw, nh := w, h
	if w >= h && w > maxSide {
		nw, nh = maxSide, h*maxSide/w
	} else if h > w && h > maxSide {
		nw, nh = w*maxSide/h, maxSide
	}
	nw, nh = max(nw, 1), max(nh, 1)
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)
	return dst
}
