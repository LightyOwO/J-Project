package tts

import (
	"log"
	"os/exec"
)

// Speak starts a non-blocking TTS play of the provided text.
// It returns immediately and does the actual playback in a goroutine so callers don't wait.
// The implementation attempts to use `espeak` by default; if that's not available it will
// simply log the text. This keeps the function safe and non-blocking on servers without
// a TTS binary installed.
func Speak(provider string, text string) {
	go func() {
		// Allow specifying provider in future; for now attempt espeak for local playback.
		// If espeak fails or is not available we just log the text.
		cmd := exec.Command("espeak", text)
		if err := cmd.Run(); err != nil {
			log.Printf("tts: espeak failed or not available, falling back to log output: %v (text=%q)", err, text)
			return
		}
		log.Printf("tts: spoke text (provider=%s)", provider)
	}()
}
