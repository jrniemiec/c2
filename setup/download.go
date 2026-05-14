// Package setup handles first-run voice model download and configuration.
package setup

import (
	"archive/tar"
	"bufio"
	"compress/bzip2"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	vadURL     = "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/silero_vad.onnx"
	whisperURL = "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-whisper-tiny.en.tar.bz2"
	kokoroURL  = "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/kokoro-en-v0_19.tar.bz2"
	kwsURL     = "https://github.com/k2-fsa/sherpa-onnx/releases/download/kws-models/sherpa-onnx-kws-zipformer-gigaspeech-3.3M-2024-01-01.tar.bz2"
)

// kwsKeywordsContent is written to c2_keywords.txt after KWS model download.
// Each line: phoneme sequence, score threshold, beam threshold, @label.
const kwsKeywordsContent = "▁COMP U TER :2.0 #0.25 @computer\n▁HE Y ▁COMP U TER :2.0 #0.25 @computer\n"

// Prompt asks the user whether to download voice models.
// Returns true if the user confirmed.
func Prompt() bool {
	fmt.Fprintln(os.Stderr, "c2: voice mode requires model downloads (~500 MB)")
	fmt.Fprint(os.Stderr, "c2: download now? [Y/n]: ")
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "" || line == "y" || line == "yes"
}

// RunVoiceSetup downloads models into dataDir/models/ and writes the c2
// section into the config file at cfgPath.
func RunVoiceSetup(dataDir, cfgPath string) error {
	// Open debug log so setup progress is recorded alongside runtime logs.
	logPath := filepath.Join(dataDir, "debug.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	var setupLog *log.Logger
	if err == nil {
		setupLog = log.New(logFile, "", log.Ltime|log.Lmicroseconds)
		defer logFile.Close()
	}

	// logf writes to stderr (for the user watching the bootstrap) and to the debug log.
	logf := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "c2: "+format+"\n", args...)
		if setupLog != nil {
			setupLog.Printf("setup: "+format, args...)
		}
	}

	modelsDir := filepath.Join(dataDir, "models")
	logf("creating models directory → %s", modelsDir)
	if err := os.MkdirAll(modelsDir, 0755); err != nil {
		return fmt.Errorf("create models dir: %w", err)
	}

	// VAD
	vadPath := filepath.Join(modelsDir, "silero_vad.onnx")
	logf("downloading VAD model → %s", vadPath)
	if err := downloadFile(vadURL, vadPath, logf); err != nil {
		return fmt.Errorf("download VAD model: %w", err)
	}

	// STT (Whisper tiny.en)
	whisperDir := filepath.Join(modelsDir, "sherpa-onnx-whisper-tiny.en")
	logf("downloading STT model (Whisper tiny.en) → %s", whisperDir)
	if err := downloadAndExtract(whisperURL, modelsDir, logf); err != nil {
		return fmt.Errorf("download STT model: %w", err)
	}

	// TTS (Kokoro)
	kokoroDir := filepath.Join(modelsDir, "kokoro-en-v0_19")
	logf("downloading TTS model (Kokoro) → %s", kokoroDir)
	if err := downloadAndExtract(kokoroURL, modelsDir, logf); err != nil {
		return fmt.Errorf("download TTS model: %w", err)
	}

	// KWS (keyword spotter — wake word "Computer")
	kwsDir := filepath.Join(modelsDir, "sherpa-onnx-kws-zipformer-gigaspeech-3.3M-2024-01-01")
	logf("downloading KWS model (wake word) → %s", kwsDir)
	if err := downloadAndExtract(kwsURL, modelsDir, logf); err != nil {
		return fmt.Errorf("download KWS model: %w", err)
	}
	kwsKeywordsPath := filepath.Join(kwsDir, "c2_keywords.txt")
	logf("writing keywords file → %s", kwsKeywordsPath)
	if err := os.WriteFile(kwsKeywordsPath, []byte(kwsKeywordsContent), 0644); err != nil {
		return fmt.Errorf("write keywords file: %w", err)
	}

	// Write c2 section into config.json
	logf("writing voice config → %s", cfgPath)
	if err := writeC2Config(cfgPath, vadPath, whisperDir, kokoroDir, kwsDir, kwsKeywordsPath); err != nil {
		return err
	}
	logf("setup complete")
	return nil
}

// downloadFile downloads url to destPath with progress output.
func downloadFile(url, destPath string, logf func(string, ...any)) error {
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()

	pr := newProgressReader(resp.Body, resp.ContentLength)
	n, err := io.Copy(f, pr)
	pr.clear()
	if err != nil {
		return err
	}
	logf("  done (%.1f MB)", float64(n)/1e6)
	return nil
}

