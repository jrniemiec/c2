// Package setup handles first-run voice model download and configuration.
package setup

import (
	"archive/tar"
	"bufio"
	"compress/bzip2"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	vadURL     = "https://github.com/k2-fsa/sherpa-onnx/releases/download/vad-models/silero_vad.onnx"
	whisperURL = "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-whisper-tiny.en.tar.bz2"
	kokoroURL  = "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/kokoro-en-v0_19.tar.bz2"
)

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
	modelsDir := filepath.Join(dataDir, "models")
	if err := os.MkdirAll(modelsDir, 0755); err != nil {
		return fmt.Errorf("create models dir: %w", err)
	}

	// VAD
	vadPath := filepath.Join(modelsDir, "silero_vad.onnx")
	if err := downloadFile(vadURL, vadPath); err != nil {
		return fmt.Errorf("download VAD model: %w", err)
	}

	// STT (Whisper tiny.en)
	whisperDir := filepath.Join(modelsDir, "sherpa-onnx-whisper-tiny.en")
	if err := downloadAndExtract(whisperURL, modelsDir); err != nil {
		return fmt.Errorf("download STT model: %w", err)
	}

	// TTS (Kokoro)
	kokoroDir := filepath.Join(modelsDir, "kokoro-en-v0_19")
	if err := downloadAndExtract(kokoroURL, modelsDir); err != nil {
		return fmt.Errorf("download TTS model: %w", err)
	}

	// Write c2 section into config.json
	return writeC2Config(cfgPath, vadPath, whisperDir, kokoroDir)
}

// downloadFile downloads url to destPath with progress output.
func downloadFile(url, destPath string) error {
	name := filepath.Base(destPath)
	fmt.Fprintf(os.Stderr, "c2: downloading %s...", name)

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

	n, err := io.Copy(f, resp.Body)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, " done (%.1f MB)\n", float64(n)/1e6)
	return nil
}

// downloadAndExtract downloads a .tar.bz2 from url and extracts it into destDir.
func downloadAndExtract(url, destDir string) error {
	name := filepath.Base(url)
	fmt.Fprintf(os.Stderr, "c2: downloading %s...", name)

	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	counter := &writeCounter{}
	tr := tar.NewReader(bzip2.NewReader(io.TeeReader(resp.Body, counter)))

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
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
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}

	fmt.Fprintf(os.Stderr, " done (%.1f MB)\n", float64(counter.n)/1e6)
	return nil
}

type writeCounter struct{ n int64 }

func (w *writeCounter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	return len(p), nil
}

// writeC2Config writes the c2 section into the config file at cfgPath,
// preserving all existing keys.
func writeC2Config(cfgPath, vadPath, whisperDir, kokoroDir string) error {
	// Read existing config as raw map to preserve all keys.
	rawMap := make(map[string]json.RawMessage)
	if b, err := os.ReadFile(cfgPath); err == nil && len(b) > 0 {
		_ = json.Unmarshal(b, &rawMap)
	}

	c2Section := map[string]any{
		"vad_model":    vadPath,
		"stt_encoder":  filepath.Join(whisperDir, "tiny.en-encoder.int8.onnx"),
		"stt_decoder":  filepath.Join(whisperDir, "tiny.en-decoder.int8.onnx"),
		"stt_tokens":   filepath.Join(whisperDir, "tiny.en-tokens.txt"),
		"stt_language": "en",
		"tts_backend":  "kokoro",
		"tts_model":    filepath.Join(kokoroDir, "model.onnx"),
		"tts_voices":   filepath.Join(kokoroDir, "voices.bin"),
		"tts_tokens":   filepath.Join(kokoroDir, "tokens.txt"),
		"tts_data_dir": filepath.Join(kokoroDir, "espeak-ng-data"),
		"tts_voice":    "en-us",
		"tts_speed":    1.0,
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
