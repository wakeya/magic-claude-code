package admin

import (
	"sync"
	"testing"
	"time"

	"magic-claude-code/internal/config"
)

// TestModeState_NoRaceUnderConcurrentReadWrite is a regression test for the
// data race on Server.config.{ConfiguredMode,EffectiveMode,ModeRationale}.
// It drives the locked setter (setModeState) and getter (modeState) directly,
// bypassing handleConfig/handleStatus so that MockStore's own (unlocked)
// state does not pollute the race report.
//
// Run with `go test -race` to catch any future regression if the modeMu
// protection is accidentally removed.
//
// Related: bootstrap feedback #5 — ConfiguredMode was read and written
// without synchronization across /api/config and /api/status handlers.
func TestModeState_NoRaceUnderConcurrentReadWrite(t *testing.T) {
	server := NewServer(&AdminConfig{
		Password:       "secret",
		ConfiguredMode: config.ConnectionModeTunnel,
		EffectiveMode:  config.ConnectionModeTunnel,
	}, config.NewMockStore(config.DefaultConfig()), nil)

	modes := []string{
		config.ConnectionModeTunnel,
		config.ConnectionModeGateway,
		config.ConnectionModeTransparent,
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writers: hammer setModeState to mutate mode fields.
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			i := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				server.setModeState(modes[i%len(modes)], modes[i%len(modes)], "concurrent test")
				i++
			}
		}()
	}

	// Readers: hammer modeState to read mode fields.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				server.modeState()
			}
		}()
	}

	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
}