// downloadAndExtract downloads a .tar.bz2 from url and extracts it into destDir.
func downloadAndExtract(url, destDir string, logf func(string, ...any)) error {
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	pr := newProgressReader(resp.Body, resp.ContentLength)
	tr := tar.NewReader(bzip2.NewReader(pr))

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			pr.clear()
			return err
		}

		// Security: reject any path that escapes destDir
		target := filepath.Join(destDir, filepath.Clean("/"+hdr.Name))
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				pr.clear()
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				pr.clear()
				return err
			}
			f, err := os.Create(target)
			if err != nil {
				pr.clear()
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				pr.clear()
				return err
			}
			f.Close()
		}
	}

	pr.clear()
	logf("  done (%.1f MB)", float64(pr.n)/1e6)
	return nil
}

// progressReader wraps an io.Reader and prints a live progress line to stderr.
type progressReader struct {
	r       io.Reader
	total   int64 // -1 if unknown
	n       int64 // bytes read so far
	lastPrint int64
	spinner int
}

const progressChunk = 200 * 1024 // print every 200 KB

var spinChars = []rune{'|', '/', '-', '\\'}

func newProgressReader(r io.Reader, total int64) *progressReader {
	return &progressReader{r: r, total: total}
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	p.n += int64(n)
	if p.n-p.lastPrint >= progressChunk {
		p.lastPrint = p.n
		p.spinner = (p.spinner + 1) % len(spinChars)
		p.print()
	}
	return n, err
}

func (p *progressReader) print() {
	mb := float64(p.n) / 1e6
	if p.total > 0 {
		totalMB := float64(p.total) / 1e6
		pct := int(float64(p.n) / float64(p.total) * 100)
		barWidth := 20
		filled := barWidth * pct / 100
		bar := strings.Repeat("=", filled)
		if filled < barWidth {
			bar += ">"
			bar += strings.Repeat(" ", barWidth-filled-1)
		}
		fmt.Fprintf(os.Stderr, "\r    [%s] %d%%  %.1f / %.1f MB  ", bar, pct, mb, totalMB)
	} else {
		fmt.Fprintf(os.Stderr, "\r    %.1f MB  %c  ", mb, spinChars[p.spinner])
	}
}

// clear erases the progress line so the next logf output starts cleanly.
func (p *progressReader) clear() {
	fmt.Fprint(os.Stderr, "\r\033[K")
}

// writeC2Config writes the c2 section into the config file at cfgPath,
// preserving all existing keys.
func writeC2Config(cfgPath, vadPath, whisperDir, kokoroDir, kwsDir, kwsKeywordsPath string) error {
	// Read existing config as raw map to preserve all keys.
	rawMap := make(map[string]json.RawMessage)
	if b, err := os.ReadFile(cfgPath); err == nil && len(b) > 0 {
		_ = json.Unmarshal(b, &rawMap)
	}

	c2Section := map[string]any{
		"vad_model":          vadPath,
		"stt_encoder":        filepath.Join(whisperDir, "tiny.en-encoder.int8.onnx"),
		"stt_decoder":        filepath.Join(whisperDir, "tiny.en-decoder.int8.onnx"),
		"stt_tokens":         filepath.Join(whisperDir, "tiny.en-tokens.txt"),
		"stt_language":       "en",
		"tts_backend":        "say",
		"tts_voice":          "",
		"tts_model":          filepath.Join(kokoroDir, "model.onnx"),
		"tts_voices":         filepath.Join(kokoroDir, "voices.bin"),
		"tts_tokens":         filepath.Join(kokoroDir, "tokens.txt"),
		"tts_data_dir":       filepath.Join(kokoroDir, "espeak-ng-data"),
		"tts_speed":          200,
		"kws_encoder":        filepath.Join(kwsDir, "encoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx"),
		"kws_decoder":        filepath.Join(kwsDir, "decoder-epoch-12-avg-2-chunk-16-left-64.onnx"),
		"kws_joiner":         filepath.Join(kwsDir, "joiner-epoch-12-avg-2-chunk-16-left-64.int8.onnx"),
		"kws_tokens":         filepath.Join(kwsDir, "tokens.txt"),
		"kws_keywords":       kwsKeywordsPath,
		"kws_gain":           1.5,
		"correction_profile": "oai-mini",
	}

	b, err := json.Marshal(c2Section)
	if err != nil {
		return err
	}
	rawMap["c2"] = json.RawMessage(b)

	out, err := json.MarshalIndent(rawMap, "", "  ")
	if err != nil {
		return err
	}

	tmp := cfgPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, cfgPath)
}
