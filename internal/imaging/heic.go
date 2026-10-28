package imaging

import (
	"context"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// ExecDecoder converts HEIC by running a CLI into a temp PNG (lossless), then decodes it.
//
// Chosen over cgo libheif bindings: the runtime image only needs `apk add libheif-tools`
// (heif-dec/heif-convert, which apply the HEIF irot/imir orientation transforms), the Go
// build stays CGO_ENABLED=0, and a crash in the decoder can't take down the server. On macOS
// dev machines without libheif, `sips` is used as a fallback (never in the container).
type ExecDecoder struct {
	Tool    string // path of heif-dec, heif-convert or sips
	Timeout time.Duration
}

// FindHEICDecoder looks for heif-dec, heif-convert, then (macOS only) sips. nil if none.
// override, if set (HEIC_DECODER env), is used as the tool path.
func FindHEICDecoder(override string) *ExecDecoder {
	if override != "" {
		if p, err := exec.LookPath(override); err == nil {
			return &ExecDecoder{Tool: p}
		}
		return nil
	}
	candidates := []string{"heif-dec", "heif-convert"}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, "sips")
	}
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return &ExecDecoder{Tool: p}
		}
	}
	return nil
}

func (d *ExecDecoder) Name() string { return filepath.Base(d.Tool) }

func (d *ExecDecoder) Decode(ctx context.Context, data []byte) (image.Image, error) {
	dir, err := os.MkdirTemp("", "heic-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in, out := filepath.Join(dir, "in.heic"), filepath.Join(dir, "out.png")
	if err := os.WriteFile(in, data, 0o600); err != nil {
		return nil, err
	}
	t := d.Timeout
	if t <= 0 {
		t = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, t)
	defer cancel()
	var cmd *exec.Cmd
	if filepath.Base(d.Tool) == "sips" {
		cmd = exec.CommandContext(ctx, d.Tool, "-s", "format", "png", in, "--out", out)
	} else {
		cmd = exec.CommandContext(ctx, d.Tool, in, out)
	}
	if msg, err := cmd.CombinedOutput(); err != nil {
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("%s: %v: %s", d.Name(), err, msg)
	}
	f, err := os.Open(out)
	if err != nil {
		return nil, fmt.Errorf("%s produced no output: %w", d.Name(), err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}
